package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

type stubRulesStreamClient struct {
	mu      sync.Mutex
	reqs    []llm.StreamHTTPRequest
	handler func(step int, req llm.StreamHTTPRequest) string
}

func (s *stubRulesStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://mock.local/v1",
		Protocol:     llm.ProtocolOpenAI,
		ContextLimit: 128000,
		OutputLimit:  8192,
		HasEndpoint:  true,
	}
}

func (s *stubRulesStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	idx := len(s.reqs)
	h := s.handler
	s.mu.Unlock()
	sse := h(idx, req)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(sse))),
		Header:     make(http.Header),
	}, nil
}

func TestMonorepoRulesFullLoadSubdirectoryDiscoveryAndBudgetNotice(t *testing.T) {
	monoRoot := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	if err := os.MkdirAll(filepath.Join(monoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("Root architecture rule line with UTF-8 中文守卫说明.\n", 360) // ~18 KB > legacy 12 KB cap
	tailRule := "TAIL_MONOREPO_SENTINEL_RULE: always preserve payment_idempotency_key in billing mutations."
	if err := os.MkdirAll(filepath.Join(monoRoot, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(monoRoot, "docs", "extra-rule.md"), []byte("INCLUDE_SENTINEL_RULE: enforce strict schema version 2."), 0o644); err != nil {
		t.Fatal(err)
	}
	rootAgents := "# Root Monorepo Rules\nSee @docs/extra-rule.md for schema rules.\n\n" + padding + "\n" + tailRule + "\n"
	if err := os.WriteFile(filepath.Join(monoRoot, "AGENTS.md"), []byte(rootAgents), 0o644); err != nil {
		t.Fatal(err)
	}

	subPkg := filepath.Join(monoRoot, "packages", "billing")
	if err := os.MkdirAll(subPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	subRule := "SUBDIR_BILLING_SENTINEL: use integer cents, never float64 for currency."
	if err := os.WriteFile(filepath.Join(subPkg, "AGENTS.md"), []byte(subRule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subPkg, "charge.go"), []byte("package billing\n\nfunc Charge() int { return 100 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stream := &stubRulesStreamClient{
		handler: func(step int, _ llm.StreamHTTPRequest) string {
			if step == 1 {
				return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_read_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"filePath\\\":\\\"packages/billing/charge.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"Billing rules verified.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		},
	}
	eng := NewDefaultEngine(stream)
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_rules_test",
		WorktreeDir: monoRoot,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Inspect packages/billing/charge.go",
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if len(stream.reqs) != 2 {
		t.Fatalf("expected 2 model steps, got %d", len(stream.reqs))
	}
	step2Body := string(stream.reqs[1].Body)
	if !strings.Contains(step2Body, tailRule) {
		t.Fatalf("expected >12KB root AGENTS.md tail sentinel in system prompt")
	}
	if !strings.Contains(step2Body, "INCLUDE_SENTINEL_RULE") {
		t.Fatalf("expected @docs/extra-rule.md included rule in system prompt")
	}
	if !strings.Contains(step2Body, subRule) {
		t.Fatalf("expected packages/billing/AGENTS.md subdirectory rule injected on read tool output")
	}

	var budgetEvents []EngineEvent
	err = eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_rules_budget",
		WorktreeDir: monoRoot,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Check budget feedback",
		MCPConfig: func() map[string]any {
			return map[string]any{"rules_max_bytes": 4096}
		},
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineRulesBudget {
				budgetEvents = append(budgetEvents, evt)
			}
		}),
	})
	if err != nil {
		t.Fatalf("budget RunTurn failed: %v", err)
	}
	if len(budgetEvents) != 1 {
		t.Fatalf("expected 1 EngineRulesBudget event on overflow, got %d", len(budgetEvents))
	}
	lastBody := string(stream.reqs[len(stream.reqs)-1].Body)
	if !strings.Contains(lastBody, "rules_budget_notice") {
		t.Fatalf("expected <rules_budget_notice> feedback in system prompt when exceeding budget")
	}

	sysPrompt, _ := buildSystemPromptWithRules(monoRoot, "", "", "build", 0, nil)
	inherited := extractInheritedSubtaskConstraints(sysPrompt, true)
	if !strings.Contains(inherited, tailRule) {
		t.Fatalf("expected readOnly subagent to inherit full root AGENTS.md tail rule")
	}
}

func TestBuildEnvironmentPromptWindowsGitBashVsCmdAndWSLRejection(t *testing.T) {
	envMap := map[string]string{
		"ProgramFiles": `C:\Program Files`,
		"USERPROFILE":  `C:\Users\dev`,
	}
	getenv := func(k string) string { return envMap[k] }

	// 1. Windows with Git Bash installed
	withGitBash := buildEnvironmentPromptForPlatform(
		"windows",
		"amd64",
		`C:\Users\dev\code\app`,
		getenv,
		func(p string) bool { return p == `C:\Program Files\Git\bin\bash.exe` },
		nil,
	)
	if !strings.Contains(withGitBash, "Platform: windows/amd64") ||
		!strings.Contains(withGitBash, `Working directory: C:\Users\dev\code\app`) ||
		!strings.Contains(withGitBash, `C:\Program Files\Git\bin\bash.exe -c (Git Bash`) {
		t.Fatalf("unexpected Windows Git Bash environment prompt:\n%s", withGitBash)
	}

	// 2. Windows without Git Bash where PATH only exposes WSL's System32\bash.exe stub -> must fall back to cmd.exe
	wslOnly := buildEnvironmentPromptForPlatform(
		"windows",
		"arm64",
		`D:\work\repo`,
		getenv,
		func(string) bool { return false },
		func(string) (string, error) { return `C:\Windows\System32\bash.exe`, nil },
	)
	if strings.Contains(wslOnly, "System32") || !strings.Contains(wslOnly, "cmd.exe /C") {
		t.Fatalf("expected WSL System32 bash stub to be rejected in favor of cmd.exe guidance:\n%s", wslOnly)
	}

	// 3. No duplicate <environment> when callerSystem already includes it
	p1, _ := buildSystemPromptWithRules("/tmp/ws", "", "", "build", 0, nil)
	if c := strings.Count(p1, "<environment>"); c != 1 {
		t.Fatalf("expected 1 <environment> block, got %d", c)
	}
	p2, _ := buildSystemPromptWithRules("/tmp/ws", p1, "", "plan", 1, nil)
	if c := strings.Count(p2, "<environment>"); c != 1 {
		t.Fatalf("expected <environment> not to be duplicated for child prompt, got %d", c)
	}

	// 4. Environment reports real git repository presence without shelling out.
	nonGitDir := t.TempDir()
	envNoGit := buildEnvironmentPrompt(nonGitDir)
	if !strings.Contains(envNoGit, "Git repository: no") {
		t.Fatalf("expected non-git temp dir to report Git repository: no, got:\n%s", envNoGit)
	}
	gitDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(gitDir, "sub", "pkg")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envGit := buildEnvironmentPrompt(nestedDir)
	if !strings.Contains(envGit, "Git repository: yes") {
		t.Fatalf("expected nested dir inside git repo to report Git repository: yes, got:\n%s", envGit)
	}
}

func TestExtractInheritedSubtaskConstraintsStripsSkillCatalogAndParentSubtaskProtocol(t *testing.T) {
	parentSystem := strings.Join([]string{
		"Hoxi command execution:",
		"Keep build, test and workspace-check progress visible.",
		`When the user asks to spawn/start a background subtask: output a fenced hoxi-subtasks JSON block {"title":"plan"}.`,
		"",
		"<project_memory>",
		"Always run ./verify.sh.",
		"</project_memory>",
		"",
		"<hoxi_work_store_profile>",
		"storeId: st_1",
		"</hoxi_work_store_profile>",
		"",
		"Skills provide specialized instructions and workflows for specific tasks.",
		"<available_skills>",
		"  <skill><name>giant-skill-1</name><description>Huge catalog entry</description></skill>",
		"</available_skills>",
		"",
		"<session_handoff_memory>",
		"old transcript",
		"</session_handoff_memory>",
	}, "\n")

	inherited := extractInheritedSubtaskConstraints(parentSystem, false)
	for _, mustKeep := range []string{
		"Keep build, test and workspace-check progress visible.",
		"Always run ./verify.sh.",
		"storeId: st_1",
	} {
		if !strings.Contains(inherited, mustKeep) {
			t.Fatalf("expected general subagent constraints to keep %q, got:\n%s", mustKeep, inherited)
		}
	}
	for _, mustDrop := range []string{
		"<available_skills>",
		"giant-skill-1",
		"Skills provide specialized instructions and workflows for specific tasks.",
		"hoxi-subtasks",
		"<session_handoff_memory>",
	} {
		if strings.Contains(inherited, mustDrop) {
			t.Fatalf("expected general subagent constraints to strip %q, got:\n%s", mustDrop, inherited)
		}
	}
}

type stubSmallCtxStreamClient struct {
	mu   sync.Mutex
	reqs []llm.StreamHTTPRequest
}

func (s *stubSmallCtxStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "mock-local-v1",
		Protocol:     llm.ProtocolOpenAI,
		ContextLimit: 8000,
		OutputLimit:  1024,
		HasEndpoint:  true,
	}
}

func (s *stubSmallCtxStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(sse))),
		Header:     make(http.Header),
	}, nil
}

