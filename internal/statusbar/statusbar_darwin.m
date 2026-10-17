//go:build darwin

#import <Cocoa/Cocoa.h>

extern void HoxiShowMainWindow(void);
extern void HoxiQuitApplication(void);

@interface HoxiStatusBarController : NSObject
@property(nonatomic, strong) NSStatusItem *statusItem;
@property(nonatomic, strong) NSMenuItem *summaryItem;
@end

@implementation HoxiStatusBarController

- (void)showMainWindow:(id)sender {
    HoxiShowMainWindow();
}

- (void)quitApplication:(id)sender {
    HoxiQuitApplication();
}

@end

static HoxiStatusBarController *hoxiStatusBarController;

static void hoxi_run_on_main(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), block);
}

void hoxi_statusbar_start(void) {
    hoxi_run_on_main(^{
        if (hoxiStatusBarController != nil) {
            return;
        }

        hoxiStatusBarController = [HoxiStatusBarController new];
        hoxiStatusBarController.statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];

        NSStatusBarButton *button = hoxiStatusBarController.statusItem.button;
        if (@available(macOS 11.0, *)) {
            button.image = [NSImage imageWithSystemSymbolName:@"terminal.fill" accessibilityDescription:@"Hoxi"];
            button.image.template = YES;
            button.imagePosition = NSImageLeft;
        } else {
            button.title = @"Hoxi";
        }
        button.toolTip = @"Hoxi Workspace";

        NSMenu *menu = [NSMenu new];
        NSMenuItem *openWorkspace = [[NSMenuItem alloc] initWithTitle:@"唤醒 Hoxi 工作台"
                                                               action:@selector(showMainWindow:)
                                                        keyEquivalent:@""];
        openWorkspace.target = hoxiStatusBarController;
        [menu addItem:openWorkspace];

        hoxiStatusBarController.summaryItem = [[NSMenuItem alloc] initWithTitle:@"Agent: Idle"
                                                                           action:nil
                                                                    keyEquivalent:@""];
        hoxiStatusBarController.summaryItem.enabled = NO;
        [menu addItem:hoxiStatusBarController.summaryItem];
        [menu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *quitApplication = [[NSMenuItem alloc] initWithTitle:@"退出 Hoxi"
                                                                 action:@selector(quitApplication:)
                                                          keyEquivalent:@"q"];
        quitApplication.target = hoxiStatusBarController;
        [menu addItem:quitApplication];
        hoxiStatusBarController.statusItem.menu = menu;
        [NSApp dockTile].badgeLabel = @"";
    });
}

void hoxi_statusbar_update(int attention, int running, int queued, int available) {
    hoxi_run_on_main(^{
        if (hoxiStatusBarController == nil) {
            return;
        }

        NSStatusBarButton *button = hoxiStatusBarController.statusItem.button;
        if (@available(macOS 11.0, *)) {
            if (!available) {
                button.title = @"";
            } else if (attention > 0) {
                button.title = attention > 99 ? @" !99+" : [NSString stringWithFormat:@" !%d", attention];
            } else if (running > 0) {
                button.title = [NSString stringWithFormat:@" •%d", running];
            } else {
                button.title = @"";
            }
        }
        if (!available) {
            button.toolTip = @"Hoxi: OpenCode 引擎正在启动";
            hoxiStatusBarController.summaryItem.title = @"OpenCode 正在连接...";
            [NSApp dockTile].badgeLabel = @"";
            return;
        }
        button.toolTip = [NSString stringWithFormat:@"Hoxi：待审批 %d · 运行中 %d", attention, running];
        hoxiStatusBarController.summaryItem.title = [NSString stringWithFormat:@"待审批 %d · 运行中 %d", attention, running];
        [NSApp dockTile].badgeLabel = attention > 0 ? [NSString stringWithFormat:@"%d", attention] : @"";
    });
}

void hoxi_statusbar_stop(void) {
    hoxi_run_on_main(^{
        [NSApp dockTile].badgeLabel = @"";
        if (hoxiStatusBarController != nil) {
            [[NSStatusBar systemStatusBar] removeStatusItem:hoxiStatusBarController.statusItem];
            hoxiStatusBarController = nil;
        }
    });
}
