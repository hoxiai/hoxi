//go:build darwin

#import <Cocoa/Cocoa.h>
#import <math.h>
#import <string.h>

extern void HoxiCompanionTriggerAction(const char *kind, const char *sessionID);

static NSString * const kPrefOriginSet = @"hoxi.companion.originSet";
static NSString * const kPrefOriginX   = @"hoxi.companion.originX";
static NSString * const kPrefOriginY   = @"hoxi.companion.originY";

static const CGFloat kPanelWidth  = 300.0;
static const CGFloat kPanelHeight = 244.0;

static void hoxi_companion_on_main(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), block);
}

static NSColor *hoxi_color_from_hex(NSString *hex, NSColor *fallback) {
    if (hex == nil || hex.length < 6) {
        return fallback;
    }
    NSString *clean = [hex hasPrefix:@"#"] ? [hex substringFromIndex:1] : hex;
    if (clean.length != 6) {
        return fallback;
    }
    unsigned int rgb = 0;
    NSScanner *scanner = [NSScanner scannerWithString:clean];
    if (![scanner scanHexInt:&rgb]) {
        return fallback;
    }
    CGFloat r = ((rgb >> 16) & 0xFF) / 255.0;
    CGFloat g = ((rgb >> 8) & 0xFF) / 255.0;
    CGFloat b = (rgb & 0xFF) / 255.0;
    return [NSColor colorWithSRGBRed:r green:g blue:b alpha:1.0];
}

@interface HoxiPassthroughImageView : NSImageView
@end

@implementation HoxiPassthroughImageView
- (NSView *)hitTest:(NSPoint)point {
    return nil;
}
@end

@interface HoxiCompanionPanel : NSPanel
@end

