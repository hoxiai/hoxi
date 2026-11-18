package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestFileRevisionGuardBlocksUnreadOverwriteAndExternalConflict(t *testing.T) {
	wsDir := t.TempDir()
	existingFile := filepath.Join(wsDir, "critical.go")
	origContent := "package core\n\nconst SecretLogic = 42\n"
	if err := os.WriteFile(existingFile, []byte(origContent), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker := agent.NewFileRevisionTracker()

	// Step 1: Attempt blind `write` AND `apply_patch` Add File on unread existing file -> both MUST fail!
	blindWriteArgs, _ := json.Marshal(map[string]any{
		"filePath": "critical.go",
		"content":  "package core\n// wiped!\n",
	})
	blindPatchAddArgs, _ := json.Marshal(map[string]any{
		"patch": "*** Begin Patch\n*** Add File: critical.go\n+package core\n*** End Patch",
	})
	sseBlind := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"bw1","function":{"name":"write","arguments":` + mustJSONString(string(blindWriteArgs)) + `}},` +
			`{"index":1,"id":"bp1","function":{"name":"apply_patch","arguments":` + mustJSONString(string(blindPatchAddArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseStop := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	sc1 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseBlind, sseStop}}
	eng := agent.NewDefaultEngine(sc1)

	errs := map[string]string{}
	if err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_rev_1",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Overwrite critical.go",
		FileRevisions: tracker,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				errs[evt.CallID] = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(errs["bw1"], "without reading it first") {
		t.Fatalf("expected blind write to be rejected with read-first error, got: %q", errs["bw1"])
	}
	if !strings.Contains(errs["bp1"], "without reading it first") {
		t.Fatalf("expected blind apply_patch Add File to be rejected, got: %q", errs["bp1"])
	}
	diskNow, _ := os.ReadFile(existingFile)
	if string(diskNow) != origContent {
		t.Fatalf("critical.go must remain untouched after rejected blind writes, got: %q", string(diskNow))
	}

	// Step 2: Read `critical.go`, then simulate an external IDE modification before `edit` and `write`.
	readArgs, _ := json.Marshal(map[string]any{"filePath": "critical.go"})
	sseRead := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"r1","function":{"name":"read","arguments":` + mustJSONString(string(readArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	scRead := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseRead, sseStop}}
	if err := agent.NewDefaultEngine(scRead).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_rev_1",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Read critical.go",
		FileRevisions: tracker,
	}); err != nil {
		t.Fatalf("read turn failed: %v", err)
	}

	// User modifies critical.go externally in their IDE!
	userEdited := "package core\n\nconst SecretLogic = 99 // user hotfix\n"
	if err := os.WriteFile(existingFile, []byte(userEdited), 0o644); err != nil {
		t.Fatal(err)
	}

	editArgs, _ := json.Marshal(map[string]any{
		"filePath":  "critical.go",
		"oldString": "const SecretLogic = 99",
		"newString": "const SecretLogic = 0",
	})
	sseStale := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"se1","function":{"name":"edit","arguments":` + mustJSONString(string(editArgs)) + `}},` +
			`{"index":1,"id":"sw1","function":{"name":"write","arguments":` + mustJSONString(string(blindWriteArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	errs = map[string]string{}
	scStale := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseStale, sseStop}}
	if err := agent.NewDefaultEngine(scStale).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_rev_1",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Stale edit after user change",
		FileRevisions: tracker,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				errs[evt.CallID] = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("stale check turn failed: %v", err)
	}
	if !strings.Contains(errs["se1"], "modified on disk since it was last read") {
		t.Fatalf("expected stale edit to be blocked on external modification, got: %q", errs["se1"])
	}
	if !strings.Contains(errs["sw1"], "modified on disk since it was last read") {
		t.Fatalf("expected stale write to be blocked on external modification, got: %q", errs["sw1"])
	}
	diskNow, _ = os.ReadFile(existingFile)
	if string(diskNow) != userEdited {
		t.Fatalf("user external edit was overwritten! got: %q", string(diskNow))
	}

	// Step 3: Re-reading critical.go refreshes snapshot so subsequent write succeeds cleanly.
	validWriteArgs, _ := json.Marshal(map[string]any{
		"filePath": "critical.go",
		"content":  "package core\n\nconst SecretLogic = 100 // merged with user hotfix\n",
	})
	sseRefreshWrite := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"vw1","function":{"name":"write","arguments":` + mustJSONString(string(validWriteArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	scRefresh := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseRead, sseRefreshWrite, sseStop}}
	if err := agent.NewDefaultEngine(scRefresh).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_rev_1",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Read latest and write merged",
		FileRevisions: tracker,
	}); err != nil {
		t.Fatalf("refresh + write turn failed: %v", err)
	}
	diskNow, _ = os.ReadFile(existingFile)
	if !strings.Contains(string(diskNow), "SecretLogic = 100") {
		t.Fatalf("expected write after fresh read to succeed, got: %q", string(diskNow))
	}
}

