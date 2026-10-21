//go:build darwin

#import <Cocoa/Cocoa.h>
#import <UserNotifications/UserNotifications.h>
#include <stdlib.h>
#include <string.h>

extern void HoxiNotificationClicked(const char* sessionID);

@interface HoxiNotificationDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation HoxiNotificationDelegate

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions options))completionHandler {
    completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList);
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
didReceiveNotificationResponse:(UNNotificationResponse *)response
         withCompletionHandler:(void (^)(void))completionHandler {
    id sessionID = response.notification.request.content.userInfo[@"sessionID"];
    if ([sessionID isKindOfClass:[NSString class]] && [sessionID length] > 0) {
        HoxiNotificationClicked([sessionID UTF8String]);
    }
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp activateIgnoringOtherApps:YES];
    });
    completionHandler();
}

@end

static HoxiNotificationDelegate *hoxiNotificationDelegate;
static BOOL hoxiNotificationStarted = NO;

static void hoxi_run_on_main(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), block);
}

static void hoxi_schedule_notification(NSString *title,
                                       NSString *body,
                                       NSString *sessionID,
                                       NSString *identifier) {
    @autoreleasepool {
        UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
        content.title = title.length > 0 ? title : @"Hoxi";
        content.body = body;
        // Sound is intentionally nil: the workspace plays its own configurable cue.
        if (sessionID.length > 0) {
            content.userInfo = @{@"sessionID": sessionID};
            content.threadIdentifier = sessionID;
        }
        UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:identifier
                                                                              content:content
                                                                              trigger:nil];
        [[UNUserNotificationCenter currentNotificationCenter] addNotificationRequest:request
                                                              withCompletionHandler:^(NSError *error) {}];
        [content release];
    }
}

void hoxi_notify_start(void) {
    hoxi_run_on_main(^{
        if (hoxiNotificationStarted || NSBundle.mainBundle.bundleIdentifier.length == 0) {
            return;
        }
        hoxiNotificationStarted = YES;
        hoxiNotificationDelegate = [[HoxiNotificationDelegate alloc] init];
        [UNUserNotificationCenter currentNotificationCenter].delegate = hoxiNotificationDelegate;
    });
}

// Returns 0 when the host cannot deliver notifications (no app bundle), so the
// caller can fall back to the Web Notification API outside the desktop shell.
int hoxi_notify_show(const char* title, const char* body, const char* sessionID, const char* identifier) {
    if (NSBundle.mainBundle.bundleIdentifier.length == 0 || identifier == NULL || identifier[0] == '\0') {
        return 0;
    }
    char *titleCopy = strdup(title != NULL ? title : "");
    char *bodyCopy = strdup(body != NULL ? body : "");
    char *sessionIDCopy = strdup(sessionID != NULL ? sessionID : "");
    char *identifierCopy = strdup(identifier);
    if (titleCopy == NULL || bodyCopy == NULL || sessionIDCopy == NULL || identifierCopy == NULL) {
        free(titleCopy);
        free(bodyCopy);
        free(sessionIDCopy);
        free(identifierCopy);
        return 0;
    }

    hoxi_notify_start();
    hoxi_run_on_main(^{
        @autoreleasepool {
            NSString *titleString = [NSString stringWithUTF8String:titleCopy];
            NSString *bodyString = [NSString stringWithUTF8String:bodyCopy];
            NSString *sessionIDString = [NSString stringWithUTF8String:sessionIDCopy];
            NSString *identifierString = [NSString stringWithUTF8String:identifierCopy];
            free(titleCopy);
            free(bodyCopy);
            free(sessionIDCopy);
            free(identifierCopy);
            // Permission is requested on the first notification rather than at launch.
            [[UNUserNotificationCenter currentNotificationCenter]
                requestAuthorizationWithOptions:UNAuthorizationOptionAlert
                              completionHandler:^(BOOL granted, NSError *error) {
                if (granted) {
                    hoxi_schedule_notification(titleString, bodyString, sessionIDString, identifierString);
                }
            }];
        }
    });
    return 1;
}
