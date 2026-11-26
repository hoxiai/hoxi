package browser

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizePreviewOrigin(t *testing.T) {
	tests := []struct {
		raw      string
		wantOrig string
		wantPath string
		wantErr  bool
	}{
		{"http://127.0.0.1:5173", "http://127.0.0.1:5173", "/", false},
		{"http://LOCALHOST:3000/dashboard?tab=1#top", "http://localhost:3000", "/dashboard?tab=1#top", false},
		{"http://example.com:80/docs", "http://example.com", "/docs", false},
		{"https://staging.example.com:443/app", "https://staging.example.com", "/app", false},
		{"http://[::1]:4321/", "http://[::1]:4321", "/", false},
		// Rejected inputs
		{"", "", "", true},
		{"javascript:alert(1)", "", "", true},
		{"file:///etc/passwd", "", "", true},
		{"http://user:pass@127.0.0.1:5173", "", "", true},
		{"http://169.254.169.254/latest/meta-data", "", "", true},
		{"http://0.0.0.0:3000", "", "", true},
		{"http://127.0.0.1:5400/", "", "", true},
		{"http://localhost:5410", "", "", true},
		{"http://127.0.0.1.nip.io:5400", "", "", true},
		{"http://127-0-0-1.sslip.io:5410", "", "", true},
		{"http://7f000001.nip.io:5417", "", "", true},
		{"http://localtest.me:5420", "", "", true},
		{"http://127.1:5400", "", "", true},
		{"http://0x7f000001:5400", "", "", true},
		{"http://2130706433:5400", "", "", true},
		{"http://169.254.169.254.nip.io:80", "", "", true},
		{"http://127.0.0.1:5173\r\nX-Injected: 1", "", "", true},
	}

	for _, tc := range tests {
		orig, p, err := NormalizePreviewOrigin(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizePreviewOrigin(%q) succeeded (%q, %q), want error", tc.raw, orig, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizePreviewOrigin(%q) error = %v", tc.raw, err)
			continue
		}
		if orig != tc.wantOrig || p != tc.wantPath {
			t.Errorf("NormalizePreviewOrigin(%q) = (%q, %q), want (%q, %q)", tc.raw, orig, p, tc.wantOrig, tc.wantPath)
		}
	}
}

func TestResolvePreviewTargetURLOriginLock(t *testing.T) {
	origin := "http://127.0.0.1:5173"

	okCases := map[string]string{
		"":                                   "http://127.0.0.1:5173/",
		"/":                                  "http://127.0.0.1:5173/",
		"/pricing?plan=pro#faq":              "http://127.0.0.1:5173/pricing?plan=pro#faq",
		"settings/profile":                   "http://127.0.0.1:5173/settings/profile",
		"http://127.0.0.1:5173/orders?id=12": "http://127.0.0.1:5173/orders?id=12",
	}
	for input, want := range okCases {
		got, err := ResolvePreviewTargetURL(origin, input)
		if err != nil {
			t.Errorf("ResolvePreviewTargetURL(%q) unexpected error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ResolvePreviewTargetURL(%q) = %q, want %q", input, got, want)
		}
	}

	badCases := []string{
		"//evil.com/phish",
		"http://127.0.0.1:5410/api/hoxi/sessions",
		"http://localhost:5173/",
		"https://127.0.0.1:5173/",
		"https://evil.com/",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,<h1>hi</h1>",
		"http://user@127.0.0.1:5173/",
	}
	for _, input := range badCases {
		if got, err := ResolvePreviewTargetURL(origin, input); err == nil {
			t.Errorf("ResolvePreviewTargetURL(%q) = %q, want origin-lock error", input, got)
		}
	}
}

func TestValidatePreviewActionAllowlistAndBounds(t *testing.T) {
	origin := "http://127.0.0.1:5173"

	// Disallowed dangerous browser actions must always fail closed.
	for _, forbidden := range []string{"evaluate", "mode", "control", "copy", "paste", "context", "pair", "tab_new", "upload_cancel", "snapshot"} {
		if _, err := validatePreviewAction(PreviewActionRequest{Action: forbidden, Text: "1+1"}, origin); err == nil {
			t.Errorf("validatePreviewAction(%q) succeeded, want error", forbidden)
		}
	}

	// Valid resize & out-of-bounds resize
	w, h := 390, 844
	if act, err := validatePreviewAction(PreviewActionRequest{Action: "resize", Width: &w, Height: &h}, origin); err != nil || act.Action != "resize" {
		t.Fatalf("valid mobile resize failed: %+v, err=%v", act, err)
	}
	tooBig := 5000
	if _, err := validatePreviewAction(PreviewActionRequest{Action: "resize", Width: &tooBig, Height: &h}, origin); err == nil {
		t.Fatalf("oversized resize must be rejected")
	}

	// Text size bound
	if _, err := validatePreviewAction(PreviewActionRequest{Action: "type", Text: strings.Repeat("a", previewMaxTypeTextBytes+1)}, origin); err == nil {
		t.Fatalf("oversized type text must be rejected")
	}
}

