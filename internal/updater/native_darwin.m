//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <string.h>
#include "native.h"

// Minimal public ABI, verified against Sparkle's SPUUpdater.h,
// SPUStandardUpdaterController.h and SPUUpdaterDelegate.h (2.x).
// https://github.com/sparkle-project/Sparkle/tree/2.x/Sparkle
// Protocol names are local; no Sparkle classes or symbols are linked at build time.
@protocol HoxiSparkleUpdater <NSObject>
@property(nonatomic) BOOL automaticallyChecksForUpdates;
@property(nonatomic) BOOL automaticallyDownloadsUpdates;
- (id)initWithHostBundle:(NSBundle *)host applicationBundle:(NSBundle *)app userDriver:(id)driver delegate:(id)delegate;
- (void)checkForUpdates;
- (BOOL)startUpdater:(NSError **)error;
- (void)resetUpdateCycle;
@end

@protocol HoxiSparkleUserDriver <NSObject>
- (id)initWithHostBundle:(NSBundle *)host delegate:(id)delegate;
- (void)showUpdateFoundWithAppcastItem:(id)item state:(id)state reply:(void (^)(NSInteger))reply;
- (void)showReadyToInstallAndRelaunch:(void (^)(NSInteger))reply;
- (void)dismissUpdateInstallation;
@optional
- (id)activeUpdateAlert;
- (void)installUpdate:(id)sender;
- (void)showUserInitiatedUpdateCheckWithCancellation:(void (^)(void))cancellation;
- (void)showDownloadInitiatedWithCancellation:(void (^)(void))cancellation;
- (void)showDownloadDidReceiveExpectedContentLength:(uint64_t)expectedContentLength;
- (void)showDownloadDidReceiveDataOfLength:(uint64_t)length;
- (void)showDownloadDidStartExtractingUpdate;
- (void)showExtractionReceivedProgress:(double)progress;
- (void)showInstallingUpdateWithApplicationTerminated:(BOOL)terminated retryTerminatingApplication:(void (^)(void))retry;
- (void)showUpdaterError:(NSError *)error acknowledgement:(void (^)(void))acknowledgement;
- (void)showUpdateNotFoundWithError:(NSError *)error acknowledgement:(void (^)(void))acknowledgement;
@end

#define HOXI_TRACE(event) hoxiNativeTrace((char *)(event))

static NSString *HoxiLocalized(NSString *zh, NSString *en) {
    NSString *language = [[NSLocale preferredLanguages] firstObject] ?: @"en";
    return [language hasPrefix:@"zh"] ? zh : en;
}

// Sparkle installs cannot be paused once extraction starts. Ask before
// interrupting active agent work, then let the user proceed anyway.
static BOOL HoxiConfirmBusyUpdate(void) {
    [NSApp activateIgnoringOtherApps:YES];
    NSAlert *alert = [[NSAlert alloc] init];
    alert.alertStyle = NSAlertStyleWarning;
    alert.messageText = HoxiLocalized(@"Hoxi 有任务正在运行", @"Hoxi is still running tasks");
    alert.informativeText = HoxiLocalized(
        @"更新会退出 Hoxi 并中断正在运行的 Agent 任务。仍要现在更新吗？",
        @"Updating will quit Hoxi and interrupt running agent tasks. Update now?");
    [alert addButtonWithTitle:HoxiLocalized(@"仍然更新", @"Update Anyway")];
    [alert addButtonWithTitle:HoxiLocalized(@"取消", @"Cancel")];
    HOXI_TRACE("updater busy confirm shown");
    BOOL confirmed = [alert runModal] == NSAlertFirstButtonReturn;
    HOXI_TRACE("updater busy confirm answered");
    [alert release];
    return confirmed;
}

// Tests substitute this hook to keep the native bridge headless.
static BOOL (*hoxiConfirmUpdateHook)(void);
static BOOL HoxiConfirmUpdate(void) {
    return hoxiConfirmUpdateHook != NULL ? hoxiConfirmUpdateHook() : HoxiConfirmBusyUpdate();
}

@interface HoxiSparkleDelegate : NSObject
@property(nonatomic) unsigned long long token;
@property(nonatomic) BOOL forced;
@property(nonatomic) BOOL active;
@property(nonatomic) BOOL userRequested;
@property(nonatomic) BOOL installing;
@property(nonatomic) BOOL failed;
@property(nonatomic) long long downloadedBytes;
@property(nonatomic) long long totalBytes;
@property(nonatomic, copy) NSString *feed;
@end

