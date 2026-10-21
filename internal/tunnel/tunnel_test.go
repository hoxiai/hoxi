package tunnel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testClientID = "550e8400-e29b-41d4-a716-446655440000"
const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// fakeController plays GoPanel: it issues a credential on pair/exchange and
// hands the tunnel WebSocket to the test.
type fakeController struct {
	server *httptest.Server
	conns  chan *websocket.Conn
}

func newFakeController(t *testing.T) *fakeController {
	t.Helper()
	fc := &fakeController{conns: make(chan *websocket.Conn, 1)}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tunnel/pair/exchange", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["code"] != strings.Repeat("ab", 16) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "pairing code is invalid"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{
			"nodeId": 7, "clientId": testClientID, "token": testToken,
		}})
	})
	mux.HandleFunc("/api/tunnel/connect", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-GoPanel-Tunnel-Client") != testClientID || r.Header.Get("X-GoPanel-Tunnel-Token") != testToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		fc.conns <- conn
	})
	fc.server = httptest.NewServer(mux)
	t.Cleanup(fc.server.Close)
	return fc
}

func readFrame(t *testing.T, conn *websocket.Conn) frame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		f, err := decodeFrame(data)
		if err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if f.Type == framePing {
			continue
		}
		return f
	}
}

func sendFrame(t *testing.T, conn *websocket.Conn, f frame) {
	t.Helper()
	if err := conn.WriteMessage(websocket.BinaryMessage, encodeFrame(f)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func pairedManager(t *testing.T, handler http.Handler) (*Manager, *websocket.Conn, string) {
	t.Helper()
	fc := newFakeController(t)
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	m := NewManager(Options{Handler: handler, Version: "test", Hostname: func() string { return "mac.local" }})
	m.Configure(NewStore(configPath), 5410)
	t.Cleanup(m.Stop)
	if err := m.Pair(t.Context(), fc.server.URL, strings.Repeat("ab", 16), "Studio"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	var conn *websocket.Conn
	select {
	case conn = <-fc.conns:
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel did not connect")
	}
	t.Cleanup(func() { _ = conn.Close() })
	return m, conn, configPath
}

func TestPairConnectsAndRegistersGatewayService(t *testing.T) {
	m, conn, configPath := pairedManager(t, http.NotFoundHandler())
	sync := readFrame(t, conn)
	if sync.Type != frameServiceSync {
		t.Fatalf("first frame type = %d, want service sync", sync.Type)
	}
	var payload serviceSync
	if err := json.Unmarshal(sync.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "mac.local" || len(payload.Services) != 1 || payload.Services[0].PublicID != ServicePublicID || payload.Services[0].TargetPort != 5410 {
		t.Fatalf("unexpected service sync: %+v", payload)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %v, want 0600", info.Mode().Perm())
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Status().State != StateOnline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	status := m.Status()
	if status.State != StateOnline || status.NodeID != 7 || status.Name != "Studio" {
		t.Fatalf("status = %+v", status)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), testToken) {
		t.Fatal("status must not expose the tunnel token")
	}
}

func TestPairRejectsInvalidCode(t *testing.T) {
	fc := newFakeController(t)
	m := NewManager(Options{Handler: http.NotFoundHandler()})
	m.Configure(NewStore(filepath.Join(t.TempDir(), "tunnel.json")), 5410)
	err := m.Pair(t.Context(), fc.server.URL, strings.Repeat("cd", 16), "")
	if err == nil || !strings.Contains(err.Error(), "pairing code is invalid") {
		t.Fatalf("err = %v", err)
	}
	if err := m.Pair(t.Context(), "http://gopanel.example.com", strings.Repeat("ab", 16), ""); err == nil {
		t.Fatal("public plain-HTTP controller must be rejected")
	}
}

func TestStreamForwardsRequestAndStripsCredentials(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Host != "127.0.0.1" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			http.Error(w, "credentials leaked", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + r.URL.RequestURI() + "\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write(append([]byte("data: "), body...))
	})
	_, conn, _ := pairedManager(t, handler)
	readFrame(t, conn) // service sync

	open, _ := jsonFrame(frameOpen, 42, openFrame{
		ServiceID: ServicePublicID, Method: http.MethodPost, RequestURI: "/api/hoxi/sessions?directory=%2Ftmp%2Fwt", HasBody: true,
		Headers: map[string][]string{"Host": {"gopanel.example.com"}, "Authorization": {"Bearer gp"}, "Cookie": {"a=b"}, "Origin": {"https://evil"}},
	})
	sendFrame(t, conn, open)
	sendFrame(t, conn, frame{Type: frameData, StreamID: 42, Payload: encodeData(dataHTTP, []byte("hello"))})
	sendFrame(t, conn, frame{Type: frameEnd, StreamID: 42})

	response := readFrame(t, conn)
	var meta responseFrame
	if response.Type != frameResponse || json.Unmarshal(response.Payload, &meta) != nil || meta.Status != http.StatusOK {
		t.Fatalf("response frame = %d %s", response.Type, response.Payload)
	}
	var body strings.Builder
	for {
		f := readFrame(t, conn)
		if f.Type == frameEnd {
			break
		}
		if f.Type != frameData {
			t.Fatalf("unexpected frame %d: %s", f.Type, f.Payload)
		}
		_, payload, _ := decodeData(f.Payload)
		body.Write(payload)
	}
	if got := body.String(); got != "data: /api/hoxi/sessions?directory=%2Ftmp%2Fwt\n\ndata: hello" {
		t.Fatalf("body = %q", got)
	}
}

func TestStreamRejectsUnknownServiceAndWebSocket(t *testing.T) {
	_, conn, _ := pairedManager(t, http.NotFoundHandler())
	readFrame(t, conn)
	for id, open := range map[uint64]openFrame{
		1: {ServiceID: "other", Method: http.MethodGet, RequestURI: "/healthz"},
		2: {ServiceID: ServicePublicID, Method: http.MethodGet, RequestURI: "/pty/1/connect", Upgrade: true},
		3: {ServiceID: ServicePublicID, Method: http.MethodGet, RequestURI: "//evil.example/x"},
	} {
		f, _ := jsonFrame(frameOpen, id, open)
		sendFrame(t, conn, f)
		got := readFrame(t, conn)
		if got.Type != frameReset || got.StreamID != id {
			t.Fatalf("stream %d: frame = %d %s, want reset", id, got.Type, got.Payload)
		}
	}
}

func TestUnpairDeletesCredential(t *testing.T) {
	m, _, configPath := pairedManager(t, http.NotFoundHandler())
	if err := m.Unpair(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("credential still present: %v", err)
	}
	if m.Status().State != StateDisabled {
		t.Fatalf("state = %s", m.Status().State)
	}
}

func TestPairWithoutStorePathIsRefused(t *testing.T) {
	redeemed := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redeemed = true }))
	t.Cleanup(server.Close)
	m := NewManager(Options{Handler: http.NotFoundHandler()})
	if err := m.Pair(t.Context(), server.URL, strings.Repeat("ab", 16), ""); err == nil {
		t.Fatal("pairing must fail when no credential path is configured")
	}
	if redeemed {
		t.Fatal("pairing code must not be redeemed without a credential store")
	}
}

func TestNotifySendsFrameWhileOnlineOnly(t *testing.T) {
	// Wire value shared with gopanel_v2 tunnelFrameNotify.
	if frameNotify != 9 {
		t.Fatalf("frameNotify = %d, must stay 9", frameNotify)
	}
	m := NewManager(Options{Handler: http.NotFoundHandler()})
	if err := m.Notify(map[string]string{"kind": "approval"}); err != ErrOffline {
		t.Fatalf("offline notify err = %v", err)
	}
	m, conn, _ := pairedManager(t, http.NotFoundHandler())
	readFrame(t, conn) // service sync
	deadline := time.Now().Add(5 * time.Second)
	for m.Status().State != StateOnline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.Notify(map[string]string{"kind": "approval", "requestId": "per_1"}); err != nil {
		t.Fatal(err)
	}
	f := readFrame(t, conn)
	if f.Type != frameNotify || f.StreamID != 0 || !strings.Contains(string(f.Payload), "per_1") {
		t.Fatalf("frame = %d %d %s", f.Type, f.StreamID, f.Payload)
	}
}
