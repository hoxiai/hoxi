package runner

import (
	"strings"
	"testing"
)

func TestValidatePromptOrchestratorForRuntime(t *testing.T) {
	if spec, err := validatePromptOrchestratorForRuntime(nil, string(RuntimeClaudeCode)); err != nil || spec != nil {
		t.Fatalf("nil orchestrator should pass on any runtime, got spec=%+v err=%v", spec, err)
	}

	valid, err := validatePromptOrchestratorForRuntime(&PromptOrchestrator{
		ExecutorProviderID: "google",
		ExecutorModelID:    "gemini-2.5-flash",
		ExecutorEffort:     "HIGH",
	}, string(RuntimeNative))
	if err != nil {
		t.Fatalf("valid native orchestrator failed: %v", err)
	}
	if valid == nil || valid.ExecutorProviderID != "google" || valid.ExecutorModelID != "gemini-2.5-flash" || valid.ExecutorEffort != "high" {
		t.Fatalf("unexpected normalized orchestrator spec: %+v", valid)
	}

	rejectCases := []struct {
		name    string
		in      *PromptOrchestrator
		runtime string
		wantErr string
	}{
		{
			name:    "non-native runtime rejected",
			in:      &PromptOrchestrator{ExecutorProviderID: "google", ExecutorModelID: "gemini-2.5-flash"},
			runtime: string(RuntimeClaudeCode),
			wantErr: "native Go runtime",
		},
		{
			name:    "missing executor model rejected",
			in:      &PromptOrchestrator{ExecutorProviderID: "google"},
			runtime: string(RuntimeNative),
			wantErr: "executorProviderID and executorModelID",
		},
		{
			name:    "virtual provider rejected",
			in:      &PromptOrchestrator{ExecutorProviderID: "hoxi-auto", ExecutorModelID: "daily"},
			runtime: string(RuntimeNative),
			wantErr: "virtual provider",
		},
		{
			name:    "external CLI runtime provider rejected",
			in:      &PromptOrchestrator{ExecutorProviderID: "claude-code", ExecutorModelID: "claude-sonnet-4-5"},
			runtime: string(RuntimeNative),
			wantErr: "native API provider",
		},
		{
			name:    "invalid effort rejected",
			in:      &PromptOrchestrator{ExecutorProviderID: "google", ExecutorModelID: "gemini-2.5-flash", ExecutorEffort: "ludicrous"},
			runtime: string(RuntimeNative),
			wantErr: "unsupported orchestrator executor effort",
		},
	}
	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validatePromptOrchestratorForRuntime(tc.in, tc.runtime); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestPromptRequestDigestDistinguishesOrchestrator(t *testing.T) {
	base := PromptRequest{
		Text:  "refactor module",
		Model: &PromptModel{ProviderID: "anthropic", ModelID: "claude-opus-4-6"},
	}
	withExecA := base
	withExecA.Orchestrator = &PromptOrchestrator{
		ExecutorProviderID: "google",
		ExecutorModelID:    "gemini-2.5-flash",
		ExecutorEffort:     "medium",
	}
	withExecB := base
	withExecB.Orchestrator = &PromptOrchestrator{
		ExecutorProviderID: "google",
		ExecutorModelID:    "gemini-2.5-flash",
		ExecutorEffort:     "high",
	}

	dBase := PromptRequestDigest("ses_1", base)
	dA := PromptRequestDigest("ses_1", withExecA)
	dB := PromptRequestDigest("ses_1", withExecB)
	if dBase == dA {
		t.Fatal("PromptRequestDigest must distinguish Orchestrator on vs off")
	}
	if dA == dB {
		t.Fatal("PromptRequestDigest must distinguish different Executor effort/model specs")
	}
}

func TestIsMutatingToolPartRecognizesRunningExecutorSubagent(t *testing.T) {
	part := MessagePart{
		Type: "tool",
		Tool: "task",
		State: &ToolState{
			Status: "running",
			Input:  map[string]interface{}{"subagent_type": "executor", "description": "apply edits"},
		},
	}
	if !isMutatingToolPart(part) {
		t.Fatal("running task(subagent_type=executor) must be treated as mutating for crash settlement")
	}
}
