#import "../native_darwin.m"
#include <assert.h>

static int checks;
static int allowed;
int hoxiNativeCanInstall(unsigned long long token) { checks++; return allowed; }
void hoxiNativeRelease(unsigned long long token, int stop) {}
void hoxiNativeStaged(unsigned long long token) {}

@interface ChoiceDriver : NSObject
@property(nonatomic, copy) void (^reply)(NSInteger);
@property(nonatomic) int focused;
@end
@implementation ChoiceDriver
- (void)showUpdateFoundWithAppcastItem:(id)item state:(id)state reply:(void (^)(NSInteger))reply { self.reply = reply; }
- (void)showReadyToInstallAndRelaunch:(void (^)(NSInteger))reply { self.reply = reply; }
- (void)showUpdateInFocus { self.focused++; }
- (void)dealloc { [_reply release]; [super dealloc]; }
@end

int main(void) { @autoreleasepool {
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
    standard.reply = nil;
    [proxy release]; [standard release]; [admission release];
} return 0; }
