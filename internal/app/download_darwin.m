//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <errno.h>
#include <stdio.h>
#include <sys/stat.h>

static NSString *hoxi_mime_extension(NSString *mimeType) {
    if (mimeType == nil || [mimeType length] == 0) {
        return nil;
    }
    NSString *clean = [[mimeType componentsSeparatedByString:@";"] firstObject];
    clean = [[clean stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]] lowercaseString];
    if ([clean isEqualToString:@"image/png"]) return @"png";
    if ([clean isEqualToString:@"image/jpeg"] || [clean isEqualToString:@"image/jpg"]) return @"jpg";
    if ([clean isEqualToString:@"image/webp"]) return @"webp";
    if ([clean isEqualToString:@"image/gif"]) return @"gif";
    if ([clean isEqualToString:@"image/svg+xml"]) return @"svg";
    if ([clean isEqualToString:@"image/vnd.adobe.photoshop"] || [clean isEqualToString:@"application/x-photoshop"]) return @"psd";
    if ([clean isEqualToString:@"application/json"]) return @"json";
    if ([clean isEqualToString:@"text/csv"]) return @"csv";
    if ([clean isEqualToString:@"text/markdown"]) return @"md";
    if ([clean isEqualToString:@"text/plain"]) return @"txt";
    if ([clean isEqualToString:@"application/pdf"]) return @"pdf";
    if ([clean isEqualToString:@"application/zip"]) return @"zip";
    return nil;
}

static BOOL hoxi_is_generic_download_name(NSString *name) {
    if (name == nil || [name length] == 0) {
        return YES;
    }
    NSString *lower = [[name stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]] lowercaseString];
    return [lower length] == 0
        || [lower isEqualToString:@"unknown"]
        || [lower isEqualToString:@"download"]
        || [lower isEqualToString:@"blob"]
        || [lower isEqualToString:@"data"];
}

static NSString *hoxi_sanitize_download_name_components(NSString *suggested, NSString *responseSuggested, NSString *mimeType) {
    NSString *candidate = suggested;
    if (hoxi_is_generic_download_name(candidate) && !hoxi_is_generic_download_name(responseSuggested)) {
        candidate = responseSuggested;
    }
    if (candidate == nil) {
        candidate = @"";
    }
    // Normalize Windows separators before extracting lastPathComponent to block traversal.
    NSString *normalized = [candidate stringByReplacingOccurrencesOfString:@"\\" withString:@"/"];
    NSString *base = [normalized lastPathComponent];
    if (base == nil) {
        base = @"";
    }

    NSMutableCharacterSet *badChars = [NSMutableCharacterSet controlCharacterSet];
    [badChars addCharactersInString:@"/:"];
    NSArray<NSString *> *parts = [base componentsSeparatedByCharactersInSet:badChars];
    NSString *cleaned = [[parts componentsJoinedByString:@"-"] stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];

    while ([cleaned hasPrefix:@"."]) {
        cleaned = [cleaned substringFromIndex:1];
    }
    if ([cleaned length] == 0 || [cleaned isEqualToString:@"."] || [cleaned isEqualToString:@".."]) {
        cleaned = @"download";
    }
    if ([[cleaned pathExtension] length] == 0) {
        NSString *ext = hoxi_mime_extension(mimeType);
        if (ext != nil && [ext length] > 0) {
            cleaned = [cleaned stringByAppendingPathExtension:ext];
        }
    }
    return cleaned;
}

static BOOL hoxi_is_attachment_content_disposition(NSString *headerValue) {
    if (headerValue == nil || [headerValue length] == 0) {
        return NO;
    }
    NSString *lower = [[headerValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]] lowercaseString];
    if ([lower isEqualToString:@"attachment"]) {
        return YES;
    }
    return [lower hasPrefix:@"attachment;"] || [lower hasPrefix:@"attachment "];
}