func TestProgressiveEditRebasesMatchedIndentation(t *testing.T) {
	wsDir := t.TempDir()
	pyFile := filepath.Join(wsDir, "worker.py")
	src := "class Service:\n    def run(self):\n        if ready:\n            value = 1\n            return value\n"
	if err := os.WriteFile(pyFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Model omits the 8-space outer indentation on BOTH oldString and newString.
	editArgs, _ := json.Marshal(map[string]any{
		"filePath":  "worker.py",
		"oldString": "if ready:\n    value = 1\n    return value",
		"newString": "if ready:\n    value = 2\n    return value * 10",
	})
	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"e_ind","function":{"name":"edit","arguments":` + mustJSONString(string(editArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseStop := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sseStop}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_indent",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Update worker.py",
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	got, err := os.ReadFile(pyFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "class Service:\n    def run(self):\n        if ready:\n            value = 2\n            return value * 10\n"
	if string(got) != want {
		t.Fatalf("expected rebased indentation:\n%q\ngot:\n%q", want, string(got))
	}

	// Tab-indented Go file where model supplied 4-space outer indentation in TrimSpace match.
	goFile := filepath.Join(wsDir, "handler.go")
	goSrc := "package main\n\nfunc run() error {\n\tif ok {\n\t\tval := 1\n\t\treturn check(val)\n\t}\n\treturn nil\n}\n"
	if err := os.WriteFile(goFile, []byte(goSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	goEditArgs, _ := json.Marshal(map[string]any{
		"filePath":  "handler.go",
		"oldString": "    val := 1\n    return check(val)",
		"newString": "    val := 9\n    return check(val)",
	})
	sseGo := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"e_go_tab","function":{"name":"edit","arguments":` + mustJSONString(string(goEditArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	scGo := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseGo, sseStop}}
	if err := agent.NewDefaultEngine(scGo).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_indent_tab",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Update handler.go",
	}); err != nil {
		t.Fatalf("RunTurn go tab rebase failed: %v", err)
	}
	gotGo, err := os.ReadFile(goFile)
	if err != nil {
		t.Fatal(err)
	}
	wantGo := "package main\n\nfunc run() error {\n\tif ok {\n\t\tval := 9\n\t\treturn check(val)\n\t}\n\treturn nil\n}\n"
	if string(gotGo) != wantGo {
		t.Fatalf("expected tab-rebased Go output:\n%q\ngot:\n%q", wantGo, string(gotGo))
	}
}

