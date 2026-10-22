package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClassifyCandidateFailure(t *testing.T) {
	cases := []struct {
		name   string
		status int
		err    error
		body   []byte
		want   breakerClass
	}{
		{"timeout error", 0, context.DeadlineExceeded, nil, breakerClassTransient},
		{"canceled ignored", 0, context.Canceled, nil, breakerClassNone},
		{"bad request is caller fault", 400, nil, nil, breakerClassBadReq},
		{"validation error", 422, nil, nil, breakerClassBadReq},
		{"unauthorized is config", 401, nil, nil, breakerClassConfig},
		{"model not found is config", 404, nil, nil, breakerClassConfig},
		{"server error is transient", 503, nil, nil, breakerClassTransient},
		{"plain rate limit", 429, nil, []byte(`{"error":{"type":"rate_limit_error"}}`), breakerClassRateLimit},
		{"quota on 429", 429, nil, []byte(`{"error":{"code":"insufficient_quota"}}`), breakerClassQuota},
		{"quota text on 403", 403, nil, []byte(`Your credit balance is too low`), breakerClassQuota},
		{"auth 403 is config", 403, nil, []byte(`forbidden`), breakerClassConfig},
		{"model missing in body", 200, nil, []byte(`{"error":{"message":"The model gpt-9 does not exist"}}`), breakerClassBadReq},
		{"unknown model text", 0, errors.New("unknown model gpt-9; call GET /v1/models"), nil, breakerClassConfig},
	}
	for _, tc := range cases {
		got := classifyCandidateFailure(tc.status, tc.err, tc.body)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBreakerOpensAfterTransientThresholdAndRecovers(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	store := newBreakerStore(dir)
	now := time.Now()
	store.nowFn = func() time.Time { return now }

	if allowed, _ := store.Evaluate("p", "m"); !allowed {
		t.Fatal("a fresh candidate must be allowed")
	}
	store.RecordFailure("p", "m", breakerClassTransient, 503, 0, "boom")
	if allowed, _ := store.Evaluate("p", "m"); !allowed {
		t.Fatal("a single transient failure must not open the circuit")
	}
	store.RecordFailure("p", "m", breakerClassTransient, 503, 0, "boom again")
	if allowed, _ := store.Evaluate("p", "m"); allowed {
		t.Fatal("two transient failures must open the circuit")
	}
	if store.cooldownRemainingMs("p", "m") <= 0 {
		t.Fatal("an open circuit must report a positive cooldown")
	}

	now = now.Add(4 * time.Minute)
	if allowed, _ := store.Evaluate("p", "m"); !allowed {
		t.Fatal("an expired cooldown must be retryable")
	}
	store.RecordSuccess("p", "m", 120)
	if allowed, score := store.Evaluate("p", "m"); !allowed || score <= 0 {
		t.Fatalf("success must close the circuit, got allowed=%v score=%v", allowed, score)
	}
	if store.cooldownRemainingMs("p", "m") != 0 {
		t.Fatal("a closed circuit has no cooldown")
	}
}

func TestBreakerRateLimitAndBadRequestHandling(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	store := newBreakerStore(dir)
	now := time.Now()
	store.nowFn = func() time.Time { return now }

	store.RecordFailure("p", "m", breakerClassRateLimit, 429, 0, "429")
	if allowed, _ := store.Evaluate("p", "m"); allowed {
		t.Fatal("a rate limit must open immediately")
	}
	store.RecordSuccess("p", "m", 50)

	store.RecordFailure("q", "m", breakerClassBadReq, 400, 0, "bad payload")
	if allowed, _ := store.Evaluate("q", "m"); !allowed {
		t.Fatal("a caller's bad request must not penalize the model")
	}
	if store.items[breakerKey("q", "m")] != nil {
		t.Fatal("a bad request must not even create breaker state")
	}
}

func TestBreakerScorePrefersFastModel(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	store := newBreakerStore(dir)
	for i := 0; i < 5; i++ {
		store.RecordSuccess("fast", "m", 400)
		store.RecordSuccess("slow", "m", 12000)
	}
	fast := store.Score("fast", "m")
	slow := store.Score("slow", "m")
	if fast <= slow {
		t.Fatalf("expected fast model to outrank slow: fast=%v slow=%v", fast, slow)
	}
	if untested := store.Score("unknown", "m"); untested <= 0 || untested >= fast {
		t.Fatalf("untested candidate should be positive but below a proven fast one, got %v", untested)
	}
}

func TestSelectAutoCandidateSkipsOpenAndPrefersFast(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	svc := NewService(0, "password", dir, nil)
	savePool(t, svc, "frontier", "smart-health", []AutoPoolCandidate{
		{ProviderID: "alpha", ModelID: "m"},
		{ProviderID: "beta", ModelID: "m"},
	})

	for i := 0; i < 4; i++ {
		svc.breaker.RecordSuccess("beta", "m", 350)
	}
	pid, mid := svc.selectAutoCandidate("frontier", nil)
	if pid != "beta" || mid != "m" {
		t.Fatalf("smart-health should prefer the fast, reliable candidate, got %s/%s", pid, mid)
	}

	svc.breaker.RecordFailure("beta", "m", breakerClassRateLimit, 429, 0, "429")
	pid, _ = svc.selectAutoCandidate("frontier", nil)
	if pid != "alpha" {
		t.Fatalf("an open candidate must be skipped, got %s", pid)
	}
}

func TestSelectAutoCandidateAllOpenFallback(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	svc := NewService(0, "password", dir, nil)
	savePool(t, svc, "free", "round-robin", []AutoPoolCandidate{
		{ProviderID: "alpha", ModelID: "m"},
		{ProviderID: "beta", ModelID: "m"},
	})
	svc.breaker.RecordFailure("alpha", "m", breakerClassTransient, 503, 0, "x")
	svc.breaker.RecordFailure("alpha", "m", breakerClassTransient, 503, 0, "x")
	svc.breaker.RecordFailure("beta", "m", breakerClassTransient, 503, 0, "x")
	svc.breaker.RecordFailure("beta", "m", breakerClassTransient, 503, 0, "x")

	pid, mid := svc.selectAutoCandidate("free", nil)
	if pid == AutoProviderID || mid == "" {
		t.Fatalf("when every candidate is cooling down the pool must still degrade, got %s/%s", pid, mid)
	}
}

func TestSelectAutoCandidateHonorsExclude(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	svc := NewService(0, "password", dir, nil)
	savePool(t, svc, "free", "round-robin", []AutoPoolCandidate{
		{ProviderID: "alpha", ModelID: "m"},
		{ProviderID: "beta", ModelID: "m"},
	})
	pid, _ := svc.selectAutoCandidate("free", map[string]bool{"alpha/m": true})
	if pid != "beta" {
		t.Fatalf("excluded candidate must not be picked, got %s", pid)
	}
}

func TestCandidateOutcomeEndpointAndClear(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	svc := NewService(0, "password", dir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	body, _ := json.Marshal(candidateOutcomeRequest{ProviderID: "openrouter", ModelID: "m", OK: false, StatusCode: 429, Message: "429"})
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/candidate-outcome", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST outcome status = %d: %s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/provider/candidates", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	var resp struct {
		OK       bool                       `json:"ok"`
		Breakers map[string]BreakerSnapshot `json:"breakers"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil || !resp.OK {
		t.Fatalf("GET candidates invalid: %v %s", err, getRec.Body.String())
	}
	snap, ok := resp.Breakers["openrouter/m"]
	if !ok || !snap.Open {
		t.Fatalf("expected openrouter/m to be open, got %+v", resp.Breakers)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/hoxi/provider/candidates?providerID=openrouter&modelID=m", nil)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	var delResp struct {
		OK       bool                       `json:"ok"`
		Breakers map[string]BreakerSnapshot `json:"breakers"`
	}
	if err := json.Unmarshal(delRec.Body.Bytes(), &delResp); err != nil {
		t.Fatalf("DELETE candidates invalid: %v", err)
	}
	if _, ok := delResp.Breakers["openrouter/m"]; ok {
		t.Fatal("cleared candidate must be gone from the snapshot")
	}
}

func TestBreakerPersistsAcrossReload(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	store := newBreakerStore(dir)
	store.RecordFailure("p", "m", breakerClassConfig, 401, 0, "bad key")

	reloaded := newBreakerStore(dir)
	allowed, _ := reloaded.Evaluate("p", "m")
	if allowed {
		t.Fatal("a config-open circuit must survive a reload")
	}
	snap, ok := reloaded.Snapshot()["p/m"]
	if !ok || snap.LastClass != string(breakerClassConfig) {
		t.Fatalf("expected persisted config class, got %+v", snap)
	}
}

// savePool writes a customized auto pool directly to the service store.
func savePool(t *testing.T, svc *Service, poolID, strategy string, candidates []AutoPoolCandidate) {
	t.Helper()
	rawCands := make([]map[string]string, 0, len(candidates))
	for _, c := range candidates {
		rawCands = append(rawCands, map[string]string{"providerID": c.ProviderID, "modelID": c.ModelID})
	}
	payload := map[string]interface{}{
		"poolID": poolID,
		"pool": map[string]interface{}{
			"id":         poolID,
			"strategy":   strategy,
			"customized": true,
			"candidates": rawCands,
		},
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/provider/auto-pools", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	svc.handleAutoPoolsConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("savePool %s status = %d: %s", poolID, rec.Code, rec.Body.String())
	}
}
