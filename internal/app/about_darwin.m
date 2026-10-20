//go:build darwin

#import <Cocoa/Cocoa.h>

static NSString *hoxiAboutApplicationName;
static NSString *hoxiAboutVersion;

@interface HoxiAboutHandler : NSObject
- (void)showHoxiAbout:(id)sender;
@end

@implementation HoxiAboutHandler

- (void)showHoxiAbout:(id)sender {
    NSString *language = [[NSLocale preferredLanguages] firstObject] ?: @"en";
    BOOL chinese = [language hasPrefix:@"zh"];
    NSString *summary = chinese
        ? @"Hoxi 是为并行 AI 工程打造的原生指挥中枢\n在隔离的 Git 工作树中编排 OpenCode、Claude Code、Antigravity、Qoder 与 CodeBuddy，兼得实时纠偏、结构化记忆与移动控制平面\n"
        : @"Hoxi is a native command center for parallel AI engineering.\nOrchestrate OpenCode, Claude Code, Antigravity, Qoder and CodeBuddy across isolated Git worktrees, with live steering, structured memory and a mobile control plane.\n";

    NSMutableParagraphStyle *paragraph = [NSMutableParagraphStyle new];
    paragraph.alignment = NSTextAlignmentCenter;
    paragraph.lineSpacing = 3.0;

    NSMutableAttributedString *credits = [[NSMutableAttributedString alloc] initWithString:summary attributes:@{
        NSFontAttributeName: [NSFont systemFontOfSize:11.0],
        NSForegroundColorAttributeName: [NSColor secondaryLabelColor],
        NSParagraphStyleAttributeName: paragraph,
    }];
    NSAttributedString *website = [[NSAttributedString alloc] initWithString:@"hoxi.ai" attributes:@{
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
        hoxiAboutApplicationName = applicationName != NULL ? [[NSString alloc] initWithUTF8String:applicationName] : @"Hoxi";
        hoxiAboutVersion = version != NULL ? [[NSString alloc] initWithUTF8String:version] : @"";
        dispatch_async(dispatch_get_main_queue(), ^{
            hoxi_about_apply_menu_patch();
        });
    }
}
