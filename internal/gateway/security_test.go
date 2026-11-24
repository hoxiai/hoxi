package gateway

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func localRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Host = "127.0.0.1:5089"
	return r
}

func TestRequestSecurityBoundary(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	g.SetBrowserUIOrigins("http://127.0.0.1:5089", "http://localhost:5173", "http://127.0.0.1:5173", "http://wails.localhost", "http://localhost:34115", "wails://wails.localhost:34115")
	calls := 0
	g.mux.HandleFunc("/boundary", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	tests := []struct {
		name, host, origin, site string
		allowed                  bool
	}{
		{"CLI and OpenCode bridge", "127.0.0.1:5089", "", "", true},
		{"IPv6 CLI", "[::1]:5089", "", "", true},
		{"localhost CLI", "localhost:5089", "", "", true},
		{"headless", "127.0.0.1:5089", "http://127.0.0.1:5089", "same-origin", true},
		{"Vite proxy", "127.0.0.1:5089", "http://localhost:5173", "same-origin", true},
		{"Vite direct", "127.0.0.1:5089", "http://127.0.0.1:5173", "cross-site", true},
		{"native Wails", "wails", "wails://wails", "", true},
		{"Wails development WebView", "wails.localhost:34115", "wails://wails.localhost:34115", "", true},
		{"Wails development browser", "localhost:34115", "http://localhost:34115", "same-origin", true},
		{"unconfigured Wails port", "wails.localhost:34115", "wails://wails.localhost:34116", "", false},
		{"native no Origin", "wails", "", "", true},
		{"Windows Wails", "wails.localhost", "http://wails.localhost", "same-origin", true},
		{"same-origin GET", "127.0.0.1:5089", "", "same-origin", true},
		{"address bar", "127.0.0.1:5089", "", "none", true},
		{"hostile origin", "127.0.0.1:5089", "https://evil.example", "", false},
		{"null origin", "127.0.0.1:5089", "null", "", false},
		{"other local port", "127.0.0.1:5089", "http://localhost:5174", "", false},
		{"other scheme", "127.0.0.1:5089", "https://localhost:5173", "", false},
		{"origin path", "127.0.0.1:5089", "http://localhost:5173/", "", false},
		{"origin list", "127.0.0.1:5089", "http://localhost:5173 https://evil.example", "", false},
		{"arbitrary Wails", "wails", "wails://evil", "", false},
		{"Wails suffix", "wails", "wails://evil.wails.localhost", "", false},
		{"cross-site no Origin", "127.0.0.1:5089", "", "cross-site", false},
		{"rebinding", "evil.example:5089", "", "", false},
		{"spoofed host trusted origin", "evil.example:5089", "http://localhost:5173", "", false},
		{"host userinfo", "evil@localhost:5089", "", "", false},
		{"host suffix", "localhost.evil:5089", "", "", false},
		{"Wails host suffix", "evil.wails.localhost", "", "", false},
		{"host path", "localhost:5089/path", "", "", false},
		{"empty Host", "", "", "", false},
	}
	for _, tt := range tests {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
			t.Run(tt.name+"/"+method, func(t *testing.T) {
				r := localRequest(method, "/boundary", nil)
				r.Host = tt.host
				if tt.origin != "" {
					r.Header.Set("Origin", tt.origin)
				}
				r.Header.Set("Sec-Fetch-Site", tt.site)
				// Forwarding headers must not override the request authority.
				r.Header.Set("X-Forwarded-Host", "localhost:5089")
				r.Header.Set("Forwarded", "host=localhost:5089")
				before := calls
				w := httptest.NewRecorder()
				g.ServeHTTP(w, r)
				want := http.StatusForbidden
				if tt.allowed {
					want = http.StatusOK
					if method == http.MethodOptions && tt.origin != "" {
						want = http.StatusNoContent
					}
				}
				if w.Code != want {
					t.Fatalf("status = %d, want %d: %s", w.Code, want, w.Body.String())
				}
				if !tt.allowed && (calls != before || w.Header().Get("Access-Control-Allow-Origin") != "") {
					t.Fatal("rejected request dispatched or received CORS permission")
				}
			})
		}
	}
}

