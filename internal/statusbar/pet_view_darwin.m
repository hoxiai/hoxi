//go:build darwin

#import "pet_darwin.h"
#import <math.h>
#import <string.h>

@implementation HoxiPassthroughImageView
- (NSView *)hitTest:(NSPoint)point {
    return nil;
}
@end

@implementation HoxiCompanionPanel
- (BOOL)canBecomeKeyWindow { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

@implementation HoxiCompanionView

- (instancetype)initWithFrame:(NSRect)frameRect {
    self = [super initWithFrame:frameRect];
    if (self != nil) {
        _persona = @"hoxi-chan";
        _personaName = @"呼吸酱";
        _mood = @"idle";
        _badgeLabel = @"待机陪伴中";
        _levelText = @"Lv.1";
        _dialogue = @"";
        _targetSessionID = @"";
        _accentColor = [[NSColor colorWithSRGBRed:0.15 green:0.39 blue:0.92 alpha:1.0] retain];
        _metricsDirty = YES;
        _cachedPillWidth = 98.0;

        _avatarView = [[HoxiPassthroughImageView alloc] initWithFrame:NSMakeRect(202, 136, 86, 86)];
        _avatarView.imageScaling = NSImageScaleProportionallyUpOrDown;
        _avatarView.animates = YES;
        _avatarView.wantsLayer = YES;
        _avatarView.layer.cornerRadius = 16.0;
        _avatarView.layer.masksToBounds = YES;
        [self addSubview:_avatarView];
        [self syncSubframesAndLayout];
    }
    return self;
}

- (void)dealloc {
    [_persona release];
    [_personaName release];
    [_mood release];
    [_badgeLabel release];
    [_levelText release];
    [_dialogue release];
    [_targetSessionID release];
    [_accentColor release];
    [_avatarView release];
    [super dealloc];
}

- (BOOL)isFlipped { return YES; }
- (BOOL)acceptsFirstMouse:(NSEvent *)event { return YES; }

- (BOOL)isUrgentGate {
    return [self.mood isEqualToString:@"gate-danger"] || [self.mood isEqualToString:@"gate-approval"];
}

- (BOOL)isEffectivelyMinimized {
    return self.minimizedMode && ![self isUrgentGate];
}

- (CGFloat)currentBobOffset {
    CGFloat bounce = self.pokeBounce > 0.01 ? (-8.0 * sin(self.pokeBounce * M_PI)) : 0.0;
    return sin(self.animPhase) * 3.2 + bounce;
}

- (CGFloat)currentShakeOffset {
    if ([self.mood isEqualToString:@"gate-danger"]) {
        return sin(self.animPhase * 5.5) * 2.2;
    }
    return 0.0;
}

- (void)ensureMetrics {
    if (!self.metricsDirty) {
        return;
    }
    self.metricsDirty = NO;
    NSFont *badgeFont = [NSFont systemFontOfSize:11.5 weight:NSFontWeightSemibold];
    NSFont *levelFont = [NSFont monospacedDigitSystemFontOfSize:10.5 weight:NSFontWeightMedium];
    CGFloat badgeW = ceil([(self.badgeLabel ?: @"") sizeWithAttributes:@{NSFontAttributeName: badgeFont}].width);
    CGFloat levelW = ceil([(self.levelText ?: @"") sizeWithAttributes:@{NSFontAttributeName: levelFont}].width);
    self.cachedPillWidth = MIN(184.0, MAX(98.0, MAX(badgeW, levelW) + 46.0));

    if (self.dialogue != nil && self.dialogue.length > 0) {
        NSMutableParagraphStyle *pStyle = [[NSMutableParagraphStyle new] autorelease];
        pStyle.lineSpacing = 2.5;
        NSDictionary *bodyAttrs = @{
            NSFontAttributeName: [NSFont systemFontOfSize:12.0 weight:NSFontWeightRegular],
            NSParagraphStyleAttributeName: pStyle,
        };
        NSRect measured = [self.dialogue boundingRectWithSize:NSMakeSize(252.0, 96.0)
                                                      options:NSStringDrawingUsesLineFragmentOrigin | NSStringDrawingUsesFontLeading
                                                   attributes:bodyAttrs];
        CGFloat bubbleW = MIN(276.0, MAX(148.0, ceil(measured.size.width)) + 24.0);
        CGFloat bubbleH = MIN(116.0, MAX(50.0, ceil(measured.size.height) + 32.0));
        self.cachedBubbleSize = NSMakeSize(bubbleW, bubbleH);
    } else {
        self.cachedBubbleSize = NSZeroSize;
    }
}

- (void)syncSubframesAndLayout {
    CGFloat bob = [self currentBobOffset];
    CGFloat shake = [self currentShakeOffset];

    if ([self isEffectivelyMinimized]) {
        NSRect orb = NSMakeRect(232.0 + shake, 174.0 + bob, 54.0, 54.0);
        self.standeeHitRect = orb;
        self.pillHitRect = NSZeroRect;
        self.minimizeBtnRect = NSZeroRect;
        self.bubbleHitRect = NSZeroRect;
        self.bubbleDrawn = NO;
        self.avatarView.layer.cornerRadius = 21.0;
        NSRect nextFrame = NSInsetRect(orb, 6.0, 6.0);
        if (!NSEqualRects(self.avatarView.frame, nextFrame)) {
            self.avatarView.frame = nextFrame;
        }
        return;
    }

    [self ensureMetrics];
    NSRect standee = NSMakeRect(202.0 + shake, 136.0 + bob, 86.0, 86.0);
    self.standeeHitRect = NSInsetRect(standee, -4.0, -4.0);
    self.avatarView.layer.cornerRadius = [self.persona isEqualToString:@"custom"] ? 18.0 : 0.0;
    if (!NSEqualRects(self.avatarView.frame, standee)) {
        self.avatarView.frame = standee;
    }

    CGFloat pillW = self.cachedPillWidth;
    CGFloat pillX = MAX(8.0, NSMinX(standee) - pillW - 6.0);
    CGFloat pillY = 176.0 + bob * 0.35;
    self.pillHitRect = NSMakeRect(pillX, pillY, pillW, 38.0);
    self.minimizeBtnRect = NSMakeRect(NSMaxX(self.pillHitRect) - 24.0, pillY + 9.0, 18.0, 20.0);

    if (self.showBubble && self.dialogue != nil && self.dialogue.length > 0 && self.cachedBubbleSize.width > 0.0) {
        CGFloat bubbleW = self.cachedBubbleSize.width;
        CGFloat bubbleH = self.cachedBubbleSize.height;
        CGFloat bubbleX = kPanelWidth - 12.0 - bubbleW;
        CGFloat bubbleY = MAX(6.0, NSMinY(standee) - bubbleH - 10.0);
        self.bubbleHitRect = NSMakeRect(bubbleX, bubbleY, bubbleW, bubbleH);
        self.bubbleDrawn = YES;
    } else {
        self.bubbleHitRect = NSZeroRect;
        self.bubbleDrawn = NO;
    }
}

- (NSView *)hitTest:(NSPoint)point {
    NSPoint local = [self convertPoint:point fromView:[self superview]];
    if (NSPointInRect(local, self.standeeHitRect)) {
        return self;
    }
    if (![self isEffectivelyMinimized] && NSPointInRect(local, self.pillHitRect)) {
        return self;
    }
    if (self.bubbleDrawn && NSPointInRect(local, self.bubbleHitRect)) {
        return self;
    }
    return nil;
}

- (void)drawFallbackCharacterInRect:(NSRect)rect {
    NSColor *accent = self.accentColor ?: [NSColor systemBlueColor];
    NSBezierPath *circle = [NSBezierPath bezierPathWithOvalInRect:NSInsetRect(rect, 6.0, 6.0)];
    [[accent colorWithAlphaComponent:0.9] setFill];
    [circle fill];
    [[NSColor whiteColor] setStroke];
    circle.lineWidth = 2.0;
    [circle stroke];

    NSDictionary *attrs = @{
        NSFontAttributeName: [NSFont monospacedSystemFontOfSize:13.0 weight:NSFontWeightBold],
        NSForegroundColorAttributeName: [NSColor whiteColor],
    };
    NSString *face = [self.mood isEqualToString:@"gate-danger"] ? @"!>_<!" : @"^ω^";
    NSSize sz = [face sizeWithAttributes:attrs];
    [face drawAtPoint:NSMakePoint(NSMidX(rect) - sz.width / 2.0, NSMidY(rect) - sz.height / 2.0)
       withAttributes:attrs];
}

- (void)drawRect:(NSRect)dirtyRect {
    NSColor *accent = self.accentColor ?: [NSColor colorWithSRGBRed:0.15 green:0.39 blue:0.92 alpha:1.0];
    BOOL isDanger = [self.mood isEqualToString:@"gate-danger"];
    BOOL isApproval = [self.mood isEqualToString:@"gate-approval"];
    NSColor *dangerColor = [NSColor colorWithSRGBRed:0.96 green:0.25 blue:0.37 alpha:1.0];
    NSColor *warnColor = [NSColor colorWithSRGBRed:0.96 green:0.62 blue:0.04 alpha:1.0];

    NSColor *cardBg = self.darkTheme
        ? [NSColor colorWithSRGBRed:0.08 green:0.10 blue:0.17 alpha:0.92]
        : [NSColor colorWithSRGBRed:1.0 green:1.0 blue:1.0 alpha:0.94];
    NSColor *cardBorder = isDanger
        ? dangerColor
        : (isApproval
            ? warnColor
            : (self.darkTheme
                ? [accent colorWithAlphaComponent:0.45]
                : [NSColor colorWithSRGBRed:0.08 green:0.12 blue:0.20 alpha:0.15]));
    NSColor *primaryText = self.darkTheme
        ? [NSColor colorWithSRGBRed:0.95 green:0.97 blue:1.0 alpha:1.0]
        : [NSColor colorWithSRGBRed:0.07 green:0.10 blue:0.17 alpha:1.0];
    NSColor *secondaryText = self.darkTheme
        ? [NSColor colorWithSRGBRed:0.74 green:0.80 blue:0.90 alpha:1.0]
        : [NSColor colorWithSRGBRed:0.28 green:0.33 blue:0.42 alpha:1.0];
    NSColor *mutedText = self.darkTheme
        ? [NSColor colorWithSRGBRed:0.55 green:0.62 blue:0.74 alpha:1.0]
        : [NSColor colorWithSRGBRed:0.45 green:0.50 blue:0.60 alpha:1.0];

    NSShadow *softShadow = [[NSShadow new] autorelease];
    softShadow.shadowColor = [NSColor colorWithCalibratedWhite:0.0 alpha:(self.darkTheme ? 0.46 : 0.18)];
    softShadow.shadowOffset = NSMakeSize(0.0, -4.0);
    softShadow.shadowBlurRadius = 12.0;

    if ([self isEffectivelyMinimized]) {
        [NSGraphicsContext saveGraphicsState];
        [softShadow set];
        NSBezierPath *orbPath = [NSBezierPath bezierPathWithOvalInRect:NSInsetRect(self.standeeHitRect, 2.0, 2.0)];
        [cardBg setFill];
        [orbPath fill];
        [NSGraphicsContext restoreGraphicsState];

        [cardBorder setStroke];
        orbPath.lineWidth = 1.6;
        [orbPath stroke];
        if (!self.hasCustomImage) {
            [self drawFallbackCharacterInRect:self.standeeHitRect];
        }
        return;
    }

    // 1. Ambient aura & ground shadow behind character standee
    NSRect standee = self.avatarView.frame;
    NSRect auraRect = NSInsetRect(standee, 6.0, 6.0);
    NSBezierPath *aura = [NSBezierPath bezierPathWithOvalInRect:auraRect];
    [[(isDanger ? dangerColor : accent) colorWithAlphaComponent:(self.darkTheme ? 0.18 : 0.12)] setFill];
    [aura fill];

    NSRect groundRect = NSMakeRect(NSMidX(standee) - 28.0, 224.0, 56.0, 8.0);
    NSBezierPath *ground = [NSBezierPath bezierPathWithOvalInRect:groundRect];
    [[NSColor colorWithCalibratedWhite:0.0 alpha:(self.darkTheme ? 0.34 : 0.14)] setFill];
    [ground fill];

    if (!self.hasCustomImage) {
        [self drawFallbackCharacterInRect:standee];
    }

    // Urgent alert shield badge on standee corner
    if (isDanger || isApproval) {
        NSRect alertBadge = NSMakeRect(NSMaxX(standee) - 20.0, NSMinY(standee) - 2.0, 20.0, 20.0);
        NSBezierPath *alertCircle = [NSBezierPath bezierPathWithOvalInRect:alertBadge];
        [(isDanger ? dangerColor : warnColor) setFill];
        [alertCircle fill];
        [[NSColor whiteColor] setStroke];
        alertCircle.lineWidth = 1.5;
        [alertCircle stroke];
        NSDictionary *bangAttrs = @{
            NSFontAttributeName: [NSFont systemFontOfSize:12.0 weight:NSFontWeightBold],
            NSForegroundColorAttributeName: [NSColor whiteColor],
        };
        NSString *mark = isDanger ? @"!" : @"?";
        NSSize mSz = [mark sizeWithAttributes:bangAttrs];
        [mark drawAtPoint:NSMakePoint(NSMidX(alertBadge) - mSz.width / 2.0, NSMidY(alertBadge) - mSz.height / 2.0)
           withAttributes:bangAttrs];
    }

    // 2. Compact floating status nameplate pill
    [NSGraphicsContext saveGraphicsState];
    [softShadow set];
    NSBezierPath *pillPath = [NSBezierPath bezierPathWithRoundedRect:self.pillHitRect xRadius:13.0 yRadius:13.0];
    [cardBg setFill];
    [pillPath fill];
    [NSGraphicsContext restoreGraphicsState];

    [cardBorder setStroke];
    pillPath.lineWidth = 1.1;
    [pillPath stroke];

    NSColor *dotColor = isDanger ? dangerColor : (isApproval ? warnColor : accent);
    NSBezierPath *dot = [NSBezierPath bezierPathWithOvalInRect:NSMakeRect(NSMinX(self.pillHitRect) + 10.0, NSMinY(self.pillHitRect) + 9.5, 6.5, 6.5)];
    [dotColor setFill];
    [dot fill];

    NSDictionary *badgeAttrs = @{
        NSFontAttributeName: [NSFont systemFontOfSize:11.0 weight:NSFontWeightSemibold],
        NSForegroundColorAttributeName: isDanger ? dangerColor : primaryText,
    };
    [(self.badgeLabel ?: @"") drawInRect:NSMakeRect(NSMinX(self.pillHitRect) + 21.0, NSMinY(self.pillHitRect) + 4.5, NSWidth(self.pillHitRect) - 46.0, 15.0)
                          withAttributes:badgeAttrs];

    NSDictionary *levelAttrs = @{
        NSFontAttributeName: [NSFont monospacedDigitSystemFontOfSize:10.0 weight:NSFontWeightMedium],
        NSForegroundColorAttributeName: mutedText,
    };
    [(self.levelText ?: @"") drawInRect:NSMakeRect(NSMinX(self.pillHitRect) + 21.0, NSMinY(self.pillHitRect) + 20.0, NSWidth(self.pillHitRect) - 46.0, 14.0)
                         withAttributes:levelAttrs];

    NSDictionary *minAttrs = @{
        NSFontAttributeName: [NSFont systemFontOfSize:13.0 weight:NSFontWeightBold],
        NSForegroundColorAttributeName: mutedText,
    };
    NSString *minGlyph = @"–";
    NSSize minSz = [minGlyph sizeWithAttributes:minAttrs];
    [minGlyph drawAtPoint:NSMakePoint(NSMidX(self.minimizeBtnRect) - minSz.width / 2.0, NSMidY(self.minimizeBtnRect) - minSz.height / 2.0 - 1.0)
           withAttributes:minAttrs];

    // 3. Speech bubble with pointer tail
    if (self.bubbleDrawn) {
        NSRect b = self.bubbleHitRect;
        CGFloat tailX = MIN(NSMaxX(b) - 24.0, MAX(NSMinX(b) + 24.0, NSMidX(standee)));

        NSBezierPath *bubblePath = [NSBezierPath bezierPathWithRoundedRect:b xRadius:14.0 yRadius:14.0];
        NSBezierPath *tail = [NSBezierPath bezierPath];
        [tail moveToPoint:NSMakePoint(tailX - 6.0, NSMaxY(b) - 1.0)];
        [tail lineToPoint:NSMakePoint(tailX, NSMaxY(b) + 6.5)];
        [tail lineToPoint:NSMakePoint(tailX + 6.0, NSMaxY(b) - 1.0)];
        [tail closePath];

        [NSGraphicsContext saveGraphicsState];
        [softShadow set];
        [cardBg setFill];
        [bubblePath fill];
        [tail fill];
        [NSGraphicsContext restoreGraphicsState];

        [cardBorder setStroke];
        bubblePath.lineWidth = isDanger ? 1.5 : 1.1;
        [bubblePath stroke];

        NSDictionary *nameAttrs = @{
            NSFontAttributeName: [NSFont systemFontOfSize:11.0 weight:NSFontWeightBold],
            NSForegroundColorAttributeName: isDanger ? dangerColor : accent,
        };
        [(self.personaName ?: @"Hoxi") drawAtPoint:NSMakePoint(NSMinX(b) + 12.0, NSMinY(b) + 8.0)
                                    withAttributes:nameAttrs];

        NSMutableParagraphStyle *pStyle = [[NSMutableParagraphStyle new] autorelease];
        pStyle.lineSpacing = 2.2;
        pStyle.lineBreakMode = NSLineBreakByWordWrapping;
        NSDictionary *bodyAttrs = @{
            NSFontAttributeName: [NSFont systemFontOfSize:11.8 weight:NSFontWeightRegular],
            NSForegroundColorAttributeName: isDanger ? primaryText : secondaryText,
            NSParagraphStyleAttributeName: pStyle,
        };
        NSRect textRect = NSMakeRect(NSMinX(b) + 12.0, NSMinY(b) + 25.0, NSWidth(b) - 24.0, NSHeight(b) - 32.0);
        [(self.dialogue ?: @"") drawInRect:textRect withAttributes:bodyAttrs];
    }
}

- (void)mouseDown:(NSEvent *)event {
    self.dragStartMouse = [NSEvent mouseLocation];
    self.dragStartOrigin = self.window.frame.origin;
    self.didDrag = NO;
}

- (void)mouseDragged:(NSEvent *)event {
    NSPoint cur = [NSEvent mouseLocation];
    CGFloat dx = cur.x - self.dragStartMouse.x;
    CGFloat dy = cur.y - self.dragStartMouse.y;
    if (!self.didDrag && hypot(dx, dy) < 3.5) {
        return;
    }
    self.didDrag = YES;
    [self.window setFrameOrigin:NSMakePoint(self.dragStartOrigin.x + dx, self.dragStartOrigin.y + dy)];
}

- (void)mouseUp:(NSEvent *)event {
    if (self.didDrag) {
        NSPoint origin = self.window.frame.origin;
        NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
        [defaults setBool:YES forKey:kPrefOriginSet];
        [defaults setDouble:origin.x forKey:kPrefOriginX];
        [defaults setDouble:origin.y forKey:kPrefOriginY];
        return;
    }

    NSPoint local = [self convertPoint:[event locationInWindow] fromView:nil];

    if (![self isEffectivelyMinimized] && NSPointInRect(local, self.minimizeBtnRect)) {
        HoxiCompanionTriggerAction("toggle-minimize", "");
        return;
    }

    if ([self isUrgentGate]) {
        [NSApp activateIgnoringOtherApps:YES];
        const char *sid = self.targetSessionID != nil ? [self.targetSessionID UTF8String] : "";
        HoxiCompanionTriggerAction("attention", sid);
        return;
    }

    if ([self isEffectivelyMinimized]) {
        HoxiCompanionTriggerAction("toggle-minimize", "");
        return;
    }

    if (self.bubbleDrawn && NSPointInRect(local, self.bubbleHitRect)) {
        [NSApp activateIgnoringOtherApps:YES];
        HoxiCompanionTriggerAction("workspace", "");
        return;
    }

    if (event.clickCount >= 2) {
        [NSApp activateIgnoringOtherApps:YES];
        HoxiCompanionTriggerAction("workspace", "");
        return;
    }

    self.pokeBounce = 0.02;
    hoxi_companion_wake_animation();
    HoxiCompanionTriggerAction("tap", "");
}

- (void)ctxOpenWorkspace:(id)sender { [NSApp activateIgnoringOtherApps:YES]; HoxiCompanionTriggerAction("workspace", ""); }
- (void)ctxOpenAttention:(id)sender {
    [NSApp activateIgnoringOtherApps:YES];
    HoxiCompanionTriggerAction("attention", self.targetSessionID != nil ? [self.targetSessionID UTF8String] : "");
}
- (void)ctxNewThread:(id)sender { [NSApp activateIgnoringOtherApps:YES]; HoxiCompanionTriggerAction("new-thread", ""); }
- (void)ctxOpenSettings:(id)sender { [NSApp activateIgnoringOtherApps:YES]; HoxiCompanionTriggerAction("settings", ""); }
- (void)ctxToggleMinimize:(id)sender { HoxiCompanionTriggerAction("toggle-minimize", ""); }
- (void)ctxResetPosition:(id)sender { hoxi_companion_reset_position(); }
- (void)ctxDismissCompanion:(id)sender { HoxiCompanionTriggerAction("dismiss", ""); }

static void hoxi_menu_add(NSMenu *menu, NSString *title, id target, SEL sel) {
    NSMenuItem *item = [[[NSMenuItem alloc] initWithTitle:title action:sel keyEquivalent:@""] autorelease];
    item.target = target;
    [menu addItem:item];
}

- (NSMenu *)menuForEvent:(NSEvent *)event {
    NSMenu *menu = [[[NSMenu alloc] initWithTitle:@"Hoxi Companion"] autorelease];
    menu.autoenablesItems = NO;
    hoxi_menu_add(menu, @"唤醒 Hoxi 工作台", self, @selector(ctxOpenWorkspace:));
    if ([self isUrgentGate]) {
        hoxi_menu_add(menu, @"立即处理待审批任务", self, @selector(ctxOpenAttention:));
    }
    hoxi_menu_add(menu, @"新建对话", self, @selector(ctxNewThread:));
    hoxi_menu_add(menu, @"看板娘与外观设置…", self, @selector(ctxOpenSettings:));
    [menu addItem:[NSMenuItem separatorItem]];
    hoxi_menu_add(menu, self.minimizedMode ? @"展开桌宠挂件" : @"收起为迷你徽章", self, @selector(ctxToggleMinimize:));
    hoxi_menu_add(menu, @"重置桌宠屏幕位置", self, @selector(ctxResetPosition:));
    hoxi_menu_add(menu, @"隐藏桌面桌宠", self, @selector(ctxDismissCompanion:));
    return menu;
}

@end
