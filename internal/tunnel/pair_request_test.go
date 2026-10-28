package tunnel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
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
