//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <CoreGraphics/CoreGraphics.h>
#import <WebKit/WebKit.h>

extern void hoxiGoWindowCalibrateInitialFrame(int width, int height);
extern void hoxiGoWindowDidResize(int width, int height);

static BOOL hoxi_window_contains_webview(NSView *view) {
    if (view == nil) {
        return NO;
    }
    if ([view isKindOfClass:[WKWebView class]]) {
        return YES;
    }
    for (NSView *subview in [view subviews]) {
        if (hoxi_window_contains_webview(subview)) {
            return YES;
        }
    }
    return NO;
}

static BOOL hoxi_is_main_wails_window(NSWindow *window) {
    if (window == nil) {
        return NO;
    }
    Class wailsWindowClass = NSClassFromString(@"WailsWindow");
    if (wailsWindowClass != Nil && [window isKindOfClass:wailsWindowClass]) {
        return YES;
    }
    if (([window styleMask] & NSWindowStyleMaskResizable) == 0) {
        return NO;
    }
    return hoxi_window_contains_webview([window contentView]);
}

@interface HoxiWindowResizeObserver : NSObject
- (void)windowDidResize:(NSNotification *)notification;
@end

@implementation HoxiWindowResizeObserver

- (void)windowDidResize:(NSNotification *)notification {
    id obj = [notification object];
    if (![obj isKindOfClass:[NSWindow class]]) {
        return;
    }
    NSWindow *window = (NSWindow *)obj;
    if (!hoxi_is_main_wails_window(window)) {
        return;
    }
    NSWindowStyleMask mask = [window styleMask];
    if ((mask & NSWindowStyleMaskFullScreen) != 0 || [window isMiniaturized]) {
        return;
    }
    NSRect frame = [window frame];
    if (frame.size.width <= 0 || frame.size.height <= 0) {
        return;
    }
    hoxiGoWindowDidResize((int)frame.size.width, (int)frame.size.height);
}

@end

static HoxiWindowResizeObserver *hoxiWindowResizeObserver;

void hoxi_primary_screen_work_area(int *outWidth, int *outHeight) {
    if (outWidth == NULL || outHeight == NULL) {
        return;
    }
    *outWidth = 0;
    *outHeight = 0;
    @autoreleasepool {
        if ([NSThread isMainThread]) {
            [NSApplication sharedApplication];
            NSScreen *screen = [NSScreen mainScreen];
            if (screen == nil) {
                NSArray<NSScreen *> *screens = [NSScreen screens];
                if ([screens count] > 0) {
                    screen = [screens objectAtIndex:0];
                }
            }
            if (screen != nil) {
                NSRect vf = [screen visibleFrame];
                *outWidth = (int)vf.size.width;
                *outHeight = (int)vf.size.height;
                return;
            }
        }
        CGDirectDisplayID mainID = CGMainDisplayID();
        CGRect bounds = CGDisplayBounds(mainID);
        if (bounds.size.width > 0 && bounds.size.height > 0) {
            *outWidth = (int)bounds.size.width;
            *outHeight = (int)bounds.size.height;
        }
    }
}

void hoxi_window_observer_start(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            if (NSApp != nil) {
                for (NSWindow *window in [NSApp windows]) {
                    if (!hoxi_is_main_wails_window(window)) {
                        continue;
                    }
                    NSWindowStyleMask mask = [window styleMask];
                    if ((mask & NSWindowStyleMaskFullScreen) == 0 && ![window isMiniaturized]) {
                        NSRect frame = [window frame];
                        if (frame.size.width > 0 && frame.size.height > 0) {
                            hoxiGoWindowCalibrateInitialFrame((int)frame.size.width, (int)frame.size.height);
                        }
                    }
                    break;
                }
            }
            if (hoxiWindowResizeObserver == nil) {
                hoxiWindowResizeObserver = [HoxiWindowResizeObserver new];
            } else {
                [[NSNotificationCenter defaultCenter] removeObserver:hoxiWindowResizeObserver];
            }
            [[NSNotificationCenter defaultCenter] addObserver:hoxiWindowResizeObserver
                                                     selector:@selector(windowDidResize:)
                                                         name:NSWindowDidResizeNotification
                                                       object:nil];
        }
    });
}

void hoxi_window_observer_stop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            if (hoxiWindowResizeObserver != nil) {
                [[NSNotificationCenter defaultCenter] removeObserver:hoxiWindowResizeObserver];
            }
        }
    });
}
