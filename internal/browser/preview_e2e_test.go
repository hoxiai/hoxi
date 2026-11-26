package browser

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
)

func TestPreviewFetchDecisionAndPublicSubresourceIPFilter(t *testing.T) {
	allowedOrigin := "http://127.0.0.1:5173"
	mainFrame := cdp.FrameID("MAIN_FRAME")
	subFrame := cdp.FrameID("SUB_FRAME")

	// 1. Same-origin document and subresources always pass.
	if allow, navBlocked, subHost, _ := decidePreviewFetchAction(
		allowedOrigin, mainFrame, "http://127.0.0.1:5173/dashboard", network.ResourceTypeDocument, mainFrame, "document",
	); !allow || navBlocked || subHost != "" {
		t.Fatalf("same-origin document: got allow=%v navBlocked=%v subHost=%q", allow, navBlocked, subHost)
	}

	// 2. Cross-origin main-frame document is blocked AND flags navBlocked=true.
	if allow, navBlocked, _, _ := decidePreviewFetchAction(
		allowedOrigin, mainFrame, "https://evil.example.com/phish", network.ResourceTypeDocument, mainFrame, "document",
	); allow || !navBlocked {
		t.Fatalf("cross-origin main document: got allow=%v navBlocked=%v, want false/true", allow, navBlocked)
	}

	// 3. Cross-origin iframe document is blocked WITHOUT falsely flagging top-level navBlocked
	//    (even when Chromium reports mainFrame as initiating frameId with Sec-Fetch-Dest: iframe).
	if allow, navBlocked, _, _ := decidePreviewFetchAction(
		allowedOrigin, mainFrame, "https://evil.example.com/embed", network.ResourceTypeDocument, subFrame, "iframe",
	); allow || navBlocked {
		t.Fatalf("cross-origin iframe (subframe): got allow=%v navBlocked=%v, want false/false", allow, navBlocked)
	}
	if allow, navBlocked, _, _ := decidePreviewFetchAction(
		allowedOrigin, mainFrame, "https://evil.example.com/embed", network.ResourceTypeDocument, mainFrame, "iframe",
	); allow || navBlocked {
		t.Fatalf("cross-origin iframe (mainFrame initiator): got allow=%v navBlocked=%v, want false/false", allow, navBlocked)
	}

	// 4. Cross-origin public HTTPS CDN static assets & fonts are allowed with host/port returned.
	for _, tc := range []struct {
		rawURL   string
		rt       network.ResourceType
		wantHost string
		wantPort int
	}{
		{"https://fonts.googleapis.com/css2?family=Inter", network.ResourceTypeStylesheet, "fonts.googleapis.com", 443},
		{"https://cdn.example.com/hero.webp", network.ResourceTypeImage, "cdn.example.com", 443},
		{"https://fonts.gstatic.com/s/inter/v1.woff2", network.ResourceTypeFont, "fonts.gstatic.com", 443},
		{"http://cdn.example.com/logo.png", network.ResourceTypeImage, "cdn.example.com", 80},
		{"https://api.example.com/v1/products", network.ResourceTypeFetch, "api.example.com", 443},
	} {
		allow, navBlocked, host, port := decidePreviewFetchAction(allowedOrigin, mainFrame, tc.rawURL, tc.rt, mainFrame, "")
		if !allow || navBlocked || host != tc.wantHost || port != tc.wantPort {
			t.Errorf("public subresource %q (%s) = (%v,%v,%q,%d), want (true,false,%q,%d)",
				tc.rawURL, tc.rt, allow, navBlocked, host, port, tc.wantHost, tc.wantPort)
		}
	}

	// 5. Cross-origin loopback, private LAN, CGNAT, metadata, internal hostnames, non-80/443 ports,
	//    and cross-origin WebSocket/EventSource are ALWAYS blocked.
	blockedSubresources := []struct {
		rawURL string
		rt     network.ResourceType
	}{
		{"http://127.0.0.1:9999/secret.png", network.ResourceTypeImage},
		{"http://localhost:5410/api/hoxi/sessions", network.ResourceTypeFetch},
		{"http://[::1]:8080/app.js", network.ResourceTypeScript},
		{"http://192.168.1.1/router.png", network.ResourceTypeImage},
		{"http://10.0.0.8/internal.css", network.ResourceTypeStylesheet},
		{"http://172.16.0.5/logo.png", network.ResourceTypeImage},
		{"http://100.64.0.1/tailscale.png", network.ResourceTypeImage},
		{"http://169.254.169.254/latest/meta-data/", network.ResourceTypeFetch},
		{"http://127.0.0.1.nip.io/leak.png", network.ResourceTypeImage},
		{"http://10.0.0.1.sslip.io/leak.png", network.ResourceTypeImage},
		{"https://printer.local/icon.png", network.ResourceTypeImage},
		{"https://metadata.google.internal/computeMetadata/v1/", network.ResourceTypeFetch},
		{"https://redis/info", network.ResourceTypeFetch},
		{"https://cdn.example.com:8443/app.js", network.ResourceTypeScript},
		{"http://cdn.example.com:8080/logo.png", network.ResourceTypeImage},
		{"https://cdn.example.com/ws", network.ResourceTypeWebSocket},
		{"https://cdn.example.com/events", network.ResourceTypeEventSource},
	}
	for _, tc := range blockedSubresources {
		allow, navBlocked, _, _ := decidePreviewFetchAction(allowedOrigin, mainFrame, tc.rawURL, tc.rt, mainFrame, "")
		if allow || navBlocked {
			t.Errorf("blocked subresource %q (%s) = (allow=%v, navBlocked=%v), want (false, false)", tc.rawURL, tc.rt, allow, navBlocked)
		}
	}

	// 6. Direct IP checks for isPublicSubresourceIP.
	for _, badIP := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.1.1", "192.168.0.1", "100.64.0.1", "169.254.169.254", "0.0.0.0", "fe80::1", "fc00::1", "224.0.0.1", "198.18.0.1"} {
		if isPublicSubresourceIP(netip.MustParseAddr(badIP)) {
			t.Errorf("isPublicSubresourceIP(%s) = true, want false", badIP)
		}
	}
	for _, goodIP := range []string{"93.184.216.34", "1.1.1.1", "8.8.8.8", "2606:2800:220:1:248:1893:25c8:1946"} {
		if !isPublicSubresourceIP(netip.MustParseAddr(goodIP)) {
			t.Errorf("isPublicSubresourceIP(%s) = false, want true", goodIP)
		}
	}
}

