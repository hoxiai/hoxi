package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

type orchestratorMockStream struct {
	mu          sync.Mutex
	calls       []capturedOrchestratorCall
	responderFn func(idx int, c capturedOrchestratorCall) string
}

type capturedOrchestratorCall struct {
	Model          string
	Effort         string
	PromptCacheKey string
	ToolNames      []string
	System         string
	MessageCount   int
	LastUserText   string
}

func (m *orchestratorMockStream) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		Protocol:     llm.ProtocolOpenAI,
		BaseURL:      "https://mock.local/v1",
		ContextLimit: 200000,
		OutputLimit:  8192,
		HasEndpoint:  true,
	}
}

func (m *orchestratorMockStream) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	var body struct {
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
		PromptCacheKey  string `json:"prompt_cache_key"`
		Tools           []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(req.Body, &body)
	var toolNames []string
	for _, tl := range body.Tools {
		toolNames = append(toolNames, tl.Function.Name)
	}
	var sys, lastUser string
	for _, msg := range body.Messages {
		s, _ := msg.Content.(string)
		if msg.Role == "system" {
			sys += s
		}
		if msg.Role == "user" {
			lastUser = s
		}
	}
	c := capturedOrchestratorCall{
		Model:          body.Model,
		Effort:         body.ReasoningEffort,
		PromptCacheKey: body.PromptCacheKey,
		ToolNames:      toolNames,
		System:         sys,
		MessageCount:   len(body.Messages),
		LastUserText:   lastUser,
	}
	m.mu.Lock()
	idx := len(m.calls)
	m.calls = append(m.calls, c)
	m.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(m.responderFn(idx, c))),
	}, nil
}

func sseToolCallChunk(id, name string, args map[string]any) string {
	argBytes, _ := json.Marshal(args)
	argStr, _ := json.Marshal(string(argBytes))
	return fmt.Sprintf(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%s}}]}}]}\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n"+
			"data: [DONE]\n",
		id, name, string(argStr),
	)
}

func sseTextChunk(text string) string {
	quoted, _ := json.Marshal(text)
	return fmt.Sprintf(
		"data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n"+
			"data: [DONE]\n",
		string(quoted),
	)
}

