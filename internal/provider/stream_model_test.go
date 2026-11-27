package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

// Capture the real provider-resolved target but never make a model API call.
type captureModelRequest struct {
	svc  *Service
	body []byte
}

func (c *captureModelRequest) ResolveStreamTarget(provider, model string) llm.StreamTarget {
	return c.svc.ResolveStreamTarget(provider, model)
}
func (c *captureModelRequest) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.body = req.Body
	return nil, context.Canceled
}

func TestNativeStreamModelOptionsMatchOpenCode(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	svc := NewService(0, "", workspace, nil)
	for _, tc := range []struct {
		provider string
		model    string
		effort   string
		want     string
		absent   string
	}{
		{"anthropic", "claude-sonnet-4-5", "high", `"budget_tokens":16000`, ""},
		{"anthropic", "claude-opus-4-6", "xhigh", `"output_config":{"effort":"max"}`, `"budget_tokens"`},
		{"anthropic", "claude-opus-4-7", "xhigh", `"output_config":{"effort":"xhigh"}`, `"budget_tokens"`},
		{"google", "gemini-2.5-pro", "high", `"thinkingBudget":16000`, `"thinkingLevel"`},
		{"google", "gemini-3-pro", "medium", `"thinkingLevel":"high"`, `"thinkingBudget"`},
		{"google", "gemini-3-flash", "medium", `"thinkingLevel":"medium"`, `"thinkingBudget"`},
		{"google", "gemini-2.0-flash", "high", `"generationConfig"`, `"thinkingConfig"`},
	} {
		t.Run(tc.model, func(t *testing.T) {
			client := &captureModelRequest{svc: svc}
			_ = agent.NewDefaultEngine(client).RunTurn(context.Background(), agent.TurnRequest{
				SessionID: "ses_options", WorktreeDir: workspace,
				ProviderID: tc.provider, ModelID: tc.model, Effort: tc.effort, UserPrompt: "Inspect code",
			})
			if !strings.Contains(string(client.body), tc.want) || (tc.absent != "" && strings.Contains(string(client.body), tc.absent)) {
				t.Fatalf("unexpected model request: %s; want %s, absent %s", client.body, tc.want, tc.absent)
			}
		})
	}
}

func TestNativeStreamHonorsConfiguredLimitsAndDisabledReasoning(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	path := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"provider":{"custom-google":{"npm":"@ai-sdk/google","options":{"baseURL":"https://example.test/v1beta","geminiExplicitCache":false},"models":{
		"gemini-3-pro":{"options":{"geminiExplicitCache":true},"limit":{"context":120000,"output":24000},"reasoning":false,"attachment":true},
		"gemini-3-flash":{"variants":{"high":{"thinkingConfig":{"thinkingLevel":"low","includeThoughts":true}}}},
		"gemini-2.5-flash":{"reasoning":false,"attachment":true,"capabilities":{"reasoning":true}}
	}}}}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(0, "", workspace, nil)
	target := svc.ResolveStreamTarget("custom-google", "gemini-3-pro")
	if target.DisableGeminiExplicitCache || !svc.ResolveStreamTarget("custom-google", "gemini-3-flash").DisableGeminiExplicitCache {
		t.Fatal("provider and model cache preferences not respected")
	}
	if target.ContextLimit != 120000 || target.OutputLimit != 24000 || len(target.Variants) != 0 {
		t.Fatalf("ignored model configuration: %+v", target)
	}
	for model, want := range map[string]string{
		"gemini-3-flash":   `"thinkingLevel":"low"`,
		"gemini-2.5-flash": "",
	} {
		client := &captureModelRequest{svc: svc}
		_ = agent.NewDefaultEngine(client).RunTurn(context.Background(), agent.TurnRequest{
			SessionID: "ses_custom_options", WorktreeDir: workspace,
			ProviderID: "custom-google", ModelID: model, Effort: "high", UserPrompt: "Inspect code",
		})
		if want != "" && !strings.Contains(string(client.body), want) {
			t.Fatalf("ignored custom variant: %s", client.body)
		}
		var body map[string]any
		if err := json.Unmarshal(client.body, &body); err != nil {
			t.Fatal(err)
		}
		if want == "" && strings.Contains(string(client.body), `"thinkingConfig"`) {
			t.Fatalf("injected reasoning despite disabled reasoning: %s", client.body)
		}
	}
}

