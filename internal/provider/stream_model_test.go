package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
