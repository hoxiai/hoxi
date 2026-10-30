package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

// effortServer answers chat completions and records whether each request
// carried reasoning_effort. When reject is set it refuses the parameter the
// way OpenAI does for non-reasoning models.
type effortServer struct {
	mu      sync.Mutex
	reject  bool
	efforts []string
}

func (e *effortServer) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	effort, _ := body["reasoning_effort"].(string)
	e.mu.Lock()
	e.efforts = append(e.efforts, effort)
	reject := e.reject
	e.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if reject && effort != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'reasoning_effort' is not supported with this model.","type":"invalid_request_error"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"decision\":\"allow\"}"}}]}`))
}

func (e *effortServer) seen() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.efforts...)
}

func TestCompleteSendsReasoningEffortWhenAccepted(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	es := &effortServer{}
	srv := httptest.NewServer(http.HandlerFunc(es.handler))
	defer srv.Close()
	svc := NewService(0, "", workspaceDir, nil)

	target := svc.Resolve("custom-oai", "flash-thinker", srv.URL+"/v1", "")
	text, err := svc.Complete(context.Background(), target, llm.Request{Prompt: "x", Timeout: 5 * time.Second, ReasoningEffort: "low"})
	if err != nil || text == "" {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	if got := es.seen(); len(got) != 1 || got[0] != "low" {
		t.Fatalf("expected one request with reasoning_effort=low, got %q", got)
	}

	// Callers that do not ask for an effort never send the parameter.
	if _, err := svc.Complete(context.Background(), target, llm.Request{Prompt: "x", Timeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if got := es.seen(); got[1] != "" {
		t.Fatalf("expected no reasoning_effort without a request for it, got %q", got)
	}
}

func TestCompleteRetriesWithoutRejectedReasoningEffort(t *testing.T) {
	_, workspaceDir := setupIsolatedEnv(t)
	es := &effortServer{reject: true}
	srv := httptest.NewServer(http.HandlerFunc(es.handler))
	defer srv.Close()
	svc := NewService(0, "", workspaceDir, nil)

	target := svc.Resolve("custom-oai", "gpt-4o-mini", srv.URL+"/v1", "")
	req := llm.Request{Prompt: "x", Timeout: 5 * time.Second, ReasoningEffort: "low"}
	text, err := svc.Complete(context.Background(), target, req)
	if err != nil || text == "" {
		t.Fatalf("expected the plain retry to answer, got %q, %v", text, err)
	}
	if got := es.seen(); len(got) != 2 || got[0] != "low" || got[1] != "" {
		t.Fatalf("expected a rejected try then a plain one, got %q", got)
	}

	// The rejection is remembered: the next call goes out without it at once.
	if _, err := svc.Complete(context.Background(), target, req); err != nil {
		t.Fatal(err)
	}
	if got := es.seen(); len(got) != 3 || got[2] != "" {
		t.Fatalf("expected the remembered model to skip reasoning_effort, got %q", got)
	}

	// The 400 is a bad request, which never cools the model down.
	if allowed, _ := svc.breaker.Evaluate("custom-oai", "gpt-4o-mini"); !allowed {
		t.Fatalf("a reasoning_effort rejection must not put the model into cooldown")
	}
}

func TestIsReasoningEffortRejection(t *testing.T) {
	cases := map[string]bool{
		"HTTP 400: Unsupported parameter: 'reasoning_effort' is not supported with this model.": true,
		"HTTP 422: unknown field reasoningEffort":                                               true,
		"HTTP 400: context length exceeded":                                                     false,
		"HTTP 503: reasoning_effort backend overloaded":                                         false,
		"dial tcp: connection refused":                                                          false,
	}
	for msg, want := range cases {
		if got := isReasoningEffortRejection(errString(msg)); got != want {
			t.Errorf("isReasoningEffortRejection(%q) = %v, want %v", msg, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