func TestPartialReadBlocksFullWriteAndSymlinkForgetCleansDeletedSnapshot(t *testing.T) {
	baseDir := t.TempDir()
	realWS := filepath.Join(baseDir, "real_ws")
	if err := os.MkdirAll(realWS, 0o755); err != nil {
		t.Fatal(err)
	}
	symWS := filepath.Join(baseDir, "sym_ws")
	if err := os.Symlink(realWS, symWS); err != nil {
		t.Skipf("symlink unsupported in test env: %v", err)
	}

	multiLinePath := filepath.Join(realWS, "service.go")
	origTenLines := "package svc\n\nvar A = 1\nvar B = 2\nvar C = 3\nvar D = 4\nvar E = 5\nvar F = 6\nvar G = 7\nvar H = 8\n"
	if err := os.WriteFile(multiLinePath, []byte(origTenLines), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker := agent.NewFileRevisionTracker()
	sseStop := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	// 1. Read only lines 1-3 (`limit: 3`), then attempt full-file `write` -> MUST be blocked!
	partialReadArgs, _ := json.Marshal(map[string]any{"filePath": "service.go", "offset": 1, "limit": 3})
	blindOverwriteArgs, _ := json.Marshal(map[string]any{"filePath": "service.go", "content": "package svc\nvar A = 99\n"})
	ssePartialRead := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"pr1","function":{"name":"read","arguments":` + mustJSONString(string(partialReadArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseWriteAfterPartial := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"pw1","function":{"name":"write","arguments":` + mustJSONString(string(blindOverwriteArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	var writeErr string
	sc1 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{ssePartialRead, sseWriteAfterPartial, sseStop}}
	if err := agent.NewDefaultEngine(sc1).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_partial_sym",
		WorktreeDir:   symWS,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Partial read then write",
		FileRevisions: tracker,
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError && evt.CallID == "pw1" {
				writeErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(writeErr, "partial range") {
		t.Fatalf("expected full write after partial read to be rejected with partial-range message, got: %q", writeErr)
	}
	diskBytes, _ := os.ReadFile(multiLinePath)
	if string(diskBytes) != origTenLines {
		t.Fatalf("unread tail lines must remain intact, got: %q", string(diskBytes))
	}

	// 2. Targeted `edit` on line 3 after partial read MUST succeed, and `apply_patch` Delete File via symlinked root
	// must cleanly Forget the canonical real path even after os.Remove.
	targetedEditArgs, _ := json.Marshal(map[string]any{
		"filePath":  "service.go",
		"oldString": "var A = 1",
		"newString": "var A = 10",
	})
	deletePatchArgs, _ := json.Marshal(map[string]any{
		"patch": "*** Begin Patch\n*** Delete File: service.go\n*** End Patch",
	})
	sseEditAndDelete := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"te1","function":{"name":"edit","arguments":` + mustJSONString(string(targetedEditArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseDelete := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"del1","function":{"name":"apply_patch","arguments":` + mustJSONString(string(deletePatchArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sc2 := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseEditAndDelete, sseDelete, sseStop}}
	if err := agent.NewDefaultEngine(sc2).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_partial_sym",
		WorktreeDir:   symWS,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Edit then delete",
		FileRevisions: tracker,
	}); err != nil {
		t.Fatalf("edit + delete failed: %v", err)
	}

	// Recreate service.go externally with new content; if Forget failed to resolve symlink after os.Remove,
	// CheckWrite would say "modified on disk since it was last read" instead of "without reading it first",
	// or worse, CheckEdit wouldn't treat it as a fresh unread file.
	if err := os.WriteFile(multiLinePath, []byte("package svc\nvar Recreated = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errAfterForget := tracker.CheckWrite(filepath.Join(symWS, "service.go"), "service.go")
	if errAfterForget == nil || !strings.Contains(errAfterForget.Error(), "without reading it first") {
		t.Fatalf("expected deleted symlink path snapshot to be forgotten cleanly, got CheckWrite err=%v", errAfterForget)
	}
}

func TestStepRetryHonorsRetryAfterEmitsStatusAndRejectsQuotaOrOverBudget(t *testing.T) {
	// 1. 429 with Retry-After=35ms floor -> retries after >=35ms and emits step_retry + step_retry_done.
	var calls atomic.Int32
	rateLimited := &flakyStreamClient{
		onOpen: func() (*http.Response, error) {
			if calls.Add(1) == 1 {
				return nil, &llm.StreamHTTPError{
					StatusCode: http.StatusTooManyRequests,
					RetryAfter: 35 * time.Millisecond,
					Message:    "HTTP 429: rate_limit_error (burst TPM)",
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")),
				Header:     make(http.Header),
			}, nil
		},
	}

	eng := agent.NewDefaultEngine(rateLimited)
	eng.SetStepRetryDelays([]time.Duration{5 * time.Millisecond})

	var sawRetry, sawRetryDone bool
	started := time.Now()
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_rl_ok",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "anthropic",
		ModelID:     "claude-sonnet-4-5",
		UserPrompt:  "hello",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			switch evt.Kind {
			case agent.EngineStepRetry:
				sawRetry = true
				if delayMs, _ := evt.Metadata["delayMs"].(int64); delayMs < 35 {
					t.Errorf("expected Retry-After floor >=35ms, got delayMs=%d", delayMs)
				}
			case agent.EngineStepRetryDone:
				sawRetryDone = true
			}
		}),
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("expected 429 with Retry-After to recover, got: %v", err)
	}
	if elapsed < 30*time.Millisecond {
		t.Fatalf("expected Engine to honor Retry-After minimum floor (~35ms), elapsed=%v", elapsed)
	}
	if !sawRetry || !sawRetryDone {
		t.Fatalf("expected EngineStepRetry (%v) and EngineStepRetryDone (%v)", sawRetry, sawRetryDone)
	}

	// 2. Rejection paths: quota exhaustion, 401 auth error, and Retry-After exceeding turn retry budget must NOT retry.
	for _, tc := range []struct {
		name   string
		budget time.Duration
		err    error
	}{
		{
			name:   "quota_exhausted_429",
			budget: time.Minute,
			err: &llm.StreamHTTPError{
				StatusCode: http.StatusTooManyRequests,
				Message:    "HTTP 429: insufficient_quota — exceeded your current quota",
			},
		},
		{
			name:   "auth_401",
			budget: time.Minute,
			err: &llm.StreamHTTPError{
				StatusCode: http.StatusUnauthorized,
				Message:    "HTTP 401: invalid_api_key",
			},
		},
		{
			name:   "retry_after_exceeds_budget",
			budget: 20 * time.Millisecond,
			err: &llm.StreamHTTPError{
				StatusCode: http.StatusTooManyRequests,
				RetryAfter: 5 * time.Second,
				Message:    "HTTP 429: rate limit exceeded",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			fc := &flakyStreamClient{
				onOpen: func() (*http.Response, error) {
					n.Add(1)
					return nil, tc.err
				},
			}
			e := agent.NewDefaultEngine(fc)
			e.SetStepRetryDelays([]time.Duration{5 * time.Millisecond, 5 * time.Millisecond})
			e.SetMaxTurnRetryBudget(tc.budget)
			gotErr := e.RunTurn(context.Background(), agent.TurnRequest{
				SessionID:   "ses_reject_" + tc.name,
				WorktreeDir: t.TempDir(),
				Mode:        "build",
				ProviderID:  "openai",
				ModelID:     "gpt-5",
				UserPrompt:  "hi",
			})
			if gotErr == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if n.Load() != 1 {
				t.Fatalf("expected 0 retries (1 attempt) for %s, got %d attempts", tc.name, n.Load())
			}
		})
	}
}

func TestUTF8SafeTruncationAcrossMultibyteAndMalformedInputs(t *testing.T) {
	// Dense 3-byte CJK + 4-byte emoji + malformed raw byte sequence.
	segment := "中文工程架构校验🚀安全门控边界测试—"
	huge := strings.Repeat(segment, 2400) + string([]byte{0xff, 0xfe, 0x80}) + strings.Repeat("尾部多字节验证✨", 800)

	// 1. Verify PruneOldToolResults across small/large context windows never emits invalid UTF-8.
	msgs := make([]agent.Message, 0, 18)
	for i := 0; i < 9; i++ {
		msgs = append(msgs,
			agent.Message{
				Role:    agent.RoleAssistant,
				Content: []agent.ContentBlock{{Type: agent.ContentToolUse, ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"echo"}`)}},
			},
			agent.Message{
				Role:    agent.RoleUser,
				Content: []agent.ContentBlock{{Type: agent.ContentToolResult, ToolUseID: "c1", Text: huge}},
			},
		)
	}
	for _, limit := range []int{8000, 24000, 64000} {
		pruned := agent.PruneOldToolResults(msgs, "系统规则提示词", limit)
		for mi, m := range pruned {
			for bi, b := range m.Content {
				if !utf8.ValidString(b.Text) {
					t.Fatalf("limit=%d msg=%d block=%d produced invalid UTF-8 after PruneOldToolResults", limit, mi, bi)
				}
			}
		}
	}

	// 2. Verify >32 KiB bash tool output truncation preserves valid UTF-8 on both head and tail cuts.
	wsDir := t.TempDir()
	sseBash := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"b_utf8\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"emit-utf8\\\"}\"}}]}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n"
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseBash, sseDone}}

	var truncatedOut string
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_utf8_tool",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "run",
		CommandRunner: func(_ context.Context, _, _ string) (string, int, error) {
			return huge, 0, nil
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				truncatedOut = evt.Text
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(truncatedOut, "bytes truncated") {
		t.Fatalf("expected >32 KiB output to be truncated, len=%d", len(truncatedOut))
	}
	if !utf8.ValidString(truncatedOut) {
		t.Fatalf("truncated tool output contains invalid UTF-8 bytes")
	}
}

func TestCoordinateFileEditBlocksUnreadPeerClobberAndAllowsPostReadEdit(t *testing.T) {
	wsDir := t.TempDir()
	sharedPath := filepath.Join(wsDir, "shared.go")
	if err := os.WriteFile(sharedPath, []byte("package app\n\nvar A = 1\nvar B = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate session B receiving ModifiedByPeer=true after waiting on session A's edit to shared.go.
	if err := os.WriteFile(sharedPath, []byte("package app\n\nvar A = 2\nvar B = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	trackerB := agent.NewFileRevisionTracker()
	editArgs, _ := json.Marshal(map[string]any{
		"filePath":  "shared.go",
		"oldString": "var B = 1",
		"newString": "var B = 9",
	})
	readArgs, _ := json.Marshal(map[string]any{"filePath": "shared.go"})
	sseBlindEdit := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"eb1","function":{"name":"edit","arguments":` + mustJSONString(string(editArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseRead := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"rb1","function":{"name":"read","arguments":` + mustJSONString(string(readArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseValidEdit := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"eb2","function":{"name":"edit","arguments":` + mustJSONString(string(editArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseStop := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	var firstErr string
	var sawRunningWait bool
	var recordedPaths []string
	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseBlindEdit, sseRead, sseValidEdit, sseStop}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:     "ses_peer_b",
		WorktreeDir:   wsDir,
		Mode:          "build",
		ProviderID:    "openai",
		ModelID:       "gpt-5",
		UserPrompt:    "Edit shared.go",
		FileRevisions: trackerB,
		CoordinateFileEdit: func(_ context.Context, paths []string, onWait func(string)) (agent.FileEditCoordination, error) {
			if onWait != nil && len(paths) > 0 {
				onWait(paths[0])
			}
			return agent.FileEditCoordination{
				Release:        func() {},
				Waited:         true,
				ModifiedByPeer: map[string]bool{sharedPath: true},
			}, nil
		},
		RecordFileEdit: func(paths []string) {
			recordedPaths = append(recordedPaths, paths...)
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolRunning && evt.CallID == "eb1" {
				if out, _ := evt.Metadata["output"].(string); strings.Contains(out, "Waiting for another active task") {
					sawRunningWait = true
				}
			}
			if evt.Kind == agent.EngineToolError && evt.CallID == "eb1" {
				firstErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if !sawRunningWait {
		t.Fatal("expected live waiting progress output on ToolRunning metadata")
	}
	if !strings.Contains(firstErr, "modified on disk by another concurrent session") {
		t.Fatalf("expected unread peer-modified edit to require read first, got: %q", firstErr)
	}
	if len(recordedPaths) != 1 || filepath.Base(recordedPaths[0]) != "shared.go" {
		t.Fatalf("expected RecordFileEdit callback on succeeded edit, got: %v", recordedPaths)
	}
	gotDisk, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotDisk) != "package app\n\nvar A = 2\nvar B = 9\n" {
		t.Fatalf("expected combined edits after read+edit, got:\n%s", string(gotDisk))
	}
}
