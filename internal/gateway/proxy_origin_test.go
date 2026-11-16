package gateway

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestReverseProxyRewritesOriginToUpstream(t *testing.T) {
	var seenOrigin string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "origin-pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		seenOrigin = r.Header.Get("Origin")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	gw := New(port, "origin-pass", t.TempDir(), nil, nil)
	wantOrigin := upstream.URL

	// Native Wails clients send wails://wails, which upstream rejects on
	// WebSocket upgrades. The proxy must present the loopback upstream origin.
	rec := httptest.NewRecorder()
	req := localRequest(http.MethodGet, "/session", nil)
	req.Header.Set("Origin", "wails://wails")
	gw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seenOrigin != wantOrigin {
		t.Fatalf("upstream Origin = %q, want %q", seenOrigin, wantOrigin)
	}

	// Non-browser callers without an Origin keep sending none.
	seenOrigin = "unset"
	rec = httptest.NewRecorder()
	gw.ServeHTTP(rec, localRequest(http.MethodGet, "/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seenOrigin != "" {
		t.Fatalf("upstream Origin = %q, want empty", seenOrigin)
	}
}

func TestReverseProxyUpgradesWebSocketWithUpstreamOrigin(t *testing.T) {
	var wantOrigin string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != wantOrigin {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("pty-ready"))
	}))
	defer upstream.Close()
	wantOrigin = upstream.URL

	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	gw := New(port, "origin-pass", t.TempDir(), nil, nil)
	server := httptest.NewServer(gw)
	defer server.Close()

	header := http.Header{"Origin": []string{"wails://wails"}}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/pty/pty_1/connect?directory=/tmp/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial gateway: %v (status %d)", err, status)
	}
	defer conn.Close()
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read upstream frame: %v", err)
	}
	if string(msg) != "pty-ready" {
		t.Fatalf("frame = %q, want %q", msg, "pty-ready")
	}
}
