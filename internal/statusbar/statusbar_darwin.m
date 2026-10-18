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

static NSImage *hoxi_create_statusbar_icon(void) {
    NSSize size = NSMakeSize(18.0, 18.0);
    NSImage *image = [NSImage imageWithSize:size flipped:YES drawingHandler:^BOOL(NSRect dstRect) {
        CGContextRef cg = [[NSGraphicsContext currentContext] CGContext];
        if (cg == NULL) {
            return NO;
        }
        CGContextSaveGState(cg);
        CGFloat scale = size.width / 64.0;
        CGContextScaleCTM(cg, scale, scale);
        CGContextTranslateCTM(cg, 0.0, 2.5);

        [[NSColor blackColor] setFill];
        [[NSColor blackColor] setStroke];

        // 1. Roof (合之檐)
        CGMutablePathRef roof = CGPathCreateMutable();
        CGPathMoveToPoint(roof, NULL, 32.0, 8.0);
        CGPathAddLineToPoint(roof, NULL, 52.0, 21.0);
        CGPathAddLineToPoint(roof, NULL, 44.0, 21.0);
        CGPathAddLineToPoint(roof, NULL, 32.0, 13.0);
        CGPathAddLineToPoint(roof, NULL, 20.0, 21.0);
        CGPathAddLineToPoint(roof, NULL, 12.0, 21.0);
        CGPathCloseSubpath(roof);
        CGContextAddPath(cg, roof);
        CGContextFillPath(cg);
        CGPathRelease(roof);

        // 2. Eyes (H之目)
        CGPathRef leftEye = CGPathCreateWithRoundedRect(CGRectMake(20.0, 25.0, 6.5, 12.0), 3.25, 3.25, NULL);
        CGContextAddPath(cg, leftEye);
        CGContextFillPath(cg);
        CGPathRelease(leftEye);

        CGPathRef rightEye = CGPathCreateWithRoundedRect(CGRectMake(37.5, 25.0, 6.5, 12.0), 3.25, 3.25, NULL);
        CGContextAddPath(cg, rightEye);
        CGContextFillPath(cg);
        CGPathRelease(rightEye);

        // 3. Smile (喜之弧)
        CGMutablePathRef smile = CGPathCreateMutable();
        CGPathMoveToPoint(smile, NULL, 19.0, 42.5);
        CGPathAddCurveToPoint(smile, NULL, 23.0, 50.0, 41.0, 50.0, 45.0, 42.5);
        CGContextAddPath(cg, smile);
        CGContextSetLineWidth(cg, 5.5);
        CGContextSetLineCap(cg, kCGLineCapRound);
        CGContextStrokePath(cg);
        CGPathRelease(smile);

        CGContextRestoreGState(cg);
        return YES;
    }];
    image.template = YES;
    image.accessibilityDescription = @"Hoxi";
    return image;
}

void hoxi_statusbar_start(void) {
    hoxi_run_on_main(^{
        if (hoxiStatusBarController != nil) {
            return;
        }

        hoxiStatusBarController = [HoxiStatusBarController new];
        hoxiStatusBarController.statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];

        NSStatusBarButton *button = hoxiStatusBarController.statusItem.button;
        button.image = hoxi_create_statusbar_icon();
        button.imagePosition = NSImageLeft;
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
