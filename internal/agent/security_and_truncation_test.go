package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestGrepReadAndWriteBlockSymlinkAndSensitiveEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test")
	}
	root := t.TempDir()
	wsDir := filepath.Join(root, "workspace")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(wsDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(outsideDir, "secret.env"), []byte("OUTSIDE_SECRET_MARKER=leak1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "plain.txt"), []byte("OUTSIDE_PLAIN_MARKER=leak2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, ".env"), []byte("INREPO_DOTENV_MARKER=leak3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, ".git", "config"), []byte("GIT_CONFIG_MARKER=leak4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "safe.go"), []byte("package demo // SAFE_MARKER_OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ordinary-named symlinks pointing to outside files, in-repo .env, and .git/config
	if err := os.Symlink(filepath.Join(outsideDir, "secret.env"), filepath.Join(wsDir, "notes_ext_env.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "plain.txt"), filepath.Join(wsDir, "notes_ext_plain.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(wsDir, ".env"), filepath.Join(wsDir, "notes_local_env.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(wsDir, ".git", "config"), filepath.Join(wsDir, "notes_git.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(wsDir, "linked_out_dir")); err != nil {
		t.Fatal(err)
	}

	grepArgs, _ := json.Marshal(map[string]any{
		"pattern": "OUTSIDE_SECRET_MARKER|OUTSIDE_PLAIN_MARKER|INREPO_DOTENV_MARKER|GIT_CONFIG_MARKER|SAFE_MARKER_OK",
	})
	readExtArgs, _ := json.Marshal(map[string]any{"filePath": "notes_ext_env.txt"})
	readDotEnvLinkArgs, _ := json.Marshal(map[string]any{"filePath": "notes_local_env.txt"})
	writeEscapeArgs, _ := json.Marshal(map[string]any{"filePath": "linked_out_dir/escaped.txt", "content": "pwned"})

	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"g1","function":{"name":"grep","arguments":` + mustJSONString(string(grepArgs)) + `}},` +
			`{"index":1,"id":"r1","function":{"name":"read","arguments":` + mustJSONString(string(readExtArgs)) + `}},` +
			`{"index":2,"id":"r2","function":{"name":"read","arguments":` + mustJSONString(string(readDotEnvLinkArgs)) + `}},` +
			`{"index":3,"id":"w1","function":{"name":"write","arguments":` + mustJSONString(string(writeEscapeArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sse2 := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sse2}}
	eng := agent.NewDefaultEngine(sc)

	completed := map[string]string{}
	errored := map[string]string{}
	if err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_symlink_guard",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Check symlink safety",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			switch evt.Kind {
			case agent.EngineToolCompleted:
				completed[evt.CallID] = evt.Text
			case agent.EngineToolError:
				errored[evt.CallID] = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	grepOut := completed["g1"]
	if !strings.Contains(grepOut, "SAFE_MARKER_OK") {
		t.Fatalf("expected grep to find SAFE_MARKER_OK in safe.go, got:\n%s", grepOut)
	}
	for _, forbidden := range []string{"OUTSIDE_SECRET_MARKER", "OUTSIDE_PLAIN_MARKER", "INREPO_DOTENV_MARKER", "GIT_CONFIG_MARKER"} {
		if strings.Contains(grepOut, forbidden) {
			t.Fatalf("grep leaked restricted symlink/secret content %q:\n%s", forbidden, grepOut)
		}
	}
	if errored["r1"] == "" {
		t.Fatalf("expected read of symlink escaping workspace to error, got completed=%q", completed["r1"])
	}
	if errored["r2"] == "" {
		t.Fatalf("expected read of symlink targeting .env to error, got completed=%q", completed["r2"])
	}
	if errored["w1"] == "" {
		t.Fatalf("expected write through symlinked directory outside workspace to error, got completed=%q", completed["w1"])
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatal("outside file must not be created through symlinked directory")
	}
}

func TestAtomicEditsPreserveExecutableFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode test")
	}
	wsDir := t.TempDir()
	scriptPath := filepath.Join(wsDir, "deploy.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho v1\necho step2\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	editArgs, _ := json.Marshal(map[string]any{
		"filePath":  "deploy.sh",
		"oldString": "echo v1",
		"newString": "echo v2",
	})
	multiArgs, _ := json.Marshal(map[string]any{
		"filePath": "deploy.sh",
		"edits": []map[string]any{
			{"oldString": "echo v2", "newString": "echo v3"},
		},
	})
	patchArgs, _ := json.Marshal(map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: deploy.sh",
			"*** Move to: run-deploy.sh",
			"@@",
			" #!/bin/sh",
			"-echo v3",
			"+echo v4",
			" echo step2",
			"*** Add File: created.txt",
			"+hello",
			"*** End Patch",
		}, "\n"),
	})

	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"e1","function":{"name":"edit","arguments":` + mustJSONString(string(editArgs)) + `}},` +
			`{"index":1,"id":"m1","function":{"name":"multiedit","arguments":` + mustJSONString(string(multiArgs)) + `}},` +
			`{"index":2,"id":"p1","function":{"name":"apply_patch","arguments":` + mustJSONString(string(patchArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sse2 := "data: {\"choices\":[{\"delta\":{\"content\":\"modes preserved\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sse2}}
	eng := agent.NewDefaultEngine(sc)
	if err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_mode_keep",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Update executable script",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	movedInfo, err := os.Stat(filepath.Join(wsDir, "run-deploy.sh"))
	if err != nil {
		t.Fatalf("expected moved script run-deploy.sh: %v", err)
	}
	if movedInfo.Mode().Perm() != 0o755 {
		t.Fatalf("expected executable permission 0755 preserved after edit/multiedit/apply_patch move, got %04o", movedInfo.Mode().Perm())
	}
	createdInfo, err := os.Stat(filepath.Join(wsDir, "created.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if createdInfo.Mode().Perm() != 0o644 {
		t.Fatalf("expected newly added file to have mode 0644 instead of 0600, got %04o", createdInfo.Mode().Perm())
	}
}

func TestGeneralSubagentInheritsProjectInstructionsAndParentConstraints(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "AGENTS.md"), []byte("MANDATORY_AGENTS_MD_GUARDRAIL_42"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := &inspectableStreamClient{
		responder: func(idx int, call recordedLLMCall) string {
			if idx == 0 {
				genArgs, _ := json.Marshal(map[string]any{
					"subagent_type": "general",
					"description":   "apply change",
					"prompt":        "Check rules",
				})
				return strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tg1","function":{"name":"task","arguments":` + mustJSONString(string(genArgs)) + `}}]}}]}`,
					`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`data: [DONE]`,
				}, "\n") + "\n"
			}
			return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		},
	}

	parentSys := strings.Join([]string{
		"<project_memory>\nMEM_INVARIANT_7\n</project_memory>",
		"<session_handoff_memory>\nLONG_SESSION_HANDOFF_NOISE\n</session_handoff_memory>",
		"PARENT_WORKSPACE_RULE_99",
	}, "\n\n")

	eng := agent.NewDefaultEngine(sc)
	if err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_inherit",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		System:      parentSys,
		UserPrompt:  "Run general subagent",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	sc.mu.Lock()
	calls := append([]recordedLLMCall(nil), sc.calls...)
	sc.mu.Unlock()

	var generalSys string
	for _, c := range calls {
		if strings.Contains(c.System, "Subagent role: general.") {
			generalSys = c.System
		}
	}
	if generalSys == "" {
		t.Fatal("expected general subagent LLM call")
	}
	for _, mustHave := range []string{"MANDATORY_AGENTS_MD_GUARDRAIL_42", "MEM_INVARIANT_7", "PARENT_WORKSPACE_RULE_99"} {
		if !strings.Contains(generalSys, mustHave) {
			t.Fatalf("expected general subagent system prompt to inherit %q, got:\n%s", mustHave, generalSys)
		}
	}
	if strings.Contains(generalSys, "LONG_SESSION_HANDOFF_NOISE") {
		t.Fatalf("expected session handoff noise stripped from subagent system prompt, got:\n%s", generalSys)
	}
}

func TestOutputLengthTruncationPropagatesFinishAndSummarizeRejectsTruncated(t *testing.T) {
	// 1. Summarize must reject truncated length/max_tokens summaries.
	truncSumSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"### Partial checkpoint cut off mid-"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sumClient := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{truncSumSSE}}
	engSum := agent.NewDefaultEngine(sumClient)
	if _, err := engSum.Summarize(context.Background(), "openai", "gpt-5", "some transcript"); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("expected Summarize to reject length-truncated summary, got err=%v", err)
	}

	// 2. RunTurn: empty-text length truncation (e.g. dropped truncated tool JSON) recovers via continuation step;
	// visible-text length truncation emits EngineTurnFinish{Finish: "length"} instead of "stop".
	step1DroppedToolSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_big","function":{"name":"write","arguments":"{\"filePath\":\"huge.go\",\"content\":\"cut"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	step2VisibleTruncSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Partial long explanation that hit max_tokens"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	turnClient := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{step1DroppedToolSSE, step2VisibleTruncSSE}}
	engTurn := agent.NewDefaultEngine(turnClient)
	var gotFinish string
	if err := engTurn.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_trunc",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Generate output",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineTurnFinish {
				gotFinish = evt.Finish
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if turnClient.callCount.Load() != 2 {
		t.Fatalf("expected 2 steps (1 continuation after empty-text length truncation), got %d", turnClient.callCount.Load())
	}
	if gotFinish != "length" {
		t.Fatalf("expected EngineTurnFinish Finish=%q on truncated output, got %q", "length", gotFinish)
	}
}
