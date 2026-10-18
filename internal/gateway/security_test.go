package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
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
