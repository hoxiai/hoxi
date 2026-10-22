package tunnel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testClientID = "550e8400-e29b-41d4-a716-446655440000"
const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// oneClickCode is the one-time code GoPanel issues when it approves a request
// opened by /api/tunnel/pair/request.
const oneClickCode = "efefefefefefefefefefefefefefefef"

// fakeController plays GoPanel: it issues a credential on pair/exchange, opens
// and polls one-click pairing requests, and hands the tunnel WebSocket to the
// test.
type fakeController struct {
	server *httptest.Server
	conns  chan *websocket.Conn

	mu          sync.Mutex
	state       string // waiting|approved|denied|expired
	pollCount   int
	requestHits int
	rejectReq   bool
}

func newFakeController(t *testing.T) *fakeController {
	t.Helper()
	fc := &fakeController{conns: make(chan *websocket.Conn, 8), state: PendingWaiting}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tunnel/pair/exchange", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["code"] != strings.Repeat("ab", 16) && req["code"] != oneClickCode {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "pairing code is invalid"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{
			"nodeId": 7, "clientId": testClientID, "token": testToken,
		}})
	})
	mux.HandleFunc("/api/tunnel/pair/request", func(w http.ResponseWriter, r *http.Request) {
		fc.mu.Lock()
		fc.requestHits++
		reject := fc.rejectReq
		fc.mu.Unlock()
		if reject {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "pairing is disabled"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{
			"requestId": "req_1", "userCode": "ABCD-EFGH",
			"expiresAt": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
			"pollToken": "poll-secret",
		}})
	})
	mux.HandleFunc("/api/tunnel/pair/request/", func(w http.ResponseWriter, r *http.Request) {
		fc.mu.Lock()
		fc.pollCount++
		state := fc.state
		fc.mu.Unlock()
		code := ""
		if state == PendingApproved {
			code = oneClickCode
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{
			"state": state, "code": code,
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

func (fc *fakeController) setPairState(state string) {
	fc.mu.Lock()
	fc.state = state
	fc.mu.Unlock()
}

func (fc *fakeController) rejectRequests() {
	fc.mu.Lock()
	fc.rejectReq = true
	fc.mu.Unlock()
}

func (fc *fakeController) polls() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.pollCount
}

func (fc *fakeController) requests() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.requestHits
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// oneClickManager wires a manager with a fast poll loop and a real credential
// path so the one-click flow can be exercised.
func oneClickManager(t *testing.T, handler http.Handler) (*Manager, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	m := NewManager(Options{
		Handler:      handler,
		Version:      "test",
		Hostname:     func() string { return "mac.local" },
		PollInterval: 10 * time.Millisecond,
		PollTimeout:  5 * time.Second,
	})
	m.Configure(NewStore(configPath), 5410)
	t.Cleanup(m.Stop)
	return m, configPath
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

func TestDuplicateStreamIDClosesPreviousWithoutResetOrCrossTalk(t *testing.T) {
	firstStarted := make(chan struct{})
	firstCanceled := make(chan struct{})
	firstRelease := make(chan struct{})
	var firstOnce sync.Once

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			firstOnce.Do(func() { close(firstStarted) })
			<-r.Context().Done()
			close(firstCanceled)
			<-firstRelease
			// A stale handler trying to write after its stream ID was reused must be dropped.
			_, _ = w.Write([]byte("stale-first-body"))
			return
		}
		_, _ = w.Write([]byte("fresh-second-body"))
	})

	_, conn, _ := pairedManager(t, handler)
	readFrame(t, conn) // service sync

	open1, _ := jsonFrame(frameOpen, 77, openFrame{
		ServiceID: ServicePublicID, Method: http.MethodGet, RequestURI: "/first",
	})
	sendFrame(t, conn, open1)

	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first stream did not start")
	}

	open2, _ := jsonFrame(frameOpen, 77, openFrame{
		ServiceID: ServicePublicID, Method: http.MethodGet, RequestURI: "/second",
	})
	sendFrame(t, conn, open2)

	select {
	case <-firstCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("reused stream ID did not cancel previous stream")
	}
	close(firstRelease)

	resp := readFrame(t, conn)
	var meta responseFrame
	if resp.Type != frameResponse || resp.StreamID != 77 || json.Unmarshal(resp.Payload, &meta) != nil || meta.Status != http.StatusOK {
		t.Fatalf("response frame = %+v", resp)
	}
	data := readFrame(t, conn)
	if data.Type != frameData || data.StreamID != 77 {
		t.Fatalf("data frame = %+v", data)
	}
	_, payload, err := decodeData(data.Payload)
	if err != nil || string(payload) != "fresh-second-body" {
		t.Fatalf("payload = %q, err = %v", string(payload), err)
	}
	end := readFrame(t, conn)
	if end.Type != frameEnd || end.StreamID != 77 {
		t.Fatalf("end frame = %+v", end)
	}
}