func TestPreviewOriginProxySSEFlushAndSubresourceSSRF(t *testing.T) {
	// 1. Verify same-origin SSE flushes chunk 1 immediately before the server closes the stream.
	releaseStream := make(chan struct{})
	sseSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: chunk-1\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-releaseStream:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(releaseStream)
		sseSrv.Close()
	}()

	proxy, err := startPreviewOriginProxy(sseSrv.URL)
	if err != nil {
		t.Fatalf("startPreviewOriginProxy: %v", err)
	}
	defer proxy.Close()

	proxyURL, _ := url.Parse("http://" + proxy.Addr())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	resp, err := client.Get(sseSrv.URL + "/events")
	if err != nil {
		t.Fatalf("SSE GET error: %v", err)
	}
	lineCh := make(chan string, 1)
	go func() {
		br := bufio.NewReader(resp.Body)
		line, _ := br.ReadString('\n')
		lineCh <- line
	}()
	select {
	case line := <-lineCh:
		if strings.TrimSpace(line) != "data: chunk-1" {
			t.Fatalf("unexpected SSE first line: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSE response did not flush first chunk promptly through previewOriginProxy")
	}
	resp.Body.Close()

	// 2. Verify blocked HTTP subresource (Sec-Fetch-Dest: image) does NOT increment NavBlockedCount.
	beforeNav := proxy.NavBlockedCount()
	beforeTotal := proxy.BlockedCount()
	imgReq, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:9999/secret.png", nil)
	imgReq.Header.Set("Sec-Fetch-Dest", "image")
	imgReq.Header.Set("Sec-Fetch-Mode", "no-cors")
	imgResp, err := client.Do(imgReq)
	if err != nil {
		t.Fatalf("blocked img GET error: %v", err)
	}
	imgResp.Body.Close()
	if imgResp.StatusCode != http.StatusForbidden {
		t.Fatalf("blocked local img status = %d, want 403", imgResp.StatusCode)
	}
	if proxy.BlockedCount() <= beforeTotal {
		t.Fatal("expected BlockedCount to increment on blocked local img")
	}
	if proxy.NavBlockedCount() != beforeNav {
		t.Fatalf("NavBlockedCount incremented on subresource block: got %d, want %d", proxy.NavBlockedCount(), beforeNav)
	}

	// 3. Verify dial-time DNS rebinding defense even when a hostname was approved via AllowSubresourceHost.
	rebindProxy, err := startPreviewOriginProxyWithLookup(sseSrv.URL, func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "rebind-private.example.com":
			return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
		case "rebind-loopback.example.com":
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		case "rebind-cgnat.example.com":
			return []netip.Addr{netip.MustParseAddr("100.64.0.1")}, nil
		default:
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		}
	})
	if err != nil {
		t.Fatalf("startPreviewOriginProxyWithLookup: %v", err)
	}
	defer rebindProxy.Close()
	rebindProxyURL, _ := url.Parse("http://" + rebindProxy.Addr())
	rebindClient := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(rebindProxyURL)},
		Timeout:   5 * time.Second,
	}

	for _, host := range []string{"rebind-private.example.com", "rebind-loopback.example.com", "rebind-cgnat.example.com", "rebind-meta.example.com"} {
		rebindProxy.AllowSubresourceHost(host, 80, 30*time.Second)
		req, _ := http.NewRequest(http.MethodGet, "http://"+host+"/asset.png", nil)
		req.Header.Set("Sec-Fetch-Dest", "image")
		req.Header.Set("Sec-Fetch-Mode", "no-cors")
		r, rErr := rebindClient.Do(req)
		if rErr != nil {
			t.Fatalf("rebind GET %s error: %v", host, rErr)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("DNS-rebound subresource %s status = %d, want 403", host, r.StatusCode)
		}
	}
}

