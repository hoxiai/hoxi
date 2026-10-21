//go:build darwin

#import <Cocoa/Cocoa.h>
#import <UserNotifications/UserNotifications.h>
#include <stdatomic.h>
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
static _Atomic(NSInteger) hoxiCachedAuthStatus = UNAuthorizationStatusNotDetermined;

static void hoxi_run_on_main(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), block);
}

static void hoxi_refresh_auth_status_async(void) {
    [[UNUserNotificationCenter currentNotificationCenter]
        getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
        if (settings != nil) {
            atomic_store(&hoxiCachedAuthStatus, (NSInteger)settings.authorizationStatus);
        }
    }];
}

static NSInteger hoxi_query_auth_status(void) {
    if ([NSThread isMainThread]) {
        hoxi_refresh_auth_status_async();
        return atomic_load(&hoxiCachedAuthStatus);
    }
    __block NSInteger resolvedStatus = atomic_load(&hoxiCachedAuthStatus);
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    dispatch_retain(sem);
    [[UNUserNotificationCenter currentNotificationCenter]
        getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
        if (settings != nil) {
            resolvedStatus = (NSInteger)settings.authorizationStatus;
            atomic_store(&hoxiCachedAuthStatus, resolvedStatus);
        }
        dispatch_semaphore_signal(sem);
        dispatch_release(sem);
    }];
    dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC);
    long timedOut = dispatch_semaphore_wait(sem, timeout);
    dispatch_release(sem);
    if (timedOut != 0) {
        return atomic_load(&hoxiCachedAuthStatus);
    }
    return resolvedStatus;
}

static BOOL hoxi_schedule_notification(NSString *title,
                                       NSString *body,
                                       NSString *sessionID,
                                       NSString *identifier) {
    @autoreleasepool {
        UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
        content.title = title.length > 0 ? title : @"Hoxi";
        content.body = body != nil ? body : @"";
        // Sound is intentionally nil: the workspace plays its own configurable cue.
        if (sessionID.length > 0) {
            content.userInfo = @{@"sessionID": sessionID};
            content.threadIdentifier = sessionID;
        }
        UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:identifier
                                                                              content:content
                                                                              trigger:nil];
        [content release];

        if ([NSThread isMainThread]) {
            [[UNUserNotificationCenter currentNotificationCenter] addNotificationRequest:request
                                                                   withCompletionHandler:^(NSError *error) {}];
            return YES;
        }

        __block BOOL delivered = NO;
        dispatch_semaphore_t sem = dispatch_semaphore_create(0);
        dispatch_retain(sem);
        [[UNUserNotificationCenter currentNotificationCenter] addNotificationRequest:request
                                                               withCompletionHandler:^(NSError *error) {
            delivered = (error == nil);
            dispatch_semaphore_signal(sem);
            dispatch_release(sem);
        }];
        dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC);
        long timedOut = dispatch_semaphore_wait(sem, timeout);
        dispatch_release(sem);
        return timedOut == 0 && delivered;
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
        hoxi_refresh_auth_status_async();
    });
}

// Returns 0 when the host cannot deliver notifications (no app bundle or
// permission denied), so the caller can fall back to the Web Notification API.
int hoxi_notify_show(const char* title, const char* body, const char* sessionID, const char* identifier) {
    if (NSBundle.mainBundle.bundleIdentifier.length == 0 || identifier == NULL || identifier[0] == '\0') {
        return 0;
    }
    @autoreleasepool {
        NSString *titleString = [NSString stringWithUTF8String:(title != NULL ? title : "")];
        NSString *bodyString = [NSString stringWithUTF8String:(body != NULL ? body : "")];
        NSString *sessionIDString = [NSString stringWithUTF8String:(sessionID != NULL ? sessionID : "")];
        NSString *identifierString = [NSString stringWithUTF8String:identifier];
        if (identifierString == nil || identifierString.length == 0) {
            return 0;
        }
        if (titleString == nil) {
            titleString = @"Hoxi";
        }
        if (bodyString == nil) {
            bodyString = @"";
        }
        if (sessionIDString == nil) {
            sessionIDString = @"";
        }

        hoxi_notify_start();

        NSInteger status = hoxi_query_auth_status();
        if (status == UNAuthorizationStatusDenied) {
            return 0;
        }
        if (status == UNAuthorizationStatusNotDetermined) {
            // Never block the main thread waiting for an interactive authorization prompt.
            if ([NSThread isMainThread]) {
                NSString *titleCopy = [titleString copy];
                NSString *bodyCopy = [bodyString copy];
                NSString *sessionCopy = [sessionIDString copy];
                NSString *identifierCopy = [identifierString copy];
                [[UNUserNotificationCenter currentNotificationCenter]
                    requestAuthorizationWithOptions:UNAuthorizationOptionAlert
                                  completionHandler:^(BOOL granted, NSError *error) {
                    BOOL ok = granted && error == nil;
                    atomic_store(&hoxiCachedAuthStatus, ok ? UNAuthorizationStatusAuthorized : UNAuthorizationStatusDenied);
                    if (ok) {
                        hoxi_schedule_notification(titleCopy, bodyCopy, sessionCopy, identifierCopy);
                    }
                    [titleCopy release];
                    [bodyCopy release];
                    [sessionCopy release];
                    [identifierCopy release];
                }];
                return 0;
            }

            __block BOOL grantedResult = NO;
            dispatch_semaphore_t sem = dispatch_semaphore_create(0);
            dispatch_retain(sem);
            [[UNUserNotificationCenter currentNotificationCenter]
                requestAuthorizationWithOptions:UNAuthorizationOptionAlert
                              completionHandler:^(BOOL granted, NSError *error) {
                grantedResult = (granted && error == nil);
                atomic_store(&hoxiCachedAuthStatus, grantedResult ? UNAuthorizationStatusAuthorized : UNAuthorizationStatusDenied);
                dispatch_semaphore_signal(sem);
                dispatch_release(sem);
            }];
            dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC);
            long timedOut = dispatch_semaphore_wait(sem, timeout);
            dispatch_release(sem);
            if (timedOut != 0 || !grantedResult) {
                return 0;
            }
            status = UNAuthorizationStatusAuthorized;
        }

        if (status != UNAuthorizationStatusAuthorized && status != UNAuthorizationStatusProvisional) {
            return 0;
        }
        return hoxi_schedule_notification(titleString, bodyString, sessionIDString, identifierString) ? 1 : 0;
    }
}
