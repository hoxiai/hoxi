//go:build darwin

#import "pet_darwin.h"
#import <math.h>
#import <string.h>

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

@interface HoxiCompanionController : NSObject
@property(nonatomic, retain) HoxiCompanionPanel *panel;
@property(nonatomic, retain) HoxiCompanionView *companionView;
@property(nonatomic, retain) NSTimer *animTimer;
@property(nonatomic, assign) NSTimeInterval animInterval;
- (void)ensurePanel;
- (void)applyDefaultPosition:(BOOL)clearSaved;
- (void)syncAnimationTimer;
- (void)stopAnimationTimer;
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

- (void)stopAnimationTimer {
    if (self.animTimer != nil) {
        [self.animTimer invalidate];
        self.animTimer = nil;
        self.animInterval = 0.0;
    }
}

- (void)syncAnimationTimer {
    if (self.companionView == nil || self.panel == nil || !self.panel.isVisible) {
        [self stopAnimationTimer];
        return;
    }
    BOOL bouncing = self.companionView.pokeBounce > 0.0;
    BOOL idle = [self.companionView.mood isEqualToString:@"idle"];
    if (idle && !bouncing) {
        [self stopAnimationTimer];
        if (self.companionView.animPhase != 0.0) {
            self.companionView.animPhase = 0.0;
            [self.companionView syncSubframesAndLayout];
            [self.companionView setNeedsDisplay:YES];
        }
        return;
    }
    NSTimeInterval want = (bouncing || [self.companionView.mood isEqualToString:@"gate-danger"])
        ? (1.0 / 30.0)
        : (1.0 / 15.0);
    if (self.animTimer != nil && fabs(self.animInterval - want) < 0.001) {
        return;
    }
    [self stopAnimationTimer];
    NSTimer *t = [NSTimer timerWithTimeInterval:want
                                         target:self
                                       selector:@selector(onAnimTick:)
                                       userInfo:nil
                                        repeats:YES];
    t.tolerance = want * 0.25;
    [[NSRunLoop mainRunLoop] addTimer:t forMode:NSRunLoopCommonModes];
    self.animTimer = t;
    self.animInterval = want;
}

- (void)onAnimTick:(NSTimer *)timer {
    if (self.companionView == nil || self.panel == nil || !self.panel.isVisible) {
        [self stopAnimationTimer];
        return;
    }
    BOOL slowTick = self.animInterval > 0.05;
    self.companionView.animPhase += slowTick ? 0.13 : 0.065;
    if (self.companionView.animPhase > M_PI * 200.0) {
        self.companionView.animPhase = 0.0;
    }
    BOOL bounceEnded = NO;
    if (self.companionView.pokeBounce > 0.0) {
        self.companionView.pokeBounce += slowTick ? 0.18 : 0.09;
        if (self.companionView.pokeBounce >= 1.0) {
            self.companionView.pokeBounce = 0.0;
            bounceEnded = YES;
        }
    }
    [self.companionView syncSubframesAndLayout];
    [self.companionView setNeedsDisplay:YES];
    if (bounceEnded) {
        [self syncAnimationTimer];
    }
}

@end

static HoxiCompanionController *hoxiCompanionCtrl;

void hoxi_companion_wake_animation(void) {
    if (hoxiCompanionCtrl != nil) {
        [hoxiCompanionCtrl syncAnimationTimer];
    }
}

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
                [hoxiCompanionCtrl stopAnimationTimer];
                [imgData release];
                return;
            }

            [hoxiCompanionCtrl ensurePanel];
            HoxiCompanionView *view = hoxiCompanionCtrl.companionView;
            if (![view.badgeLabel isEqualToString:sBadge] ||
                ![view.levelText isEqualToString:sLevel] ||
                ![view.dialogue isEqualToString:sDialogue]) {
                view.metricsDirty = YES;
            }
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
            [hoxiCompanionCtrl syncAnimationTimer];
        }
    });
}

void hoxi_companion_stop(void) {
    hoxi_companion_on_main(^{
        @autoreleasepool {
            if (hoxiCompanionCtrl != nil) {
                [hoxiCompanionCtrl stopAnimationTimer];
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