func TestPreviewScopeRejectsNonPreviewIDsAndAlwaysForcesHeadless(t *testing.T) {
	svc := New()
	defer svc.Close()
	svc.SetDefaultWindowMode(true)
	dir := t.TempDir()

	if _, err := svc.PreviewState(context.Background(), "ses_normal", dir, "http://127.0.0.1:5173"); err == nil {
		t.Fatalf("non-preview_* scopeID must be rejected")
	}
	if _, err := svc.PreviewState(context.Background(), "workstore_123", dir, "http://127.0.0.1:5173"); err == nil {
		t.Fatalf("workstore_* scopeID must be rejected")
	}

	snap, err := svc.PreviewState(context.Background(), "preview_clnk_test1", dir, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatalf("PreviewState error = %v", err)
	}
	if snap.Running {
		t.Fatalf("expected Running=false before cold start")
	}
	if snap.Path != "" {
		t.Fatalf("expected empty Path when preview is not running, got %q", snap.Path)
	}

	// Resize reserves scope without launching Chrome and must force window=false even when defaultWindow=true.
	w, h := 1024, 768
	snap, err = svc.PreviewAction(context.Background(), "preview_clnk_test1", dir, "http://127.0.0.1:5173", PreviewActionRequest{
		Action: "resize",
		Width:  &w,
		Height: &h,
	})
	if err != nil {
		t.Fatalf("PreviewAction resize error = %v", err)
	}
	if snap.Width != 1024 || snap.Height != 768 || snap.Running || snap.Path != "" {
		t.Fatalf("unexpected resize snap = %+v", snap)
	}
	key, err := svc.resolvePreviewScopeKey("preview_clnk_test1", dir)
	if err != nil {
		t.Fatalf("resolvePreviewScopeKey error = %v", err)
	}
	svc.mu.Lock()
	sc := svc.scopes[key]
	svc.mu.Unlock()
	if sc == nil {
		t.Fatalf("expected reserved preview scope")
	}
	if sc.window {
		t.Fatalf("preview scope must force window=false even when defaultWindow=true")
	}
	svc.ClosePreview("preview_clnk_test1", dir)
	svc.mu.Lock()
	_, stillPresent := svc.scopes[key]
	svc.mu.Unlock()
	if stillPresent {
		t.Fatalf("ClosePreview should remove scope")
	}
	if !isPreviewErrorTabURL("chrome-error://chromewebdata/") || isPreviewErrorTabURL("http://127.0.0.1:5173/") {
		t.Fatalf("isPreviewErrorTabURL classification mismatch")
	}
}

