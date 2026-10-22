//go:build darwin

#import <Cocoa/Cocoa.h>
#import <string.h>

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
        // Fit the mark itself, rather than the app icon's padded 64-point canvas.
        CGFloat scale = size.width / 48.0;
        CGContextScaleCTM(cg, scale, scale);
        CGContextTranslateCTM(cg, -8.0, -4.0);

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

static NSImage *hoxi_create_statusbar_summary(int attention, int running, int available) {
    NSImage *icon = hoxi_create_statusbar_icon();
    if (!available || (attention <= 0 && running <= 0)) {
        return icon;
    }

    NSDictionary *attributes = @{
        NSFontAttributeName: [NSFont monospacedDigitSystemFontOfSize:10.0 weight:NSFontWeightSemibold],
        NSForegroundColorAttributeName: [NSColor blackColor],
    };
    NSString *runningText = running > 0 ? (running > 99 ? @"99+" : [NSString stringWithFormat:@"%d", running]) : @"";
    NSString *attentionText = attention > 0 ? (attention > 99 ? @"!99+" : [NSString stringWithFormat:@"!%d", attention]) : @"";
    CGFloat runningWidth = running > 0 ? MAX(18.0, ceil([runningText sizeWithAttributes:attributes].width) + 10.0) : 0.0;
    CGFloat attentionWidth = attention > 0 ? MAX(18.0, ceil([attentionText sizeWithAttributes:attributes].width) + 10.0) : 0.0;
    CGFloat width = 18.0 + (running > 0 ? runningWidth + 4.0 : 0.0) + (attention > 0 ? attentionWidth + 4.0 : 0.0);

    NSImage *image = [NSImage imageWithSize:NSMakeSize(width, 18.0) flipped:YES drawingHandler:^BOOL(NSRect dstRect) {
        [icon drawInRect:NSMakeRect(0.0, 0.0, 18.0, 18.0) fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1.0 respectFlipped:YES hints:nil];
        CGFloat x = 22.0;
        for (NSUInteger index = 0; index < 2; index++) {
            NSString *text = index == 0 ? runningText : attentionText;
            CGFloat badgeWidth = index == 0 ? runningWidth : attentionWidth;
            if (badgeWidth == 0.0) {
                continue;
            }
            NSRect rect = NSMakeRect(x, 1.5, badgeWidth, 15.0);
            NSBezierPath *pill = [NSBezierPath bezierPathWithRoundedRect:NSInsetRect(rect, 0.5, 0.5) xRadius:5.0 yRadius:5.0];
            [[NSColor blackColor] set];
            if (index == 0) {
                pill.lineWidth = 1.0;
                [pill stroke];
            } else {
                [pill fill];
            }
            NSSize textSize = [text sizeWithAttributes:attributes];
            NSPoint origin = NSMakePoint(x + (badgeWidth - textSize.width) / 2.0, NSMidY(rect) - textSize.height / 2.0);
            [NSGraphicsContext saveGraphicsState];
            // Knock attention digits out of the template so system tinting stays legible.
            if (index == 1) {
                CGContextSetBlendMode([[NSGraphicsContext currentContext] CGContext], kCGBlendModeDestinationOut);
            }
            [text drawAtPoint:origin withAttributes:attributes];
            [NSGraphicsContext restoreGraphicsState];
            x += badgeWidth + 4.0;
        }
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
        button.imagePosition = NSImageOnly;
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

void hoxi_statusbar_update(int attention, int running, int queued, int available, const char *badge) {
    // The block may run later on the main queue, so own the text before Go frees it.
    char *badgeCopy = badge != NULL ? strdup(badge) : NULL;
    hoxi_run_on_main(^{
        NSString *badgeText = badgeCopy != NULL ? [NSString stringWithUTF8String:badgeCopy] : @"";
        free(badgeCopy);
        if (hoxiStatusBarController == nil) {
            return;
        }

        NSStatusBarButton *button = hoxiStatusBarController.statusItem.button;
        button.image = hoxi_create_statusbar_summary(attention, running, available);
        if (!available) {
            button.toolTip = @"Hoxi: OpenCode 引擎正在启动";
            hoxiStatusBarController.summaryItem.title = @"OpenCode 正在连接...";
            [NSApp dockTile].badgeLabel = @"";
            return;
        }
        button.toolTip = [NSString stringWithFormat:@"Hoxi：待审批 %d · 运行中 %d", attention, running];
        hoxiStatusBarController.summaryItem.title = [NSString stringWithFormat:@"待审批 %d · 运行中 %d", attention, running];
        [NSApp dockTile].badgeLabel = badgeText;
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