static BOOL hoxi_response_is_attachment(NSURLResponse *response) {
    if (![response isKindOfClass:[NSHTTPURLResponse class]]) {
        return NO;
    }
    NSHTTPURLResponse *httpResp = (NSHTTPURLResponse *)response;
    NSString *disp = [httpResp valueForHTTPHeaderField:@"Content-Disposition"];
    return hoxi_is_attachment_content_disposition(disp);
}

static NSURL *hoxi_default_downloads_directory(void) {
    NSArray<NSURL *> *urls = [[NSFileManager defaultManager] URLsForDirectory:NSDownloadsDirectory inDomains:NSUserDomainMask];
    if ([urls count] > 0) {
        return [urls objectAtIndex:0];
    }
    NSString *home = NSHomeDirectory();
    if (home != nil && [home length] > 0) {
        return [NSURL fileURLWithPath:home isDirectory:YES];
    }
    return nil;
}

// WKDownload requires a nonexistent destination. Stage on the same volume
// as the user's choice so completing the download is a single atomic rename.
@interface HoxiDownloadDestination : NSObject {
    NSURL *_targetURL;
    NSURL *_stageDirectory;
    NSURL *_stageURL;
    BOOL _replaceExisting;
}
@property (nonatomic, readonly) NSURL *stageURL;
- (instancetype)initWithTargetURL:(NSURL *)targetURL error:(NSError **)error;
- (BOOL)commit:(NSError **)error;
@end

@implementation HoxiDownloadDestination
@synthesize stageURL = _stageURL;

- (instancetype)initWithTargetURL:(NSURL *)targetURL error:(NSError **)error {
    self = [super init];
    if (self == nil) return nil;

    struct stat existing;
    int statResult = lstat(targetURL.fileSystemRepresentation, &existing);
    int statError = statResult == 0 ? (S_ISDIR(existing.st_mode) ? EISDIR : 0) : errno;
    if (statError != 0 && statError != ENOENT) {
        if (error != NULL) *error = [NSError errorWithDomain:NSPOSIXErrorDomain code:statError userInfo:nil];
        [self release];
        return nil;
    }
    _targetURL = [targetURL copy];
    _replaceExisting = statResult == 0;
    NSString *stageName = [@".hoxi-download-" stringByAppendingString:[NSUUID UUID].UUIDString];
    NSURL *directory = [[targetURL URLByDeletingLastPathComponent] URLByAppendingPathComponent:stageName isDirectory:YES];
    if (![[NSFileManager defaultManager] createDirectoryAtURL:directory
                                  withIntermediateDirectories:NO
                                                   attributes:@{NSFilePosixPermissions: @0700}
                                                        error:error]) {
        [self release];
        return nil;
    }
    _stageDirectory = [directory copy];
    _stageURL = [[directory URLByAppendingPathComponent:targetURL.lastPathComponent isDirectory:NO] copy];
    return self;
}

- (BOOL)commit:(NSError **)error {
    // Without overwrite confirmation, don't clobber a file created during download.
    const char *source = _stageURL.fileSystemRepresentation;
    const char *target = _targetURL.fileSystemRepresentation;
    int result = _replaceExisting ? rename(source, target) : renamex_np(source, target, RENAME_EXCL);
    if (result == 0) return YES;
    int renameError = errno;
    if (error != NULL) {
        *error = [NSError errorWithDomain:NSPOSIXErrorDomain code:renameError
                                 userInfo:@{NSFilePathErrorKey: _targetURL.path}];
    }
    return NO;
}

- (void)dealloc {
    if (_stageDirectory != nil) [[NSFileManager defaultManager] removeItemAtURL:_stageDirectory error:NULL];
    [_stageDirectory release];
    [_stageURL release];
    [_targetURL release];
    [super dealloc];
}
@end

