//go:build darwin

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static NSString *hoxiAboutApplicationName;
static NSString *hoxiAboutVersion;

@interface HoxiAboutHandler : NSObject
- (void)showHoxiAbout:(id)sender;
@end

@implementation HoxiAboutHandler

- (void)showHoxiAbout:(id)sender {
    NSString *language = [[NSLocale preferredLanguages] firstObject] ?: @"en";
    BOOL chinese = [language hasPrefix:@"zh"];
    NSString *headline = chinese
        ? @"为并行 AI 工程打造的原生指挥中枢"
        : @"Native command center for parallel AI engineering.";
    NSString *tagline = chinese
        ? @"多 Agent 并行，一个工作台交付"
        : @"Many agents. One workspace.";

    NSMutableParagraphStyle *paragraph = [NSMutableParagraphStyle new];
    paragraph.alignment = NSTextAlignmentCenter;
    paragraph.lineSpacing = 4.0;
    paragraph.paragraphSpacing = 8.0;

    NSMutableAttributedString *credits = [[NSMutableAttributedString alloc] initWithString:headline attributes:@{
        NSFontAttributeName: [NSFont systemFontOfSize:12.5 weight:NSFontWeightSemibold],
        NSForegroundColorAttributeName: [NSColor labelColor],
        NSParagraphStyleAttributeName: paragraph,
    }];
    NSAttributedString *taglineText = [[NSAttributedString alloc] initWithString:[@"\n\n" stringByAppendingString:tagline] attributes:@{
        NSFontAttributeName: [NSFont systemFontOfSize:11.0],
        NSForegroundColorAttributeName: [NSColor secondaryLabelColor],
        NSParagraphStyleAttributeName: paragraph,
    }];
    [credits appendAttributedString:taglineText];
    [taglineText release];
    NSAttributedString *website = [[NSAttributedString alloc] initWithString:@"\n\nhoxi.ai" attributes:@{
        NSFontAttributeName: [NSFont systemFontOfSize:11.0 weight:NSFontWeightMedium],
        NSForegroundColorAttributeName: [NSColor linkColor],
        NSParagraphStyleAttributeName: paragraph,
        NSLinkAttributeName: [NSURL URLWithString:@"https://hoxi.ai"],
    }];
    [credits appendAttributedString:website];
    [website release];

    NSDictionary *options = @{
        NSAboutPanelOptionApplicationName: hoxiAboutApplicationName ?: @"Hoxi",
        NSAboutPanelOptionApplicationVersion: hoxiAboutVersion ?: @"",
        NSAboutPanelOptionCredits: credits,
    };
    [NSApp orderFrontStandardAboutPanelWithOptions:options];
    [credits release];
    [paragraph release];
}

@end

static HoxiAboutHandler *hoxiAboutHandler;

static void hoxi_about_apply_menu_patch(void) {
    NSMenu *mainMenu = [NSApp mainMenu];
    if (mainMenu == nil || mainMenu.numberOfItems == 0) {
        return;
    }
    NSMenu *appMenu = [[mainMenu itemAtIndex:0] submenu];
    if (appMenu == nil) {
        return;
    }
    if (hoxiAboutHandler == nil) {
        hoxiAboutHandler = [HoxiAboutHandler new];
    }
    for (NSMenuItem *item in [appMenu itemArray]) {
        NSString *selectorName = item.action != NULL ? NSStringFromSelector(item.action) : @"";
        if ([selectorName isEqualToString:@"About"] || [item.title hasPrefix:@"About "]) {
            item.target = hoxiAboutHandler;
            item.action = @selector(showHoxiAbout:);
        }
    }
}

void hoxi_about_install(const char *applicationName, const char *version) {
    @autoreleasepool {
        [hoxiAboutApplicationName release];
        [hoxiAboutVersion release];
        hoxiAboutApplicationName = applicationName != NULL ? [[NSString alloc] initWithUTF8String:applicationName] : @"Hoxi";
        hoxiAboutVersion = version != NULL ? [[NSString alloc] initWithUTF8String:version] : @"";
        dispatch_async(dispatch_get_main_queue(), ^{
            hoxi_about_apply_menu_patch();
        });
    }
}

static void hoxi_eval_desktop_intent_in_view(NSView *view) {
    if (view == nil) {
        return;
    }
    if ([view isKindOfClass:[WKWebView class]]) {
        // Static NSString literal (@"...") is an immortal __NSCFConstantString.
        // Calling evaluateJavaScript: directly on WKWebView bypasses Wails v2.13.0's
        // C.ExecJS, which over-releases its autoreleased NSString and crashes with SIGSEGV.
        static NSString * const kDesktopIntentScript =
            @"if(window.wails&&typeof window.wails.EventsNotify==='function'){"
             "window.wails.EventsNotify('{\"name\":\"hoxi:desktop-intent\",\"data\":[]}');"
             "}else{"
             "window.dispatchEvent(new CustomEvent('hoxi:desktop-intent'));"
             "}";
        [(WKWebView *)view evaluateJavaScript:kDesktopIntentScript completionHandler:nil];
        return;
    }
    for (NSView *subview in [view subviews]) {
        hoxi_eval_desktop_intent_in_view(subview);
    }
}

void hoxi_emit_desktop_intent(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            if (NSApp == nil) {
                return;
            }
            for (NSWindow *window in [NSApp windows]) {
                hoxi_eval_desktop_intent_in_view([window contentView]);
            }
        }
    });
}
