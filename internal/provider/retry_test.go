package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsTransientLLMError(t *testing.T) {
	transient := []struct {
		name   string
		err    error
		status int
	}{
		{"deadline", context.DeadlineExceeded, 0},
		{"unexpected eof", io.ErrUnexpectedEOF, 0},
		{"too many requests", nil, http.StatusTooManyRequests},
		{"server error", nil, http.StatusInternalServerError},
		{"bad gateway", nil, http.StatusBadGateway},
		{"request timeout status", nil, http.StatusRequestTimeout},
	}
	for _, tc := range transient {
		if !isTransientLLMError(tc.err, tc.status) {
			t.Errorf("%s: expected transient", tc.name)
		}
	}
	permanent := []struct {
		name   string
		err    error
		status int
	}{
		{"canceled", context.Canceled, 0},
		{"bad request", nil, http.StatusBadRequest},
		{"unauthorized", nil, http.StatusUnauthorized},
		{"not found", nil, http.StatusNotFound},
		{"success status with err", nil, http.StatusOK},
	}
	for _, tc := range permanent {
		if isTransientLLMError(tc.err, tc.status) {
			t.Errorf("%s: expected permanent", tc.name)
		}
	}
}

func TestIsTransientLLMErrorClassifiesHTTPStatusErrors(t *testing.T) {
	if !isTransientLLMError(errors.New("HTTP 502: bad gateway"), http.StatusBadGateway) {
		t.Fatal("502 with plain error must be transient")
	}
	if isTransientLLMError(errors.New("HTTP 401: bad key"), http.StatusUnauthorized) {
		t.Fatal("401 with plain error must be permanent")
	}
	if !isTransientLLMError(nil, http.StatusTooManyRequests) {
		t.Fatal("429 must be transient")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Now()
	if d := parseRetryAfter("2", now); d != 2*time.Second {
		t.Fatalf("seconds form: got %v", d)
	}
	if d := parseRetryAfter("", now); d != 0 {
		t.Fatalf("empty form: got %v", d)
	}
	if d := parseRetryAfter("-1", now); d != 0 {
		t.Fatalf("negative form: got %v", d)
	}
	httpDate := now.Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(httpDate, now); d <= 0 || d > 4*time.Second {
		t.Fatalf("http-date form: got %v", d)
	}
}

func TestCallSimpleLLMRetriesTransientThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "recovered"}}},
		})
	}))
	defer server.Close()
	text, err := callSimpleLLM(context.Background(), llmCompletionRequest{
		BaseURL: server.URL, ProviderID: "retry-prov", ModelID: "m", Prompt: "hi",
		Timeout: 10 * time.Second,
	})
	if err != nil || text != "recovered" {
		t.Fatalf("expected recovery, got %q %v", text, err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestCallSimpleLLMDoesNotRetryPermanentErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()
	_, err := callSimpleLLM(context.Background(), llmCompletionRequest{
		BaseURL: server.URL, ProviderID: "perm-prov", ModelID: "m", Prompt: "hi",
		Timeout: 5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("expected api error, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected single attempt, got %d", got)
	}
}

func TestCallSimpleLLMHonorsTotalBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(60 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	started := time.Now()
	_, err := callSimpleLLM(context.Background(), llmCompletionRequest{
		BaseURL: server.URL, ProviderID: "budget-prov", ModelID: "m", Prompt: "hi",
		Timeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("expected failure under exhausted budget")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("retry chain exceeded bounded budget: %v", elapsed)
	}
	if got := calls.Load(); got < 1 || got > llmMaxAttempts {
		t.Fatalf("unexpected attempts: %d", got)
	}
}

func TestRecordLLMRequestTracksRetriesAndDegradation(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	store := newModelStatsStore(workspaceDir)
	store.recordProbeResult("openai", true, 100, "", "gpt-4o")
	var degradedEvents []bool
	store.setOnDegradedChange(func(_ string, d bool) { degradedEvents = append(degradedEvents, d) })

	store.recordLLMRequest("openai", "gpt-4o", 3, nil)
	if store.records["openai"].RetryCount != 2 {
		t.Fatalf("expected retryCount 2, got %d", store.records["openai"].RetryCount)
	}
	for i := 0; i < llmDegradedThreshold; i++ {
		store.recordLLMRequest("openai", "gpt-4o", 1, errors.New("boom"))
	}
	if len(degradedEvents) != 1 || degradedEvents[0] != true {
		t.Fatalf("expected one degradation event, got %v", degradedEvents)
	}
	store.recordLLMRequest("openai", "gpt-4o", 1, nil)
	if len(degradedEvents) != 2 || degradedEvents[1] != false {
		t.Fatalf("expected recovery event, got %v", degradedEvents)
	}
}

func TestRecordLLMRequestNeverCreatesRecords(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	store := newModelStatsStore(workspaceDir)
	store.recordLLMRequest("ghost", "m", 3, errors.New("boom"))
	if len(store.records) != 0 {
		t.Fatalf("internal LLM calls must not create telemetry records: %+v", store.records)
	}
}

func TestCallSimpleLLMDoesNotRetryExhaustedQuota(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","code":"insufficient_quota"}}`)
	}))
	defer srv.Close()
	_, err := callSimpleLLM(context.Background(), llmCompletionRequest{BaseURL: srv.URL, ModelID: "m", Prompt: "hi"})
	if err == nil {
		t.Fatal("expected the quota error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("an exhausted quota must not be retried, got %d calls", got)
	}
}

func TestIsQuotaExhaustedKeepsPlainRateLimitsRetryable(t *testing.T) {
	if isQuotaExhausted([]byte(`{"error":{"type":"rate_limit_error","message":"Rate limit reached, retry later"}}`)) {
		t.Fatal("a plain rate limit is transient")
	}
	if !isQuotaExhausted([]byte(`{"error":{"message":"Your credit balance is too low to access the Anthropic API."}}`)) {
		t.Fatal("an empty balance is permanent")
	}
}