func TestOrchestratorToolsetDelegationStickyResumeAndReceipt(t *testing.T) {
	wsDir := t.TempDir()
	var sc *orchestratorMockStream
	sc = &orchestratorMockStream{
		responderFn: func(idx int, c capturedOrchestratorCall) string {
			switch idx {
			case 0:
				// Orchestrator Step 1: delegate edit + test to executor.
				return sseToolCallChunk("call_exec_1", "task", map[string]any{
					"subagent_type": "executor",
					"description":   "Create hello.txt and run check",
					"prompt":        "Write hello.txt with 'v1' and run echo ok",
				})
			case 1:
				// Executor Step 1: write file.
				return sseToolCallChunk("call_w_1", "write", map[string]any{
					"filePath": "hello.txt",
					"content":  "v1\n",
				})
			case 2:
				// Executor Step 2: run bash command.
				return sseToolCallChunk("call_b_1", "bash", map[string]any{
					"command": "echo ok",
				})
			case 3:
				// Executor Step 3: finish first delegation.
				return sseTextChunk("Created hello.txt and verified echo ok.")
			case 4:
				// Orchestrator Step 2: review receipt and send follow-up revision (should sticky-resume!).
				return sseToolCallChunk("call_exec_2", "task", map[string]any{
					"subagent_type": "executor",
					"description":   "Update hello.txt to v2",
					"prompt":        "Change hello.txt to v2",
				})
			case 5:
				// Executor (sticky resumed!) updates file.
				return sseToolCallChunk("call_w_2", "write", map[string]any{
					"filePath": "hello.txt",
					"content":  "v2\n",
				})
			case 6:
				// Executor finishes follow-up.
				return sseTextChunk("Updated hello.txt to v2.")
			default:
				// Orchestrator final summary.
				return sseTextChunk("All changes verified.")
			}
		},
	}

	eng := NewDefaultEngine(sc)
	var taskOutputs []string
	var finalText strings.Builder
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_orch_1",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "orch-pro",
		Effort:      "xhigh",
		Orchestrator: &OrchestratorConfig{
			ExecutorProviderID: "google",
			ExecutorModelID:    "exec-flash",
			ExecutorEffort:     "medium",
		},
		CommandRunner: func(_ context.Context, _ string, command string) (string, int, error) {
			return "ok\n", 0, nil
		},
		UserPrompt: "Implement hello.txt",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.ParentCallID == "" && evt.Kind == EngineToolCompleted && evt.ToolName == "task" {
				taskOutputs = append(taskOutputs, evt.Text)
			}
			if evt.ParentCallID == "" && evt.Kind == EngineTextEnd {
				finalText.WriteString(evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if finalText.String() != "All changes verified." {
		t.Fatalf("unexpected final output: %q", finalText.String())
	}
	raw, err := os.ReadFile(filepath.Join(wsDir, "hello.txt"))
	if err != nil || string(raw) != "v2\n" {
		t.Fatalf("expected hello.txt to contain v2, err=%v content=%q", err, string(raw))
	}

	sc.mu.Lock()
	calls := append([]capturedOrchestratorCall(nil), sc.calls...)
	sc.mu.Unlock()
	if len(calls) < 8 {
		t.Fatalf("expected 8 LLM calls, got %d", len(calls))
	}

	// 1. Orchestrator tools omit direct write/edit/patch/bash while retaining read + task.
	orchTools := map[string]bool{}
	for _, n := range calls[0].ToolNames {
		orchTools[n] = true
	}
	for _, forbidden := range []string{"write", "edit", "multiedit", "apply_patch", "bash"} {
		if orchTools[forbidden] {
			t.Fatalf("Orchestrator must not expose direct %q tool, got %v", forbidden, calls[0].ToolNames)
		}
	}
	if !orchTools["read"] || !orchTools["task"] {
		t.Fatalf("Orchestrator must keep read and task tools, got %v", calls[0].ToolNames)
	}
	if !strings.Contains(calls[0].System, "<orchestrator_protocol>") {
		t.Fatalf("expected <orchestrator_protocol> in Orchestrator system prompt")
	}

	// 2. Executor uses configured ExecutorModel & independent ExecutorEffort, and has write/bash.
	if calls[1].Model != "exec-flash" || calls[1].Effort != "medium" {
		t.Fatalf("expected Executor model=exec-flash effort=medium, got model=%q effort=%q", calls[1].Model, calls[1].Effort)
	}
	execTools := map[string]bool{}
	for _, n := range calls[1].ToolNames {
		execTools[n] = true
	}
	if !execTools["write"] || !execTools["bash"] {
		t.Fatalf("Executor must have write and bash tools, got %v", calls[1].ToolNames)
	}

	// 3. Second delegation auto-resumes sticky Executor thread (larger message count & same cache key).
	if calls[5].MessageCount <= calls[1].MessageCount {
		t.Fatalf("expected sticky Executor resume to preserve prior messages: first=%d second=%d", calls[1].MessageCount, calls[5].MessageCount)
	}
	if calls[5].PromptCacheKey != calls[1].PromptCacheKey {
		t.Fatalf("expected sticky Executor calls to share cache key: %q vs %q", calls[1].PromptCacheKey, calls[5].PromptCacheKey)
	}

	// 4. Factual <executor_receipt> includes modified files, command status, and thread state.
	if len(taskOutputs) != 2 {
		t.Fatalf("expected 2 task outputs, got %d", len(taskOutputs))
	}
	if !strings.Contains(taskOutputs[0], "<executor_receipt>") ||
		!strings.Contains(taskOutputs[0], "modified_files (1): hello.txt") ||
		!strings.Contains(taskOutputs[0], "[ok] echo ok") ||
		!strings.Contains(taskOutputs[0], "thread: fresh") {
		t.Fatalf("first executor receipt missing facts:\n%s", taskOutputs[0])
	}
	if !strings.Contains(taskOutputs[1], "thread: resumed") {
		t.Fatalf("second executor receipt should report resumed thread:\n%s", taskOutputs[1])
	}
}

func TestOrchestratorBlocksHallucinatedDirectWriteAndRespectsExecutorPermissionDeny(t *testing.T) {
	wsDir := t.TempDir()
	sc := &orchestratorMockStream{
		responderFn: func(idx int, c capturedOrchestratorCall) string {
			switch idx {
			case 0:
				// Orchestrator hallucinates a direct write call.
				return sseToolCallChunk("call_bad_w", "write", map[string]any{
					"filePath": "blocked.txt",
					"content":  "nope",
				})
			case 1:
				// Orchestrator then delegates to executor.
				return sseToolCallChunk("call_exec_deny", "task", map[string]any{
					"subagent_type": "executor",
					"description":   "Attempt write blocked by approval",
					"prompt":        "Write blocked.txt",
				})
			case 2:
				// Executor attempts write (denied by PermissionHook).
				return sseToolCallChunk("call_exec_w", "write", map[string]any{
					"filePath": "blocked.txt",
					"content":  "denied",
				})
			case 3:
				return sseTextChunk("Write was denied by approval policy.")
			default:
				return sseTextChunk("Stopped because approval denied.")
			}
		},
	}

	eng := NewDefaultEngine(sc)
	var directErr string
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_orch_deny",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "orch-pro",
		Orchestrator: &OrchestratorConfig{
			ExecutorProviderID: "google",
			ExecutorModelID:    "exec-flash",
		},
		PermissionHook: func(_ context.Context, req PermissionRequest) error {
			if req.ToolName == "write" {
				return fmt.Errorf("approval denied for write")
			}
			return nil
		},
		UserPrompt: "Write blocked.txt",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.ParentCallID == "" && evt.Kind == EngineToolError && evt.ToolName == "write" {
				directErr = evt.ErrorText
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(directErr, "Orchestrator mode") {
		t.Fatalf("expected Orchestrator mode rejection error for direct write, got %q", directErr)
	}
	if _, statErr := os.Stat(filepath.Join(wsDir, "blocked.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("blocked.txt must not be created when Orchestrator direct write and Executor approval both block")
	}
}

type mockSteerSource struct {
	mu      sync.Mutex
	pending []string
}

func (s *mockSteerSource) Push(msg string) {
	s.mu.Lock()
	s.pending = append(s.pending, msg)
	s.mu.Unlock()
}

func (s *mockSteerSource) Drain() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	s.pending = nil
	return out
}

func (s *mockSteerSource) HasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

func (s *mockSteerSource) NotifyCh() <-chan struct{} { return nil }

func TestOrchestratorExecutorYieldsEarlyOnParentSteer(t *testing.T) {
	wsDir := t.TempDir()
	steer := &mockSteerSource{}
	sc := &orchestratorMockStream{
		responderFn: func(idx int, _ capturedOrchestratorCall) string {
			switch idx {
			case 0:
				return sseToolCallChunk("call_exec_steer", "task", map[string]any{
					"subagent_type": "executor",
					"description":   "Multi-step work",
					"prompt":        "Run step 1 then step 2",
				})
			case 1:
				// Executor Step 1 runs a bash command; during that command, user sends a steer!
				return sseToolCallChunk("call_exec_b1", "bash", map[string]any{
					"command": "echo step1",
				})
			default:
				// Orchestrator should immediately regain control without Executor running Step 2.
				return sseTextChunk("Adapted to user steer.")
			}
		},
	}

	eng := NewDefaultEngine(sc)
	var receiptText string
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_orch_steer",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "orch-pro",
		Orchestrator: &OrchestratorConfig{
			ExecutorProviderID: "google",
			ExecutorModelID:    "exec-flash",
		},
		Steer: steer,
		CommandRunner: func(_ context.Context, _ string, _ string) (string, int, error) {
			steer.Push("Actually switch to option B")
			return "step1\n", 0, nil
		},
		UserPrompt: "Do option A",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.ParentCallID == "" && evt.Kind == EngineToolCompleted && evt.ToolName == "task" {
				receiptText = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(receiptText, "status: yielded_for_user_steer") {
		t.Fatalf("expected Executor to yield early with yielded_for_user_steer receipt, got:\n%s", receiptText)
	}
	sc.mu.Lock()
	callCount := len(sc.calls)
	sc.mu.Unlock()
	if callCount != 3 {
		t.Fatalf("expected exactly 3 LLM calls (orch -> exec step1 -> orch after steer), got %d", callCount)
	}
}

type missingExecutorEndpointStream struct {
	*orchestratorMockStream
	missingProvider string
}

func (m *missingExecutorEndpointStream) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	if providerID == m.missingProvider {
		return llm.StreamTarget{ProviderID: providerID, ModelID: modelID, HasEndpoint: false}
	}
	return m.orchestratorMockStream.ResolveStreamTarget(providerID, modelID)
}

func TestOrchestratorFailsClosedWhenExecutorEndpointMissing(t *testing.T) {
	wsDir := t.TempDir()
	base := &orchestratorMockStream{
		responderFn: func(idx int, _ capturedOrchestratorCall) string {
			if idx == 0 {
				return sseToolCallChunk("call_missing_exec", "task", map[string]any{
					"subagent_type": "executor",
					"description":   "Edit with missing executor endpoint",
					"prompt":        "Write fail.txt",
				})
			}
			return sseTextChunk("Reported missing executor endpoint.")
		},
	}
	sc := &missingExecutorEndpointStream{orchestratorMockStream: base, missingProvider: "disconnected"}
	eng := NewDefaultEngine(sc)
	var taskErr string
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_orch_missing_exec",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "orch-pro",
		Orchestrator: &OrchestratorConfig{
			ExecutorProviderID: "disconnected",
			ExecutorModelID:    "exec-gone",
		},
		UserPrompt: "Edit something",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.ParentCallID == "" && evt.Kind == EngineToolError && evt.ToolName == "task" {
				taskErr = evt.ErrorText
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(taskErr, "no configured HTTP endpoint") {
		t.Fatalf("expected fail-closed missing executor endpoint error, got %q", taskErr)
	}
	base.mu.Lock()
	calls := append([]capturedOrchestratorCall(nil), base.calls...)
	base.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls (orchestrator only, never fallback execution), got %d", len(calls))
	}
}

type failMidRunExecutorStream struct {
	*orchestratorMockStream
}

func (m *failMidRunExecutorStream) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	m.mu.Lock()
	n := len(m.calls)
	m.mu.Unlock()
	if req.Target.ModelID == "exec-flash" && n >= 2 {
		return nil, fmt.Errorf("upstream executor stream broke mid-run")
	}
	return m.orchestratorMockStream.OpenStream(ctx, req)
}

func TestOrchestratorPreservesReceiptOnExecutorMidRunError(t *testing.T) {
	wsDir := t.TempDir()
	base := &orchestratorMockStream{
		responderFn: func(idx int, _ capturedOrchestratorCall) string {
			switch idx {
			case 0:
				return sseToolCallChunk("call_exec_partial", "task", map[string]any{
					"subagent_type": "executor",
					"description":   "Write partial file then fail",
					"prompt":        "Write partial.txt",
				})
			case 1:
				return sseToolCallChunk("call_partial_w", "write", map[string]any{
					"filePath": "partial.txt",
					"content":  "saved before crash\n",
				})
			default:
				return sseTextChunk("Saw partial receipt after executor failure.")
			}
		},
	}
	sc := &failMidRunExecutorStream{orchestratorMockStream: base}
	eng := NewDefaultEngine(sc)
	eng.SetStepRetryDelays([]time.Duration{0})
	var errOutput string
	err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_orch_partial_err",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "orch-pro",
		Orchestrator: &OrchestratorConfig{
			ExecutorProviderID: "google",
			ExecutorModelID:    "exec-flash",
		},
		UserPrompt: "Do partial edit",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.ParentCallID == "" && evt.Kind == EngineToolError && evt.ToolName == "task" {
				errOutput = evt.ErrorText
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !strings.Contains(errOutput, "<executor_receipt>") || !strings.Contains(errOutput, "modified_files (1): partial.txt") {
		t.Fatalf("expected factual <executor_receipt> on executor mid-run failure, got:\n%s", errOutput)
	}
}