func TestStreamWithoutBodyUsesNoBodyAndIgnoresStrayData(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != http.NoBody || r.ContentLength != 0 {
			http.Error(w, "expected http.NoBody and ContentLength 0", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			http.Error(w, "unexpected body bytes", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	_, conn, _ := pairedManager(t, handler)
	readFrame(t, conn) // service sync

	open, _ := jsonFrame(frameOpen, 88, openFrame{
		ServiceID:  ServicePublicID,
		Method:     http.MethodPost,
		RequestURI: "/api/hoxi/sessions",
		HasBody:    false,
		Headers:    map[string][]string{"Content-Length": {"5"}},
	})
	sendFrame(t, conn, open)
	sendFrame(t, conn, frame{Type: frameData, StreamID: 88, Payload: encodeData(dataHTTP, []byte("stray"))})

	resp := readFrame(t, conn)
	var meta responseFrame
	if resp.Type != frameResponse || json.Unmarshal(resp.Payload, &meta) != nil || meta.Status != http.StatusOK {
		t.Fatalf("response = %d %s", resp.Type, resp.Payload)
	}
	data := readFrame(t, conn)
	_, payload, _ := decodeData(data.Payload)
	if string(payload) != "ok" {
		t.Fatalf("payload = %q, want ok", string(payload))
	}
	if end := readFrame(t, conn); end.Type != frameEnd {
		t.Fatalf("end = %d, want frameEnd", end.Type)
	}
}

func TestStopResetsStatusAndConcurrentRestartsSerialize(t *testing.T) {
	fc := newFakeController(t)
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	m := NewManager(Options{Handler: http.NotFoundHandler(), Version: "test", Hostname: func() string { return "mac.local" }})
	m.Configure(NewStore(configPath), 5410)
	t.Cleanup(m.Stop)

	if err := m.Pair(t.Context(), fc.server.URL, strings.Repeat("ab", 16), "Studio"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	conn := <-fc.conns
	t.Cleanup(func() { _ = conn.Close() })

	deadline := time.Now().Add(5 * time.Second)
	for m.Status().State != StateOnline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.Status().State != StateOnline {
		t.Fatalf("state before stop = %s, want online", m.Status().State)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Start()
		}()
	}
	wg.Wait()

	m.Stop()
	status := m.Status()
	if status.State == StateOnline || status.State == StateConnecting || status.ConnectedAt != nil {
		t.Fatalf("status after Stop() = %+v, want offline with nil ConnectedAt", status)
	}
	if status.State != StateOffline {
		t.Fatalf("state after Stop() = %s, want %s", status.State, StateOffline)
	}
}

func TestPairDoesNotFollowRedirects(t *testing.T) {
	redirectTargetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetHit = true
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{
			"nodeId": 7, "clientId": testClientID, "token": testToken,
		}})
	}))
	t.Cleanup(target.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/tunnel/pair/exchange", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	m := NewManager(Options{Handler: http.NotFoundHandler()})
	m.Configure(NewStore(filepath.Join(t.TempDir(), "tunnel.json")), 5410)
	t.Cleanup(m.Stop)

	if err := m.Pair(t.Context(), redirector.URL, strings.Repeat("ab", 16), "Studio"); err == nil {
		t.Fatal("expected Pair to reject redirect response")
	}
	if redirectTargetHit {
		t.Fatal("Pair followed HTTP redirect and leaked pairing request to redirect target")
	}
}