@interface HoxiWebViewDownloadDelegate : NSObject <WKNavigationDelegate, WKDownloadDelegate> {
    id<WKNavigationDelegate> _originalDelegate;
    WKWebView *_webView;
    NSMutableSet *_activeDownloads;
    NSMapTable *_downloadDestinations;
}
- (instancetype)initWithWebView:(WKWebView *)webView originalDelegate:(id<WKNavigationDelegate>)originalDelegate;
#if defined(__MAC_11_3) && MAC_OS_X_VERSION_MAX_ALLOWED >= 110300
- (void)hoxiAttachDownload:(WKDownload *)download API_AVAILABLE(macos(11.3));
- (NSURL *)hoxiStageDownload:(WKDownload *)download targetURL:(NSURL *)targetURL error:(NSError **)error API_AVAILABLE(macos(11.3));
#endif
@end

@implementation HoxiWebViewDownloadDelegate

- (instancetype)initWithWebView:(WKWebView *)webView originalDelegate:(id<WKNavigationDelegate>)originalDelegate {
    self = [super init];
    if (self != nil) {
        _webView = webView;
        _originalDelegate = originalDelegate;
        _activeDownloads = [[NSMutableSet alloc] init];
        _downloadDestinations = [[NSMapTable alloc] initWithKeyOptions:NSPointerFunctionsStrongMemory | NSPointerFunctionsObjectPointerPersonality
                                                         valueOptions:NSPointerFunctionsStrongMemory capacity:0];
    }
    return self;
}

- (void)dealloc {
    [_downloadDestinations release];
    [_activeDownloads release];
    _activeDownloads = nil;
    _webView = nil;
    _originalDelegate = nil;
    [super dealloc];
}

- (BOOL)respondsToSelector:(SEL)aSelector {
    if ([super respondsToSelector:aSelector]) {
        return YES;
    }
    return _originalDelegate != nil && [_originalDelegate respondsToSelector:aSelector];
}

- (id)forwardingTargetForSelector:(SEL)aSelector {
    if (_originalDelegate != nil && [_originalDelegate respondsToSelector:aSelector]) {
        return _originalDelegate;
    }
    return [super forwardingTargetForSelector:aSelector];
}

- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    if (_originalDelegate != nil && [_originalDelegate respondsToSelector:@selector(webView:didFinishNavigation:)]) {
        [_originalDelegate webView:webView didFinishNavigation:navigation];
    }
}

- (void)webView:(WKWebView *)webView
decidePolicyForNavigationAction:(WKNavigationAction *)navigationAction
decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
#if defined(__MAC_11_3) && MAC_OS_X_VERSION_MAX_ALLOWED >= 110300
    if (@available(macOS 11.3, *)) {
        if (navigationAction.shouldPerformDownload) {
            decisionHandler(WKNavigationActionPolicyDownload);
            return;
        }
    }
#endif
    if (_originalDelegate != nil &&
        [_originalDelegate respondsToSelector:@selector(webView:decidePolicyForNavigationAction:decisionHandler:)]) {
        [_originalDelegate webView:webView decidePolicyForNavigationAction:navigationAction decisionHandler:decisionHandler];
        return;
    }
    decisionHandler(WKNavigationActionPolicyAllow);
}

- (void)webView:(WKWebView *)webView
decidePolicyForNavigationResponse:(WKNavigationResponse *)navigationResponse
decisionHandler:(void (^)(WKNavigationResponsePolicy))decisionHandler {
#if defined(__MAC_11_3) && MAC_OS_X_VERSION_MAX_ALLOWED >= 110300
    if (@available(macOS 11.3, *)) {
        if (!navigationResponse.canShowMIMEType || hoxi_response_is_attachment(navigationResponse.response)) {
            decisionHandler(WKNavigationResponsePolicyDownload);
            return;
        }
    }
#endif
    if (_originalDelegate != nil &&
        [_originalDelegate respondsToSelector:@selector(webView:decidePolicyForNavigationResponse:decisionHandler:)]) {
        [_originalDelegate webView:webView decidePolicyForNavigationResponse:navigationResponse decisionHandler:decisionHandler];
        return;
    }
    decisionHandler(WKNavigationResponsePolicyAllow);
}

