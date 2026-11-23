//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreImage/CoreImage.h>
#import <ImageIO/ImageIO.h>
#import <unistd.h>

static NSDictionary *failure(NSString *message) { return @{ @"error":message }; }
static NSString *safeSubstrPrefix(NSString *s, NSUInteger limit) {
 if (s.length <= limit) return s;
 NSUInteger cut = limit;
 if (cut > 0) {
  unichar c = [s characterAtIndex:cut - 1];
  if (c >= 0xD800 && c <= 0xDBFF) cut--;
 }
 return [s substringToIndex:cut];
}
static NSString *safeSubstrSuffix(NSString *s, NSUInteger limit) {
 if (s.length <= limit) return s;
 NSUInteger start = s.length - limit;
 unichar c = [s characterAtIndex:start];
 if (c >= 0xDC00 && c <= 0xDFFF && start + 1 < s.length) start++;
 return [s substringFromIndex:start];
}
static NSString *shortText(id value, NSUInteger limit) {
 NSString *s = [value isKindOfClass:NSString.class] ? value : @"";
 return safeSubstrPrefix(s, limit);
}
static NSString *shortValueText(id value, NSUInteger limit) {
 NSString *s = [value isKindOfClass:NSString.class] ? value : @"";
 if (s.length <= limit) return s;
 if (limit < 32) return safeSubstrPrefix(s, limit);
 NSUInteger head = (limit - 7) / 2;
 NSUInteger tail = limit - 7 - head;
 return [NSString stringWithFormat:@"%@ [...] %@", safeSubstrPrefix(s, head), safeSubstrSuffix(s, tail)];
}
static NSDictionary *rectJSON(CGRect r) {
 return @{ @"x":@(r.origin.x), @"y":@(r.origin.y), @"width":@(r.size.width), @"height":@(r.size.height) };
}
static BOOL isAXErrorValue(id v) {
 if (!v) return YES;
 CFTypeRef ref = (__bridge CFTypeRef)v;
 return CFGetTypeID(ref) == AXValueGetTypeID() && AXValueGetType((AXValueRef)ref) == kAXValueAXErrorType;
}
static id axValue(AXUIElementRef el, CFStringRef name) {
 CFTypeRef value = NULL;
 if (AXUIElementCopyAttributeValue(el, name, &value) != kAXErrorSuccess) return nil;
 return CFBridgingRelease(value);
}
static CGRect axRectFromValues(id pos, id size) {
 CGPoint p = CGPointZero;
 CGSize s = CGSizeZero;
 if (pos && !isAXErrorValue(pos) && CFGetTypeID((__bridge CFTypeRef)pos) == AXValueGetTypeID()) {
  AXValueGetValue((__bridge AXValueRef)pos, kAXValueCGPointType, &p);
 }
 if (size && !isAXErrorValue(size) && CFGetTypeID((__bridge CFTypeRef)size) == AXValueGetTypeID()) {
  AXValueGetValue((__bridge AXValueRef)size, kAXValueCGSizeType, &s);
 }
 return (CGRect){p, s};
}
static CGRect axRect(AXUIElementRef el) {
 return axRectFromValues(axValue(el, kAXPositionAttribute), axValue(el, kAXSizeAttribute));
}
static NSArray *axBatchAttributes(void) {
 static NSArray *attrs = nil;
 static dispatch_once_t onceToken;
 dispatch_once(&onceToken, ^{
  attrs = @[
   (__bridge NSString *)kAXRoleAttribute,
   (__bridge NSString *)kAXSubroleAttribute,
   (__bridge NSString *)kAXTitleAttribute,
   (__bridge NSString *)kAXDescriptionAttribute,
   (__bridge NSString *)kAXHelpAttribute,
   @"AXPlaceholderValue",
   (__bridge NSString *)kAXValueAttribute,
   (__bridge NSString *)kAXPositionAttribute,
   (__bridge NSString *)kAXSizeAttribute,
   (__bridge NSString *)kAXFocusedAttribute,
   (__bridge NSString *)kAXEnabledAttribute,
   (__bridge NSString *)kAXChildrenAttribute
  ];
 });
 return attrs;
}
static BOOL isInteractiveRole(NSString *role, NSString *subrole) {
 static NSSet<NSString *> *roles = nil;
 static NSSet<NSString *> *subroles = nil;
 static dispatch_once_t onceToken;
 dispatch_once(&onceToken, ^{
  roles = [NSSet setWithArray:@[
   @"AXButton", @"AXTextField", @"AXTextArea", @"AXComboBox", @"AXSearchField",
   @"AXSecureTextField", @"AXCheckBox", @"AXRadioButton", @"AXPopUpButton",
   @"AXMenuButton", @"AXMenuItem", @"AXLink", @"AXTab", @"AXSwitch",
   @"AXSlider", @"AXIncrementor", @"AXDisclosureTriangle", @"AXSegmentedControl",
   @"AXColorWell", @"AXDateField", @"AXTimeField"
  ]];
  subroles = [NSSet setWithArray:@[
   @"AXSecureTextField", @"AXSearchField", @"AXSwitch", @"AXTabButton",
   @"AXCloseButton", @"AXMinimizeButton", @"AXZoomButton", @"AXFullScreenButton",
   @"AXToggle"
  ]];
 });
 return [roles containsObject:role] || [subroles containsObject:subrole];
}
static BOOL isContainerRole(NSString *role) {
 static NSSet<NSString *> *containers = nil;
 static dispatch_once_t onceToken;
 dispatch_once(&onceToken, ^{
  containers = [NSSet setWithArray:@[
   @"AXWindow", @"AXSheet", @"AXDialog", @"AXGroup", @"AXScrollArea",
   @"AXSplitGroup", @"AXLayoutArea", @"AXList", @"AXWebArea", @"AXGenericElement",
   @"AXOutline", @"AXTable", @"AXRow", @"AXColumn", @"AXCell", @"AXSection",
   @"AXToolbar", @"AXTabGroup", @"AXSplitter", @"AXGrowArea", @"AXMatte",
   @"AXRuler", @"AXPopover", @"AXDrawer", @"AXScrollBar", @"AXUnknown",
   @"AXApplication"
  ]];
 });
 return [containers containsObject:role];
}
static NSArray *axLabelAttributes(void) {
 static NSArray *attrs = nil;
 static dispatch_once_t onceToken;
 dispatch_once(&onceToken, ^{
  attrs = @[
   (__bridge NSString *)kAXRoleAttribute,
   (__bridge NSString *)kAXSubroleAttribute,
   (__bridge NSString *)kAXTitleAttribute,
   (__bridge NSString *)kAXDescriptionAttribute,
   (__bridge NSString *)kAXValueAttribute,
   (__bridge NSString *)kAXChildrenAttribute
  ];
 });
 return attrs;
}
static NSString *extractChildLabel(NSArray *children, int depth, int *budget) {
 if (depth > 2 || *budget <= 0 || ![children isKindOfClass:NSArray.class]) return @"";
 CFArrayRef labelAttrs = (__bridge CFArrayRef)axLabelAttributes();
 for (id child in children) {
  if (*budget <= 0) break;
  (*budget)--;
  AXUIElementRef ref = (__bridge AXUIElementRef)child;
  CFArrayRef rawVals = NULL;
  if (AXUIElementCopyMultipleAttributeValues(ref, labelAttrs, 0, &rawVals) != kAXErrorSuccess || !rawVals) continue;
  NSArray *vals = CFBridgingRelease(rawVals);
  if (vals.count < 6) continue;
  NSString *role = isAXErrorValue(vals[0]) ? @"" : shortText(vals[0], 80);
  NSString *subrole = isAXErrorValue(vals[1]) ? @"" : shortText(vals[1], 80);
  if ([role isEqualToString:@"AXSecureTextField"] || [subrole isEqualToString:@"AXSecureTextField"]) continue;
  NSString *label = isAXErrorValue(vals[2]) ? @"" : shortText(vals[2], 256);
  if (!label.length && !isAXErrorValue(vals[3])) label = shortText(vals[3], 256);
  if (!label.length && [role isEqualToString:@"AXStaticText"] && !isAXErrorValue(vals[4])) {
   label = shortText(vals[4], 256);
  }
  if (label.length) return label;
  if (!isAXErrorValue(vals[5]) && [vals[5] isKindOfClass:NSArray.class] && [(NSArray *)vals[5] count] > 0) {
   NSString *nested = extractChildLabel(vals[5], depth + 1, budget);
   if (nested.length) return nested;
  }
 }
 return @"";
}
typedef struct {
 __unsafe_unretained NSMutableArray *visibleInteractive;
 __unsafe_unretained NSMutableArray *visibleContent;
 CGRect windowRect;
 int visited;
 int maxVisited;
 CFAbsoluteTime deadline;
} HoxiAXCollector;
static void collectAX(AXUIElementRef node, HoxiAXCollector *ctx, int depth) {
 if (depth > 32 || ctx->visited >= ctx->maxVisited || CFAbsoluteTimeGetCurrent() >= ctx->deadline) return;
 ctx->visited++;
 CFArrayRef rawVals = NULL;
 if (AXUIElementCopyMultipleAttributeValues(node, (__bridge CFArrayRef)axBatchAttributes(), 0, &rawVals) != kAXErrorSuccess || !rawVals) return;
 NSArray *vals = CFBridgingRelease(rawVals);
 if (vals.count < 12) return;

 NSString *role = isAXErrorValue(vals[0]) ? @"" : shortText(vals[0], 80);
 if ([role isEqualToString:@"AXScrollBar"]) return;
 NSString *subrole = isAXErrorValue(vals[1]) ? @"" : shortText(vals[1], 80);
 BOOL secure = [subrole isEqualToString:@"AXSecureTextField"] || [role isEqualToString:@"AXSecureTextField"];
 BOOL interactive = secure || isInteractiveRole(role, subrole);
 NSString *name = @"";
 NSString *value = @"";
 if (!secure) {
  if (!isAXErrorValue(vals[2])) name = shortText(vals[2], 256);
  if (!name.length && !isAXErrorValue(vals[3])) name = shortText(vals[3], 256);
  if (!name.length && !isAXErrorValue(vals[4])) name = shortText(vals[4], 256);
  if (!name.length && !isAXErrorValue(vals[5])) name = shortText(vals[5], 256);
  if (!isAXErrorValue(vals[6])) value = shortValueText(vals[6], 1000);
 }
 CGRect r = axRectFromValues(vals[7], vals[8]);
 BOOL hasSize = (r.size.width > 0.5 && r.size.height > 0.5);
 BOOL visible = hasSize && (CGRectIsEmpty(ctx->windowRect) || CGRectIntersectsRect(r, ctx->windowRect));
 id children = isAXErrorValue(vals[11]) ? nil : vals[11];

 if (interactive && !secure && visible && !name.length && !value.length &&
     r.size.width > 12.0 && r.size.height > 12.0 &&
     [children isKindOfClass:NSArray.class] && [(NSArray *)children count] > 0) {
  int labelBudget = 8;
  name = extractChildLabel(children, 0, &labelBudget);
 }

 if (!secure && name.length > 0 && value.length > 0) {
  if ([name isEqualToString:value]) {
   value = @"";
  } else if ([role isEqualToString:@"AXStaticText"] &&
             ([value hasPrefix:name] || (name.length > 40 && [value hasPrefix:safeSubstrPrefix(name, 32)]))) {
   name = @"";
  }
 }

 BOOL isThinNamelessChrome = interactive && !secure && !name.length && !value.length && (r.size.width <= 12.0 || r.size.height <= 12.0);
 BOOL shouldEmit = secure || (interactive && visible && !isThinNamelessChrome) ||
  (!isContainerRole(role) && visible && (name.length > 0 || value.length > 0)) ||
  (visible && name.length > 0 && value.length > 0);
 if (shouldEmit) {
  NSMutableDictionary *entry = [@{
   @"role": role,
   @"name": name,
   @"value": value,
   @"secure": (secure ? @YES : @NO),
   @"bounds": rectJSON(r)
  } mutableCopy];
  if (!isAXErrorValue(vals[9]) && [vals[9] isKindOfClass:NSNumber.class] && [vals[9] boolValue]) {
   entry[@"focused"] = @YES;
  }
  if (interactive && !isAXErrorValue(vals[10]) && [vals[10] isKindOfClass:NSNumber.class] && ![vals[10] boolValue]) {
   entry[@"disabled"] = @YES;
  }
  if (interactive) {
   [ctx->visibleInteractive addObject:entry];
  } else {
   if (ctx->visibleContent.count < 900) [ctx->visibleContent addObject:entry];
  }
 }
 if (secure) return;
 BOOL isOverlayContainer = [role isEqualToString:@"AXSheet"] || [role isEqualToString:@"AXPopover"] ||
  [role isEqualToString:@"AXDialog"] || [role isEqualToString:@"AXMenu"];
 if (depth > 0 && !isOverlayContainer && !CGRectIsEmpty(ctx->windowRect)) {
  if (hasSize && !visible) return;
  if (!hasSize && ([role isEqualToString:@"AXRow"] || [role isEqualToString:@"AXCell"])) return;
 }
 id walkChildren = children;
 if ([children isKindOfClass:NSArray.class] && [(NSArray *)children count] > 20) {
  if ([role isEqualToString:@"AXOutline"] || [role isEqualToString:@"AXTable"]) {
   id visRows = axValue(node, kAXVisibleRowsAttribute);
   if ([visRows isKindOfClass:NSArray.class] && [(NSArray *)visRows count] > 0) {
    walkChildren = visRows;
   } else {
    id visChildren = axValue(node, kAXVisibleChildrenAttribute);
    if ([visChildren isKindOfClass:NSArray.class] && [(NSArray *)visChildren count] > 0) {
     walkChildren = visChildren;
    }
   }
  } else if ([role isEqualToString:@"AXList"]) {
   id visChildren = axValue(node, kAXVisibleChildrenAttribute);
   if ([visChildren isKindOfClass:NSArray.class] && [(NSArray *)visChildren count] > 0) {
    walkChildren = visChildren;
   }
  }
 }
 if ([walkChildren isKindOfClass:NSArray.class]) {
  for (id child in walkChildren) {
   if (ctx->visited >= ctx->maxVisited || CFAbsoluteTimeGetCurrent() >= ctx->deadline) break;
   collectAX((__bridge AXUIElementRef)child, ctx, depth + 1);
  }
 }
}
static NSMutableArray *mergeAXBuckets(NSMutableArray *visibleInteractive, NSMutableArray *visibleContent) {
 const NSUInteger maxElements = 300;
 NSMutableArray *merged = [NSMutableArray arrayWithCapacity:maxElements];
 if (visibleInteractive.count <= maxElements) {
  [merged addObjectsFromArray:visibleInteractive];
 } else {
  [merged addObjectsFromArray:[visibleInteractive subarrayWithRange:NSMakeRange(0, 150)]];
  [merged addObjectsFromArray:[visibleInteractive subarrayWithRange:NSMakeRange(visibleInteractive.count - 150, 150)]];
 }
 if (merged.count < maxElements && visibleContent.count > 0) {
  NSUInteger remain = maxElements - merged.count;
  if (visibleContent.count <= remain) {
   [merged addObjectsFromArray:visibleContent];
  } else {
   [merged addObjectsFromArray:[visibleContent subarrayWithRange:NSMakeRange(visibleContent.count - remain, remain)]];
  }
 }
 for (NSUInteger i = 0; i < merged.count; i++) {
  merged[i][@"id"] = @(i + 1);
 }
 return merged;
}
static NSArray *appWindows(pid_t pid) {
 NSArray *all = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
 NSMutableArray *result = [NSMutableArray array];
 for (NSDictionary *row in all) {
  if ([row[(id)kCGWindowOwnerPID] intValue] != pid || [row[(id)kCGWindowLayer] intValue] != 0) continue;
  CGRect r;
  if (!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)row[(id)kCGWindowBounds], &r) || r.size.width < 1 || r.size.height < 1) continue;
  [result addObject:@{ @"id":[row[(id)kCGWindowNumber] stringValue], @"title":shortText(row[(id)kCGWindowName], 256), @"bounds":rectJSON(r) }];
  if (result.count >= 64) break;
 }
 return result;
}
@interface HoxiComputerFrame : NSObject<SCStreamOutput>
@property(nonatomic,strong)NSData *data;
@property(nonatomic,strong)dispatch_semaphore_t done;
@end
@implementation HoxiComputerFrame
- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)buffer ofType:(SCStreamOutputType)type {
 if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(buffer)) return;
 CVPixelBufferRef pixel = CMSampleBufferGetImageBuffer(buffer);
 if (!pixel) return;
 @synchronized(self) {
  if (self.data) return;
  CIImage *image = [CIImage imageWithCVPixelBuffer:pixel];
  CIContext *ctx = [CIContext contextWithOptions:nil];
  CGImageRef cg = [ctx createCGImage:image fromRect:image.extent];
  if (!cg) return;
  NSMutableData *data = [NSMutableData data];
  CGImageDestinationRef dest = CGImageDestinationCreateWithData((__bridge CFMutableDataRef)data, CFSTR("public.jpeg"), 1, NULL);
  if (dest) {
   CGImageDestinationAddImage(dest, cg, (__bridge CFDictionaryRef)@{ (id)kCGImageDestinationLossyCompressionQuality:@0.82 });
   if (CGImageDestinationFinalize(dest)) self.data = data;
   CFRelease(dest);
  }
  CGImageRelease(cg);
  dispatch_semaphore_signal(self.done);
 }
}
@end
static NSDictionary *capture(CGWindowID windowID) {
 dispatch_semaphore_t contentReady = dispatch_semaphore_create(0);
 __block SCShareableContent *content = nil;
 [SCShareableContent getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *value, NSError *error) {
  content = value;
  dispatch_semaphore_signal(contentReady);
 }];
 if (dispatch_semaphore_wait(contentReady, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC))) return failure(@"screen_capture_timeout");
 SCWindow *window = nil;
 for (SCWindow *candidate in content.windows) {
  if (candidate.windowID == windowID) { window = candidate; break; }
 }
 if (!window) return failure(@"window_not_capturable");
 double scale = fmin(1.0, 1600.0 / fmax(window.frame.size.width, window.frame.size.height));
 NSUInteger width = MAX(1, (NSUInteger)(window.frame.size.width * scale));
 NSUInteger height = MAX(1, (NSUInteger)(window.frame.size.height * scale));
 SCStreamConfiguration *config = [SCStreamConfiguration new];
 config.width = width;
 config.height = height;
 config.queueDepth = 1;
 config.showsCursor = NO;
 config.capturesAudio = NO;
 config.minimumFrameInterval = CMTimeMake(1, 5);
 config.pixelFormat = kCVPixelFormatType_32BGRA;
 SCContentFilter *filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:window];
 SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:config delegate:nil];
 HoxiComputerFrame *frame = [HoxiComputerFrame new];
 frame.done = dispatch_semaphore_create(0);
 NSError *error = nil;
 if (![stream addStreamOutput:frame type:SCStreamOutputTypeScreen sampleHandlerQueue:dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0) error:&error]) {
  return failure(@"screen_capture_unavailable");
 }
 [stream startCaptureWithCompletionHandler:^(NSError *err) {
  if (err) dispatch_semaphore_signal(frame.done);
 }];
 dispatch_semaphore_wait(frame.done, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC));
 [stream stopCaptureWithCompletionHandler:nil];
 NSData *data;
 @synchronized(frame) { data = frame.data; }
 if (!data) return failure(@"screen_capture_unavailable");
 return @{ @"image":[data base64EncodedStringWithOptions:0], @"width":@(width), @"height":@(height) };
}
static BOOL frontIs(pid_t pid) {
 return NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier == pid;
}
static BOOL isClickThroughDockCanvas(NSDictionary *row, CGPoint point) {
 if ([row[(id)kCGWindowLayer] intValue] != kCGDockWindowLevel) return NO;
 NSString *name = row[(id)kCGWindowName];
 if (![name isKindOfClass:NSString.class] || ![name isEqualToString:@"Dock"]) return NO;
 pid_t ownerPID = [row[(id)kCGWindowOwnerPID] intValue];
 NSRunningApplication *owner = [NSRunningApplication runningApplicationWithProcessIdentifier:ownerPID];
 if (![owner.bundleIdentifier isEqualToString:@"com.apple.dock"]) return NO;
 AXUIElementRef sys = AXUIElementCreateSystemWide();
 if (sys) {
  AXUIElementSetMessagingTimeout(sys, 0.1);
  AXUIElementRef hit = NULL;
  if (AXUIElementCopyElementAtPosition(sys, (float)point.x, (float)point.y, &hit) == kAXErrorSuccess && hit) {
   pid_t hitPID = 0;
   AXUIElementGetPid(hit, &hitPID);
   CFRelease(hit);
   CFRelease(sys);
   return hitPID != ownerPID;
  }
  CFRelease(sys);
 }
 return YES;
}
static BOOL pointOwned(CGPoint point, pid_t pid) {
 NSArray *all = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
 for (NSDictionary *row in all) {
  int layer = [row[(id)kCGWindowLayer] intValue];
  if (layer < 0) continue;
  NSNumber *alphaNum = row[(id)kCGWindowAlpha];
  if (alphaNum != nil && alphaNum.doubleValue <= 0.01) continue;
  CGRect bounds;
  if (!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)row[(id)kCGWindowBounds], &bounds)) continue;
  if (!CGRectContainsPoint(bounds, point)) continue;
  if (isClickThroughDockCanvas(row, point)) continue;
  return [row[(id)kCGWindowOwnerPID] intValue] == pid;
 }
 return NO;
}
static CGEventFlags flags(NSDictionary *key) {
 CGEventFlags f = 0;
 if ([key[@"Control"] boolValue]) f |= kCGEventFlagMaskControl;
 if ([key[@"Alt"] boolValue]) f |= kCGEventFlagMaskAlternate;
 if ([key[@"Shift"] boolValue]) f |= kCGEventFlagMaskShift;
 if ([key[@"Meta"] boolValue]) f |= kCGEventFlagMaskCommand;
 return f;
}
static CGKeyCode keyCode(NSString *key) {
 NSDictionary *codes = @{
  @"Enter":@36, @"Tab":@48, @"Space":@49, @"Backspace":@51, @"Escape":@53, @"Delete":@117,
  @"Home":@115, @"End":@119, @"PageUp":@116, @"PageDown":@121,
  @"ArrowLeft":@123, @"ArrowRight":@124, @"ArrowDown":@125, @"ArrowUp":@126,
  @"F1":@122, @"F2":@120, @"F3":@99, @"F4":@118, @"F5":@96, @"F6":@97,
  @"F7":@98, @"F8":@100, @"F9":@101, @"F10":@109, @"F11":@103, @"F12":@111,
  @"a":@0, @"s":@1, @"d":@2, @"f":@3, @"h":@4, @"g":@5, @"z":@6, @"x":@7,
  @"c":@8, @"v":@9, @"b":@11, @"q":@12, @"w":@13, @"e":@14, @"r":@15, @"y":@16,
  @"t":@17, @"1":@18, @"2":@19, @"3":@20, @"4":@21, @"6":@22, @"5":@23, @"=":@24,
  @"9":@25, @"7":@26, @"-":@27, @"8":@28, @"0":@29, @"]":@30, @"o":@31, @"u":@32,
  @"[":@33, @"i":@34, @"p":@35, @"l":@37, @"j":@38, @"'":@39, @"k":@40, @";":@41,
  @"\\":@42, @",":@43, @"/":@44, @"n":@45, @"m":@46, @".":@47, @"`":@50
 };
 NSNumber *code = codes[key] ?: codes[key.lowercaseString];
 return code ? (CGKeyCode)code.intValue : UINT16_MAX;
}
static void postMouse(CGEventType type, CGPoint p, CGMouseButton button, int count) {
 CGEventRef e = CGEventCreateMouseEvent(NULL, type, p, button);
 if (e) {
  CGEventSetIntegerValueField(e, kCGMouseEventClickState, count);
  CGEventPost(kCGHIDEventTap, e);
  CFRelease(e);
 }
}
static void postKeyToPid(pid_t pid, CGKeyCode code, CGEventFlags eventFlags) {
 CGEventRef down = CGEventCreateKeyboardEvent(NULL, code, true);
 CGEventRef up = CGEventCreateKeyboardEvent(NULL, code, false);
 CGEventSetFlags(down, eventFlags);
 CGEventSetFlags(up, 0);
 CGEventPostToPid(pid, down);
 CGEventPostToPid(pid, up);
 CFRelease(down);
 CFRelease(up);
}
static BOOL postUnicodeText(pid_t pid, NSString *raw) {
 NSString *normalized = [[raw stringByReplacingOccurrencesOfString:@"\r\n" withString:@"\n"] stringByReplacingOccurrencesOfString:@"\r" withString:@"\n"];
 NSArray<NSString *> *lines = [normalized componentsSeparatedByString:@"\n"];
 for (NSUInteger lineIdx = 0; lineIdx < lines.count; lineIdx++) {
  if (lineIdx > 0) {
   if (!frontIs(pid)) return NO;
   postKeyToPid(pid, 36, kCGEventFlagMaskShift);
   usleep(5000);
  }
  NSString *line = lines[lineIdx];
  NSUInteger i = 0;
  while (i < line.length) {
   if (!frontIs(pid)) return NO;
   NSUInteger length = MIN((NSUInteger)16, line.length - i);
   UniChar chars[16];
   [line getCharacters:chars range:NSMakeRange(i, length)];
   if (length > 1 && chars[length - 1] >= 0xD800 && chars[length - 1] <= 0xDBFF) {
    length--;
   }
   CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
   CGEventRef up = CGEventCreateKeyboardEvent(NULL, 0, false);
   CGEventSetFlags(down, 0);
   CGEventSetFlags(up, 0);
   CGEventKeyboardSetUnicodeString(down, length, chars);
   CGEventKeyboardSetUnicodeString(up, length, chars);
   CGEventPostToPid(pid, down);
   CGEventPostToPid(pid, up);
   CFRelease(down);
   CFRelease(up);
   i += length;
   if (i < line.length) usleep(3000);
  }
 }
 return YES;
}
static NSDictionary *input(NSDictionary *payload) {
 pid_t pid = [payload[@"pid"] intValue];
 NSString *wanted = payload[@"windowID"];
 NSDictionary *window = nil;
 for (NSDictionary *w in appWindows(pid)) {
  if ([w[@"id"] isEqualToString:wanted]) { window = w; break; }
 }
 if (!window) return failure(@"window_closed");
 AXUIElementRef root = AXUIElementCreateApplication(pid);
 AXUIElementSetMessagingTimeout(root, 0.4);
 NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
 [app unhide];
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
 [app activateWithOptions:(NSApplicationActivateAllWindows | NSApplicationActivateIgnoringOtherApps)];
#pragma clang diagnostic pop
 AXUIElementSetAttributeValue(root, kAXFrontmostAttribute, kCFBooleanTrue);
 id axWindows = axValue(root, kAXWindowsAttribute);
 if ([axWindows isKindOfClass:NSArray.class]) {
  for (id w in axWindows) {
   CGRect r = axRect((__bridge AXUIElementRef)w);
   NSDictionary *b = window[@"bounds"];
   if (fabs(r.origin.x - [b[@"x"] doubleValue]) < 2 && fabs(r.origin.y - [b[@"y"] doubleValue]) < 2 &&
       fabs(r.size.width - [b[@"width"] doubleValue]) < 2 && fabs(r.size.height - [b[@"height"] doubleValue]) < 2) {
    AXUIElementSetAttributeValue((__bridge AXUIElementRef)w, kAXMainAttribute, kCFBooleanTrue);
    AXUIElementPerformAction((__bridge AXUIElementRef)w, kAXRaiseAction);
    break;
   }
  }
 }
 for (int i = 0; i < 50 && !frontIs(pid); i++) usleep(10000);
 if (!frontIs(pid)) { CFRelease(root); return failure(@"target_app_not_frontmost"); }
 for (NSDictionary *w in appWindows(pid)) {
  if ([w[@"id"] isEqualToString:wanted]) { window = w; break; }
 }
 NSDictionary *action = payload[@"action"], *bounds = window[@"bounds"], *expected = payload[@"expectedBounds"];
 if (fabs([bounds[@"width"] doubleValue] - [expected[@"width"] doubleValue]) > 0.5 ||
     fabs([bounds[@"height"] doubleValue] - [expected[@"height"] doubleValue]) > 0.5) {
  CFRelease(root);
  return failure(@"window_resized_or_closed");
 }
 NSString *op = action[@"action"];
 if ([op isEqualToString:@"focus"]) { CFRelease(root); return @{}; }
 if ([op isEqualToString:@"type"] || [op isEqualToString:@"key"]) {
  id focused = axValue(root, kAXFocusedUIElementAttribute);
  if (focused) {
   NSString *subrole = shortText(axValue((__bridge AXUIElementRef)focused, kAXSubroleAttribute), 80);
   NSString *role = shortText(axValue((__bridge AXUIElementRef)focused, kAXRoleAttribute), 80);
   if ([subrole isEqualToString:@"AXSecureTextField"] || [role isEqualToString:@"AXSecureTextField"]) {
    CFRelease(root);
    return failure(@"secure_field_requires_manual_input");
   }
  }
  if ([op isEqualToString:@"type"]) {
   if (!postUnicodeText(pid, action[@"text"] ?: @"")) {
    CFRelease(root);
    return failure(@"target_app_changed");
   }
  } else {
   NSDictionary *key = payload[@"key"];
   CGKeyCode code = keyCode(key[@"Key"]);
   if (code == UINT16_MAX) { CFRelease(root); return failure(@"unsupported_key"); }
   postKeyToPid(pid, code, flags(key));
  }
  CFRelease(root);
  return @{};
 }
 CFRelease(root);
 CGPoint start = CGPointMake([bounds[@"x"] doubleValue] + [action[@"x"] doubleValue], [bounds[@"y"] doubleValue] + [action[@"y"] doubleValue]);
 if (!pointOwned(start, pid)) return failure(@"target_window_occluded");
 if ([op isEqualToString:@"move"]) { postMouse(kCGEventMouseMoved, start, kCGMouseButtonLeft, 0); return @{}; }
 if ([op isEqualToString:@"scroll"]) {
  CGEventRef event = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitPixel, 2, -[action[@"deltaY"] intValue], -[action[@"deltaX"] intValue]);
  CGEventSetLocation(event, start);
  CGEventPost(kCGHIDEventTap, event);
  CFRelease(event);
  return @{};
 }
 NSString *button = action[@"button"];
 CGMouseButton b = [button isEqualToString:@"right"] ? kCGMouseButtonRight : [button isEqualToString:@"middle"] ? kCGMouseButtonCenter : kCGMouseButtonLeft;
 CGEventType down = b == kCGMouseButtonRight ? kCGEventRightMouseDown : b == kCGMouseButtonCenter ? kCGEventOtherMouseDown : kCGEventLeftMouseDown;
 CGEventType up = b == kCGMouseButtonRight ? kCGEventRightMouseUp : b == kCGMouseButtonCenter ? kCGEventOtherMouseUp : kCGEventLeftMouseUp;
 if ([op isEqualToString:@"drag"]) {
  CGPoint end = CGPointMake([bounds[@"x"] doubleValue] + [action[@"endX"] doubleValue], [bounds[@"y"] doubleValue] + [action[@"endY"] doubleValue]);
  CGPoint current = start;
  postMouse(down, start, b, 1);
  for (int i = 1; i <= 20; i++) {
   CGPoint next = CGPointMake(start.x + (end.x - start.x) * i / 20, start.y + (end.y - start.y) * i / 20);
   if (!frontIs(pid) || !pointOwned(next, pid)) { postMouse(up, current, b, 1); return failure(@"target_app_changed"); }
   current = next;
   postMouse(kCGEventLeftMouseDragged, current, b, 1);
   usleep(10000);
  }
  postMouse(up, current, b, 1);
 } else {
  int count = MAX(1, [action[@"clickCount"] intValue]);
  for (int i = 1; i <= count; i++) {
   if (!frontIs(pid) || !pointOwned(start, pid)) return failure(@"target_app_changed");
   postMouse(down, start, b, i);
   postMouse(up, start, b, i);
   usleep(20000);
  }
 }
 return @{};
}
static void openPrivacyPane(NSString *anchor) {
 NSString *url = [NSString stringWithFormat:@"x-apple.systempreferences:com.apple.preference.security?%@", anchor];
 [NSWorkspace.sharedWorkspace openURL:[NSURL URLWithString:url]];
}
static void resetTCC(NSString *service, NSString *bundleID) {
 if (!bundleID.length) return;
 NSTask *task = [NSTask new];
 task.executableURL = [NSURL fileURLWithPath:@"/usr/bin/tccutil"];
 task.arguments = @[@"reset", service, bundleID];
 task.standardOutput = [NSPipe pipe];
 task.standardError = [NSPipe pipe];
 NSError *err = nil;
 if ([task launchAndReturnError:&err]) [task waitUntilExit];
}
static NSDictionary *permissions(NSDictionary *request) {
 NSBundle *bundle = NSBundle.mainBundle;
 NSString *bundleID = bundle.bundleIdentifier ?: @"";
 NSString *appPath = bundle.bundlePath ?: @"";
 NSString *appName = [bundle objectForInfoDictionaryKey:@"CFBundleDisplayName"] ?: [bundle objectForInfoDictionaryKey:@"CFBundleName"] ?: [NSProcessInfo.processInfo processName] ?: @"Hoxi";
 NSString *prompt = [request[@"prompt"] isKindOfClass:NSString.class] ? request[@"prompt"] : @"";
 if ([prompt isEqualToString:@"screen"]) {
  CGRequestScreenCaptureAccess();
  if (!CGPreflightScreenCaptureAccess()) openPrivacyPane(@"Privacy_ScreenCapture");
 } else if ([prompt isEqualToString:@"accessibility"]) {
  AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)@{ (__bridge NSString *)kAXTrustedCheckOptionPrompt:@YES });
  if (!AXIsProcessTrusted()) openPrivacyPane(@"Privacy_Accessibility");
 } else if ([prompt isEqualToString:@"reset"]) {
  BOOL needAX = !AXIsProcessTrusted(), needScreen = !CGPreflightScreenCaptureAccess(), bothReady = !needAX && !needScreen;
  if (needAX || bothReady) {
   resetTCC(@"Accessibility", bundleID);
   AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)@{ (__bridge NSString *)kAXTrustedCheckOptionPrompt:@YES });
  }
  if (needScreen || bothReady) {
   resetTCC(@"ScreenCapture", bundleID);
   CGRequestScreenCaptureAccess();
  }
  if (!AXIsProcessTrusted()) openPrivacyPane(@"Privacy_Accessibility");
  else if (!CGPreflightScreenCaptureAccess()) openPrivacyPane(@"Privacy_ScreenCapture");
 } else if ([prompt isEqualToString:@"reveal"]) {
  NSURL *targetURL = bundle.bundleURL ?: bundle.executableURL;
  if (targetURL) [NSWorkspace.sharedWorkspace activateFileViewerSelectingURLs:@[targetURL]];
 }
 return @{
  @"supported":@YES, @"platform":@"darwin",
  @"accessibility":(AXIsProcessTrusted() ? @YES : @NO),
  @"screenRecording":(CGPreflightScreenCaptureAccess() ? @YES : @NO),
  @"foregroundOnly":@YES, @"appName":appName, @"bundleID":bundleID, @"appPath":appPath
 };
}
static NSDictionary *inspect(NSDictionary *request) {
 pid_t pid = [request[@"pid"] intValue];
 NSArray *windows = appWindows(pid);
 NSString *wanted = request[@"windowID"];
 NSDictionary *selected = nil;
 for (NSDictionary *w in windows) {
  if (!wanted.length || [w[@"id"] isEqualToString:wanted]) { selected = w; break; }
 }
 if (!selected) return failure(@"no_visible_target_window");
 AXUIElementRef root = AXUIElementCreateApplication(pid);
 AXUIElementSetMessagingTimeout(root, 0.25);
 // Unlock Electron / Chromium / WebView accessibility trees on macOS; only wait
 // briefly when the target app actually accepts AXManualAccessibility.
 if (AXUIElementSetAttributeValue(root, CFSTR("AXManualAccessibility"), kCFBooleanTrue) == kAXErrorSuccess) {
  usleep(40000);
 }
 NSMutableArray *visibleInteractive = [NSMutableArray array];
 NSMutableArray *visibleContent = [NSMutableArray array];
 HoxiAXCollector collector = {
  .visibleInteractive = visibleInteractive,
  .visibleContent = visibleContent,
  .windowRect = CGRectZero,
  .visited = 0,
  .maxVisited = 4000,
  .deadline = CFAbsoluteTimeGetCurrent() + 1.5
 };
 id axWindows = axValue(root, kAXWindowsAttribute);
 if ([axWindows isKindOfClass:NSArray.class]) {
  NSDictionary *sb = selected[@"bounds"];
  CGRect selectedRect = CGRectMake([sb[@"x"] doubleValue], [sb[@"y"] doubleValue], [sb[@"width"] doubleValue], [sb[@"height"] doubleValue]);
  for (id window in axWindows) {
   CGRect r = axRect((__bridge AXUIElementRef)window);
   if (fabs(r.origin.x - selectedRect.origin.x) < 4 && fabs(r.origin.y - selectedRect.origin.y) < 4 &&
       fabs(r.size.width - selectedRect.size.width) < 4 && fabs(r.size.height - selectedRect.size.height) < 4) {
    collector.windowRect = CGRectIsEmpty(r) ? selectedRect : r;
    collectAX((__bridge AXUIElementRef)window, &collector, 0);
    break;
   }
  }
  for (id window in axWindows) {
   if (collector.visited >= collector.maxVisited || CFAbsoluteTimeGetCurrent() >= collector.deadline) break;
   CGRect r = axRect((__bridge AXUIElementRef)window);
   if (fabs(r.origin.x - selectedRect.origin.x) < 4 && fabs(r.origin.y - selectedRect.origin.y) < 4 &&
       fabs(r.size.width - selectedRect.size.width) < 4 && fabs(r.size.height - selectedRect.size.height) < 4) continue;
   collector.windowRect = r;
   collectAX((__bridge AXUIElementRef)window, &collector, 0);
  }
 }
 CFRelease(root);
 NSMutableArray *elements = mergeAXBuckets(visibleInteractive, visibleContent);
 NSMutableDictionary *value = [@{ @"windows":windows, @"windowID":selected[@"id"], @"elements":elements, @"width":@0, @"height":@0 } mutableCopy];
 if ([request[@"image"] boolValue]) {
  if (!CGPreflightScreenCaptureAccess()) {
   CGRequestScreenCaptureAccess();
   return failure(@"screen_recording_permission_required");
  }
  NSDictionary *cap = capture((CGWindowID)[selected[@"id"] longLongValue]);
  if (cap[@"error"]) return cap;
  [value addEntriesFromDictionary:cap];
 }
 return value;
}
char *hoxi_computer_call(const char *operation, const char *payload) { @autoreleasepool {
 NSString *op = [NSString stringWithUTF8String:operation];
 NSData *data = [[NSString stringWithUTF8String:payload] dataUsingEncoding:NSUTF8StringEncoding];
 NSDictionary *request = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
 NSDictionary *result = nil;
 if ([op isEqualToString:@"permissions"]) result = permissions(request);
 else if ([op isEqualToString:@"apps"]) {
  NSMutableArray *apps = [NSMutableArray array];
  for (NSRunningApplication *app in NSWorkspace.sharedWorkspace.runningApplications) {
   if (app.activationPolicy != NSApplicationActivationPolicyRegular || app.terminated || !app.launchDate || !app.executableURL || app.processIdentifier == getpid()) continue;
   NSString *started = [NSString stringWithFormat:@"%.6f", app.launchDate.timeIntervalSince1970];
   [apps addObject:@{ @"id":[NSString stringWithFormat:@"%d:%@", app.processIdentifier, started], @"pid":@(app.processIdentifier), @"name":app.localizedName ?: @"", @"bundleID":app.bundleIdentifier ?: @"", @"path":app.executableURL.path ?: @"", @"started":started }];
  }
  result = @{ @"apps":apps };
 }
 else if (!AXIsProcessTrusted()) {
  AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)@{ (__bridge NSString *)kAXTrustedCheckOptionPrompt:@YES });
  result = failure(@"accessibility_permission_required");
 }
 else if ([op isEqualToString:@"inspect"]) result = inspect(request);
 else if ([op isEqualToString:@"input"]) result = input(request);
 else result = failure(@"unsupported_computer_operation");
 NSData *json = [NSJSONSerialization dataWithJSONObject:result ?: failure(@"native_computer_failure") options:0 error:nil];
 return strdup([[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding].UTF8String);
}}
