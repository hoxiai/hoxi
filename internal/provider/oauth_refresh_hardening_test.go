package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestOAuthRefreshSerializedUnderConcurrencyPreventsDuplicateRotation(t *testing.T) {
	homeDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("HOXI_HOME", homeDir)

	var refreshCalls atomic.Int32
	var usedV1 atomic.Bool

	SetProviderOAuthTransportForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rawBody, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(rawBody, &req)
		refreshCalls.Add(1)
		// Simulate network latency so concurrent callers pile up on the lock.
		time.Sleep(40 * time.Millisecond)
		if req["refresh_token"] != "ref_single_use_v1" || !usedV1.CompareAndSwap(false, true) {
			return jsonHTTPResponse(http.StatusUnauthorized, `{"error":"invalid_grant","error_description":"refresh token already rotated"}`), nil
		}
		return jsonHTTPResponse(http.StatusOK, `{"access_token":"tok_rotated_v2","refresh_token":"ref_single_use_v2","expires_in":3600}`), nil
	}))
	defer SetProviderOAuthTransportForTest(nil)

	svc := NewService(0, "", wsDir, nil)
	if err := svc.persistEngineProviderAuthMap("acme-concurrent", map[string]any{
		"type":     "oauth",
		"access":   "tok_expired_v1",
		"refresh":  "ref_single_use_v1",
		"expires":  time.Now().Add(-1 * time.Minute).UnixMilli(),
		"tokenURL": "https://oauth.acme.test/token",
		"clientID": "acme-cli",
	}); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	results := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			tok, revoked, err := svc.resolveAuthFileCredential(context.Background(), "acme-concurrent")
			if revoked {
				errs[idx] = errors.New("unexpectedly revoked")
				return
			}
			results[idx] = tok
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 serialized OAuth refresh call, got %d", got)
	}
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d failed: %v", i, errs[i])
		}
		if results[i] != "tok_rotated_v2" {
			t.Fatalf("worker %d got %q, want tok_rotated_v2", i, results[i])
		}
	}
}

func TestOAuthUncertainRotationBlocksReplayWhileKeepingValidAccessToken(t *testing.T) {
	homeDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("HOXI_HOME", homeDir)

	var refreshCalls atomic.Int32
	SetProviderOAuthTransportForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		refreshCalls.Add(1)
		return nil, errors.New("unexpected EOF after request write")
	}))
	defer SetProviderOAuthTransportForTest(nil)

	svc := NewService(0, "", wsDir, nil)
	nowMs := time.Now().UnixMilli()
	// Token issued 30m ago with 1h total lifetime -> past 1/4 (15m) proactive due,
	// but still has 30m of valid access lifetime remaining.
	if err := svc.persistEngineProviderAuthMap("acme-uncertain", map[string]any{
		"type":     "oauth",
		"access":   "tok_still_valid_v1",
		"refresh":  "ref_uncertain_v1",
		"issuedAt": nowMs - 30*60*1000,
		"expires":  nowMs + 30*60*1000,
		"tokenURL": "https://oauth.acme.test/token",
		"clientID": "acme-cli",
	}); err != nil {
		t.Fatal(err)
	}

	// First call attempts proactive refresh, hits transport EOF -> falls back to valid access token.
	tok1, revoked1, err1 := svc.resolveAuthFileCredential(context.Background(), "acme-uncertain")
	if err1 != nil || revoked1 || tok1 != "tok_still_valid_v1" {
		t.Fatalf("first call: tok=%q revoked=%v err=%v", tok1, revoked1, err1)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected 1 refresh attempt, got %d", refreshCalls.Load())
	}

	// Second call must NOT replay the uncertain rotation and still return the valid access token.
	tok2, revoked2, err2 := svc.resolveAuthFileCredential(context.Background(), "acme-uncertain")
	if err2 != nil || revoked2 || tok2 != "tok_still_valid_v1" {
		t.Fatalf("second call: tok=%q revoked=%v err=%v", tok2, revoked2, err2)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected uncertain rotation replay to be blocked (still 1 call), got %d", refreshCalls.Load())
	}

	// Once the access token actually expires, it must fail cleanly without replaying the uncertain refresh token.
	if err := svc.persistEngineProviderAuthMap("acme-uncertain", map[string]any{
		"type":     "oauth",
		"access":   "tok_still_valid_v1",
		"refresh":  "ref_uncertain_v1",
		"issuedAt": nowMs - 90*60*1000,
		"expires":  nowMs - 1000,
		"tokenURL": "https://oauth.acme.test/token",
		"clientID": "acme-cli",
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err3 := svc.resolveAuthFileCredential(context.Background(), "acme-uncertain")
	if err3 == nil {
		t.Fatal("expected error after access token expiry with uncertain rotation")
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected no replay after expiry for same uncertain refresh token, got %d calls", refreshCalls.Load())
	}
}

func TestOAuthQuarterLifetimeProactiveRefreshAndExpiryFloor(t *testing.T) {
	homeDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("HOXI_HOME", homeDir)

	var refreshCalls atomic.Int32
	SetProviderOAuthTransportForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		refreshCalls.Add(1)
		// Upstream returns a shorter expires_in (300s) than the already recorded expiry (45m remaining).
		return jsonHTTPResponse(http.StatusOK, `{"access_token":"tok_proactive_v2","refresh_token":"ref_v2","expires_in":300}`), nil
	}))
	defer SetProviderOAuthTransportForTest(nil)

	svc := NewService(0, "", wsDir, nil)
	nowMs := time.Now().UnixMilli()
	recordedExp := nowMs + 45*60*1000
	// Issued 20m ago, expires in 45m (total 65m -> 1/4 = 16.25m -> already due!).
	if err := svc.persistEngineProviderAuthMap("acme-quarter", map[string]any{
		"type":     "oauth",
		"access":   "tok_v1",
		"refresh":  "ref_v1",
		"issuedAt": nowMs - 20*60*1000,
		"expires":  recordedExp,
		"tokenURL": "https://oauth.acme.test/token",
		"clientID": "acme-cli",
	}); err != nil {
		t.Fatal(err)
	}

	tok, revoked, err := svc.resolveAuthFileCredential(context.Background(), "acme-quarter")
	if err != nil || revoked || tok != "tok_proactive_v2" {
		t.Fatalf("expected proactive refresh to tok_proactive_v2, got tok=%q revoked=%v err=%v", tok, revoked, err)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected 1 proactive refresh call, got %d", refreshCalls.Load())
	}

	saved, ok := readProviderAuthEntry(hoxipaths.EngineAuthPath(), "acme-quarter")
	if !ok {
		t.Fatal("expected refreshed auth entry persisted")
	}
	if saved.Expires < recordedExp {
		t.Fatalf("renewal shortened recorded expiry: got %d, want >= %d", saved.Expires, recordedExp)
	}
	if saved.IssuedAt <= 0 {
		t.Fatalf("expected issuedAt recorded on refresh, got %d", saved.IssuedAt)
	}
}

