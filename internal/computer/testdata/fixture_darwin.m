#import <Cocoa/Cocoa.h>

@interface ComputerFixture : NSObject <NSApplicationDelegate, NSTextFieldDelegate>
@property(nonatomic, strong) NSWindow *window;
@property(nonatomic, strong) NSTextField *field;
@property(nonatomic, copy) NSString *output;
@property(nonatomic) NSInteger clicks;
@end

@implementation ComputerFixture
- (void)save {
    NSDictionary *state = @{ @"clicks": @(self.clicks), @"text": self.field.stringValue };
    NSData *data = [NSJSONSerialization dataWithJSONObject:state options:0 error:nil];
    [data writeToFile:self.output atomically:YES];
}
- (void)clicked:(id)sender { self.clicks++; [self save]; }
- (void)controlTextDidChange:(NSNotification *)notification { [self save]; }
- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    self.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 520, 280)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
        backing:NSBackingStoreBuffered defer:NO];
    self.window.title = @"Computer control fixture";
    self.field = [[NSTextField alloc] initWithFrame:NSMakeRect(40, 170, 420, 32)];
    self.field.stringValue = @"ready";
    self.field.accessibilityLabel = @"Fixture input";
    self.field.delegate = self;
    [self.window.contentView addSubview:self.field];
    NSButton *button = [[NSButton alloc] initWithFrame:NSMakeRect(40, 100, 170, 40)];
    button.title = @"Fixture click";
    button.bezelStyle = NSBezelStyleRounded;
    button.target = self;
    button.action = @selector(clicked:);
    [self.window.contentView addSubview:button];
    [self.window center];
    [self.window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [self save];
}
@end

int main(int argc, const char **argv) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        ComputerFixture *fixture = [ComputerFixture new];
        fixture.output = [NSString stringWithUTF8String:argv[1]];
        NSApp.delegate = fixture;
        [NSApp run];
    }
}
