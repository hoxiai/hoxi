package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	s := &Service{
		store:         &TokenStore{filePath: filepath.Join(t.TempDir(), "auth.json")},
		serverBaseURL: server.URL,
		httpClient:    server.Client(),
	}
	t.Cleanup(func() {
		s.mu.Lock()
		s.cancelPendingLocked()
		s.mu.Unlock()
	})
	return s
}

func startTestAuth(t *testing.T, s *Service) string {
	t.Helper()
	res, err := s.StartAuthorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res.State
}

func authorizedResponse(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true, "status": "authorized", "deviceToken": "new-token",
		"user": UserProfile{ID: 1, Email: "test@example.com"},
	})
}

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for worker")
		var zero T
		return zero
	}
}

func assertStoredToken(t *testing.T, s *Service, want string) {
	t.Helper()
	if got := s.CurrentDeviceToken(); got != want {
		t.Fatalf("current token = %q, want %q", got, want)
	}
	stored, err := s.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	if stored != nil {
		got = stored.DeviceToken
	}
	if got != want {
		t.Fatalf("persisted token = %q, want %q", got, want)
	}
}

func TestExchangeRequiresLivePendingState(t *testing.T) {
	var requests atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		authorizedResponse(w)
	})
	if _, err := s.ExchangeToken(context.Background(), "handoff", "unsolicited"); err == nil {
		t.Fatal("accepted exchange without pending authorization")
	}
	state := startTestAuth(t, s)
	if _, err := s.ExchangeToken(context.Background(), "handoff", "mismatch"); err == nil {
		t.Fatal("accepted mismatched state")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid state sent an outbound request")
	}
	if _, err := s.ExchangeToken(context.Background(), "handoff", state); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExchangeToken(context.Background(), "handoff", state); err == nil {
		t.Fatal("accepted replay")
	}
	if requests.Load() != 1 {
		t.Fatal("replay sent an outbound request")
	}
	assertStoredToken(t, s, "new-token")
}

func TestHandleCallback(t *testing.T) {
	var requests atomic.Int32
	var s *Service
	s = testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/hoxi/device-tokens/authorize" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body ExchangeTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.HandoffToken != "handoff+with&encoding" || body.State != s.Snapshot().PendingState {
			t.Error("callback credentials were not forwarded correctly")
		}
		authorizedResponse(w)
	})
	state := startTestAuth(t, s)
	query := "?handoff=" + url.QueryEscape("handoff+with&encoding") + "&state=" + state
	for _, rawURL := range []string{
		"https://auth-callback" + query,
		"hoxi://other" + query,
		"hoxi://user@auth-callback" + query,
		"hoxi://auth-callback:80" + query,
		"hoxi://auth-callback/path" + query,
		"hoxi://auth-callback" + query + "#fragment",
		"hoxi://auth-callback" + query + "&state=duplicate",
		"hoxi://auth-callback" + query + "&handoff=duplicate",
		"hoxi://auth-callback" + query + "&invalid=%zz",
		"hoxi://auth-callback?handoff=&state=" + state,
		"hoxi://auth-callback?handoff=handoff",
		"hoxi://auth-callback?handoff=handoff&state=mismatch",
	} {
		if err := s.HandleCallback(context.Background(), rawURL); err == nil {
			t.Error("accepted invalid callback")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid callback sent an outbound request")
	}
	if err := s.HandleCallback(context.Background(), "hoxi://auth-callback"+query); err != nil {
		t.Fatal(err)
	}
	assertStoredToken(t, s, "new-token")
	if s.Snapshot().IsAuthorizing {
		t.Fatal("callback did not complete pending authorization")
	}
	if err := s.HandleCallback(context.Background(), "hoxi://auth-callback"+query); err == nil {
		t.Fatal("accepted callback replay")
	}
	if requests.Load() != 1 {
		t.Fatal("callback replay sent an outbound request")
	}
}

