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

	// 2. Unauthenticated loopback calls to raw upstream prefixes (/pty, /session, /auth, /api/session) fail with 403.
	for _, target := range []string{"/pty", "/session", "/auth/openai", "/api/session", "/global/config"} {
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
