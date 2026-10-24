//go:build darwin

#import <Cocoa/Cocoa.h>
#import <string.h>

extern void HoxiShowMainWindow(void);
extern void HoxiTriggerMenuAction(const char *kind, const char *sessionID);
extern void HoxiQuitApplication(void);

@interface HoxiStatusBarController : NSObject
@property(nonatomic, strong) NSStatusItem *statusItem;
@property(nonatomic, strong) NSMenu *menu;
@property(nonatomic, strong) NSMenuItem *attentionItem;
@property(nonatomic, strong) NSMenuItem *runningItem;
@property(nonatomic, strong) NSMenuItem *keepAwakeItem;
@property(nonatomic, strong) NSMutableArray<NSMenuItem *> *pendingSessionItems;
@property(nonatomic, strong) NSMutableArray<NSMenuItem *> *runningSessionItems;
@end

@implementation HoxiStatusBarController

- (instancetype)init {
    self = [super init];
    if (self != nil) {
        _pendingSessionItems = [[NSMutableArray alloc] init];
        _runningSessionItems = [[NSMutableArray alloc] init];
    }
    return self;
}

- (void)dealloc {
    [_statusItem release];
    [_menu release];
    [_attentionItem release];
    [_runningItem release];
    [_keepAwakeItem release];
    [_pendingSessionItems release];
    [_runningSessionItems release];
    [super dealloc];
}

- (void)showMainWindow:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiShowMainWindow();
}

- (void)openAttentionTasks:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiTriggerMenuAction("attention", "");
}

- (void)openPendingSession:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    id represented = [sender isKindOfClass:[NSMenuItem class]] ? [(NSMenuItem *)sender representedObject] : nil;
    NSString *sessionID = [represented isKindOfClass:[NSString class]] ? (NSString *)represented : @"";
    HoxiTriggerMenuAction("attention", [sessionID UTF8String]);
}

- (void)openRunningTasks:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiTriggerMenuAction("running", "");
}

- (void)openRunningSession:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    id represented = [sender isKindOfClass:[NSMenuItem class]] ? [(NSMenuItem *)sender representedObject] : nil;
    NSString *sessionID = [represented isKindOfClass:[NSString class]] ? (NSString *)represented : @"";
    HoxiTriggerMenuAction("running", [sessionID UTF8String]);
}

- (void)newThread:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiTriggerMenuAction("new-thread", "");
}

- (void)openHandoff:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiTriggerMenuAction("handoff", "");
}

- (void)toggleKeepAwake:(id)sender {
    HoxiTriggerMenuAction("toggle-keep-awake", "");
}

- (void)openApprovalPolicy:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiTriggerMenuAction("policy", "");
}

- (void)openSettings:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiTriggerMenuAction("settings", "");
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

static void hoxi_rebuild_session_items(
    NSMenu *menu,
    NSMenuItem *anchorItem,
    NSMutableArray<NSMenuItem *> *trackedItems,
    NSString *encodedLines,
    id target,
    SEL action
) {
    if (menu == nil || anchorItem == nil || trackedItems == nil) {
        return;
    }
    for (NSMenuItem *oldItem in trackedItems) {
        if ([menu indexOfItem:oldItem] >= 0) {
            [menu removeItem:oldItem];
        }
    }
    [trackedItems removeAllObjects];
    if (encodedLines.length == 0) {
        return;
    }
    NSInteger insertIndex = [menu indexOfItem:anchorItem];
    if (insertIndex < 0) {
        return;
    }
    NSArray<NSString *> *lines = [encodedLines componentsSeparatedByString:@"\n"];
    for (NSString *line in lines) {
        NSRange tab = [line rangeOfString:@"\t"];
        if (tab.location == NSNotFound || tab.location == 0) {
            continue;
        }
        NSString *sessionID = [line substringToIndex:tab.location];
        NSString *label = [line substringFromIndex:tab.location + 1];
        if (sessionID.length == 0 || label.length == 0) {
            continue;
        }
        NSMenuItem *item = [[[NSMenuItem alloc] initWithTitle:label
                                                       action:action
                                                keyEquivalent:@""] autorelease];
        item.target = target;
        item.representedObject = sessionID;
        insertIndex++;
        [menu insertItem:item atIndex:insertIndex];
        [trackedItems addObject:item];
    }
}