func TestOpenStreamSessionAffinityHeadersAndCustomOverride(t *testing.T) {
	homeDir := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("HOXI_HOME", homeDir)

	var gotMu sync.Mutex
	var lastHeaders http.Header
	SetStreamTransportForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotMu.Lock()
		lastHeaders = r.Header.Clone()
		gotMu.Unlock()
		h := make(http.Header)
		h.Set("Content-Type", "text/event-stream")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     h,
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
		}, nil
	}))
	defer SetStreamTransportForTest(nil)

	svc := NewService(0, "", wsDir, nil)
	if err := svc.PersistHoxiConfigDoc(map[string]any{
		"provider": map[string]any{
			"relay-default": map[string]any{
				"name": "Relay Default",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": "https://relay.example.test/v1",
					"apiKey":  "sk-test-default",
				},
			},
			"relay-custom": map[string]any{
				"name": "Relay Custom",
				"npm":  "@ai-sdk/openai-compatible",
				"options": map[string]any{
					"baseURL": "https://relay.example.test/v1",
					"apiKey":  "sk-test-custom",
					"headers": map[string]any{
						"X-Session-Affinity": "custom-affinity-override",
						"x-session-id":       "custom-session-override",
					},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// 1. With SessionID -> both x-session-affinity and X-Session-Id are sent.
	resp, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target:    svc.ResolveStreamTarget("relay-default", "gpt-5"),
		Body:      []byte(`{"model":"gpt-5","messages":[]}`),
		SessionID: "ses_affinity_123",
	})
	if err != nil {
		t.Fatalf("OpenStream with session failed: %v", err)
	}
	_ = resp.Body.Close()
	gotMu.Lock()
	if got := lastHeaders.Get("x-session-affinity"); got != "ses_affinity_123" {
		t.Fatalf("x-session-affinity = %q, want ses_affinity_123", got)
	}
	if got := lastHeaders.Get("X-Session-Id"); got != "ses_affinity_123" {
		t.Fatalf("X-Session-Id = %q, want ses_affinity_123", got)
	}
	gotMu.Unlock()

	// 2. Without SessionID -> neither header is sent.
	resp2, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: svc.ResolveStreamTarget("relay-default", "gpt-5"),
		Body:   []byte(`{"model":"gpt-5","messages":[]}`),
	})
	if err != nil {
		t.Fatalf("OpenStream without session failed: %v", err)
	}
	_ = resp2.Body.Close()
	gotMu.Lock()
	if got := lastHeaders.Get("x-session-affinity"); got != "" {
		t.Fatalf("expected empty x-session-affinity when SessionID omitted, got %q", got)
	}
	if got := lastHeaders.Get("X-Session-Id"); got != "" {
		t.Fatalf("expected empty X-Session-Id when SessionID omitted, got %q", got)
	}
	gotMu.Unlock()

	// 3. User-configured custom headers override defaults case-insensitively.
	resp3, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target:    svc.ResolveStreamTarget("relay-custom", "gpt-5"),
		Body:      []byte(`{"model":"gpt-5","messages":[]}`),
		SessionID: "ses_affinity_123",
	})
	if err != nil {
		t.Fatalf("OpenStream with custom headers failed: %v", err)
	}
	_ = resp3.Body.Close()
	gotMu.Lock()
	if got := lastHeaders.Get("x-session-affinity"); got != "custom-affinity-override" {
		t.Fatalf("custom X-Session-Affinity override failed: got %q", got)
	}
	if got := lastHeaders.Get("X-Session-Id"); got != "custom-session-override" {
		t.Fatalf("custom x-session-id override failed: got %q", got)
	}
	gotMu.Unlock()
}
