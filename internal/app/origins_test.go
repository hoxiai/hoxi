package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/gateway"
)

func TestLoopbackUIOrigins(t *testing.T) {
	for _, bind := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(bind, func(t *testing.T) {
			listener, err := net.Listen("tcp", bind)
			if err != nil {
				if strings.HasPrefix(bind, "[") {
					t.Skipf("IPv6 loopback unavailable: %v", err)
				}
				t.Fatal(err)
			}
			defer listener.Close()
			addr := listener.Addr().(*net.TCPAddr)
			port := strconv.Itoa(addr.Port)
			g := gateway.New(0, "test", t.TempDir(), nil, nil)
			g.SetBrowserUIOrigins(loopbackUIOrigins(addr)...)
			for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
				for _, requestKind := range []string{"POST", "WebSocket", "preflight"} {
					t.Run(host+"/"+requestKind, func(t *testing.T) {
						method := http.MethodPost
						if requestKind == "WebSocket" {
							method = http.MethodGet
						} else if requestKind == "preflight" {
							method = http.MethodOptions
						}
						authority := net.JoinHostPort(host, port)
						r := httptest.NewRequest(method, "http://"+authority+"/healthz", nil)
						r.Header.Set("Origin", "http://"+authority)
						r.Header.Set("Sec-Fetch-Site", "same-origin")
						if requestKind == "WebSocket" {
							r.Header.Set("Connection", "Upgrade")
							r.Header.Set("Upgrade", "websocket")
						}
						want := http.StatusForbidden
						if host == "localhost" || host == addr.IP.String() {
							want = http.StatusOK
							if requestKind == "preflight" {
								want = http.StatusNoContent
							}
						}
						w := httptest.NewRecorder()
						g.ServeHTTP(w, r)
						if w.Code != want {
							t.Fatalf("status = %d, want %d", w.Code, want)
						}
					})
				}
			}
			// Even a matching Host and same-origin fetch metadata cannot grant
			// permission to a different, unconfigured local port.
			otherPort := addr.Port%65535 + 1
			for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
				r := httptest.NewRequest(http.MethodPost, "http://"+net.JoinHostPort(host, strconv.Itoa(otherPort))+"/healthz", nil)
				r.Header.Set("Origin", "http://"+r.Host)
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				w := httptest.NewRecorder()
				g.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("unconfigured origin %q: status = %d", r.Header.Get("Origin"), w.Code)
				}
			}
		})
	}
}
