package appbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

func bridgeFixture(t *testing.T) *Service {
	t.Helper()
	t.Setenv("HOXI_HOME", t.TempDir())
	return NewService()
}

func TestScopesAndRedirectURI(t *testing.T) {
	if len(AllScopes()) != 17 {
		t.Fatal("unexpected scope count")
	}
	for _, scope := range []string{"*", "llm:*", "host:*", "auth:admin"} {
		if _, err := NormalizeScopes([]string{scope}); err == nil {
			t.Errorf("accepted scope %q", scope)
		}
	}
	for _, uri := range []string{"http://example.com:3211/callback", "https://example.com/callback", "javascript:alert(1)", "file:///tmp/data", "hoxi://settings", "http://localhost:3211/callback", "http://127.0.0.1/callback", "http://127.0.0.1:65536/callback", "http://user@127.0.0.1:3211/callback", "com.example.app://callback#fragment", "mailto:someone@example.com"} {
		if ValidateRedirectURI(uri) == nil {
			t.Errorf("accepted redirect %q", uri)
		}
	}
	for _, uri := range []string{"http://127.0.0.1:3211/callback", "http://[::1]:3211/callback", "com.example.app://callback"} {
		if err := ValidateRedirectURI(uri); err != nil {
			t.Errorf("rejected redirect %q: %v", uri, err)
		}
	}
}

func connectInput(mode string) ConnectInput {
	challenge := sha256.Sum256([]byte(verifier))
	return ConnectInput{AppID: "demo.app", Name: "Demo", Scopes: []string{ScopeLLMModels}, Mode: mode, CodeChallenge: base64.RawURLEncoding.EncodeToString(challenge[:]), CodeChallengeMethod: "S256", State: "client-state"}
}

func TestPKCERedirectAndPoll(t *testing.T) {
	s := bridgeFixture(t)
	in := connectInput(ConnectModeRedirect)
	in.RedirectURI = "http://127.0.0.1:3211/callback?keep=yes"
	req, err := s.BeginConnect(in)
	if err != nil {
		t.Fatal(err)
	}
	req.Scopes[0] = ScopeComputerControl // snapshots cannot expand queued consent
	if _, err := s.DecideConnect(req.RequestID, true, req.Scopes, nil, ""); err == nil {
		t.Fatal("scope expansion accepted")
	}
	dec, err := s.DecideConnect(req.RequestID, true, nil, nil, "http://127.0.0.1:5410")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dec.RedirectTarget)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") != in.State || q.Get("keep") != "yes" || q.Get("baseURL") != "http://127.0.0.1:5410" {
		t.Fatalf("invalid callback: %v", q)
	}
	for _, pair := range [][2]string{{strings.Repeat("a", 43), in.RedirectURI}, {verifier, "http://127.0.0.1:3211/other"}} {
		if _, _, err := s.ExchangeAuthCode(q.Get("code"), pair[0], pair[1]); err == nil {
			t.Fatal("bad proof accepted")
		}
	}
	g, token, err := s.ExchangeAuthCode(q.Get("code"), verifier, in.RedirectURI)
	if err != nil || !strings.HasPrefix(token, TokenPrefix) || !HasScope(g.Scopes, ScopeLLMModels) {
		t.Fatalf("exchange: %v", err)
	}
	if _, _, err := s.ExchangeAuthCode(q.Get("code"), verifier, in.RedirectURI); err == nil {
		t.Fatal("replayed code")
	}

	for _, approved := range []bool{true, false} {
		req, err := s.BeginConnect(connectInput(ConnectModePoll))
		if err != nil {
			t.Fatal(err)
		}
		if p, err := s.PollToken(req.RequestID, verifier); err != nil || p.Status != "pending" || p.Token != "" {
			t.Fatal("not pending")
		}
		if _, err := s.DecideConnect(req.RequestID, approved, nil, nil, ""); err != nil {
			t.Fatal(err)
		}
		p, err := s.PollToken(req.RequestID, verifier)
		if err != nil || (approved && (p.Status != "approved" || p.Token == "")) || (!approved && (p.Status != "denied" || p.Token != "")) {
			t.Fatalf("unexpected poll state %q: %v", p.Status, err)
		}
		if p, _ := s.PollToken(req.RequestID, verifier); p.Status != "expired" || p.Token != "" {
			t.Fatal("replayed poll")
		}
	}
}