func TestPreviewHeadlessChromeRealEndToEnd(t *testing.T) {
	if resolveBrowserExecPath() == "" {
		t.Skip("Chrome not installed on this host")
	}

	var foreignHits atomic.Int32
	foreignSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHits.Add(1)
		_, _ = w.Write([]byte("FOREIGN_SECRET"))
	}))
	defer foreignSrv.Close()

	allowedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html>
<html>
<head><title>Hoxi Preview E2E</title>
<style>
  body { margin: 0; font-family: sans-serif; background: #fff; }
  button, a, input { position: absolute; left: 20px; width: 220px; height: 40px; font-size: 14px; }
  #dialogBtn { top: 20px; }
  #spaBtn { top: 70px; }
  #nameInput { top: 120px; }
  #sameBlank { top: 170px; display: block; line-height: 40px; }
  #extLink { top: 220px; display: block; line-height: 40px; }
</style>
</head>
<body>
  <button id="dialogBtn" onclick="alert('hi'); confirm('sure?'); prompt('who?'); document.title='After Dialog'">Trigger Dialogs</button>
  <button id="spaBtn" onclick="history.pushState({}, '', '/updated'); document.title='SPA Updated'">SPA Nav</button>
  <input id="nameInput" type="text" oninput="document.title='VAL:' + this.value" />
  <a id="sameBlank" href="/page2" target="_blank">Same Origin Blank</a>
  <a id="extLink" href="%s/evil">Foreign Link</a>
  <img src="%s/secret.png" width="10" height="10" />
  <iframe src="%s/secret-frame" width="10" height="10"></iframe>
  <script>fetch(%q + '/secret-api').catch(()=>{});</script>
</body>
</html>`, foreignSrv.URL, foreignSrv.URL, foreignSrv.URL, foreignSrv.URL)
		case "/page2":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Page Two</title></head><body><h1>Page Two</h1></body></html>`))
		case "/redir-foreign":
			http.Redirect(w, r, foreignSrv.URL+"/redirected", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer allowedSrv.Close()

	svc := New()
	defer svc.Close()
	dir := t.TempDir()
	scopeID := "preview_clnk_e2e"

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// 1. Open allowed site (contains blocked foreign img, iframe, fetch).
	snap, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "open", Path: "/"})
	if err != nil {
		t.Fatalf("PreviewAction open failed: %v", err)
	}
	if !snap.Running || snap.Path != "/" || snap.Title != "Hoxi Preview E2E" {
		t.Fatalf("unexpected open snap: %+v", snap)
	}
	if snap.BlockedExternalNav {
		t.Fatalf("blocked local subresources must NOT falsely set BlockedExternalNav=true: %+v", snap)
	}

	// 2. Capture real JPEG frame and confirm zero hits on foreignSrv.
	frameBytes, frameSnap, err := svc.PreviewFrame(ctx, scopeID, dir, allowedSrv.URL)
	if err != nil {
		t.Fatalf("PreviewFrame failed: %v", err)
	}
	if len(frameBytes) < 200 || !bytes.HasPrefix(frameBytes, []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("expected valid JPEG frame, got %d bytes", len(frameBytes))
	}
	if frameSnap.BlockedExternalNav {
		t.Fatalf("expected BlockedExternalNav=false on initial frame, got %+v", frameSnap)
	}
	if hits := foreignHits.Load(); hits != 0 {
		t.Fatalf("foreign local server received %d hits from img/iframe/fetch, want 0", hits)
	}

	// 3. Click button triggering alert() + confirm() + prompt(); verify Chrome does NOT hang.
	dialogCtx, dialogCancel := context.WithTimeout(ctx, 8*time.Second)
	snap, err = svc.PreviewAction(dialogCtx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{
		Action: "click", X: 100, Y: 40,
	})
	dialogCancel()
	if err != nil {
		t.Fatalf("click triggering JS dialogs failed/hung: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	_, afterDialogSnap, err := svc.PreviewFrame(ctx, scopeID, dir, allowedSrv.URL)
	if err != nil {
		t.Fatalf("PreviewFrame after JS dialogs failed/hung: %v", err)
	}
	if afterDialogSnap.Title != "After Dialog" {
		t.Fatalf("expected JS dialogs to auto-dismiss and update title to 'After Dialog', got %q", afterDialogSnap.Title)
	}

	// 4. Focus input, type text, test cross-OS Control+a select-all + replace.
	if _, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "click", X: 100, Y: 140}); err != nil {
		t.Fatalf("click input failed: %v", err)
	}
	if _, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "type", Text: "hello"}); err != nil {
		t.Fatalf("type hello failed: %v", err)
	}
	if _, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "key", Key: "Control+a"}); err != nil {
		t.Fatalf("Control+a failed: %v", err)
	}
	if _, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "type", Text: "replaced"}); err != nil {
		t.Fatalf("type replaced failed: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	_, typedSnap, err := svc.PreviewFrame(ctx, scopeID, dir, allowedSrv.URL)
	if err != nil {
		t.Fatalf("PreviewFrame after type failed: %v", err)
	}
	if typedSnap.Title != "VAL:replaced" {
		t.Fatalf("expected input value 'VAL:replaced' after Control+a, got title=%q", typedSnap.Title)
	}

	// 5. Click SPA button -> verify history.pushState updates X-Preview-Path to /updated.
	if _, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "click", X: 100, Y: 90}); err != nil {
		t.Fatalf("click SPA button failed: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	_, spaSnap, err := svc.PreviewFrame(ctx, scopeID, dir, allowedSrv.URL)
	if err != nil {
		t.Fatalf("PreviewFrame after SPA nav failed: %v", err)
	}
	if spaSnap.Path != "/updated" || spaSnap.Title != "SPA Updated" {
		t.Fatalf("expected SPA path=/updated title='SPA Updated', got %+v", spaSnap)
	}

	// 6. Click foreign link -> verify blocked, foreignHits stays 0, tab reverts/stays on allowed origin, BlockedExternalNav=true.
	clickExtSnap, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "click", X: 100, Y: 240})
	if err != nil {
		t.Fatalf("click foreign link should recover cleanly, got err=%v", err)
	}
	time.Sleep(150 * time.Millisecond)
	_, afterExtSnap, err := svc.PreviewFrame(ctx, scopeID, dir, allowedSrv.URL)
	if err != nil {
		t.Fatalf("PreviewFrame after foreign link click failed: %v", err)
	}
	if !clickExtSnap.BlockedExternalNav && !afterExtSnap.BlockedExternalNav {
		t.Fatalf("expected BlockedExternalNav=true after clicking foreign link; click=%+v frame=%+v", clickExtSnap, afterExtSnap)
	}
	if afterExtSnap.Path != "/updated" && afterExtSnap.Path != "/" {
		t.Fatalf("expected tab to stay/revert to allowed origin, got %+v", afterExtSnap)
	}
	if hits := foreignHits.Load(); hits != 0 {
		t.Fatalf("foreign server received %d hits after foreign link click, want 0", hits)
	}

	// 7. Navigate to same-origin endpoint that 302-redirects to foreignSrv -> blocked & reverted.
	redirSnap, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "navigate", Path: "/redir-foreign"})
	if err != nil {
		t.Fatalf("cross-origin redirect from existing page should revert cleanly, got err=%v", err)
	}
	if !redirSnap.BlockedExternalNav {
		t.Fatalf("expected BlockedExternalNav=true after cross-origin redirect, got %+v", redirSnap)
	}
	if hits := foreignHits.Load(); hits != 0 {
		t.Fatalf("foreign server received %d hits after cross-origin redirect, want 0", hits)
	}

	// 8. Navigate back to "/" and click same-origin target="_blank" link -> follows cleanly in single tab.
	if _, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "navigate", Path: "/"}); err != nil {
		t.Fatalf("navigate / failed: %v", err)
	}
	if _, err := svc.PreviewAction(ctx, scopeID, dir, allowedSrv.URL, PreviewActionRequest{Action: "click", X: 100, Y: 190}); err != nil {
		t.Fatalf("click same-origin target=_blank failed: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	_, blankSnap, err := svc.PreviewFrame(ctx, scopeID, dir, allowedSrv.URL)
	if err != nil {
		t.Fatalf("PreviewFrame after same-origin target=_blank failed: %v", err)
	}
	if blankSnap.Path != "/page2" || blankSnap.Title != "Page Two" {
		t.Fatalf("expected same-origin target=_blank to follow to /page2, got %+v", blankSnap)
	}

	// 9. ClosePreview stops scope cleanly.
	svc.ClosePreview(scopeID, dir)
	closedSnap, err := svc.PreviewState(ctx, scopeID, dir, allowedSrv.URL)
	if err != nil || closedSnap.Running {
		t.Fatalf("expected Running=false after ClosePreview, got %+v err=%v", closedSnap, err)
	}
}

func TestPreviewTargetInfoFromEventCoversDeferredPopupNavigation(t *testing.T) {
	created := &target.EventTargetCreated{
		TargetInfo: &target.Info{TargetID: "tab-1", Type: "page", URL: "about:blank"},
	}
	if got := previewTargetInfoFromEvent(created); got == nil || got.TargetID != "tab-1" || got.URL != "about:blank" {
		t.Fatalf("unexpected created target info: %+v", got)
	}
	changed := &target.EventTargetInfoChanged{
		TargetInfo: &target.Info{TargetID: "tab-1", Type: "page", URL: "https://evil.example.com/phish"},
	}
	if got := previewTargetInfoFromEvent(changed); got == nil || got.TargetID != "tab-1" || got.URL != "https://evil.example.com/phish" {
		t.Fatalf("unexpected changed target info: %+v", got)
	}
	if got := previewTargetInfoFromEvent("unrelated"); got != nil {
		t.Fatalf("expected nil for unrelated event, got %+v", got)
	}
}