func TestPreviewOriginProxyBlocksCrossOriginRedirectsAndDNSBypasses(t *testing.T) {
	var foreignHits atomic.Int32
	foreignSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHits.Add(1)
		_, _ = w.Write([]byte("INTERNAL_SECRET"))
	}))
	defer foreignSrv.Close()

	allowedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("SAME_ORIGIN_OK"))
		case "/redir-same":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/redir-foreign":
			http.Redirect(w, r, foreignSrv.URL+"/steal", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer allowedSrv.Close()

	proxy, err := startPreviewOriginProxy(allowedSrv.URL)
	if err != nil {
		t.Fatalf("startPreviewOriginProxy: %v", err)
	}
	defer proxy.Close()

	proxyURL, err := url.Parse("http://" + proxy.Addr())
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}

	// 1. Same-origin request passes through cleanly.
	resp, err := client.Get(allowedSrv.URL + "/ok")
	if err != nil {
		t.Fatalf("same-origin GET error: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "SAME_ORIGIN_OK" {
		t.Fatalf("same-origin GET = %d %q", resp.StatusCode, string(body))
	}

	// 2. Cross-origin request (e.g. iframe/img/fetch to another local port) is blocked before dialing.
	before := proxy.BlockedCount()
	resp, err = client.Get(foreignSrv.URL + "/admin")
	if err != nil {
		t.Fatalf("foreign GET proxy error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign GET status = %d, want 403", resp.StatusCode)
	}
	if foreignHits.Load() != 0 {
		t.Fatalf("foreign server received %d hits, want 0", foreignHits.Load())
	}
	if proxy.BlockedCount() <= before {
		t.Fatalf("expected BlockedCount to increment on foreign GET")
	}

	// 3. Same-origin redirect to a same-origin path succeeds; cross-origin redirect is blocked at proxy.
	resp, err = client.Get(allowedSrv.URL + "/redir-same")
	if err != nil {
		t.Fatalf("same-origin redirect error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("same-origin redirect status = %d, want 302", resp.StatusCode)
	}
	before = proxy.BlockedCount()
	resp, err = client.Get(allowedSrv.URL + "/redir-foreign")
	if err != nil {
		t.Fatalf("cross-origin redirect error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin redirect status = %d, want 403", resp.StatusCode)
	}
	if foreignHits.Load() != 0 || proxy.BlockedCount() <= before {
		t.Fatalf("cross-origin redirect was not blocked cleanly (hits=%d)", foreignHits.Load())
	}

	// 4. CONNECT tunnel to a foreign host:port is blocked with 403.
	foreignURL, _ := url.Parse(foreignSrv.URL)
	conn, err := net.DialTimeout("tcp", proxy.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	_, _ = io.WriteString(conn, "CONNECT "+foreignURL.Host+" HTTP/1.1\r\nHost: "+foreignURL.Host+"\r\n\r\n")
	br := bufio.NewReader(conn)
	cResp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		t.Fatalf("CONNECT foreign read: %v", err)
	}
	cResp.Body.Close()
	conn.Close()
	if cResp.StatusCode != http.StatusForbidden || foreignHits.Load() != 0 {
		t.Fatalf("CONNECT foreign = %d hits=%d, want 403 and 0 hits", cResp.StatusCode, foreignHits.Load())
	}

	// 5. CONNECT tunnel to allowed http:// origin rejects TLS ClientHello (0x16) to prevent scheme bypass.
	allowedURL, _ := url.Parse(allowedSrv.URL)
	before = proxy.BlockedCount()
	conn2, err := net.DialTimeout("tcp", proxy.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	_, _ = io.WriteString(conn2, "CONNECT "+allowedURL.Host+" HTTP/1.1\r\nHost: "+allowedURL.Host+"\r\n\r\n")
	br2 := bufio.NewReader(conn2)
	cResp2, err := http.ReadResponse(br2, &http.Request{Method: http.MethodConnect})
	if err != nil || cResp2.StatusCode != http.StatusOK {
		conn2.Close()
		t.Fatalf("CONNECT allowed setup failed: status=%v err=%v", cResp2, err)
	}
	_, _ = conn2.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x00})
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	_, readErr := conn2.Read(buf)
	conn2.Close()
	if readErr == nil {
		t.Fatalf("expected proxy to close TLS tunnel on http://-only preview origin")
	}
	if proxy.BlockedCount() <= before {
		t.Fatalf("expected BlockedCount to increment when rejecting TLS on http:// origin")
	}

	// 6. Dial-time DNS validation blocks custom domain resolving to loopback on Hoxi port 5400.
	dnsProxy, err := startPreviewOriginProxyWithLookup("http://rebind.example:5400", func(_ context.Context, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	})
	if err != nil {
		t.Fatalf("startPreviewOriginProxyWithLookup: %v", err)
	}
	defer dnsProxy.Close()
	dnsProxyURL, _ := url.Parse("http://" + dnsProxy.Addr())
	dnsClient := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(dnsProxyURL)},
		Timeout:   5 * time.Second,
	}
	resp, err = dnsClient.Get("http://rebind.example:5400/api/hoxi/sessions")
	if err != nil {
		t.Fatalf("dns rebind GET error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DNS-resolved loopback:5400 status = %d, want 403", resp.StatusCode)
	}

	// 7. Dial-time DNS validation blocks metadata IP 169.254.169.254.
	metaProxy, err := startPreviewOriginProxyWithLookup("http://meta.example", func(_ context.Context, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	})
	if err != nil {
		t.Fatalf("startPreviewOriginProxyWithLookup meta: %v", err)
	}
	defer metaProxy.Close()
	metaProxyURL, _ := url.Parse("http://" + metaProxy.Addr())
	metaClient := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(metaProxyURL)},
		Timeout:   5 * time.Second,
	}
	resp, err = metaClient.Get("http://meta.example/latest/meta-data/")
	if err != nil {
		t.Fatalf("meta GET error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DNS-resolved link-local metadata status = %d, want 403", resp.StatusCode)
	}
}
