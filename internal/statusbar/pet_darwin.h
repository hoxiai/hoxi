#ifndef HOXI_PET_DARWIN_H
#define HOXI_PET_DARWIN_H

#import <Cocoa/Cocoa.h>

extern void HoxiCompanionTriggerAction(const char *kind, const char *sessionID);
void hoxi_companion_wake_animation(void);
void hoxi_companion_reset_position(void);

static NSString * const kPrefOriginSet = @"hoxi.companion.originSet";
static NSString * const kPrefOriginX   = @"hoxi.companion.originX";
static NSString * const kPrefOriginY   = @"hoxi.companion.originY";

static const CGFloat kPanelWidth  = 300.0;
static const CGFloat kPanelHeight = 244.0;

@interface HoxiPassthroughImageView : NSImageView
@end

@interface HoxiCompanionPanel : NSPanel
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
@property(nonatomic, assign) BOOL metricsDirty;
@property(nonatomic, assign) CGFloat cachedPillWidth;
@property(nonatomic, assign) NSSize cachedBubbleSize;
@property(nonatomic, assign) NSPoint dragStartMouse;
@property(nonatomic, assign) NSPoint dragStartOrigin;
@property(nonatomic, assign) BOOL didDrag;
- (BOOL)isUrgentGate;
- (void)syncSubframesAndLayout;
@end

#endif