func TestStartSurfacesCorruptConfigError(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	if err := os.WriteFile(configPath, []byte("{corrupt json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{Handler: http.NotFoundHandler()})
	m.Configure(NewStore(configPath), 5410)
	m.Start()

	status := m.Status()
	if status.State != StateDisabled {
		t.Fatalf("state = %s, want %s", status.State, StateDisabled)
	}
	if !strings.Contains(status.Message, "read tunnel config") {
		t.Fatalf("expected corrupt config error in status.Message, got %q", status.Message)
	}
	matches, err := filepath.Glob(configPath + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected 1 corrupt backup file, got %v (err=%v)", matches, err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil || string(raw) != "{corrupt json" {
		t.Fatalf("corrupt backup = %q (err=%v), want {corrupt json", string(raw), err)
	}
}

func TestRequestPairApprovedConnectsAndRegistersGateway(t *testing.T) {
	fc := newFakeController(t)
	m, configPath := oneClickManager(t, http.NotFoundHandler())

	if err := m.RequestPair(t.Context(), fc.server.URL, "Studio"); err != nil {
		t.Fatalf("request pair: %v", err)
	}
	pending := m.Status().Pending
	if pending == nil || pending.State != PendingWaiting || pending.UserCode == "" || pending.RequestID == "" {
		t.Fatalf("pending = %+v", pending)
	}
	if pending.ExpiresAt.IsZero() {
		t.Fatal("pending.expiresAt must carry the GoPanel deadline")
	}
	raw, _ := json.Marshal(m.Status())
	if strings.Contains(string(raw), testToken) || strings.Contains(string(raw), "poll-secret") {
		t.Fatal("status must not expose the tunnel token or poll token")
	}

	fc.setPairState(PendingApproved)
	var conn *websocket.Conn
	select {
	case conn = <-fc.conns:
	case <-time.After(5 * time.Second):
		t.Fatal("approved request did not connect the tunnel")
	}
	t.Cleanup(func() { _ = conn.Close() })
	if sync := readFrame(t, conn); sync.Type != frameServiceSync {
		t.Fatalf("first frame type = %d, want service sync", sync.Type)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %v, want 0600", info.Mode().Perm())
	}
	if !waitFor(t, 5*time.Second, func() bool { return m.Status().State == StateOnline }) {
		t.Fatalf("state = %s, want online", m.Status().State)
	}
	if m.Status().Pending != nil {
		t.Fatal("pending must clear once the request is approved and connected")
	}
}

func TestRequestPairDeniedAndExpiredStopWithoutCredential(t *testing.T) {
	for _, state := range []string{PendingDenied, PendingExpired} {
		fc := newFakeController(t)
		m, configPath := oneClickManager(t, http.NotFoundHandler())
		if err := m.RequestPair(t.Context(), fc.server.URL, ""); err != nil {
			t.Fatalf("request pair: %v", err)
		}
		fc.setPairState(state)
		if !waitFor(t, 2*time.Second, func() bool {
			p := m.Status().Pending
			return p != nil && p.State == state
		}) {
			t.Fatalf("pending = %+v, want %s", m.Status().Pending, state)
		}
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			t.Fatalf("credential must not be written on %s: %v", state, err)
		}
	}
}

func TestRequestPairTimesOutWhenNeverApproved(t *testing.T) {
	fc := newFakeController(t)
	configPath := filepath.Join(t.TempDir(), "tunnel.json")
	m := NewManager(Options{
		Handler: http.NotFoundHandler(), Hostname: func() string { return "mac.local" },
		PollInterval: 10 * time.Millisecond, PollTimeout: 80 * time.Millisecond,
	})
	m.Configure(NewStore(configPath), 5410)
	t.Cleanup(m.Stop)
	if err := m.RequestPair(t.Context(), fc.server.URL, ""); err != nil {
		t.Fatalf("request pair: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		p := m.Status().Pending
		return p != nil && p.State == PendingExpired
	}) {
		t.Fatalf("pending = %+v, want expired after the overall timeout", m.Status().Pending)
	}
	if fc.polls() == 0 {
		t.Fatal("expected at least one poll before the timeout")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("credential must not be written on timeout: %v", err)
	}
}

func TestRequestPairRejectsControllerAndNetworkErrors(t *testing.T) {
	fc := newFakeController(t)
	fc.rejectRequests()
	m, configPath := oneClickManager(t, http.NotFoundHandler())
	if err := m.RequestPair(t.Context(), fc.server.URL, ""); err == nil || !strings.Contains(err.Error(), "pairing is disabled") {
		t.Fatalf("err = %v, want the GoPanel rejection message", err)
	}
	if m.Status().Pending != nil {
		t.Fatalf("pending = %+v, want nil after a rejected request", m.Status().Pending)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	deadURL := server.URL
	server.Close()
	if err := m.RequestPair(t.Context(), deadURL, ""); err == nil {
		t.Fatal("expected a network error when GoPanel is unreachable")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("credential must not be written on error: %v", err)
	}
}

func TestRequestPairCancelUnpairStopAndPairStopPolling(t *testing.T) {
	fc := newFakeController(t)
	m, configPath := oneClickManager(t, http.NotFoundHandler())
	if err := m.RequestPair(t.Context(), fc.server.URL, ""); err != nil {
		t.Fatalf("request pair: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool { return fc.polls() >= 1 }) {
		t.Fatal("expected the poll loop to run")
	}
	m.CancelPair()
	if m.Status().Pending != nil {
		t.Fatal("pending must clear when canceled")
	}
	before := fc.polls()
	time.Sleep(80 * time.Millisecond)
	if after := fc.polls(); after != before {
		t.Fatalf("polling continued after cancel: %d -> %d", before, after)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("credential must not be written after cancel: %v", err)
	}

	if err := m.RequestPair(t.Context(), fc.server.URL, ""); err != nil {
		t.Fatalf("request pair: %v", err)
	}
	if err := m.Unpair(); err != nil || m.Status().Pending != nil {
		t.Fatalf("Unpair() must clear pending: err=%v pending=%+v", err, m.Status().Pending)
	}

	if err := m.RequestPair(t.Context(), fc.server.URL, ""); err != nil {
		t.Fatalf("request pair: %v", err)
	}
	m.Stop()
	if m.Status().Pending != nil {
		t.Fatal("Stop() must cancel and clear pending one-click pairing")
	}

	if err := m.RequestPair(t.Context(), fc.server.URL, ""); err != nil {
		t.Fatalf("request pair: %v", err)
	}
	if err := m.Pair(t.Context(), fc.server.URL, strings.Repeat("ab", 16), "Studio"); err != nil {
		t.Fatalf("manual pair: %v", err)
	}
	conn := <-fc.conns
	t.Cleanup(func() { _ = conn.Close() })
	if m.Status().Pending != nil {
		t.Fatal("manual Pair() must cancel any pending one-click pairing")
	}
}

func TestRequestPairRecordsExchangeFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tunnel/pair/request", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{
			"requestId": "req_bad", "userCode": "ABCD-EFGH",
			"expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
			"pollToken": "poll-secret",
		}})
	})
	mux.HandleFunc("/api/tunnel/pair/request/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{
			"state": PendingApproved, "code": oneClickCode,
		}})
	})
	mux.HandleFunc("/api/tunnel/pair/exchange", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "code already consumed"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m, _ := oneClickManager(t, http.NotFoundHandler())
	if err := m.RequestPair(t.Context(), srv.URL, "Studio"); err != nil {
		t.Fatalf("request pair: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool { return m.Status().Pending == nil }) {
		t.Fatal("expected pending request to clear after exchange failure")
	}
	if msg := m.Status().Message; !strings.Contains(msg, "code already consumed") {
		t.Fatalf("Status().Message = %q, want exchange error", msg)
	}
}

func TestRequestPairWithoutStorePathIsRefused(t *testing.T) {
	fc := newFakeController(t)
	m := NewManager(Options{Handler: http.NotFoundHandler()})
	if err := m.RequestPair(t.Context(), fc.server.URL, ""); err == nil {
		t.Fatal("request must fail when no credential path is configured")
	}
	if fc.requests() != 0 {
		t.Fatal("no pending request may be opened without a credential store")
	}
}

func TestRequestPairDoesNotFollowRedirects(t *testing.T) {
	redirectTargetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetHit = true
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{}})
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/tunnel/pair/request", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	m, _ := oneClickManager(t, http.NotFoundHandler())
	if err := m.RequestPair(t.Context(), redirector.URL, "Studio"); err == nil {
		t.Fatal("expected the request to reject a redirect response")
	}
	if redirectTargetHit {
		t.Fatal("RequestPair followed an HTTP redirect and leaked the request")
	}
}
