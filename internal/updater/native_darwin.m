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
@end

static NSString *HoxiLocalized(NSString *zh, NSString *en) {
    NSString *language = [[NSLocale preferredLanguages] firstObject] ?: @"en";
    return [language hasPrefix:@"zh"] ? zh : en;
}

// Sparkle installs cannot be paused once extraction starts. Ask before
// interrupting active agent work, then let the user proceed anyway.
static BOOL HoxiConfirmBusyUpdate(void) {
    NSAlert *alert = [[NSAlert alloc] init];
    alert.alertStyle = NSAlertStyleWarning;
    alert.messageText = HoxiLocalized(@"Hoxi 有任务正在运行", @"Hoxi is still running tasks");
    alert.informativeText = HoxiLocalized(
        @"更新会退出 Hoxi 并中断正在运行的 Agent 任务。仍要现在更新吗？",
        @"Updating will quit Hoxi and interrupt running agent tasks. Update now?");
    [alert addButtonWithTitle:HoxiLocalized(@"仍然更新", @"Update Anyway")];
    [alert addButtonWithTitle:HoxiLocalized(@"取消", @"Cancel")];
    BOOL confirmed = [alert runModal] == NSAlertFirstButtonReturn;
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
    hoxiNativeRelease(self.token, 0);
    return NO;
}
- (BOOL)updaterShouldRelaunchApplication:(id)updater {
    // Staging already started; relaunch must not be blocked by new work.
    if (self.active && hoxiNativeCanInstall(self.token, 1)) return YES;
    hoxiNativeRelease(self.token, 0);
    return NO;
}
- (void)updater:(id)updater didAbortWithError:(NSError *)error {
    self.forced = NO;
    hoxiNativeRelease(self.token, 0);
}
- (void)updater:(id)updater didFinishUpdateCycleForUpdateCheck:(NSInteger)check error:(NSError *)error {
    self.forced = NO;
    hoxiNativeRelease(self.token, 0);
}
- (void)userDidCancelDownload:(id)updater {
    self.forced = NO;
    hoxiNativeRelease(self.token, 0);
}
- (void)updater:(id)updater willExtractUpdate:(id)item {
    // Manual downloads also prepare an installer. There is no public delegate
    // notification that reliably proves install-on-quit has been disarmed.
    // Retain admission conservatively even if extraction subsequently fails.
    hoxiNativeStaged(self.token);
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
@end

@implementation HoxiSparkleDriver
- (void)dealloc {
    [_target release];
    [_admission release];
    [super dealloc];
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
            hoxiNativeCanInstall(self.admission.token, self.admission.forced ? 1 : 0)) return choice;
        // Never dismiss silently: let the user override active agent work.
        if (self.admission.active && HoxiConfirmUpdate()) {
            self.admission.forced = YES;
            if (hoxiNativeCanInstall(self.admission.token, 1)) return choice;
        }
        self.admission.forced = NO;
        hoxiNativeRelease(self.admission.token, 0);
        return 2; // Deny before Sparkle begins downloading or staging.
    }
    self.admission.forced = NO;
    hoxiNativeRelease(self.admission.token, 0);
    return choice;
}
- (void)showUpdateFoundWithAppcastItem:(id)item state:(id)state reply:(void (^)(NSInteger))reply {
    [self.target showUpdateFoundWithAppcastItem:item state:state reply:^(NSInteger choice) {
        reply([self admittedChoice:choice]);
    }];
}
- (void)showReadyToInstallAndRelaunch:(void (^)(NSInteger))reply {
    [self.target showReadyToInstallAndRelaunch:^(NSInteger choice) {
        reply([self admittedChoice:choice]);
    }];
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
            // Repeated clicks must refocus an in-progress update instead of failing.
            // Sparkle's -checkForUpdates shows or refocuses the current session.
            [NSApp activateIgnoringOtherApps:YES];
            [updater checkForUpdates];
        }
    }});
    return failure;
}

void hoxi_native_stop(void) {
    onMain(^{ @autoreleasepool {
        if (updater == nil) return;
        delegate.active = NO;
        updater.automaticallyChecksForUpdates = NO;
        [updater resetUpdateCycle];
        [driver.target dismissUpdateInstallation];
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
