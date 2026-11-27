#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// Pull in production implementation directly so the probe verifies the exact delegate class.
#include "../download_darwin.m"

@interface ProbeOriginalDelegate : NSObject <WKNavigationDelegate>
@property (nonatomic, assign) NSInteger finishCount;
@end

@implementation ProbeOriginalDelegate
- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    self.finishCount += 1;
}
@end

@interface ProbeDownloadDelegate : HoxiWebViewDownloadDelegate
@property (nonatomic, copy) NSString *outDir;
@property (nonatomic, assign) NSInteger completedCount;
@property (nonatomic, assign) NSInteger failedCount;
@property (nonatomic, retain) NSMutableArray<NSString *> *savedFiles;
@end

@implementation ProbeDownloadDelegate

- (instancetype)initWithWebView:(WKWebView *)webView originalDelegate:(id<WKNavigationDelegate>)originalDelegate outDir:(NSString *)outDir {
    self = [super initWithWebView:webView originalDelegate:originalDelegate];
    if (self != nil) {
        _outDir = [outDir copy];
        _savedFiles = [[NSMutableArray alloc] init];
    }
    return self;
}

- (void)dealloc {
    [_outDir release];
    [_savedFiles release];
    [super dealloc];
}

- (void)download:(WKDownload *)download
decideDestinationUsingResponse:(NSURLResponse *)response
suggestedFilename:(NSString *)suggestedFilename
completionHandler:(void (^)(NSURL * _Nullable destination))completionHandler API_AVAILABLE(macos(11.3)) {
    NSString *clean = hoxi_sanitize_download_name_components(
        suggestedFilename,
        response != nil ? response.suggestedFilename : nil,
        response != nil ? response.MIMEType : nil
    );
    NSString *fullPath = [self.outDir stringByAppendingPathComponent:clean];
    NSError *error = nil;
    NSURL *staged = [self hoxiStageDownload:download targetURL:[NSURL fileURLWithPath:fullPath] error:&error];
    if (staged == nil) {
        fprintf(stderr, "staging failed: %s\n", [[error localizedDescription] UTF8String]);
    } else {
        [self.savedFiles addObject:clean];
    }
    completionHandler(staged);
}

- (void)downloadDidFinish:(WKDownload *)download API_AVAILABLE(macos(11.3)) {
    self.completedCount += 1;
    [super downloadDidFinish:download];
}

- (void)download:(WKDownload *)download didFailWithError:(NSError *)error resumeData:(NSData *)resumeData API_AVAILABLE(macos(11.3)) {
    self.failedCount += 1;
    fprintf(stderr, "download failed: %s\n", [[error localizedDescription] UTF8String]);
    [super download:download didFailWithError:error resumeData:resumeData];
}

@end

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        if (argc < 4) {
            fprintf(stderr, "usage: probe <url> <outDir> <interruptedURL>\n");
            return 2;
        }
        NSString *urlStr = [NSString stringWithUTF8String:argv[1]];
        NSString *outDir = [NSString stringWithUTF8String:argv[2]];

        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];

        WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
        WKWebView *webView = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, 800, 600) configuration:cfg];
        [cfg release];

        ProbeOriginalDelegate *orig = [[ProbeOriginalDelegate alloc] init];
        ProbeDownloadDelegate *proxy = [[ProbeDownloadDelegate alloc] initWithWebView:webView originalDelegate:orig outDir:outDir];
        webView.navigationDelegate = proxy;

        // Verify selector forwarding preserves WailsContext didFinishNavigation behavior.
        if (![proxy respondsToSelector:@selector(webView:didFinishNavigation:)]) {
            fprintf(stderr, "proxy must respond to didFinishNavigation\n");
            return 3;
        }

        [webView loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:urlStr]]];

        NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:12.0];
        BOOL startedInterruptedDownload = NO;
        while ([[NSDate date] compare:deadline] == NSOrderedAscending) {
            [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
            if (proxy.completedCount == 4 && !startedInterruptedDownload) {
                startedInterruptedDownload = YES;
                NSURL *interruptedURL = [NSURL URLWithString:[NSString stringWithUTF8String:argv[3]]];
                [webView startDownloadUsingRequest:[NSURLRequest requestWithURL:interruptedURL]
                                 completionHandler:^(WKDownload *download) { [proxy hoxiAttachDownload:download]; }];
            }
            if (proxy.completedCount + proxy.failedCount >= 5) {
                break;
            }
        }

        if (orig.finishCount < 1) {
            fprintf(stderr, "original delegate didFinishNavigation was not forwarded (finishCount=%ld)\n", (long)orig.finishCount);
            return 4;
        }
        if (proxy.failedCount != 1 || proxy.completedCount != 4 || ![proxy.savedFiles containsObject:@"interrupted.txt"]) {
            fprintf(stderr, "expected 4 completed downloads and 1 interrupted download, got completed=%ld failed=%ld files=%s\n",
                    (long)proxy.completedCount,
                    (long)proxy.failedCount,
                    [[proxy.savedFiles componentsJoinedByString:@","] UTF8String]);
            return 5;
        }

        webView.navigationDelegate = nil;
        [proxy release];
        [orig release];
        [webView release];
        return 0;
    }
}