#if defined(__MAC_11_3) && MAC_OS_X_VERSION_MAX_ALLOWED >= 110300
- (void)hoxiPresentDownloadError:(NSError *)error {
    if (error == nil) return;
    NSAlert *alert = [NSAlert alertWithError:error];
    NSWindow *window = _webView.window;
    if (window != nil) {
        [alert beginSheetModalForWindow:window completionHandler:nil];
    } else {
        NSLog(@"Hoxi download: %@", error);
    }
}

- (NSURL *)hoxiStageDownload:(WKDownload *)download targetURL:(NSURL *)targetURL error:(NSError **)error API_AVAILABLE(macos(11.3)) {
    HoxiDownloadDestination *destination = [[HoxiDownloadDestination alloc] initWithTargetURL:targetURL error:error];
    if (destination == nil) return nil;
    [_downloadDestinations setObject:destination forKey:download];
    NSURL *stageURL = destination.stageURL;
    [destination release];
    return stageURL;
}

- (void)hoxiAttachDownload:(WKDownload *)download API_AVAILABLE(macos(11.3)) {
    if (download == nil) {
        return;
    }
    [_activeDownloads addObject:download];
    download.delegate = self;
}

- (void)webView:(WKWebView *)webView
navigationAction:(WKNavigationAction *)navigationAction
didBecomeDownload:(WKDownload *)download API_AVAILABLE(macos(11.3)) {
    [self hoxiAttachDownload:download];
}

- (void)webView:(WKWebView *)webView
navigationResponse:(WKNavigationResponse *)navigationResponse
didBecomeDownload:(WKDownload *)download API_AVAILABLE(macos(11.3)) {
    [self hoxiAttachDownload:download];
}

- (void)download:(WKDownload *)download
decideDestinationUsingResponse:(NSURLResponse *)response
suggestedFilename:(NSString *)suggestedFilename
completionHandler:(void (^)(NSURL * _Nullable destination))completionHandler API_AVAILABLE(macos(11.3)) {
    void (^retainedCompletion)(NSURL * _Nullable) = [completionHandler copy];
    NSString *filename = [hoxi_sanitize_download_name_components(
        suggestedFilename,
        response != nil ? response.suggestedFilename : nil,
        response != nil ? response.MIMEType : nil
    ) copy];

    void (^presentSavePanel)(void) = ^{
        @autoreleasepool {
            NSSavePanel *panel = [NSSavePanel savePanel];
            panel.canCreateDirectories = YES;
            panel.extensionHidden = NO;
            panel.allowsOtherFileTypes = YES;
            panel.nameFieldStringValue = filename;
            NSURL *defaultDir = hoxi_default_downloads_directory();
            if (defaultDir != nil) {
                panel.directoryURL = defaultDir;
            }
            [filename release];

            void (^handlePanelResult)(NSModalResponse) = ^(NSModalResponse result) {
                NSURL *stageURL = nil;
                NSError *error = nil;
                if (result == NSModalResponseOK && panel.URL != nil && panel.URL.isFileURL) {
                    stageURL = [self hoxiStageDownload:download targetURL:panel.URL error:&error];
                }
                retainedCompletion(stageURL);
                [retainedCompletion release];
                [self hoxiPresentDownloadError:error];
            };

            NSWindow *hostWindow = _webView != nil ? _webView.window : nil;
            if (hostWindow == nil && NSApp != nil) {
                hostWindow = [NSApp keyWindow] ?: [NSApp mainWindow];
            }
            if (hostWindow != nil && hostWindow.attachedSheet == nil) {
                [panel beginSheetModalForWindow:hostWindow completionHandler:handlePanelResult];
            } else {
                [panel beginWithCompletionHandler:handlePanelResult];
            }
        }
    };

    if ([NSThread isMainThread]) {
        presentSavePanel();
    } else {
        dispatch_async(dispatch_get_main_queue(), presentSavePanel);
    }
}