func TestExchangeExpiredBeforeRequest(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("expired exchange sent an outbound request")
	})
	state := startTestAuth(t, s)
	s.mu.Lock()
	s.pending.expiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if _, err := s.ExchangeToken(context.Background(), "handoff", state); err == nil {
		t.Fatal("accepted expired state")
	}
	if s.Snapshot().IsAuthorizing {
		t.Fatal("expired state reported as authorizing")
	}
	assertStoredToken(t, s, "")
}

// Ignore cancellation at the transport boundary to exercise the post-response guards too.
func ignoreRequestCancellation(s *Service) {
	transport := s.httpClient.Transport
	s.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return transport.RoundTrip(r.Clone(context.Background()))
	})
}

func TestLateExchangeCannotApply(t *testing.T) {
	for _, action := range []string{"logout", "restart", "server change", "expiry"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			s := testService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					return
				}
				close(entered)
				<-release
				authorizedResponse(w)
			})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			ignoreRequestCancellation(s)
			state := startTestAuth(t, s)
			done := make(chan error, 1)
			go func() {
				_, err := s.ExchangeToken(context.Background(), "handoff", state)
				done <- err
			}()
			await(t, entered)
			nextState := ""
			switch action {
			case "logout":
				if err := s.Logout(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "restart":
				nextState = startTestAuth(t, s)
			case "server change":
				original := s.ServerBaseURL()
				s.SetServerBaseURL("https://other.example")
				s.SetServerBaseURL(original)
			case "expiry":
				s.mu.Lock()
				s.pending.expiresAt = time.Now().Add(-time.Second)
				s.mu.Unlock()
			}
			unblock()
			if err := await(t, done); err == nil {
				t.Fatal("late exchange succeeded")
			}
			assertStoredToken(t, s, "")
			if s.Snapshot().PendingState != nextState {
				t.Fatal("late exchange changed pending authorization")
			}
		})
	}
}

func TestExchangeCancellation(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(canceled)
	})
	state := startTestAuth(t, s)
	done := make(chan error, 1)
	go func() {
		_, err := s.ExchangeToken(context.Background(), "handoff", state)
		done <- err
	}()
	await(t, entered)
	if err := s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := await(t, done); err == nil {
		t.Fatal("canceled exchange succeeded")
	}
	await(t, canceled)
}

func TestExchangePersistenceFailure(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { authorizedResponse(w) })
	// A regular file cannot be used as the credential directory.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.store.filePath = filepath.Join(blocker, "auth.json")
	state := startTestAuth(t, s)
	if _, err := s.ExchangeToken(context.Background(), "handoff", state); err == nil {
		t.Fatal("ignored persistence failure")
	}
	if s.Snapshot().IsLoggedIn {
		t.Fatal("published credentials that were not saved")
	}
}

func TestSaveAndLogoutRemainOrdered(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {})
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		attempt := &authorization{state: "pending", baseURL: s.ServerBaseURL(), expiresAt: time.Now().Add(3 * time.Minute), ctx: ctx, cancel: cancel}
		s.mu.Lock()
		s.pending = attempt
		s.mu.Unlock()
		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			<-start
			_ = s.applyAuthorized(attempt, &StoredAuth{DeviceToken: "token"})
			close(done)
		}()
		close(start)
		if err := s.Logout(context.Background()); err != nil {
			t.Fatal(err)
		}
		await(t, done)
		assertStoredToken(t, s, "")
	}
}

func TestStaleVerificationCannotChangeAuth(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			s := testService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/hoxi/device-tokens/verify" {
					authorizedResponse(w)
					return
				}
				close(entered)
				<-release
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"valid":true,"user":{"email":"stale@example.com"}}`))
			})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			old := &StoredAuth{DeviceToken: "old-token"}
			s.current = old
			done := make(chan struct{})
			go func() {
				s.verifyBackground(old, s.ServerBaseURL(), 0)
				close(done)
			}()
			await(t, entered)
			state := startTestAuth(t, s)
			if _, err := s.ExchangeToken(context.Background(), "handoff", state); err != nil {
				t.Fatal(err)
			}
			unblock()
			await(t, done)
			assertStoredToken(t, s, "new-token")
			if s.Snapshot().User.Email != "test@example.com" {
				t.Fatal("stale verification changed user")
			}
		})
	}
}
