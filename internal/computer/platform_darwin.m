//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreImage/CoreImage.h>
#import <ImageIO/ImageIO.h>
#import <unistd.h>

static NSDictionary *failure(NSString *message) { return @{ @"error":message }; }
static NSString *shortText(id value,NSUInteger limit) { NSString *s=[value isKindOfClass:NSString.class]?value:@"";return s.length>limit?[s substringToIndex:limit]:s; }
static NSDictionary *rectJSON(CGRect r) { return @{ @"x":@(r.origin.x),@"y":@(r.origin.y),@"width":@(r.size.width),@"height":@(r.size.height) }; }
static id axValue(AXUIElementRef el,CFStringRef name) { CFTypeRef value=NULL;if(AXUIElementCopyAttributeValue(el,name,&value)!=kAXErrorSuccess)return nil;return CFBridgingRelease(value); }
static CGRect axRect(AXUIElementRef el) {CGPoint p=CGPointZero;CGSize s=CGSizeZero;id pos=axValue(el,kAXPositionAttribute),size=axValue(el,kAXSizeAttribute);if(pos&&CFGetTypeID((__bridge CFTypeRef)pos)==AXValueGetTypeID())AXValueGetValue((__bridge AXValueRef)pos,kAXValueCGPointType,&p);if(size&&CFGetTypeID((__bridge CFTypeRef)size)==AXValueGetTypeID())AXValueGetValue((__bridge AXValueRef)size,kAXValueCGSizeType,&s);return (CGRect){p,s};}
static void appendAX(AXUIElementRef node,NSMutableArray *rows,int depth) {
 if(depth>14||rows.count>=300)return;
 NSString *role=shortText(axValue(node,kAXRoleAttribute),80),*subrole=shortText(axValue(node,kAXSubroleAttribute),80);
 BOOL secure=[subrole isEqualToString:@"AXSecureTextField"]||[role isEqualToString:@"AXSecureTextField"];
 NSString *name=secure?@"":shortText(axValue(node,kAXTitleAttribute),256);if(!secure&&!name.length)name=shortText(axValue(node,kAXDescriptionAttribute),256);
 NSString *value=secure?@"":shortText(axValue(node,kAXValueAttribute),1000);
 [rows addObject:@{@"id":@(rows.count+1),@"role":role,@"name":name,@"value":value,@"secure":(secure?@YES:@NO),@"bounds":rectJSON(axRect(node))}];
 id children=axValue(node,kAXChildrenAttribute);if([children isKindOfClass:NSArray.class])for(id child in children){if(rows.count>=300)break;appendAX((__bridge AXUIElementRef)child,rows,depth+1);}
}
static NSArray *appWindows(pid_t pid) {
 NSArray *all=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID));NSMutableArray *result=[NSMutableArray array];
 for(NSDictionary *row in all){if([row[(id)kCGWindowOwnerPID] intValue]!=pid||[row[(id)kCGWindowLayer] intValue]!=0)continue;CGRect r; if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)row[(id)kCGWindowBounds],&r)||r.size.width<1||r.size.height<1)continue;
  [result addObject:@{@"id":[row[(id)kCGWindowNumber] stringValue],@"title":shortText(row[(id)kCGWindowName],256),@"bounds":rectJSON(r)}];if(result.count>=64)break;}
 return result;
}
@interface HoxiComputerFrame : NSObject<SCStreamOutput>
@property(nonatomic,strong)NSData *data;
@property(nonatomic,strong)dispatch_semaphore_t done;
@end
@implementation HoxiComputerFrame
- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)buffer ofType:(SCStreamOutputType)type {
 if(type!=SCStreamOutputTypeScreen||!CMSampleBufferIsValid(buffer))return;
 CVPixelBufferRef pixel=CMSampleBufferGetImageBuffer(buffer);if(!pixel)return;
 @synchronized(self){if(self.data)return;CIImage *image=[CIImage imageWithCVPixelBuffer:pixel];CIContext *ctx=[CIContext contextWithOptions:nil];CGImageRef cg=[ctx createCGImage:image fromRect:image.extent];if(!cg)return;
 NSMutableData *data=[NSMutableData data];CGImageDestinationRef dest=CGImageDestinationCreateWithData((__bridge CFMutableDataRef)data,CFSTR("public.jpeg"),1,NULL);
 if(dest){CGImageDestinationAddImage(dest,cg,(__bridge CFDictionaryRef)@{(id)kCGImageDestinationLossyCompressionQuality:@0.82});if(CGImageDestinationFinalize(dest))self.data=data;CFRelease(dest);}CGImageRelease(cg);dispatch_semaphore_signal(self.done);}
}
@end
static NSDictionary *capture(CGWindowID windowID) {
 dispatch_semaphore_t contentReady=dispatch_semaphore_create(0);__block SCShareableContent *content=nil;
 [SCShareableContent getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *value,NSError *error){content=value;dispatch_semaphore_signal(contentReady);}];
 if(dispatch_semaphore_wait(contentReady,dispatch_time(DISPATCH_TIME_NOW,3*NSEC_PER_SEC)))return failure(@"screen_capture_timeout");
 SCWindow *window=nil;for(SCWindow *candidate in content.windows)if(candidate.windowID==windowID){window=candidate;break;}if(!window)return failure(@"window_not_capturable");
 double scale=fmin(1.0,1600.0/fmax(window.frame.size.width,window.frame.size.height));NSUInteger width=MAX(1,(NSUInteger)(window.frame.size.width*scale)),height=MAX(1,(NSUInteger)(window.frame.size.height*scale));
 SCStreamConfiguration *config=[SCStreamConfiguration new];config.width=width;config.height=height;config.queueDepth=1;config.showsCursor=NO;config.capturesAudio=NO;config.minimumFrameInterval=CMTimeMake(1,5);config.pixelFormat=kCVPixelFormatType_32BGRA;
 SCContentFilter *filter=[[SCContentFilter alloc]initWithDesktopIndependentWindow:window];SCStream *stream=[[SCStream alloc]initWithFilter:filter configuration:config delegate:nil];HoxiComputerFrame *frame=[HoxiComputerFrame new];frame.done=dispatch_semaphore_create(0);
 NSError *error=nil;if(![stream addStreamOutput:frame type:SCStreamOutputTypeScreen sampleHandlerQueue:dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0) error:&error])return failure(@"screen_capture_unavailable");
 [stream startCaptureWithCompletionHandler:^(NSError *err){if(err)dispatch_semaphore_signal(frame.done);}];dispatch_semaphore_wait(frame.done,dispatch_time(DISPATCH_TIME_NOW,3*NSEC_PER_SEC));[stream stopCaptureWithCompletionHandler:nil];
 NSData *data;@synchronized(frame){data=frame.data;}if(!data)return failure(@"screen_capture_unavailable");
 return @{@"image":[data base64EncodedStringWithOptions:0],@"width":@(width),@"height":@(height)};
}
static BOOL frontIs(pid_t pid) {return NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier==pid;}
static BOOL pointOwned(CGPoint point,pid_t pid) {
 NSArray *all=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID));for(NSDictionary *row in all){CGRect bounds;if(!CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)row[(id)kCGWindowBounds],&bounds))continue;if(CGRectContainsPoint(bounds,point))return [row[(id)kCGWindowOwnerPID]intValue]==pid;}return NO;
}
static CGEventFlags flags(NSDictionary *key){CGEventFlags f=0;if([key[@"Control"]boolValue])f|=kCGEventFlagMaskControl;if([key[@"Alt"]boolValue])f|=kCGEventFlagMaskAlternate;if([key[@"Shift"]boolValue])f|=kCGEventFlagMaskShift;if([key[@"Meta"]boolValue])f|=kCGEventFlagMaskCommand;return f;}
static CGKeyCode keyCode(NSString *key) {
 NSDictionary *codes=@{@"Enter":@36,@"Tab":@48,@"Space":@49,@"Backspace":@51,@"Escape":@53,@"Delete":@117,@"Home":@115,@"End":@119,@"PageUp":@116,@"PageDown":@121,@"ArrowLeft":@123,@"ArrowRight":@124,@"ArrowDown":@125,@"ArrowUp":@126,
 @"a":@0,@"s":@1,@"d":@2,@"f":@3,@"h":@4,@"g":@5,@"z":@6,@"x":@7,@"c":@8,@"v":@9,@"b":@11,@"q":@12,@"w":@13,@"e":@14,@"r":@15,@"y":@16,@"t":@17,@"1":@18,@"2":@19,@"3":@20,@"4":@21,@"6":@22,@"5":@23,@"=":@24,@"9":@25,@"7":@26,@"-":@27,@"8":@28,@"0":@29,@"]":@30,@"o":@31,@"u":@32,@"[":@33,@"i":@34,@"p":@35,@"l":@37,@"j":@38,@"'":@39,@"k":@40,@";":@41,@"\\":@42,@",":@43,@"/":@44,@"n":@45,@"m":@46,@".":@47,@"`":@50};NSNumber *code=codes[key]?:codes[key.lowercaseString];return code?(CGKeyCode)code.intValue:UINT16_MAX;
}
static void postMouse(CGEventType type,CGPoint p,CGMouseButton button,int count){CGEventRef e=CGEventCreateMouseEvent(NULL,type,p,button);if(e){CGEventSetIntegerValueField(e,kCGMouseEventClickState,count);CGEventPost(kCGHIDEventTap,e);CFRelease(e);}}
static NSDictionary *input(NSDictionary *payload) {
 pid_t pid=[payload[@"pid"]intValue];NSString *wanted=payload[@"windowID"];NSDictionary *window=nil;for(NSDictionary *w in appWindows(pid))if([w[@"id"]isEqualToString:wanted]){window=w;break;}if(!window)return failure(@"window_closed");
 AXUIElementRef root=AXUIElementCreateApplication(pid);AXUIElementSetMessagingTimeout(root,0.4);
 NSRunningApplication *app=[NSRunningApplication runningApplicationWithProcessIdentifier:pid];[app activateWithOptions:0];
 id axWindows=axValue(root,kAXWindowsAttribute);if([axWindows isKindOfClass:NSArray.class])for(id w in axWindows){CGRect r=axRect((__bridge AXUIElementRef)w);NSDictionary *b=window[@"bounds"];if(fabs(r.origin.x-[b[@"x"]doubleValue])<2&&fabs(r.origin.y-[b[@"y"]doubleValue])<2&&fabs(r.size.width-[b[@"width"]doubleValue])<2&&fabs(r.size.height-[b[@"height"]doubleValue])<2){AXUIElementPerformAction((__bridge AXUIElementRef)w,kAXRaiseAction);break;}}
 for(int i=0;i<50&&!frontIs(pid);i++)usleep(10000);if(!frontIs(pid)){CFRelease(root);return failure(@"target_app_not_frontmost");}
 for(NSDictionary *w in appWindows(pid))if([w[@"id"]isEqualToString:wanted]){window=w;break;}
 NSDictionary *action=payload[@"action"],*bounds=window[@"bounds"],*expected=payload[@"expectedBounds"];if(fabs([bounds[@"width"]doubleValue]-[expected[@"width"]doubleValue])>0.5||fabs([bounds[@"height"]doubleValue]-[expected[@"height"]doubleValue])>0.5){CFRelease(root);return failure(@"window_resized_or_closed");}NSString *op=action[@"action"];
 if([op isEqualToString:@"focus"]){CFRelease(root);return @{};}
 if([op isEqualToString:@"type"]||[op isEqualToString:@"key"]){id focused=axValue(root,kAXFocusedUIElementAttribute);if(focused){NSString *subrole=shortText(axValue((__bridge AXUIElementRef)focused,kAXSubroleAttribute),80);if([subrole isEqualToString:@"AXSecureTextField"]){CFRelease(root);return failure(@"secure_field_requires_manual_input");}}
  if([op isEqualToString:@"type"]){NSString *text=action[@"text"];for(NSUInteger i=0;i<text.length;i+=64){if(!frontIs(pid)){CFRelease(root);return failure(@"target_app_changed");}UniChar chars[64];NSUInteger length=MIN(64,text.length-i);[text getCharacters:chars range:NSMakeRange(i,length)];CGEventRef down=CGEventCreateKeyboardEvent(NULL,0,true),up=CGEventCreateKeyboardEvent(NULL,0,false);CGEventKeyboardSetUnicodeString(down,length,chars);CGEventKeyboardSetUnicodeString(up,length,chars);CGEventPostToPid(pid,down);CGEventPostToPid(pid,up);CFRelease(down);CFRelease(up);}}
  else {NSDictionary *key=payload[@"key"];CGKeyCode code=keyCode(key[@"Key"]);if(code==UINT16_MAX){CFRelease(root);return failure(@"unsupported_key");}CGEventRef down=CGEventCreateKeyboardEvent(NULL,code,true),up=CGEventCreateKeyboardEvent(NULL,code,false);CGEventSetFlags(down,flags(key));CGEventSetFlags(up,0);CGEventPostToPid(pid,down);CGEventPostToPid(pid,up);CFRelease(down);CFRelease(up);}
  CFRelease(root);return @{};}
 CFRelease(root);CGPoint start=CGPointMake([bounds[@"x"]doubleValue]+[action[@"x"]doubleValue],[bounds[@"y"]doubleValue]+[action[@"y"]doubleValue]);if(!pointOwned(start,pid))return failure(@"target_window_occluded");
 if([op isEqualToString:@"scroll"]){CGEventRef event=CGEventCreateScrollWheelEvent(NULL,kCGScrollEventUnitPixel,2,-[action[@"deltaY"]intValue],-[action[@"deltaX"]intValue]);CGEventSetLocation(event,start);CGEventPost(kCGHIDEventTap,event);CFRelease(event);return @{};}
 NSString *button=action[@"button"];CGMouseButton b=[button isEqualToString:@"right"]?kCGMouseButtonRight:[button isEqualToString:@"middle"]?kCGMouseButtonCenter:kCGMouseButtonLeft;CGEventType down=b==kCGMouseButtonRight?kCGEventRightMouseDown:b==kCGMouseButtonCenter?kCGEventOtherMouseDown:kCGEventLeftMouseDown,up=b==kCGMouseButtonRight?kCGEventRightMouseUp:b==kCGMouseButtonCenter?kCGEventOtherMouseUp:kCGEventLeftMouseUp;
 if([op isEqualToString:@"drag"]){CGPoint end=CGPointMake([bounds[@"x"]doubleValue]+[action[@"endX"]doubleValue],[bounds[@"y"]doubleValue]+[action[@"endY"]doubleValue]);CGPoint current=start;postMouse(down,start,b,1);for(int i=1;i<=20;i++){CGPoint next=CGPointMake(start.x+(end.x-start.x)*i/20,start.y+(end.y-start.y)*i/20);if(!frontIs(pid)||!pointOwned(next,pid)){postMouse(up,current,b,1);return failure(@"target_app_changed");}current=next;postMouse(kCGEventLeftMouseDragged,current,b,1);usleep(10000);}postMouse(up,current,b,1);}
 else {int count=MAX(1,[action[@"clickCount"]intValue]);for(int i=1;i<=count;i++){if(!frontIs(pid)||!pointOwned(start,pid))return failure(@"target_app_changed");postMouse(down,start,b,i);postMouse(up,start,b,i);usleep(20000);}}
 return @{};
}
char *hoxi_computer_call(const char *operation,const char *payload) { @autoreleasepool {
 NSString *op=[NSString stringWithUTF8String:operation];NSData *data=[[NSString stringWithUTF8String:payload]dataUsingEncoding:NSUTF8StringEncoding];NSDictionary *request=[NSJSONSerialization JSONObjectWithData:data options:0 error:nil];NSDictionary *result=nil;
 if([op isEqualToString:@"permissions"]){NSString *prompt=request[@"prompt"];if([prompt isEqualToString:@"screen"])CGRequestScreenCaptureAccess();if([prompt isEqualToString:@"accessibility"])AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)@{(__bridge NSString*)kAXTrustedCheckOptionPrompt:@YES});result=@{@"supported":@YES,@"platform":@"darwin",@"accessibility":(AXIsProcessTrusted()?@YES:@NO),@"screenRecording":(CGPreflightScreenCaptureAccess()?@YES:@NO),@"foregroundOnly":@YES};}
 else if([op isEqualToString:@"apps"]){NSMutableArray *apps=[NSMutableArray array];for(NSRunningApplication *app in NSWorkspace.sharedWorkspace.runningApplications){if(app.activationPolicy!=NSApplicationActivationPolicyRegular||app.terminated||!app.launchDate||!app.executableURL||app.processIdentifier==getpid())continue;NSString *started=[NSString stringWithFormat:@"%.6f",app.launchDate.timeIntervalSince1970];[apps addObject:@{@"id":[NSString stringWithFormat:@"%d:%@",app.processIdentifier,started],@"pid":@(app.processIdentifier),@"name":app.localizedName?:@"",@"bundleID":app.bundleIdentifier?:@"",@"path":app.executableURL.path?:@"",@"started":started}];}result=@{@"apps":apps};}
 else if(!AXIsProcessTrusted())result=failure(@"accessibility_permission_required");
 else if([op isEqualToString:@"inspect"]){pid_t pid=[request[@"pid"]intValue];NSArray *windows=appWindows(pid);NSString *wanted=request[@"windowID"];NSDictionary *selected=nil;for(NSDictionary *w in windows)if(!wanted.length||[w[@"id"]isEqualToString:wanted]){selected=w;break;}if(!selected)result=failure(@"no_visible_target_window");else {AXUIElementRef root=AXUIElementCreateApplication(pid);AXUIElementSetMessagingTimeout(root,0.2);NSMutableArray *elements=[NSMutableArray array];id axWindows=axValue(root,kAXWindowsAttribute);if([axWindows isKindOfClass:NSArray.class])for(id window in axWindows){appendAX((__bridge AXUIElementRef)window,elements,0);if(elements.count>=300)break;}CFRelease(root);NSMutableDictionary *value=[@{@"windows":windows,@"windowID":selected[@"id"],@"elements":elements,@"width":@0,@"height":@0}mutableCopy];if([request[@"image"]boolValue]){if(!CGPreflightScreenCaptureAccess())result=failure(@"screen_recording_permission_required");else [value addEntriesFromDictionary:capture((CGWindowID)[selected[@"id"]longLongValue])];}if(!result)result=value;}}
 else if([op isEqualToString:@"input"])result=input(request);else result=failure(@"unsupported_computer_operation");
 NSData *json=[NSJSONSerialization dataWithJSONObject:result?:failure(@"native_computer_failure") options:0 error:nil];return strdup([[NSString alloc]initWithData:json encoding:NSUTF8StringEncoding].UTF8String);
}}