- (void)downloadDidFinish:(WKDownload *)download API_AVAILABLE(macos(11.3)) {
    if (download != nil) {
        HoxiDownloadDestination *destination = [_downloadDestinations objectForKey:download];
        NSError *error = nil;
        [destination commit:&error];
        [_downloadDestinations removeObjectForKey:download];
        [_activeDownloads removeObject:download];
        [self hoxiPresentDownloadError:error];
    }
}

- (void)download:(WKDownload *)download
didFailWithError:(NSError *)error
resumeData:(NSData *)resumeData API_AVAILABLE(macos(11.3)) {
    if (download != nil) {
        [_downloadDestinations removeObjectForKey:download];
        [_activeDownloads removeObject:download];
    }
}
#endif

@end

static NSMutableArray<HoxiWebViewDownloadDelegate *> *hoxiDownloadDelegates = nil;

static BOOL hoxi_attach_downloads_in_view(NSView *view) {
    if (view == nil) {
        return NO;
    }
    BOOL attached = NO;
    if ([view isKindOfClass:[WKWebView class]]) {
        WKWebView *webView = (WKWebView *)view;
        id<WKNavigationDelegate> current = webView.navigationDelegate;
        if (![current isKindOfClass:[HoxiWebViewDownloadDelegate class]]) {
            HoxiWebViewDownloadDelegate *proxy =
                [[HoxiWebViewDownloadDelegate alloc] initWithWebView:webView originalDelegate:current];
            if (hoxiDownloadDelegates == nil) {
                hoxiDownloadDelegates = [[NSMutableArray alloc] init];
            }
            [hoxiDownloadDelegates addObject:proxy];
            webView.navigationDelegate = proxy;
            [proxy release];
        }
        return YES;
    }
    for (NSView *subview in [view subviews]) {
        if (hoxi_attach_downloads_in_view(subview)) {
            attached = YES;
        }
    }
    return attached;
}

static void hoxi_try_install_downloads(int remainingAttempts) {
    if (NSApp == nil) {
        return;
    }
    BOOL found = NO;
    for (NSWindow *window in [NSApp windows]) {
        if (hoxi_attach_downloads_in_view([window contentView])) {
            found = YES;
        }
    }
    if (!found && remainingAttempts > 0) {
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(250 * NSEC_PER_MSEC)),
                       dispatch_get_main_queue(), ^{
            @autoreleasepool {
                hoxi_try_install_downloads(remainingAttempts - 1);
            }
        });
    }
}

void hoxi_webview_downloads_install(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        @autoreleasepool {
            hoxi_try_install_downloads(12);
        }
    });
}

char *hoxi_test_sanitize_download_filename(const char *suggested, const char *respSuggested, const char *mimeType) {
    @autoreleasepool {
        NSString *nsSuggested = suggested != NULL ? [NSString stringWithUTF8String:suggested] : nil;
        NSString *nsResp = respSuggested != NULL ? [NSString stringWithUTF8String:respSuggested] : nil;
        NSString *nsMime = mimeType != NULL ? [NSString stringWithUTF8String:mimeType] : nil;
        NSString *clean = hoxi_sanitize_download_name_components(nsSuggested, nsResp, nsMime);
        const char *utf8 = [clean UTF8String];
        return utf8 != NULL ? strdup(utf8) : strdup("download");
    }
}

int hoxi_test_is_attachment_disposition(const char *headerValue) {
    @autoreleasepool {
        NSString *nsHeader = headerValue != NULL ? [NSString stringWithUTF8String:headerValue] : nil;
        return hoxi_is_attachment_content_disposition(nsHeader) ? 1 : 0;
    }
}