func TestOriginConfigurationAndAmbiguousHeaders(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	check := func(origins []string, want int) {
		t.Helper()
		r := localRequest(http.MethodGet, "/healthz", nil)
		r.Header["Origin"] = origins
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("Origin %q: status %d, want %d", origins, w.Code, want)
		}
	}
	check([]string{"http://localhost:5173"}, http.StatusForbidden)
	g.SetBrowserUIOrigins("http://localhost:5173")
	check([]string{"http://localhost:5173"}, http.StatusOK)
	check([]string{"http://localhost:5173", "https://evil.example"}, http.StatusForbidden)
	check([]string{""}, http.StatusForbidden)
	g.SetBrowserUIOrigins("https://evil.example", "http://localhost:5173/", "http://localhost:5173#")
	check([]string{"http://localhost:5173"}, http.StatusForbidden)
	check([]string{"https://evil.example"}, http.StatusForbidden)
	check([]string{"wails://wails"}, http.StatusOK)
}

func TestConcurrentUIOriginConfiguration(t *testing.T) {
	g := New(0, "test", t.TempDir(), nil, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			g.SetBrowserUIOrigins("http://localhost:5173")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			r := localRequest(http.MethodGet, "/healthz", nil)
			r.Header.Set("Origin", "http://localhost:5173")
			g.ServeHTTP(httptest.NewRecorder(), r)
		}
	}()
	wg.Wait()
}

func TestUpstreamPassthroughTokenDefenseInDepth(t *testing.T) {
	var seenHeader, seenQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeader = r.Header.Get(gatewayTokenHeader)
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	g := New(port, "upstream-pass", t.TempDir(), nil, nil)
	const secretToken = "boot-token-secret-xyz"
	g.SetGatewayToken(secretToken)

	// 1. Bootstrap advertises gatewayToken and sets HttpOnly SameSite=Strict cookie.
	bootRec := httptest.NewRecorder()
	g.ServeHTTP(bootRec, localRequest(http.MethodGet, "/api/hoxi/bootstrap", nil))
	if bootRec.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d", bootRec.Code)
	}
	var bootBody map[string]any
	if err := json.Unmarshal(bootRec.Body.Bytes(), &bootBody); err != nil {
		t.Fatal(err)
	}
	if bootBody["gatewayToken"] != secretToken {
		t.Fatalf("bootstrap gatewayToken = %v, want %q", bootBody["gatewayToken"], secretToken)
	}
	cookies := bootRec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != gatewayTokenCookieName || cookies[0].Value != secretToken || !cookies[0].HttpOnly {
		t.Fatalf("unexpected bootstrap cookies: %+v", cookies)
	}

	// 2. Unauthenticated loopback calls to raw upstream prefixes and token-gated Hoxi host/provider routes fail with 403.
	for _, target := range []string{
		"/pty", "/api/hoxi/pty", "/api/hoxi/pty/pty_1/connect",
		"/api/hoxi/agents", "/api/hoxi/commands", "/api/hoxi/lsp",
		"/session", "/auth/openai", "/api/hoxi/provider/credentials/openai",
		"/api/session", "/global/config", "/api/hoxi/provider/config", "/api/hoxi/provider/catalog",
	} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, localRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "gateway token") {
			t.Fatalf("%s without token: status=%d body=%s", target, w.Code, w.Body.String())
		}
	}

	// 3. Valid X-Hoxi-Token header, Cookie, or ?hoxiToken query param succeeds and is stripped before upstream.
	reqHeader := localRequest(http.MethodPost, "/pty?directory=/tmp/ws", strings.NewReader(`{}`))
	reqHeader.Header.Set(gatewayTokenHeader, secretToken)
	wHeader := httptest.NewRecorder()
	g.ServeHTTP(wHeader, reqHeader)
	if wHeader.Code != http.StatusOK || seenHeader != "" || seenQuery != "directory=/tmp/ws" {
		t.Fatalf("X-Hoxi-Token passthrough: status=%d seenHeader=%q seenQuery=%q", wHeader.Code, seenHeader, seenQuery)
	}

	reqQuery := localRequest(http.MethodGet, "/pty/pty_1/connect?directory=/tmp/ws&hoxiToken="+secretToken, nil)
	wQuery := httptest.NewRecorder()
	g.ServeHTTP(wQuery, reqQuery)
	if wQuery.Code != http.StatusOK || strings.Contains(seenQuery, "hoxiToken") {
		t.Fatalf("hoxiToken query passthrough: status=%d seenQuery=%q", wQuery.Code, seenQuery)
	}

	reqCookie := localRequest(http.MethodGet, "/auth/openai", nil)
	reqCookie.AddCookie(&http.Cookie{Name: gatewayTokenCookieName, Value: secretToken})
	wCookie := httptest.NewRecorder()
	g.ServeHTTP(wCookie, reqCookie)
	if wCookie.Code != http.StatusOK {
		t.Fatalf("cookie passthrough: status=%d", wCookie.Code)
	}

	// 4. Native Wails host and Hoxi's unified /api/hoxi/sessions/{id}/message facade remain permitted.
	reqWails := localRequest(http.MethodGet, "/pty", nil)
	reqWails.Host = "wails"
	wWails := httptest.NewRecorder()
	g.ServeHTTP(wWails, reqWails)
	if wWails.Code != http.StatusOK {
		t.Fatalf("native Wails request blocked: %d", wWails.Code)
	}

	wFacade := httptest.NewRecorder()
	g.ServeHTTP(wFacade, localRequest(http.MethodGet, "/api/hoxi/sessions/ses_unadopted/message", nil))
	if wFacade.Code != http.StatusOK {
		t.Fatalf("unified conversation facade blocked: %d %s", wFacade.Code, wFacade.Body.String())
	}
}