@implementation HoxiCompanionPanel
- (BOOL)canBecomeKeyWindow { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

@interface HoxiCompanionView : NSView
@property(nonatomic, assign) BOOL minimizedMode;
@property(nonatomic, assign) BOOL darkTheme;
@property(nonatomic, assign) BOOL showBubble;
@property(nonatomic, copy) NSString *persona;
@property(nonatomic, copy) NSString *personaName;
@property(nonatomic, copy) NSString *mood;
@property(nonatomic, copy) NSString *badgeLabel;
@property(nonatomic, copy) NSString *levelText;
@property(nonatomic, copy) NSString *dialogue;
@property(nonatomic, copy) NSString *targetSessionID;
@property(nonatomic, retain) NSColor *accentColor;
@property(nonatomic, retain) HoxiPassthroughImageView *avatarView;
@property(nonatomic, assign) BOOL hasCustomImage;
@property(nonatomic, assign) CGFloat animPhase;
@property(nonatomic, assign) CGFloat pokeBounce;
@property(nonatomic, assign) NSRect standeeHitRect;
@property(nonatomic, assign) NSRect pillHitRect;
@property(nonatomic, assign) NSRect minimizeBtnRect;
@property(nonatomic, assign) NSRect bubbleHitRect;
@property(nonatomic, assign) BOOL bubbleDrawn;
@property(nonatomic, assign) NSPoint dragStartMouse;
@property(nonatomic, assign) NSPoint dragStartOrigin;
@property(nonatomic, assign) BOOL didDrag;
- (void)syncSubframesAndLayout;
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

        _avatarView = [[HoxiPassthroughImageView alloc] initWithFrame:NSMakeRect(202, 136, 86, 86)];
        _avatarView.imageScaling = NSImageScaleProportionallyUpOrDown;
        _avatarView.animates = YES;
        _avatarView.wantsLayer = YES;
        _avatarView.layer.cornerRadius = 16.0;
        _avatarView.layer.masksToBounds = YES;
        [self addSubview:_avatarView];
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
        self.avatarView.frame = NSInsetRect(orb, 6.0, 6.0);
        return;
    }

    NSRect standee = NSMakeRect(202.0 + shake, 136.0 + bob, 86.0, 86.0);
    self.standeeHitRect = NSInsetRect(standee, -4.0, -4.0);
    self.avatarView.layer.cornerRadius = [self.persona isEqualToString:@"custom"] ? 18.0 : 0.0;
    self.avatarView.frame = standee;

    // Measure compact nameplate pill to the left of the standee
    NSFont *badgeFont = [NSFont systemFontOfSize:11.5 weight:NSFontWeightSemibold];
    NSFont *levelFont = [NSFont monospacedDigitSystemFontOfSize:10.5 weight:NSFontWeightMedium];
    CGFloat badgeW = ceil([(self.badgeLabel ?: @"") sizeWithAttributes:@{NSFontAttributeName: badgeFont}].width);
    CGFloat levelW = ceil([(self.levelText ?: @"") sizeWithAttributes:@{NSFontAttributeName: levelFont}].width);
    CGFloat textColW = MAX(badgeW, levelW);
    CGFloat pillW = MIN(184.0, MAX(98.0, textColW + 46.0));
    CGFloat pillH = 38.0;
    CGFloat pillX = MAX(8.0, NSMinX(standee) - pillW - 6.0);
    CGFloat pillY = 176.0 + bob * 0.35;
    self.pillHitRect = NSMakeRect(pillX, pillY, pillW, pillH);
    self.minimizeBtnRect = NSMakeRect(NSMaxX(self.pillHitRect) - 24.0, pillY + 9.0, 18.0, 20.0);

    // Measure speech bubble above standee
    BOOL wantBubble = self.showBubble && self.dialogue != nil && self.dialogue.length > 0 &&
        ([self isUrgentGate] || ![self.mood isEqualToString:@"idle"] || self.pokeBounce > 0.0 || self.dialogue.length > 0);
    // Match frontend rule: in idle mood, show bubble only when showBubble && dialogue non-empty (frontend sends empty dialogue when idle & not poked)
    if (wantBubble) {
        CGFloat maxBubbleW = 276.0;
        CGFloat padX = 12.0;
        NSMutableParagraphStyle *pStyle = [[NSMutableParagraphStyle new] autorelease];
        pStyle.lineSpacing = 2.5;
        NSDictionary *bodyAttrs = @{
            NSFontAttributeName: [NSFont systemFontOfSize:12.0 weight:NSFontWeightRegular],
            NSParagraphStyleAttributeName: pStyle,
        };
        NSRect measured = [self.dialogue boundingRectWithSize:NSMakeSize(maxBubbleW - padX * 2.0, 96.0)
                                                      options:NSStringDrawingUsesLineFragmentOrigin | NSStringDrawingUsesFontLeading
                                                   attributes:bodyAttrs];
        CGFloat contentW = MAX(148.0, ceil(measured.size.width));
        CGFloat bubbleW = MIN(maxBubbleW, contentW + padX * 2.0);
        CGFloat bubbleH = MIN(116.0, MAX(50.0, ceil(measured.size.height) + 32.0));
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
    [self syncSubframesAndLayout];

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
    [(isDanger ? dangerColor : accent) colorWithAlphaComponent:(self.darkTheme ? 0.18 : 0.12)];
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

    // Status indicator dot
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

    // Minimize '-' button
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

        // Header: Persona Name + Mood Tag
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
- (void)ctxResetPosition:(id)sender {
    extern void hoxi_companion_reset_position(void);
    hoxi_companion_reset_position();
}
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

@interface HoxiCompanionController : NSObject
@property(nonatomic, retain) HoxiCompanionPanel *panel;
@property(nonatomic, retain) HoxiCompanionView *companionView;
@property(nonatomic, retain) NSTimer *animTimer;
- (void)ensurePanel;
- (void)applyDefaultPosition:(BOOL)clearSaved;
- (void)setAnimating:(BOOL)active;
@end

@implementation HoxiCompanionController

- (void)dealloc {
    [_animTimer invalidate];
    [_animTimer release];
    [_companionView release];
    [_panel release];
    [super dealloc];
}

- (NSPoint)defaultDesktopOrigin {
    NSScreen *screen = [NSScreen mainScreen];
    if (screen == nil && [NSScreen screens].count > 0) {
        screen = [[NSScreen screens] firstObject];
    }
    NSRect vf = screen != nil ? [screen visibleFrame] : NSMakeRect(0, 0, 1440, 900);
    return NSMakePoint(NSMaxX(vf) - kPanelWidth - 18.0, NSMinY(vf) + 16.0);
}

- (NSPoint)clampedOrigin:(NSPoint)pt {
    NSArray<NSScreen *> *screens = [NSScreen screens];
    if (screens.count == 0) {
        return pt;
    }
    NSRect candidate = NSMakeRect(pt.x + 180.0, pt.y + 20.0, 96.0, 96.0);
    for (NSScreen *scr in screens) {
        if (NSIntersectsRect(candidate, [scr visibleFrame])) {
            return pt;
        }
    }
    return [self defaultDesktopOrigin];
}

- (void)ensurePanel {
    if (self.panel != nil) {
        return;
    }
    NSPoint origin = [self defaultDesktopOrigin];
    NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
    if ([defaults boolForKey:kPrefOriginSet]) {
        origin = [self clampedOrigin:NSMakePoint([defaults doubleForKey:kPrefOriginX],
                                                 [defaults doubleForKey:kPrefOriginY])];
    }

    NSRect frame = NSMakeRect(origin.x, origin.y, kPanelWidth, kPanelHeight);
    HoxiCompanionPanel *panel = [[HoxiCompanionPanel alloc]
        initWithContentRect:frame
                  styleMask:(NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel)
                    backing:NSBackingStoreBuffered
                      defer:NO];
    panel.opaque = NO;
    panel.backgroundColor = [NSColor clearColor];
    panel.hasShadow = NO;
    panel.hidesOnDeactivate = NO;
    panel.floatingPanel = YES;
    panel.level = NSFloatingWindowLevel;
    panel.collectionBehavior = (NSWindowCollectionBehaviorCanJoinAllSpaces |
                                NSWindowCollectionBehaviorStationary |
                                NSWindowCollectionBehaviorIgnoresCycle |
                                NSWindowCollectionBehaviorFullScreenAuxiliary);
    panel.releasedWhenClosed = NO;

    HoxiCompanionView *view = [[HoxiCompanionView alloc] initWithFrame:NSMakeRect(0, 0, kPanelWidth, kPanelHeight)];
    panel.contentView = view;

    self.panel = panel;
    self.companionView = view;
    [panel release];
    [view release];
}

- (void)applyDefaultPosition:(BOOL)clearSaved {
    if (clearSaved) {
        NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
        [defaults removeObjectForKey:kPrefOriginSet];
        [defaults removeObjectForKey:kPrefOriginX];
        [defaults removeObjectForKey:kPrefOriginY];
    }
    if (self.panel != nil) {
        [self.panel setFrameOrigin:[self defaultDesktopOrigin]];
    }
}

- (void)onAnimTick:(NSTimer *)timer {
    if (self.companionView == nil || self.panel == nil || !self.panel.isVisible) {
        return;
    }
    self.companionView.animPhase += 0.065;
    if (self.companionView.animPhase > M_PI * 200.0) {
        self.companionView.animPhase = 0.0;
    }
    if (self.companionView.pokeBounce > 0.0) {
        self.companionView.pokeBounce += 0.09;
        if (self.companionView.pokeBounce >= 1.0) {
            self.companionView.pokeBounce = 0.0;
        }
    }
    [self.companionView syncSubframesAndLayout];
    [self.companionView setNeedsDisplay:YES];
}

- (void)setAnimating:(BOOL)active {
    if (active) {
        if (self.animTimer == nil) {
            NSTimer *t = [NSTimer timerWithTimeInterval:(1.0 / 30.0)
                                                 target:self
                                               selector:@selector(onAnimTick:)
                                               userInfo:nil
                                                repeats:YES];
            [[NSRunLoop mainRunLoop] addTimer:t forMode:NSRunLoopCommonModes];
            self.animTimer = t;
        }
    } else {
        if (self.animTimer != nil) {
            [self.animTimer invalidate];
            self.animTimer = nil;
        }
    }
}

@end

static HoxiCompanionController *hoxiCompanionCtrl;

void hoxi_companion_start(void) {
    hoxi_companion_on_main(^{
        @autoreleasepool {
            if (hoxiCompanionCtrl == nil) {
                hoxiCompanionCtrl = [HoxiCompanionController new];
            }
        }
    });
}

void hoxi_companion_reset_position(void) {
    hoxi_companion_on_main(^{
        @autoreleasepool {
            if (hoxiCompanionCtrl != nil) {
                [hoxiCompanionCtrl applyDefaultPosition:YES];
            }
        }
    });
}

void hoxi_companion_update(
    int visible,
    int minimized,
    int darkTheme,
    int showBubble,
    const char *persona,
    const char *personaName,
    const char *mood,
    const char *badgeLabel,
    const char *levelText,
    const char *dialogue,
    const char *targetSessionID,
    const char *accentHex,
    const void *avatarBytes,
    int avatarLen,
    int avatarChanged
) {
    char *personaCopy   = persona != NULL ? strdup(persona) : NULL;
    char *nameCopy      = personaName != NULL ? strdup(personaName) : NULL;
    char *moodCopy      = mood != NULL ? strdup(mood) : NULL;
    char *badgeCopy     = badgeLabel != NULL ? strdup(badgeLabel) : NULL;
    char *levelCopy     = levelText != NULL ? strdup(levelText) : NULL;
    char *dialogueCopy  = dialogue != NULL ? strdup(dialogue) : NULL;
    char *targetSidCopy = targetSessionID != NULL ? strdup(targetSessionID) : NULL;
    char *accentCopy    = accentHex != NULL ? strdup(accentHex) : NULL;
    NSData *imgData     = (avatarBytes != NULL && avatarLen > 0)
        ? [[NSData alloc] initWithBytes:avatarBytes length:(NSUInteger)avatarLen]
        : nil;

    hoxi_companion_on_main(^{
        @autoreleasepool {
            NSString *sPersona   = personaCopy != NULL ? [NSString stringWithUTF8String:personaCopy] : @"hoxi-chan";
            NSString *sName      = nameCopy != NULL ? [NSString stringWithUTF8String:nameCopy] : @"Hoxi";
            NSString *sMood      = moodCopy != NULL ? [NSString stringWithUTF8String:moodCopy] : @"idle";
            NSString *sBadge     = badgeCopy != NULL ? [NSString stringWithUTF8String:badgeCopy] : @"";
            NSString *sLevel     = levelCopy != NULL ? [NSString stringWithUTF8String:levelCopy] : @"";
            NSString *sDialogue  = dialogueCopy != NULL ? [NSString stringWithUTF8String:dialogueCopy] : @"";
            NSString *sTargetSid = targetSidCopy != NULL ? [NSString stringWithUTF8String:targetSidCopy] : @"";
            NSString *sAccent    = accentCopy != NULL ? [NSString stringWithUTF8String:accentCopy] : @"#2563eb";

            free(personaCopy);
            free(nameCopy);
            free(moodCopy);
            free(badgeCopy);
            free(levelCopy);
            free(dialogueCopy);
            free(targetSidCopy);
            free(accentCopy);

            if (hoxiCompanionCtrl == nil) {
                hoxiCompanionCtrl = [HoxiCompanionController new];
            }

            if (!visible) {
                if (hoxiCompanionCtrl.panel != nil && hoxiCompanionCtrl.panel.isVisible) {
                    [hoxiCompanionCtrl.panel orderOut:nil];
                }
                [hoxiCompanionCtrl setAnimating:NO];
                [imgData release];
                return;
            }

            [hoxiCompanionCtrl ensurePanel];
            HoxiCompanionView *view = hoxiCompanionCtrl.companionView;
            view.minimizedMode = minimized != 0;
            view.darkTheme = darkTheme != 0;
            view.showBubble = showBubble != 0;
            view.persona = sPersona;
            view.personaName = sName;
            view.mood = sMood;
            view.badgeLabel = sBadge;
            view.levelText = sLevel;
            view.dialogue = sDialogue;
            view.targetSessionID = sTargetSid;
            view.accentColor = hoxi_color_from_hex(
                sAccent,
                [NSColor colorWithSRGBRed:0.15 green:0.39 blue:0.92 alpha:1.0]
            );

            if (avatarChanged && imgData != nil) {
                NSImage *img = [[NSImage alloc] initWithData:imgData];
                if (img != nil) {
                    view.avatarView.image = img;
                    view.hasCustomImage = YES;
                    [img release];
                }
            } else if (avatarChanged && imgData == nil) {
                view.avatarView.image = nil;
                view.hasCustomImage = NO;
            }
            [imgData release];

            [view syncSubframesAndLayout];
            [view setNeedsDisplay:YES];

            if (!hoxiCompanionCtrl.panel.isVisible) {
                [hoxiCompanionCtrl.panel orderFrontRegardless];
            }
            [hoxiCompanionCtrl setAnimating:YES];
        }
    });
}

void hoxi_companion_stop(void) {
    hoxi_companion_on_main(^{
        @autoreleasepool {
            if (hoxiCompanionCtrl != nil) {
                [hoxiCompanionCtrl setAnimating:NO];
                if (hoxiCompanionCtrl.panel != nil) {
                    [hoxiCompanionCtrl.panel close];
                    hoxiCompanionCtrl.panel = nil;
                }
                hoxiCompanionCtrl.companionView = nil;
                [hoxiCompanionCtrl release];
                hoxiCompanionCtrl = nil;
            }
        }
    });
}
