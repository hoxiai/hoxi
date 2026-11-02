#import "../native_darwin.m"
#include <assert.h>

static int checks;
static int allowed;
static char lastPhase[32];
static long long lastDownloaded;
static long long lastTotal;
static char lastError[128];

int hoxiNativeCanInstall(unsigned long long token, int force) { checks++; return allowed; }
void hoxiNativeRelease(unsigned long long token, int stop) {}
void hoxiNativeStaged(unsigned long long token) {}
void hoxiNativeTrace(char *event) {}
void hoxiNativeProgress(unsigned long long token, char *phase, long long downloaded, long long total, char *errMsg) {
    strncpy(lastPhase, phase ? phase : "", sizeof(lastPhase) - 1);
    lastPhase[sizeof(lastPhase) - 1] = '\0';
    lastDownloaded = downloaded;
    lastTotal = total;
    strncpy(lastError, errMsg ? errMsg : "", sizeof(lastError) - 1);
    lastError[sizeof(lastError) - 1] = '\0';
}

@interface ChoiceDriver : NSObject
@property(nonatomic, copy) void (^reply)(NSInteger);
@property(nonatomic) int focused;
@property(nonatomic) int dismissed;
@end
@implementation ChoiceDriver
- (void)showUpdateFoundWithAppcastItem:(id)item state:(id)state reply:(void (^)(NSInteger))reply { self.reply = reply; }
- (void)showReadyToInstallAndRelaunch:(void (^)(NSInteger))reply { self.reply = reply; }
- (void)dismissUpdateInstallation { self.dismissed++; }
- (void)showUpdateInFocus { self.focused++; }
- (void)dealloc { [_reply release]; [super dealloc]; }
@end

static BOOL DenyConfirm(void) { return NO; }

int main(void) { @autoreleasepool {
    hoxiConfirmUpdateHook = DenyConfirm;
    HoxiSparkleDelegate *admission = [[HoxiSparkleDelegate alloc] init];
    admission.active = YES;
    admission.token = 7;
    ChoiceDriver *standard = [[ChoiceDriver alloc] init];
    HoxiSparkleDriver *proxy = [HoxiSparkleDriver alloc];
    proxy.target = (id)standard;
    proxy.admission = admission;
    __block NSInteger result = -1;
    assert([admission updater:nil shouldProceedWithUpdate:nil updateCheck:0 error:NULL]);
    [proxy showUpdateFoundWithAppcastItem:nil state:nil reply:^(NSInteger choice) { result = choice; }];
    assert(checks == 0 && result == -1); // Merely showing or ignoring an update cannot drain.
    standard.reply(2);
    assert(checks == 0 && result == 2); // Dismissing does not check installation admission.
    allowed = 0;
    standard.reply(1);
    assert(checks == 1 && result == 2); // Busy: never forward Install to Sparkle.
    allowed = 1;
    standard.reply(1);
    assert(checks == 2 && result == 1);
    [proxy showReadyToInstallAndRelaunch:^(NSInteger choice) { result = choice; }];
    assert(checks == 2);
    standard.reply(1);
    assert(checks == 3 && result == 1);
    admission.active = NO;
    standard.reply(1);
    assert(checks == 3 && result == 2);
    assert([proxy respondsToSelector:@selector(showUpdateInFocus)]);
    [(id)proxy showUpdateInFocus];
    assert(standard.focused == 1); // Standard UI methods are still forwarded.

    // User-initiated in-app update auto-admits and reports progress phases.
    admission.active = YES;
    admission.userRequested = YES;
    allowed = 1;
    result = -1;
    [proxy showUserInitiatedUpdateCheckWithCancellation:^{}];
    assert(strcmp(lastPhase, "preparing") == 0);
    [proxy showUpdateFoundWithAppcastItem:nil state:nil reply:^(NSInteger choice) { result = choice; }];
    assert(result == 1 && strcmp(lastPhase, "downloading") == 0);
    [proxy showDownloadDidReceiveExpectedContentLength:1000];
    [proxy showDownloadDidReceiveDataOfLength:400];
    assert(strcmp(lastPhase, "downloading") == 0 && lastDownloaded == 400 && lastTotal == 1000);
    [proxy showDownloadDidStartExtractingUpdate];
    assert(strcmp(lastPhase, "staging") == 0);
    result = -1;
    [proxy showReadyToInstallAndRelaunch:^(NSInteger choice) { result = choice; }];
    assert(result == 1 && strcmp(lastPhase, "restarting") == 0);

    // Denied user-initiated update dismisses and clears progress.
    admission.userRequested = YES;
    admission.installing = NO;
    allowed = 0;
    result = -1;
    [proxy showUpdateFoundWithAppcastItem:nil state:nil reply:^(NSInteger choice) { result = choice; }];
    assert(result == 2 && standard.dismissed == 1 && strcmp(lastPhase, "") == 0);

    standard.reply = nil;
    [proxy release]; [standard release]; [admission release];
} return 0; }