func TestOpenStreamPreservesRetryAfterHeadersInStreamHTTPError(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After-Ms", "4200")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit burst"}}`))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{"rl-test":{"npm":"@ai-sdk/openai","options":{"baseURL":"` + srv.URL + `","apiKey":"sk-secret-xyz"},"models":{"gpt-test":{}}}}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", workspace, nil)
	target := svc.ResolveStreamTarget("rl-test", "gpt-test")
	resp, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: target,
		Body:   []byte(`{"model":"gpt-test"}`),
	})
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatal("expected nil response on HTTP 429")
	}
	var httpErr *llm.StreamHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *llm.StreamHTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected StatusCode=429, got %d", httpErr.StatusCode)
	}
	if httpErr.RetryAfter != 4200*time.Millisecond {
		t.Fatalf("expected RetryAfter=4.2s (max of Retry-After-Ms and Retry-After), got %v", httpErr.RetryAfter)
	}
	if strings.Contains(httpErr.Message, "sk-secret-xyz") {
		t.Fatalf("expected API key to remain redacted in error message: %q", httpErr.Message)
	}
}

func TestParseStreamRetryHeaders(t *testing.T) {
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	hdr := make(http.Header)
	hdr.Set("Retry-After", "5")
	hdr.Set("Retry-After-Ms", "7500")
	hdr.Set("X-Ratelimit-Reset-Requests", "8h0m0s")
	if got := parseStreamRetryHeaders(hdr, http.StatusTooManyRequests, now); got != 7500*time.Millisecond {
		t.Fatalf("expected authoritative Retry-After-Ms=7.5s without RPD inflation, got %v", got)
	}

	hdrRL := make(http.Header)
	hdrRL.Set("X-Ratelimit-Reset-Tokens", "12s")
	if got := parseStreamRetryHeaders(hdrRL, http.StatusTooManyRequests, now); got != 12*time.Second {
		t.Fatalf("expected X-Ratelimit-Reset-Tokens=12s, got %v", got)
	}

	// Burst TPM 429 with non-zero daily request remaining must NOT pick 6h30m RPD reset.
	hdrBurstTPM := make(http.Header)
	hdrBurstTPM.Set("X-Ratelimit-Remaining-Requests", "9850")
	hdrBurstTPM.Set("X-Ratelimit-Reset-Requests", "6h30m0s")
	hdrBurstTPM.Set("X-Ratelimit-Remaining-Tokens", "0")
	hdrBurstTPM.Set("X-Ratelimit-Reset-Tokens", "3500ms")
	gotBurst := parseStreamRetryHeaders(hdrBurstTPM, http.StatusTooManyRequests, now)
	if gotBurst != 3500*time.Millisecond {
		t.Fatalf("expected burst TPM reset 3.5s (ignoring non-exhausted RPD window), got %v", gotBurst)
	}
	faultBurst := llm.ClassifyError(&llm.StreamHTTPError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: gotBurst,
		Message:    "HTTP 429: Rate limit reached for TPM",
	}, now)
	if faultBurst.Kind != llm.FaultKindRateLimit || !faultBurst.IsRetryable {
		t.Fatalf("expected burst TPM 429 to stay retryable FaultKindRateLimit, got %+v", faultBurst)
	}

	// Exhausted daily request bucket (Remaining-Requests: 0) MUST surface 4h reset and promote to quota.
	hdrExhaustedRPD := make(http.Header)
	hdrExhaustedRPD.Set("X-Ratelimit-Remaining-Requests", "0")
	hdrExhaustedRPD.Set("X-Ratelimit-Reset-Requests", "4h0m0s")
	hdrExhaustedRPD.Set("X-Ratelimit-Reset-Tokens", "1s")
	gotRPD := parseStreamRetryHeaders(hdrExhaustedRPD, http.StatusTooManyRequests, now)
	if gotRPD != 4*time.Hour {
		t.Fatalf("expected exhausted RPD bucket to report 4h reset, got %v", gotRPD)
	}
	faultRPD := llm.ClassifyError(&llm.StreamHTTPError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: gotRPD,
		Message:    "HTTP 429: Rate limit reached for requests per day",
	}, now)
	if faultRPD.Kind != llm.FaultKindQuota || faultRPD.IsRetryable {
		t.Fatalf("expected exhausted 4h RPD 429 to promote to non-retryable FaultKindQuota, got %+v", faultRPD)
	}

	// RFC3339 timestamp reset header support.
	hdrISO := make(http.Header)
	hdrISO.Set("X-Ratelimit-Reset-Tokens", "2026-11-17T06:00:09Z")
	if got := parseStreamRetryHeaders(hdrISO, http.StatusTooManyRequests, now); got != 9*time.Second {
		t.Fatalf("expected RFC3339 reset timestamp=9s, got %v", got)
	}
}

func TestClaudeOnUnifiedOpenAIGatewayPromotesToAnthropicMessagesExceptOpenAIOnlyHosts(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	path := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"provider":{
		"subapi":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://127.0.0.1:5431/v1"},"models":{
			"antigravity-claude-opus-5-5-high":{"limit":{"context":1000000,"output":128000}},
			"gemini-web-gemini-4-carbon":{"limit":{"context":1000000,"output":128000}}
		}},
		"openrouter":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://openrouter.ai/api/v1"},"models":{
			"anthropic/claude-opus-4-6":{"limit":{"context":200000,"output":32000}}
		}}
	}}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(0, "", workspace, nil)

	claudeSub := svc.ResolveStreamTarget("subapi", "antigravity-claude-opus-5-5-high")
	if claudeSub.Protocol != llm.ProtocolAnthropic || !claudeSub.FallbackOpenAI {
		t.Fatalf("expected Claude on unified subapi gateway to promote to ProtocolAnthropic with FallbackOpenAI, got %+v", claudeSub)
	}
	if len(claudeSub.Variants) != 0 {
		t.Fatalf("expected baked -high proxy suffix model not to inject conflicting variants, got %+v", claudeSub.Variants)
	}

	nonClaudeSub := svc.ResolveStreamTarget("subapi", "gemini-web-gemini-4-carbon")
	if nonClaudeSub.Protocol != llm.ProtocolOpenAI {
		t.Fatalf("expected non-Claude model on subapi to remain ProtocolOpenAI, got %+v", nonClaudeSub)
	}

	openRouterClaude := svc.ResolveStreamTarget("openrouter", "anthropic/claude-opus-4-6")
	if openRouterClaude.Protocol != llm.ProtocolOpenAI || openRouterClaude.FallbackOpenAI {
		t.Fatalf("expected OpenRouter host to stay ProtocolOpenAI, got %+v", openRouterClaude)
	}
}

func TestOpenStreamRetryAfterUpdatesBreakerCooldownWithoutPollutingLatency(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{"rl-breaker":{"npm":"@ai-sdk/openai","options":{"baseURL":"` + srv.URL + `","apiKey":"sk-test"},"models":{"gpt-fast":{}}}}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", workspace, nil)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	svc.breaker.nowFn = func() time.Time { return now }
	// Seed a healthy 200ms baseline so failure EWMA blending is active.
	svc.breaker.RecordSuccess("rl-breaker", "gpt-fast", 200)

	target := svc.ResolveStreamTarget("rl-breaker", "gpt-fast")
	resp, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: target,
		Body:   []byte(`{"model":"gpt-fast"}`),
	})
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatal("expected nil response on 429")
	}
	if err == nil {
		t.Fatal("expected error on 429")
	}
	lat := svc.breaker.LatencyMs("rl-breaker", "gpt-fast")
	if lat > 2000 {
		t.Fatalf("Retry-After polluted breaker LatencyEwmaMs: got %.1fms (baseline was 200ms)", lat)
	}
	rec := svc.breaker.items[breakerKey("rl-breaker", "gpt-fast")]
	if rec == nil || rec.Phase != breakerOpen {
		t.Fatalf("expected breaker to open on 429, got %+v", rec)
	}
	wantUntil := now.Add(600 * time.Second).UnixMilli()
	if rec.OpenUntil != wantUntil {
		t.Fatalf("expected OpenUntil=%d (now + 600s Retry-After), got %d", wantUntil, rec.OpenUntil)
	}
}

func TestCodexAndGPTModelsOnUnifiedGatewayPromoteToResponsesWithMemoizedFallback(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	var seenPaths []string
	var statusToReturn int
	var bodyToReturn string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths = append(seenPaths, r.URL.Path)
		w.WriteHeader(statusToReturn)
		_, _ = w.Write([]byte(bodyToReturn))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{
		"subapi":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"` + srv.URL + `/v1","apiKey":"sk-sub"},"models":{
			"openai-codex-gpt-6.2-sol":{"limit":{"context":1050000,"output":128000}},
			"gpt-5.4-codex":{"limit":{"context":400000,"output":128000}},
			"gpt-4o":{"limit":{"context":128000,"output":16384}},
			"openai/gpt-oss-120b":{"limit":{"context":131072,"output":16384}},
			"Loomy-DeepSeek-V4.1-Flash":{"limit":{"context":128000,"output":16384}}
		}},
		"openrouter":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://openrouter.ai/api/v1"},"models":{
			"openai/gpt-5":{"limit":{"context":400000,"output":128000}}
		}},
		"groq":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://api.groq.com/openai/v1"},"models":{
			"gpt-5":{"limit":{"context":128000,"output":16384}}
		}}
	}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(0, "", workspace, nil)

	// 1. Positive promotions on unified OpenAI-compatible gateway.
	codexTarget := svc.ResolveStreamTarget("subapi", "openai-codex-gpt-6.2-sol")
	if codexTarget.Protocol != llm.ProtocolOpenAIResponses || !codexTarget.FallbackOpenAI {
		t.Fatalf("expected subapi Codex model to promote to ProtocolOpenAIResponses with FallbackOpenAI, got %+v", codexTarget)
	}
	gpt5Target := svc.ResolveStreamTarget("subapi", "gpt-5.4-codex")
	if gpt5Target.Protocol != llm.ProtocolOpenAIResponses || !gpt5Target.FallbackOpenAI {
		t.Fatalf("expected subapi gpt-5.4-codex to promote to ProtocolOpenAIResponses with FallbackOpenAI, got %+v", gpt5Target)
	}

	// 2. Negative / rejection cases must stay ProtocolOpenAI without FallbackOpenAI.
	for _, tc := range []struct{ pid, mid string }{
		{"subapi", "gpt-4o"},
		{"subapi", "openai/gpt-oss-120b"},
		{"subapi", "Loomy-DeepSeek-V4.1-Flash"},
		{"openrouter", "openai/gpt-5"},
		{"groq", "gpt-5"},
	} {
		got := svc.ResolveStreamTarget(tc.pid, tc.mid)
		if got.Protocol != llm.ProtocolOpenAI || got.FallbackOpenAI {
			t.Fatalf("expected %s/%s to remain ProtocolOpenAI without FallbackOpenAI, got %+v", tc.pid, tc.mid, got)
		}
	}

	// 3. A regular 400 (e.g. context overflow) must NOT memoize noOpenAIResponses.
	statusToReturn = http.StatusBadRequest
	bodyToReturn = `{"error":{"message":"context_length_exceeded"}}`
	if _, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: codexTarget,
		Body:   []byte(`{"model":"openai-codex-gpt-6.2-sol"}`),
	}); err == nil {
		t.Fatal("expected error on 400")
	}
	if still := svc.ResolveStreamTarget("subapi", "openai-codex-gpt-6.2-sol"); still.Protocol != llm.ProtocolOpenAIResponses {
		t.Fatalf("regular 400 must not disable Responses promotion, got %+v", still)
	}

	// 4. 404 on /v1/responses memoizes noOpenAIResponses and skips circuit breaker penalty.
	statusToReturn = http.StatusNotFound
	bodyToReturn = `{"error":{"message":"404 page not found"}}`
	_, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: codexTarget,
		Body:   []byte(`{"model":"openai-codex-gpt-6.2-sol"}`),
	})
	var httpErr *llm.StreamHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 StreamHTTPError, got %v", err)
	}
	if after := svc.ResolveStreamTarget("subapi", "openai-codex-gpt-6.2-sol"); after.Protocol != llm.ProtocolOpenAI || after.FallbackOpenAI {
		t.Fatalf("expected memoized fallback to ProtocolOpenAI after 404, got %+v", after)
	}
	if len(seenPaths) != 2 || seenPaths[0] != "/v1/responses" || seenPaths[1] != "/v1/responses" {
		t.Fatalf("expected /v1/responses probes, got %v", seenPaths)
	}
}

func TestCodexChatGPTBackendReceivesSessionIDHeader(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer srv.Close()

	SetCodexOAuthBaseURLForTest(srv.URL)
	defer SetCodexOAuthBaseURLForTest("")

	cfgPath := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{"codex-direct":{"npm":"@ai-sdk/openai","options":{"baseURL":"` + srv.URL + `","apiKey":"sk-oauth"},"models":{"gpt-6.2-sol":{}}}}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", workspace, nil)
	target := svc.ResolveStreamTarget("codex-direct", "gpt-6.2-sol")
	resp, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target:    target,
		SessionID: "ses_codex_affinity_42",
		Body:      []byte(`{"model":"gpt-6.2-sol","input":[]}`),
	})
	if err != nil {
		t.Fatalf("OpenStream failed: %v", err)
	}
	_ = resp.Body.Close()

	if got := gotHeaders.Get("Session-Id"); got != "ses_codex_affinity_42" {
		t.Fatalf("expected Session-Id=ses_codex_affinity_42 on Codex backend, got %q", got)
	}
	if got := gotHeaders.Get("x-session-affinity"); got != "" {
		t.Fatalf("expected no x-session-affinity on Codex ChatGPT backend, got %q", got)
	}
	if got := gotHeaders.Get("X-Session-Id"); got != "" {
		t.Fatalf("expected no X-Session-Id on Codex ChatGPT backend, got %q", got)
	}
}

func TestHTTP1FallbackTransportIsolatesCloneWithoutMutatingSharedTransport(t *testing.T) {
	if providerTransport.IdleConnTimeout != 30*time.Second {
		t.Fatalf("expected providerTransport IdleConnTimeout=30s, got %v", providerTransport.IdleConnTimeout)
	}
	if providerTransport.HTTP2 == nil || providerTransport.HTTP2.SendPingTimeout != 15*time.Second || providerTransport.HTTP2.PingTimeout != 10*time.Second {
		t.Fatalf("expected providerTransport HTTP/2 health pings (15s/10s), got %+v", providerTransport.HTTP2)
	}
	h1 := newHTTP1FallbackTransport(providerTransport)
	if h1 == nil || h1 == providerTransport {
		t.Fatalf("expected cloned transport distinct from shared providerTransport")
	}
	if h1.Protocols == nil || !h1.Protocols.HTTP1() || h1.Protocols.HTTP2() {
		t.Fatalf("expected HTTP/1.1-only protocols on fallback clone, got %+v", h1.Protocols)
	}
	if h1.HTTP2 != nil {
		t.Fatalf("expected HTTP/1.1 fallback clone to clear HTTP2 config, got %+v", h1.HTTP2)
	}
	if providerTransport.Protocols != nil || providerTransport.HTTP2 == nil {
		t.Fatalf("shared providerTransport must not be mutated, got protocols=%+v http2=%+v", providerTransport.Protocols, providerTransport.HTTP2)
	}
	if !llm.IsHTTP2ProtocolError(errors.New("stream error: stream ID 7; PROTOCOL_ERROR; received from peer")) {
		t.Fatal("expected HTTP/2 PROTOCOL_ERROR to be detected")
	}
	if llm.IsHTTP2ProtocolError(&llm.StreamHTTPError{StatusCode: 500, Message: "http2 protocol_error"}) {
		t.Fatal("structured HTTP status responses must not be classified as HTTP/2 transport resets")
	}
}

type headerCaptureTransport func(*http.Request) (*http.Response, error)

func (f headerCaptureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOpenStreamSendsGrokConvIDHeaderOnlyForXAIHosts(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	cfgPath := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{
		"xai":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://api.x.ai/v1","apiKey":"sk-xai"},"models":{"grok-4":{}}},
		"oa":{"npm":"@ai-sdk/openai","options":{"baseURL":"https://api.openai.com/v1","apiKey":"sk-oa"},"models":{"gpt-5":{}}}
	}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotHeaders http.Header
	SetStreamTransportForTest(headerCaptureTransport(func(r *http.Request) (*http.Response, error) {
		gotHeaders = r.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
		}, nil
	}))
	defer SetStreamTransportForTest(nil)

	svc := NewService(0, "", workspace, nil)

	// Positive: xAI host + SessionID sends x-grok-conv-id for sticky KV cache routing.
	xaiTarget := svc.ResolveStreamTarget("xai", "grok-4")
	resp, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target:    xaiTarget,
		SessionID: "ses_grok_sticky_1",
		Body:      []byte(`{"model":"grok-4"}`),
	})
	if err != nil {
		t.Fatalf("OpenStream xai failed: %v", err)
	}
	_ = resp.Body.Close()
	if got := gotHeaders.Get("x-grok-conv-id"); got != "ses_grok_sticky_1" {
		t.Fatalf("expected x-grok-conv-id=ses_grok_sticky_1 on api.x.ai, got %q", got)
	}

	// Rejection 1: xAI host with empty SessionID must NOT send x-grok-conv-id.
	resp, err = svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: xaiTarget,
		Body:   []byte(`{"model":"grok-4"}`),
	})
	if err != nil {
		t.Fatalf("OpenStream xai empty sid failed: %v", err)
	}
	_ = resp.Body.Close()
	if got := gotHeaders.Get("x-grok-conv-id"); got != "" {
		t.Fatalf("expected no x-grok-conv-id when SessionID empty, got %q", got)
	}

	// Rejection 2: non-xAI host must NOT send x-grok-conv-id.
	oaTarget := svc.ResolveStreamTarget("oa", "gpt-5")
	resp, err = svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target:    oaTarget,
		SessionID: "ses_oa_1",
		Body:      []byte(`{"model":"gpt-5"}`),
	})
	if err != nil {
		t.Fatalf("OpenStream oa failed: %v", err)
	}
	_ = resp.Body.Close()
	if got := gotHeaders.Get("x-grok-conv-id"); got != "" {
		t.Fatalf("expected no x-grok-conv-id on api.openai.com, got %q", got)
	}
}

func TestOpenStreamSendsResponsesStickySessionHeadersOnGateway(t *testing.T) {
	_, workspace := setupIsolatedEnv(t)
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(workspace, ".hoxi", "models.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"provider":{"subapi":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"` + srv.URL + `/v1","apiKey":"sk-sub"},"models":{
		"openai-codex-gpt-6.2-sol":{"limit":{"context":1050000,"output":128000}},
		"gpt-4o":{"limit":{"context":128000,"output":16384}}
	}}}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(0, "", workspace, nil)
	respTarget := svc.ResolveStreamTarget("subapi", "openai-codex-gpt-6.2-sol")
	if respTarget.Protocol != llm.ProtocolOpenAIResponses {
		t.Fatalf("expected ProtocolOpenAIResponses, got %+v", respTarget)
	}

	// Positive: Responses request through gateway sends all Codex/proxy sticky headers.
	resp, err := svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target:    respTarget,
		SessionID: "ses_resp_sticky_99",
		Body:      []byte(`{"model":"openai-codex-gpt-6.2-sol","input":[]}`),
	})
	if err != nil {
		t.Fatalf("OpenStream responses failed: %v", err)
	}
	_ = resp.Body.Close()
	for _, hdr := range []string{"Session-Id", "session_id", "conversation_id", "X-Session-Id", "x-session-affinity"} {
		if got := gotHeaders.Get(hdr); got != "ses_resp_sticky_99" {
			t.Fatalf("expected header %q=ses_resp_sticky_99 on Responses gateway, got %q", hdr, got)
		}
	}

	// Rejection 1: Empty SessionID must not emit sticky session headers.
	resp, err = svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target: respTarget,
		Body:   []byte(`{"model":"openai-codex-gpt-6.2-sol","input":[]}`),
	})
	if err != nil {
		t.Fatalf("OpenStream responses empty sid failed: %v", err)
	}
	_ = resp.Body.Close()
	for _, hdr := range []string{"Session-Id", "session_id", "conversation_id", "X-Session-Id", "x-session-affinity"} {
		if got := gotHeaders.Get(hdr); got != "" {
			t.Fatalf("expected no %q header when SessionID empty, got %q", hdr, got)
		}
	}

	// Non-Responses Chat Completions on a non-official gateway sends Session-Id/session_id
	// for sticky proxy routing while omitting Responses-only conversation_id.
	chatTarget := svc.ResolveStreamTarget("subapi", "gpt-4o")
	resp, err = svc.OpenStream(context.Background(), llm.StreamHTTPRequest{
		Target:    chatTarget,
		SessionID: "ses_chat_1",
		Body:      []byte(`{"model":"gpt-4o","messages":[]}`),
	})
	if err != nil {
		t.Fatalf("OpenStream chat failed: %v", err)
	}
	_ = resp.Body.Close()
	for _, hdr := range []string{"X-Session-Id", "Session-Id", "session_id"} {
		if got := gotHeaders.Get(hdr); got != "ses_chat_1" {
			t.Fatalf("expected %q=ses_chat_1 on gateway chat target, got %q", hdr, got)
		}
	}
	if got := gotHeaders.Get("conversation_id"); got != "" {
		t.Fatalf("expected no Responses-only conversation_id on chat target, got %q", got)
	}
}
