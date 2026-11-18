package provider

import (
	"context"
	"encoding/json"
	"errors"
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
	config := `{"provider":{"custom-google":{"npm":"@ai-sdk/google","options":{"baseURL":"https://example.test/v1beta"},"models":{
		"gemini-3-pro":{"limit":{"context":120000,"output":24000},"reasoning":false,"attachment":true},
		"gemini-3-flash":{"variants":{"high":{"thinkingConfig":{"thinkingLevel":"low","includeThoughts":true}}}},
		"gemini-2.5-flash":{"reasoning":false,"attachment":true,"capabilities":{"reasoning":true}}
	}}}}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(0, "", workspace, nil)
	target := svc.ResolveStreamTarget("custom-google", "gemini-3-pro")
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