@implementation HoxiSparkleDelegate
- (void)dealloc {
    [_feed release];
    [super dealloc];
}
- (NSString *)feedURLStringForUpdater:(id)updater { return self.feed; }
- (BOOL)updater:(id)updater mayPerformUpdateCheck:(NSInteger)check error:(NSError **)error {
    return self.active;
}
- (BOOL)updater:(id)updater shouldProceedWithUpdate:(id)item updateCheck:(NSInteger)check error:(NSError **)error {
    // Discovery must never acquire write admission; only an Install reply does.
    if (self.active) return YES;
    if (error != NULL) {
        // Cocoa supplies the localized cancellation description.
        *error = [NSError errorWithDomain:NSCocoaErrorDomain code:NSUserCancelledError userInfo:nil];
    }
    self.forced = NO;
    self.userRequested = NO;
    self.installing = NO;
    hoxiNativeProgress(self.token, "", 0, 0, NULL);
    hoxiNativeRelease(self.token, 0);
    return NO;
}
- (BOOL)updaterShouldRelaunchApplication:(id)updater {
    // Staging already started; relaunch must not be blocked by new work.
    if (self.active && hoxiNativeCanInstall(self.token, 1)) {
        hoxiNativeProgress(self.token, "restarting", self.downloadedBytes, self.totalBytes, NULL);
        return YES;
    }
    hoxiNativeRelease(self.token, 0);
    return NO;
}
- (void)updater:(id)updater didAbortWithError:(NSError *)error {
    HOXI_TRACE("updater sparkle aborted");
    BOOL wasUserInstall = self.userRequested || self.installing;
    self.forced = NO;
    self.userRequested = NO;
    self.installing = NO;
    BOOL cancelled = error != nil &&
        (([error.domain isEqualToString:NSCocoaErrorDomain] && error.code == NSUserCancelledError) ||
         error.code == 4007);
    if (cancelled) {
        if (!self.failed) {
            hoxiNativeProgress(self.token, "", 0, 0, NULL);
        }
    } else if (error != nil && wasUserInstall && !self.failed) {
        self.failed = YES;
        const char *msg = error.localizedDescription.UTF8String;
        hoxiNativeProgress(self.token, "failed", self.downloadedBytes, self.totalBytes, (char *)(msg ?: "update failed"));
    } else if (!self.failed) {
        hoxiNativeProgress(self.token, "", 0, 0, NULL);
    }
    hoxiNativeRelease(self.token, 0);
}
- (void)updater:(id)updater didFinishUpdateCycleForUpdateCheck:(NSInteger)check error:(NSError *)error {
    HOXI_TRACE("updater sparkle check finished");
    BOOL wasUserInstall = self.userRequested || self.installing;
    self.forced = NO;
    self.userRequested = NO;
    self.installing = NO;
    BOOL cancelled = error != nil &&
        (([error.domain isEqualToString:NSCocoaErrorDomain] && error.code == NSUserCancelledError) ||
         error.code == 4007);
    if (error != nil && !cancelled && wasUserInstall && !self.failed) {
        self.failed = YES;
        const char *msg = error.localizedDescription.UTF8String;
        hoxiNativeProgress(self.token, "failed", self.downloadedBytes, self.totalBytes, (char *)(msg ?: "update failed"));
    } else if (!self.failed) {
        hoxiNativeProgress(self.token, "", 0, 0, NULL);
    }
    hoxiNativeRelease(self.token, 0);
}
- (void)userDidCancelDownload:(id)updater {
    self.forced = NO;
    self.userRequested = NO;
    self.installing = NO;
    self.failed = NO;
    hoxiNativeProgress(self.token, "", 0, 0, NULL);
    hoxiNativeRelease(self.token, 0);
}
- (void)updater:(id)updater willExtractUpdate:(id)item {
    // Manual downloads also prepare an installer. There is no public delegate
    // notification that reliably proves install-on-quit has been disarmed.
    // Retain admission conservatively even if extraction subsequently fails.
    hoxiNativeStaged(self.token);
    if (self.active) {
        hoxiNativeProgress(self.token, "staging", self.downloadedBytes, self.totalBytes, NULL);
    }
}
- (BOOL)updater:(id)updater willInstallUpdateOnQuit:(id)item immediateInstallationBlock:(void (^)(void))handler {
    hoxiNativeStaged(self.token);
    // Do not take ownership of or invoke the silent immediate-install block.
    return NO;
}
@end