void hoxi_statusbar_start(void) {
    hoxi_run_on_main(^{
        @autoreleasepool {
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
            menu.autoenablesItems = NO;

            NSMenuItem *openWorkspace = [[NSMenuItem alloc] initWithTitle:@"唤醒 Hoxi 工作台"
                                                                   action:@selector(showMainWindow:)
                                                            keyEquivalent:@""];
            openWorkspace.target = hoxiStatusBarController;
            [menu addItem:openWorkspace];
            [openWorkspace release];

            [menu addItem:[NSMenuItem separatorItem]];

            hoxiStatusBarController.attentionItem = [[[NSMenuItem alloc] initWithTitle:@"待审批任务 (0)"
                                                                                action:@selector(openAttentionTasks:)
                                                                         keyEquivalent:@""] autorelease];
            hoxiStatusBarController.attentionItem.target = hoxiStatusBarController;
            [menu addItem:hoxiStatusBarController.attentionItem];

            hoxiStatusBarController.runningItem = [[[NSMenuItem alloc] initWithTitle:@"运行中任务 (0)"
                                                                              action:@selector(openRunningTasks:)
                                                                       keyEquivalent:@""] autorelease];
            hoxiStatusBarController.runningItem.target = hoxiStatusBarController;
            [menu addItem:hoxiStatusBarController.runningItem];

            [menu addItem:[NSMenuItem separatorItem]];

            NSMenuItem *newThreadItem = [[NSMenuItem alloc] initWithTitle:@"新建对话"
                                                                   action:@selector(newThread:)
                                                            keyEquivalent:@"n"];
            newThreadItem.target = hoxiStatusBarController;
            [menu addItem:newThreadItem];
            [newThreadItem release];

            NSMenuItem *handoffItem = [[NSMenuItem alloc] initWithTitle:@"导入外部 CLI 会话…"
                                                                 action:@selector(openHandoff:)
                                                          keyEquivalent:@""];
            handoffItem.target = hoxiStatusBarController;
            [menu addItem:handoffItem];
            [handoffItem release];

            [menu addItem:[NSMenuItem separatorItem]];

            hoxiStatusBarController.keepAwakeItem = [[[NSMenuItem alloc] initWithTitle:@"任务运行时保持唤醒"
                                                                                action:@selector(toggleKeepAwake:)
                                                                         keyEquivalent:@""] autorelease];
            hoxiStatusBarController.keepAwakeItem.target = hoxiStatusBarController;
            hoxiStatusBarController.keepAwakeItem.state = NSControlStateValueOn;
            [menu addItem:hoxiStatusBarController.keepAwakeItem];

            NSMenuItem *policyItem = [[NSMenuItem alloc] initWithTitle:@"审批与安全策略…"
                                                                action:@selector(openApprovalPolicy:)
                                                         keyEquivalent:@""];
            policyItem.target = hoxiStatusBarController;
            [menu addItem:policyItem];
            [policyItem release];

            NSMenuItem *settingsItem = [[NSMenuItem alloc] initWithTitle:@"偏好设置…"
                                                                  action:@selector(openSettings:)
                                                           keyEquivalent:@","];
            settingsItem.target = hoxiStatusBarController;
            [menu addItem:settingsItem];
            [settingsItem release];

            [menu addItem:[NSMenuItem separatorItem]];

            NSMenuItem *quitApplication = [[NSMenuItem alloc] initWithTitle:@"退出 Hoxi"
                                                                     action:@selector(quitApplication:)
                                                              keyEquivalent:@"q"];
            quitApplication.target = hoxiStatusBarController;
            [menu addItem:quitApplication];
            [quitApplication release];

            hoxiStatusBarController.menu = menu;
            hoxiStatusBarController.statusItem.menu = menu;
            [menu release];
            [NSApp dockTile].badgeLabel = @"";
        }
    });
}

