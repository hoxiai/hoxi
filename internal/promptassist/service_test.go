package promptassist

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

type fakeLLM struct {
	text       string
	noEndpoint bool
	calls      int
	lastPrompt string
}

func (f *fakeLLM) Resolve(providerID, modelID, baseURL, npm string) llm.Target {
	return llm.Target{ProviderID: providerID, ModelID: modelID, HasEndpoint: !f.noEndpoint}
}

func (f *fakeLLM) Complete(_ context.Context, target llm.Target, req llm.Request) (string, error) {
	f.calls++
	f.lastPrompt = req.Prompt
	if !target.Ready() {
		return "", llm.ErrUnavailable
	}
	return f.text, nil
}

func post(t *testing.T, h http.HandlerFunc, body any) string {
	t.Helper()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", &buf))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestHandleSummarizeTitleUsesModelThenFallback(t *testing.T) {
	client := &fakeLLM{text: "重构 Provider 单元测试"}
	svc := NewService(client)
	body := post(t, svc.HandleSummarizeTitle, summarizeTitleRequest{ProviderID: "p", ModelID: "m", Prompt: "请帮我重构 provider 单元测试", Locale: "zh-CN"})
	if !strings.Contains(body, `"title":"重构 Provider 单元测试"`) {
		t.Fatalf("summarize-title LLM failed: %s", body)
	}

	client.noEndpoint = true
	body = post(t, svc.HandleSummarizeTitle, summarizeTitleRequest{Prompt: "Fix the login button alignment on macOS.", Locale: "en-US"})
	if !strings.Contains(body, "Fix the login button alignment on macOS") {
		t.Fatalf("summarize-title fallback failed: %s", body)
	}
	body = post(t, NewService(nil).HandleSummarizeTitle, summarizeTitleRequest{Prompt: "Fix the login button alignment on macOS.", Locale: "en-US"})
	if !strings.Contains(body, `"ok":true`) {
		t.Fatalf("summarize-title without a client must still fall back: %s", body)
	}
}

func TestHandleEnhancePrompt(t *testing.T) {
	client := &fakeLLM{text: "Enhanced prompt: Add a retry guard."}
	svc := NewService(client)
	if body := post(t, svc.HandleEnhancePrompt, enhancePromptRequest{ProviderID: "p", ModelID: "m", Prompt: "retry"}); !strings.Contains(body, `"prompt":"Add a retry guard."`) {
		t.Fatalf("unexpected enhance response: %s", body)
	}
	if !strings.Contains(client.lastPrompt, "<user_draft>\nretry\n</user_draft>") || !strings.Contains(client.lastPrompt, "same natural language the draft is written in") {
		t.Fatalf("expected <user_draft> wrapper and draft language preservation in prompt, got: %s", client.lastPrompt)
	}
	client.noEndpoint = true
	calls := client.calls
	if body := post(t, svc.HandleEnhancePrompt, enhancePromptRequest{Prompt: "retry"}); !strings.Contains(body, "No model is available") || client.calls != calls {
		t.Fatalf("unreachable model must be rejected before calling it: %s", body)
	}
}