// OpenCode permissions are answered only through Hoxi's own reply route, even
// with the gateway token or from the native window: a raw reply skips the
// approval floors, and a raw "always" stays in OpenCode for every session.
func TestRawOpenCodePermissionRepliesAreRefused(t *testing.T) {
	var forwarded []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = append(forwarded, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	g := New(port, "raw-permission", t.TempDir(), nil, nil)
	const token = "boot-token-raw-perm"
	g.SetGatewayToken(token)

	for _, target := range []string{
		"/permission/per_1/reply",
		"/api/permission/per_1/reply",
		"/permission/per_1/reply/",
		"/Permission/per_1/reply",
		"/session/ses_1/permissions/per_1",
		"/api/session/ses_1/permissions/per_1",
	} {
		for _, native := range []bool{false, true} {
			r := localRequest(http.MethodPost, target, strings.NewReader(`{"reply":"always","response":"always"}`))
			r.Header.Set(gatewayTokenHeader, token)
			if native {
				r.Host = "wails"
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("POST %s (native=%v): status=%d body=%s", target, native, w.Code, w.Body.String())
			}
		}
	}
	if len(forwarded) != 0 {
		t.Fatalf("raw replies reached OpenCode: %v", forwarded)
	}

	// Listing pending permissions still passes through.
	r := localRequest(http.MethodGet, "/permission", nil)
	r.Header.Set(gatewayTokenHeader, token)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusOK || len(forwarded) != 1 || forwarded[0] != "GET /permission" {
		t.Fatalf("GET /permission: status=%d forwarded=%v", w.Code, forwarded)
	}
}

type desktopIframeTestExtension struct {
	route string
}

func (e *desktopIframeTestExtension) HasSessionContext(string) bool { return false }
func (e *desktopIframeTestExtension) ForgetSession(string)          {}
func (e *desktopIframeTestExtension) Close()                        {}
func (e *desktopIframeTestExtension) DesktopIframeRoutes() []string { return []string{e.route} }
func (e *desktopIframeTestExtension) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc(e.route, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestDesktopIframeRouteCrossSitePolicy(t *testing.T) {
	g := New(0, "desktop-iframe", t.TempDir(), nil, nil)
	g.AttachExtension(&desktopIframeTestExtension{route: "/api/hoxi/test/iframe-bridge"})
	g.mux.HandleFunc("/api/hoxi/test/other", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name   string
		method string
		path   string
		host   string
		origin string
		site   string
		dest   string
		mode   string
		remote bool
		want   int
	}{
		{"wails cross-site iframe GET", http.MethodGet, "/api/hoxi/test/iframe-bridge?lang=zh", "127.0.0.1:5089", "", "cross-site", "iframe", "navigate", false, http.StatusOK},
		{"wails cross-site iframe HEAD", http.MethodHead, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "", "cross-site", "iframe", "", false, http.StatusOK},
		{"reject POST on iframe bridge", http.MethodPost, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "", "cross-site", "iframe", "navigate", false, http.StatusForbidden},
		{"reject fetch empty dest", http.MethodGet, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "", "cross-site", "empty", "no-cors", false, http.StatusForbidden},
		{"reject top-level document dest", http.MethodGet, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "", "cross-site", "document", "navigate", false, http.StatusForbidden},
		{"reject missing dest", http.MethodGet, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "", "cross-site", "", "navigate", false, http.StatusForbidden},
		{"reject non-navigate mode", http.MethodGet, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "", "cross-site", "iframe", "no-cors", false, http.StatusForbidden},
		{"reject hostile origin", http.MethodGet, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "https://evil.example", "cross-site", "iframe", "navigate", false, http.StatusForbidden},
		{"reject untrusted host", http.MethodGet, "/api/hoxi/test/iframe-bridge", "evil.example:5089", "", "cross-site", "iframe", "navigate", false, http.StatusForbidden},
		{"reject sibling path", http.MethodGet, "/api/hoxi/test/iframe-bridge/extra", "127.0.0.1:5089", "", "cross-site", "iframe", "navigate", false, http.StatusForbidden},
		{"reject undeclared route", http.MethodGet, "/api/hoxi/test/other", "127.0.0.1:5089", "", "cross-site", "iframe", "navigate", false, http.StatusForbidden},
		{"reject remote request", http.MethodGet, "/api/hoxi/test/iframe-bridge", "127.0.0.1:5089", "", "cross-site", "iframe", "navigate", true, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := localRequest(tc.method, tc.path, nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.dest != "" {
				req.Header.Set("Sec-Fetch-Dest", tc.dest)
			}
			if tc.mode != "" {
				req.Header.Set("Sec-Fetch-Mode", tc.mode)
			}
			if tc.remote {
				req = WithRemoteSource(req, "gopanel")
			}
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// Cross-origin loopback CORS preflight from macOS Wails must allow Work save headers.
	preflight := localRequest(http.MethodOptions, "/api/hoxi/work/apps/1688/service/v1/catalog/assets/save", nil)
	preflight.Header.Set("Origin", "wails://wails")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "content-type, authorization, x-hoxi-work-ui, x-hoxi-work-app, x-hoxi-store-id")
	preRec := httptest.NewRecorder()
	g.ServeHTTP(preRec, preflight)
	if preRec.Code != http.StatusNoContent {
		t.Fatalf("Wails CORS preflight status = %d, want 204", preRec.Code)
	}
	allowHeaders := preRec.Header().Get("Access-Control-Allow-Headers")
	for _, hdr := range []string{"X-Hoxi-Work-UI", "X-Hoxi-Work-App", "X-Hoxi-Store-Id"} {
		if !strings.Contains(allowHeaders, hdr) {
			t.Fatalf("Access-Control-Allow-Headers missing %q: %q", hdr, allowHeaders)
		}
	}
}