func TestGrantPersistenceAndRevocation(t *testing.T) {
	s := bridgeFixture(t)
	g, plain, err := s.CreateGrant("demo.app", "Demo", []string{ScopeLLMModels, ScopeNotifySend}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := hoxipaths.AppGrantsPath()
	raw, err := os.ReadFile(path)
	if err != nil || bytes.Contains(raw, []byte(plain)) || !bytes.Contains(raw, []byte(hashToken(plain))) {
		t.Fatal("grant storage must contain only the token digest")
	}
	assertPrivate(t, path)
	s2 := NewService()
	if _, err := s2.VerifyToken(plain, "other.app"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("appId mismatch: %v", err)
	}
	first, err := s2.VerifyToken(plain, g.AppID)
	if err != nil || first.LastUsedAt == 0 {
		t.Fatalf("verify: %v", err)
	}
	second, err := s.VerifyToken(plain, g.AppID)
	if err != nil || second.LastUsedAt != first.LastUsedAt {
		t.Fatal("expected throttled last-used update")
	}
	expanded := []string{ScopeComputerControl}
	if _, err := s.UpdateGrant(g.ID, GrantUpdate{Scopes: &expanded}); err == nil {
		t.Fatal("expanded existing grant")
	}
	narrow := []string{ScopeLLMModels}
	if _, err := s.UpdateGrant(g.ID, GrantUpdate{Scopes: &narrow}); err != nil {
		t.Fatal(err)
	}
	if got, err := s2.VerifyToken(plain, g.AppID); err != nil || HasScope(got.Scopes, ScopeNotifySend) {
		t.Fatalf("narrowing not visible: %v", err)
	}
	if err := s.RevokeGrant(g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.VerifyToken(plain, g.AppID); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked grant: %v", err)
	}
}

func TestCorruptStoreFailsClosedAndPreservesBackup(t *testing.T) {
	s := bridgeFixture(t)
	path := hoxipaths.AppGrantsPath()
	corrupt := []byte(`{"version":1,"grants":[invalid]}`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := s.CreateGrant("test", "", []string{ScopeLLMModels}, nil); !errors.Is(err, ErrStorage) {
			t.Fatalf("corrupt store: %v", err)
		}
	}
	files, _ := filepath.Glob(path + ".corrupt-*")
	if len(files) != 1 {
		t.Fatalf("expected one backup, got %d", len(files))
	}
	for _, p := range append(files, path) {
		raw, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(raw, corrupt) {
			t.Fatal("corrupt data was not preserved")
		}
		assertPrivate(t, p)
	}
}

func TestConcurrentGrantWriters(t *testing.T) {
	s := bridgeFixture(t)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, _, err := NewService().CreateGrant("demo.app", "", []string{ScopeLLMModels}, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if grants, err := s.ListGrants(); len(grants) != 12 || err != nil {
		t.Fatalf("lost concurrent grants: %d, %v", len(grants), err)
	}
}

func TestEphemeralTokenAndNotifyWindow(t *testing.T) {
	s := bridgeFixture(t)
	g, token, err := s.IssueEphemeralToken("demo.app", "", []string{ScopeNotifySend}, nil, time.Minute)
	if err != nil || !strings.HasPrefix(token, EphemeralTokenPrefix) {
		t.Fatalf("issue: %v", err)
	}
	if _, err := os.Stat(hoxipaths.AppGrantsPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ephemeral issue wrote to disk")
	}
	if _, err := NewService().VerifyToken(token, g.AppID); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("ephemeral token carried to a new service")
	}
	if _, err := s.VerifyToken(token, g.AppID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeEphemeralToken(g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyToken(token, g.AppID); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("ephemeral token still active")
	}
	now := time.Now()
	for i := range 10 {
		if !s.allowNotify(g.AppID, now.Add(time.Duration(i)*time.Second)) {
			t.Errorf("blocked notification %d", i)
		}
	}
	if s.allowNotify(g.AppID, now.Add(59*time.Second)) || !s.allowNotify("other.app", now.Add(59*time.Second)) {
		t.Fatal("incorrect per-app rate limit")
	}
	if !s.allowNotify(g.AppID, now.Add(time.Minute)) || s.allowNotify(g.AppID, now.Add(time.Minute)) {
		t.Fatal("incorrect sliding window")
	}
}

func TestRuntimeEndpointPIDProtection(t *testing.T) {
	bridgeFixture(t)
	if err := WriteRuntimeEndpoint(RuntimeEndpoint{BaseURL: "http://user:password@127.0.0.1:5410", PID: 1}); err == nil {
		t.Fatal("accepted secret URL")
	}
	for _, pid := range []int{201, 202} {
		if err := WriteRuntimeEndpoint(RuntimeEndpoint{BaseURL: "http://127.0.0.1:5410", PID: pid}); err != nil {
			t.Fatal(err)
		}
	}
	path := hoxipaths.RuntimeEndpointPath()
	assertPrivate(t, path)
	if err := ClearRuntimeEndpoint(201); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("stale PID cleared another endpoint", err)
	}
	if err := ClearRuntimeEndpoint(202); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owner failed to clear endpoint")
	}
}

func assertPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("non-private file mode: %v", info.Mode())
	}
}

func TestRevokingGrantCancelsPendingApproval(t *testing.T) {
	s := bridgeFixture(t)
	grant, _, err := s.CreateGrant("demo.app", "Demo", []string{ScopeApprovalRequest}, nil)
	if err != nil {
		t.Fatal(err)
	}
	request, wait, err := s.EnqueueApproval(grant, "Confirm", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeGrant(grant.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-wait:
		if ok {
			t.Fatal("revocation delivered an approval decision")
		}
	default:
		t.Fatal("revocation did not release the waiter")
	}
	if _, err := s.DecideApproval(request.ApprovalID, true); err == nil {
		t.Fatal("revoked approval was still actionable")
	}
}