func TestEngineRunTurnPassesWorktreeDirToCompactorForMixedAbsAndRelHistory(t *testing.T) {
	wsDir := t.TempDir()
	absTarget := filepath.ToSlash(filepath.Join(wsDir, "pkg", "handler.go"))
	staleBody := strings.Repeat("1: func oldEngineReadSnapshot() {}\n", 18)

	var history []Message
	history = append(history,
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "e1", Name: "read", Input: []byte(`{"filePath":"` + absTarget + `"}`)}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "e1", Text: staleBody}}},
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "e2", Name: "apply_patch", Input: []byte(`{"patch":"*** Begin Patch\n*** Update File: pkg/handler.go\n@@\n-old\n+new\n*** End Patch"}`)}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "e2", Text: "patched"}}},
	)
	for i := 0; i < 4; i++ {
		history = append(history,
			Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "ef", Name: "bash", Input: []byte(`{"command":"true"}`)}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "ef", Text: "ok"}}},
		)
	}
	bigTail := strings.Repeat("recent engine turn tail line\n", 750)
	for i := 0; i < 3; i++ {
		txt := "ok"
		if i == 0 {
			txt = bigTail
		}
		history = append(history,
			Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "et", Name: "read", Input: []byte(`{"filePath":"recent.go"}`)}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "et", Text: txt}}},
		)
	}

	sc := &stubSmallCtxStreamClient{}
	eng := NewDefaultEngine(sc)
	// Even when callerSystem overrides <environment> without Working directory,
	// Engine.applyCompact forwards req.WorktreeDir directly.
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_compact_wd",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		System:      "<environment>\nPlatform: custom\n</environment>",
		History:     history,
		UserPrompt:  "Continue",
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(sc.reqs) != 1 {
		t.Fatalf("expected 1 upstream request, got %d", len(sc.reqs))
	}
	reqPayload := string(sc.reqs[0].Body)
	if !strings.Contains(reqPayload, "stale read of pkg/handler.go superseded by a later edit") {
		t.Fatalf("expected Engine.RunTurn to fold mixed absolute read + relative apply_patch via req.WorktreeDir, got payload:\n%s", reqPayload)
	}
}