void hoxi_statusbar_update(
    int attention,
    int running,
    int queued,
    int available,
    int keepAwakeEnabled,
    int keepAwakeActive,
    const char *badge,
    const char *pendingItems,
    const char *runningItems
) {
    // The block may run later on the main queue, so own the text before Go frees it.
    char *badgeCopy = badge != NULL ? strdup(badge) : NULL;
    char *pendingCopy = pendingItems != NULL ? strdup(pendingItems) : NULL;
    char *runningCopy = runningItems != NULL ? strdup(runningItems) : NULL;
    hoxi_run_on_main(^{
        @autoreleasepool {
            NSString *badgeText = badgeCopy != NULL ? [NSString stringWithUTF8String:badgeCopy] : @"";
            NSString *pendingText = pendingCopy != NULL ? [NSString stringWithUTF8String:pendingCopy] : @"";
            NSString *runningText = runningCopy != NULL ? [NSString stringWithUTF8String:runningCopy] : @"";
            free(badgeCopy);
            free(pendingCopy);
            free(runningCopy);
            if (hoxiStatusBarController == nil) {
                return;
            }

            NSStatusBarButton *button = hoxiStatusBarController.statusItem.button;
            button.image = hoxi_create_statusbar_summary(attention, running, available);
            hoxiStatusBarController.keepAwakeItem.state = keepAwakeEnabled ? NSControlStateValueOn : NSControlStateValueOff;
            hoxiStatusBarController.keepAwakeItem.title = (keepAwakeEnabled && keepAwakeActive)
                ? @"任务运行时保持唤醒（生效中）"
                : @"任务运行时保持唤醒";

            if (!available) {
                button.toolTip = @"Hoxi: OpenCode 引擎正在启动";
                hoxiStatusBarController.attentionItem.title = @"待审批任务（引擎正在连接…）";
                hoxiStatusBarController.runningItem.title = @"运行中任务 (0)";
                hoxi_rebuild_session_items(
                    hoxiStatusBarController.menu,
                    hoxiStatusBarController.attentionItem,
                    hoxiStatusBarController.pendingSessionItems,
                    @"",
                    hoxiStatusBarController,
                    @selector(openPendingSession:)
                );
                hoxi_rebuild_session_items(
                    hoxiStatusBarController.menu,
                    hoxiStatusBarController.runningItem,
                    hoxiStatusBarController.runningSessionItems,
                    @"",
                    hoxiStatusBarController,
                    @selector(openRunningSession:)
                );
                [NSApp dockTile].badgeLabel = @"";
                return;
            }
            button.toolTip = [NSString stringWithFormat:@"Hoxi：待审批 %d · 运行中 %d", attention, running];
            hoxiStatusBarController.attentionItem.title = [NSString stringWithFormat:@"待审批任务 (%d)", attention];
            hoxiStatusBarController.runningItem.title = [NSString stringWithFormat:@"运行中任务 (%d)", running];
            hoxi_rebuild_session_items(
                hoxiStatusBarController.menu,
                hoxiStatusBarController.attentionItem,
                hoxiStatusBarController.pendingSessionItems,
                pendingText,
                hoxiStatusBarController,
                @selector(openPendingSession:)
            );
            hoxi_rebuild_session_items(
                hoxiStatusBarController.menu,
                hoxiStatusBarController.runningItem,
                hoxiStatusBarController.runningSessionItems,
                runningText,
                hoxiStatusBarController,
                @selector(openRunningSession:)
            );
            [NSApp dockTile].badgeLabel = badgeText;
        }
    });
}

void hoxi_statusbar_stop(void) {
    hoxi_run_on_main(^{
        @autoreleasepool {
            [NSApp dockTile].badgeLabel = @"";
            if (hoxiStatusBarController != nil) {
                [[NSStatusBar systemStatusBar] removeStatusItem:hoxiStatusBarController.statusItem];
                hoxiStatusBarController.statusItem = nil;
                hoxiStatusBarController.menu = nil;
                hoxiStatusBarController.attentionItem = nil;
                hoxiStatusBarController.runningItem = nil;
                hoxiStatusBarController.keepAwakeItem = nil;
                [hoxiStatusBarController.pendingSessionItems removeAllObjects];
                [hoxiStatusBarController.runningSessionItems removeAllObjects];
                [hoxiStatusBarController release];
                hoxiStatusBarController = nil;
            }
        }
    });
}