// Forward Sparkle's standard UI, intercepting only the user's install replies.
// SPUUserUpdateChoice is NSInteger: Skip=0, Install=1, Dismiss=2.
@interface HoxiSparkleDriver : NSProxy
@property(nonatomic, retain) id<HoxiSparkleUserDriver> target;
@property(nonatomic, retain) HoxiSparkleDelegate *admission;
@property(nonatomic, copy) void (^pendingUpdateReply)(NSInteger);
@property(nonatomic, copy) void (^pendingRelaunchReply)(NSInteger);
- (void)clearPendingReplies;
- (BOOL)acceptPendingInstallIfAvailable;
@end

@implementation HoxiSparkleDriver
- (void)dealloc {
    [_pendingUpdateReply release];
    [_pendingRelaunchReply release];
    [_target release];
    [_admission release];
    [super dealloc];
}
- (void)clearPendingReplies {
    self.pendingUpdateReply = nil;
    self.pendingRelaunchReply = nil;
}
- (BOOL)acceptPendingInstallIfAvailable {
    if (self.pendingRelaunchReply != nil) {
        void (^reply)(NSInteger) = [[self.pendingRelaunchReply retain] autorelease];
        self.pendingRelaunchReply = nil;
        NSInteger choice = [self admittedChoice:1];
        if (choice == 1) {
            hoxiNativeProgress(self.admission.token, "restarting", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
        }
        reply(choice);
        return YES;
    }
    if (self.pendingUpdateReply != nil) {
        void (^reply)(NSInteger) = [[self.pendingUpdateReply retain] autorelease];
        self.pendingUpdateReply = nil;
        self.admission.downloadedBytes = 0;
        self.admission.totalBytes = 0;
        NSInteger choice = [self admittedChoice:1];
        if (choice != 1 && [self.target respondsToSelector:@selector(dismissUpdateInstallation)]) {
            [self.target dismissUpdateInstallation];
        }
        reply(choice);
        return YES;
    }
    return NO;
}
- (BOOL)respondsToSelector:(SEL)selector {
    return class_getInstanceMethod(object_getClass(self), selector) != NULL || [self.target respondsToSelector:selector];
}
- (NSMethodSignature *)methodSignatureForSelector:(SEL)selector {
    return [(id)self.target methodSignatureForSelector:selector];
}
- (void)forwardInvocation:(NSInvocation *)invocation {
    [invocation invokeWithTarget:self.target];
}
- (NSInteger)admittedChoice:(NSInteger)choice {
    if (choice == 1) {
        if (self.admission.active &&
            hoxiNativeCanInstall(self.admission.token, self.admission.forced ? 1 : 0)) {
            self.admission.installing = YES;
            self.admission.failed = NO;
            if (self.admission.downloadedBytes == 0 && self.admission.totalBytes == 0) {
                hoxiNativeProgress(self.admission.token, "downloading", 0, 0, NULL);
            }
            return choice;
        }
        // Never dismiss silently: let the user override active agent work.
        if (self.admission.active && HoxiConfirmUpdate()) {
            self.admission.forced = YES;
            if (hoxiNativeCanInstall(self.admission.token, 1)) {
                self.admission.installing = YES;
                self.admission.failed = NO;
                if (self.admission.downloadedBytes == 0 && self.admission.totalBytes == 0) {
                    hoxiNativeProgress(self.admission.token, "downloading", 0, 0, NULL);
                }
                return choice;
            }
        }
        self.admission.forced = NO;
        self.admission.userRequested = NO;
        self.admission.installing = NO;
        self.admission.downloadedBytes = 0;
        self.admission.totalBytes = 0;
        hoxiNativeProgress(self.admission.token, "", 0, 0, NULL);
        hoxiNativeRelease(self.admission.token, 0);
        return 2; // Deny before Sparkle begins downloading or staging.
    }
    self.admission.forced = NO;
    self.admission.userRequested = NO;
    self.admission.installing = NO;
    self.admission.downloadedBytes = 0;
    self.admission.totalBytes = 0;
    hoxiNativeProgress(self.admission.token, "", 0, 0, NULL);
    hoxiNativeRelease(self.admission.token, 0);
    return choice;
}
- (void)showUserInitiatedUpdateCheckWithCancellation:(void (^)(void))cancellation {
    if (self.admission.active && !self.admission.installing) {
        self.admission.failed = NO;
        hoxiNativeProgress(self.admission.token, "preparing", 0, 0, NULL);
    }
    if ([self.target respondsToSelector:@selector(showUserInitiatedUpdateCheckWithCancellation:)]) {
        [self.target showUserInitiatedUpdateCheckWithCancellation:cancellation];
    }
}
- (void)showUpdateFoundWithAppcastItem:(id)item state:(id)state reply:(void (^)(NSInteger))reply {
    HOXI_TRACE("updater sparkle update found");
    if (self.admission.userRequested) {
        self.pendingUpdateReply = nil;
        NSInteger choice = [self admittedChoice:1];
        if (choice != 1 && [self.target respondsToSelector:@selector(dismissUpdateInstallation)]) {
            [self.target dismissUpdateInstallation];
        }
        reply(choice);
        return;
    }
    self.pendingUpdateReply = reply;
    [self.target showUpdateFoundWithAppcastItem:item state:state reply:^(NSInteger choice) {
        self.pendingUpdateReply = nil;
        reply([self admittedChoice:choice]);
    }];
}
- (void)showDownloadInitiatedWithCancellation:(void (^)(void))cancellation {
    self.pendingUpdateReply = nil;
    if (self.admission.active) {
        self.admission.installing = YES;
        self.admission.failed = NO;
        self.admission.downloadedBytes = 0;
        self.admission.totalBytes = 0;
        hoxiNativeProgress(self.admission.token, "downloading", 0, 0, NULL);
    }
    if ([self.target respondsToSelector:@selector(showDownloadInitiatedWithCancellation:)]) {
        [self.target showDownloadInitiatedWithCancellation:cancellation];
    }
}
- (void)showDownloadDidReceiveExpectedContentLength:(uint64_t)expectedContentLength {
    if (self.admission.active) {
        if (expectedContentLength > 0 && expectedContentLength < (uint64_t)LLONG_MAX) {
            self.admission.totalBytes = (long long)expectedContentLength;
        }
        hoxiNativeProgress(self.admission.token, "downloading", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
    }
    if ([self.target respondsToSelector:@selector(showDownloadDidReceiveExpectedContentLength:)]) {
        [self.target showDownloadDidReceiveExpectedContentLength:expectedContentLength];
    }
}
- (void)showDownloadDidReceiveDataOfLength:(uint64_t)length {
    if (self.admission.active) {
        if (length > 0 && self.admission.downloadedBytes <= LLONG_MAX - (long long)length) {
            self.admission.downloadedBytes += (long long)length;
        }
        if (self.admission.totalBytes > 0 && self.admission.downloadedBytes > self.admission.totalBytes) {
            self.admission.totalBytes = self.admission.downloadedBytes;
        }
        hoxiNativeProgress(self.admission.token, "downloading", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
    }
    if ([self.target respondsToSelector:@selector(showDownloadDidReceiveDataOfLength:)]) {
        [self.target showDownloadDidReceiveDataOfLength:length];
    }
}
- (void)showDownloadDidStartExtractingUpdate {
    if (self.admission.active) {
        hoxiNativeStaged(self.admission.token);
        hoxiNativeProgress(self.admission.token, "staging", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
    }
    if ([self.target respondsToSelector:@selector(showDownloadDidStartExtractingUpdate)]) {
        [self.target showDownloadDidStartExtractingUpdate];
    }
}
- (void)showExtractionReceivedProgress:(double)progress {
    if (self.admission.active) {
        hoxiNativeProgress(self.admission.token, "staging", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
    }
    if ([self.target respondsToSelector:@selector(showExtractionReceivedProgress:)]) {
        [self.target showExtractionReceivedProgress:progress];
    }
}
- (void)showReadyToInstallAndRelaunch:(void (^)(NSInteger))reply {
    if (self.admission.userRequested) {
        self.pendingRelaunchReply = nil;
        NSInteger choice = [self admittedChoice:1];
        if (choice == 1) {
            hoxiNativeProgress(self.admission.token, "restarting", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
        }
        reply(choice);
        return;
    }
    self.pendingRelaunchReply = reply;
    [self.target showReadyToInstallAndRelaunch:^(NSInteger choice) {
        self.pendingRelaunchReply = nil;
        NSInteger admitted = [self admittedChoice:choice];
        if (admitted == 1) {
            hoxiNativeProgress(self.admission.token, "restarting", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
        }
        reply(admitted);
    }];
}
- (void)showInstallingUpdateWithApplicationTerminated:(BOOL)terminated retryTerminatingApplication:(void (^)(void))retry {
    if (self.admission.active) {
        hoxiNativeProgress(self.admission.token, "restarting", self.admission.downloadedBytes, self.admission.totalBytes, NULL);
    }
    if ([self.target respondsToSelector:@selector(showInstallingUpdateWithApplicationTerminated:retryTerminatingApplication:)]) {
        [self.target showInstallingUpdateWithApplicationTerminated:terminated retryTerminatingApplication:retry];
    }
}
- (void)showUpdaterError:(NSError *)error acknowledgement:(void (^)(void))acknowledgement {
    HOXI_TRACE("updater sparkle error");
    [self clearPendingReplies];
    if (self.admission.active) {
        self.admission.installing = NO;
        self.admission.userRequested = NO;
        self.admission.failed = YES;
        const char *msg = error ? error.localizedDescription.UTF8String : NULL;
        hoxiNativeProgress(self.admission.token, "failed", self.admission.downloadedBytes, self.admission.totalBytes, (char *)(msg ?: "update failed"));
    }
    if ([self.target respondsToSelector:@selector(showUpdaterError:acknowledgement:)]) {
        [self.target showUpdaterError:error acknowledgement:acknowledgement];
    } else if (acknowledgement != nil) {
        acknowledgement();
    }
}
- (void)showUpdateNotFoundWithError:(NSError *)error acknowledgement:(void (^)(void))acknowledgement {
    HOXI_TRACE("updater sparkle up to date");
    [self clearPendingReplies];
    if (self.admission.active) {
        BOOL wasRequested = self.admission.userRequested;
        self.admission.installing = NO;
        self.admission.userRequested = NO;
        if (wasRequested) {
            self.admission.failed = YES;
            const char *msg = error ? error.localizedDescription.UTF8String : NULL;
            hoxiNativeProgress(self.admission.token, "failed", 0, 0, (char *)(msg ?: "application is already up to date"));
        } else if (!self.admission.failed) {
            hoxiNativeProgress(self.admission.token, "", 0, 0, NULL);
        }
    }
    if ([self.target respondsToSelector:@selector(showUpdateNotFoundWithError:acknowledgement:)]) {
        [self.target showUpdateNotFoundWithError:error acknowledgement:acknowledgement];
    } else if (acknowledgement != nil) {
        acknowledgement();
    }
}
@end

static id<HoxiSparkleUpdater> updater;
static HoxiSparkleDriver *driver;

static HoxiSparkleDelegate *delegate;
static NSMutableArray *retiredDelegates;

static void onMain(void (^block)(void)) {
    if (NSThread.isMainThread) block();
    else dispatch_sync(dispatch_get_main_queue(), block);
}

char *hoxi_native_start(unsigned long long token) {
    __block char *failure = NULL;
    onMain(^{ @autoreleasepool {
        if (updater != nil) {
            failure = strdup("native updater is already started");
            return;
        }
        NSBundle *host = NSBundle.mainBundle;
        id key = [host objectForInfoDictionaryKey:@"SUPublicEDKey"];
        id feed = [host objectForInfoDictionaryKey:@"SUFeedURL"];
        NSURL *url = [feed isKindOfClass:NSString.class] ? [NSURL URLWithString:feed] : nil;
        NSData *publicKey = [key isKindOfClass:NSString.class] ?
            [[[NSData alloc] initWithBase64EncodedString:key options:0] autorelease] : nil;
        if (![host.bundlePath.pathExtension isEqualToString:@"app"] || publicKey.length != 32 ||
            ![url.scheme.lowercaseString isEqualToString:@"https"] || url.host.length == 0) {
            failure = strdup("native updater requires an .app with SUPublicEDKey and an HTTPS SUFeedURL");
            return;
        }
        id automatic = [host objectForInfoDictionaryKey:@"SUAllowsAutomaticUpdates"];
        if (![automatic isKindOfClass:NSNumber.class] || [automatic boolValue]) {
            failure = strdup("native updater requires SUAllowsAutomaticUpdates=false to prevent automatic installation");
            return;
        }
        NSString *path = [host.privateFrameworksPath stringByAppendingPathComponent:@"Sparkle.framework"];
        NSBundle *framework = [NSBundle bundleWithPath:path];
        NSError *error = nil;
        if (framework == nil || ![framework loadAndReturnError:&error]) {
            failure = strdup(error ? error.localizedDescription.UTF8String : "bundled Sparkle.framework is unavailable");
            return;
        }
        Class updaterClass = [framework classNamed:@"SPUUpdater"];
        Class driverClass = [framework classNamed:@"SPUStandardUserDriver"];
        if (updaterClass == Nil || driverClass == Nil ||
            ![updaterClass instancesRespondToSelector:@selector(initWithHostBundle:applicationBundle:userDriver:delegate:)] ||
            ![driverClass instancesRespondToSelector:@selector(initWithHostBundle:delegate:)]) {
            failure = strdup("bundled framework does not provide Sparkle 2 updater/user driver");
            return;
        }
        delegate = [[HoxiSparkleDelegate alloc] init];
        delegate.token = token;
        delegate.active = YES;
        delegate.feed = feed; // Ignore legacy user-default feed overrides.
        driver = [HoxiSparkleDriver alloc];
        driver.target = [(id<HoxiSparkleUserDriver>)[driverClass alloc] initWithHostBundle:host delegate:nil];
        [driver.target release];
        driver.admission = delegate;
        updater = [(id<HoxiSparkleUpdater>)[updaterClass alloc] initWithHostBundle:host applicationBundle:host userDriver:driver delegate:delegate];
        updater.automaticallyDownloadsUpdates = NO;
        updater.automaticallyChecksForUpdates = YES;
        // Report configuration failures to Go before beginning a scheduled cycle.
        if (updater == nil || ![updater startUpdater:&error]) {
            failure = strdup(error ? error.localizedDescription.UTF8String : "Sparkle updater failed to start");
            delegate.active = NO;
            [updater release]; updater = nil;
            [driver release]; driver = nil;
            [delegate release]; delegate = nil;
        }
    }});
    return failure;
}

int hoxi_native_available(void) {
    __block int available = 0;
    onMain(^{ available = updater != nil && delegate.active; });
    return available;
}

char *hoxi_native_check(void) {
    __block char *failure = NULL;
    onMain(^{ @autoreleasepool {
        if (updater == nil || !delegate.active) failure = strdup("native updater is unavailable");
        else {
            delegate.userRequested = YES;
            delegate.failed = NO;
            if (!delegate.installing) {
                delegate.downloadedBytes = 0;
                delegate.totalBytes = 0;
                hoxiNativeProgress(delegate.token, "preparing", 0, 0, NULL);
            }
            // Repeated clicks or a background-discovered update must immediately
            // proceed to install instead of stalling on a duplicate check cycle.
            [NSApp activateIgnoringOtherApps:YES];
            if ([driver acceptPendingInstallIfAvailable]) {
                HOXI_TRACE("updater sparkle alert reused");
                return;
            }
            if ([driver.target respondsToSelector:@selector(activeUpdateAlert)]) {
                id alert = [driver.target activeUpdateAlert];
                if (alert != nil && [alert respondsToSelector:@selector(installUpdate:)]) {
                    HOXI_TRACE("updater sparkle alert reused");
                    [alert installUpdate:nil];
                    return;
                }
            }
            HOXI_TRACE("updater sparkle check started");
            [updater checkForUpdates];
        }
    }});
    return failure;
}

void hoxi_native_stop(void) {
    onMain(^{ @autoreleasepool {
        if (updater == nil) return;
        delegate.active = NO;
        delegate.userRequested = NO;
        delegate.installing = NO;
        delegate.failed = NO;
        updater.automaticallyChecksForUpdates = NO;
        [driver clearPendingReplies];
        [updater resetUpdateCycle];
        [driver.target dismissUpdateInstallation];
        hoxiNativeProgress(delegate.token, "", 0, 0, NULL);
        hoxiNativeRelease(delegate.token, 1);
        // No public SPUUpdater stop method exists. Keep a denying delegate alive
        // for any in-flight driver that outlives its controller. Do not unload
        // the framework while its classes/installer callbacks may still exist.
        if (retiredDelegates == nil) retiredDelegates = [[NSMutableArray alloc] init];
        [retiredDelegates addObject:delegate];
        [updater release]; updater = nil;
        [driver release]; driver = nil;
        [delegate release]; delegate = nil;
    }});
}

int hoxi_native_confirm_busy(void) {
    __block int confirmed = 0;
    onMain(^{ @autoreleasepool {
        [NSApp activateIgnoringOtherApps:YES];
        confirmed = HoxiConfirmUpdate() ? 1 : 0;
    }});
    return confirmed;
}
