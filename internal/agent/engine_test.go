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

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

type stubStreamClient struct {
	proto     llm.StreamProtocol
	responses []string
	callCount atomic.Int32
}

func (s *stubStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com/v1",
		Protocol:     s.proto,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (s *stubStreamClient) OpenStream(_ context.Context, _ llm.StreamHTTPRequest) (*http.Response, error) {
	idx := int(s.callCount.Add(1)) - 1
	body := ""
	if idx < len(s.responses) {
		body = s.responses[idx]
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestEngineParallelReadAndProgressiveEdit(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(targetFile, []byte("package main\n\nfunc hello() string {\n\treturn \"old\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read","arguments":"{\"filePath\":\"main.go\"}"}},{"index":1,"id":"c2","function":{"name":"grep","arguments":"{\"pattern\":\"hello\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	editArgs, _ := json.Marshal(map[string]any{
		"filePath":  "main.go",
		"oldString": "func hello() string {\nreturn \"old\"\n}",
		"newString": "func hello() string {\n\treturn \"new\"\n}",
	})
	sse2 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c3","function":{"name":"edit","arguments":` + mustJSONString(string(editArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sse3 := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Updated hello() successfully."}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sc := &stubStreamClient{
		proto:     llm.ProtocolOpenAI,
		responses: []string{sse1, sse2, sse3},
	}
	eng := agent.NewDefaultEngine(sc)

	var events []agent.EngineEvent
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_test",
		WorktreeDir: dir,
		Mode:        "code",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Update hello() to return new",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			events = append(events, evt)
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	updated, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `return "new"`) {
		t.Fatalf("expected updated file content, got:\n%s", string(updated))
	}
	if sc.callCount.Load() != 3 {
		t.Fatalf("expected 3 LLM steps, got %d", sc.callCount.Load())
	}
	var gotText strings.Builder
	for _, e := range events {
		if e.Kind == agent.EngineTextDelta {
			gotText.WriteString(e.Text)
		}
	}
	if gotText.String() != "Updated hello() successfully." {
		t.Fatalf("unexpected text output: %q", gotText.String())
	}
}

func TestEngineHooksDiagnosticsAndCommandRunner(t *testing.T) {
	dir := t.TempDir()

	writeArgs, _ := json.Marshal(map[string]any{
		"filePath": "broken.go",
		"content":  "package main\n",
	})
	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w1","function":{"name":"write","arguments":` + mustJSONString(string(writeArgs)) + `}},{"index":1,"id":"b1","function":{"name":"bash","arguments":"{\"command\":\"go test ./...\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sse2 := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"All checks verified."}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sc := &stubStreamClient{
		proto:     llm.ProtocolOpenAI,
		responses: []string{sse1, sse2},
	}
	eng := agent.NewDefaultEngine(sc)

	var runnerCalled atomic.Bool
	var diagCalled atomic.Bool
	var toolOutputs []string

	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_hooks",
		WorktreeDir: dir,
		Mode:        "code",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Write broken.go and run tests",
		CommandRunner: func(_ context.Context, gotDir, cmd string) (string, int, error) {
			runnerCalled.Store(true)
			if gotDir != dir || cmd != "go test ./..." {
				t.Errorf("unexpected CommandRunner args: dir=%q cmd=%q", gotDir, cmd)
			}
			return "ok\texample/pkg\t0.01s", 0, nil
		},
		Diagnostics: func(_ context.Context, gotDir, filePath string) string {
			diagCalled.Store(true)
			if gotDir != dir || filepath.Base(filePath) != "broken.go" {
				t.Errorf("unexpected Diagnostics args: dir=%q file=%q", gotDir, filePath)
			}
			return "[go vet .]\nbroken.go:1:1: sample vet warning"
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				toolOutputs = append(toolOutputs, evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if !runnerCalled.Load() {
		t.Fatal("expected CommandRunner hook to be invoked for bash tool")
	}
	if !diagCalled.Load() {
		t.Fatal("expected Diagnostics hook to be invoked after write tool")
	}
	joinedOut := strings.Join(toolOutputs, "\n---\n")
	if !strings.Contains(joinedOut, "Diagnostics:\n[go vet .]\nbroken.go:1:1: sample vet warning") {
		t.Fatalf("expected Diagnostics feedback in write tool output, got:\n%s", joinedOut)
	}
	if !strings.Contains(joinedOut, "ok\texample/pkg\t0.01s") {
		t.Fatalf("expected CommandRunner output in bash tool output, got:\n%s", joinedOut)
	}
}

func TestEngineMultiEditCodeIndexAndSubtask(t *testing.T) {
	dir := t.TempDir()
	svcFile := filepath.Join(dir, "service.go")
	initial := "package demo\n\nconst version = \"v1\"\n\nfunc Name() string {\n\treturn \"alpha\"\n}\n"
	if err := os.WriteFile(svcFile, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 1: parallel code_search + symbol_inspect + task subagent call!
	csArgs, _ := json.Marshal(map[string]any{"query": "Name"})
	siArgs, _ := json.Marshal(map[string]any{"symbol": "Name"})
	taskArgs, _ := json.Marshal(map[string]any{"description": "check version", "prompt": "Report version constant"})
	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"cs1","function":{"name":"code_search","arguments":` + mustJSONString(string(csArgs)) + `}},{"index":1,"id":"si1","function":{"name":"symbol_inspect","arguments":` + mustJSONString(string(siArgs)) + `}},{"index":2,"id":"tk1","function":{"name":"task","arguments":` + mustJSONString(string(taskArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	// Subagent turn response (invoked inside task tool).
	sseSub := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"version is currently v1 in service.go:3"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	// Step 2: multiedit updating both version and Name() atomically in one file write.
	meArgs, _ := json.Marshal(map[string]any{
		"filePath": "service.go",
		"edits": []map[string]any{
			{"oldString": `const version = "v1"`, "newString": `const version = "v2"`},
			{"oldString": `return "alpha"`, "newString": `return "beta"`},
		},
	})
	sse2 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"me1","function":{"name":"multiedit","arguments":` + mustJSONString(string(meArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sse3 := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Upgraded to v2 and beta."}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sc := &stubStreamClient{
		proto:     llm.ProtocolOpenAI,
		responses: []string{sse1, sseSub, sse2, sse3},
	}
	eng := agent.NewDefaultEngine(sc)

	var diagCount atomic.Int32
	var toolResults []string
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_phase2",
		WorktreeDir: dir,
		Mode:        "code",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Upgrade demo service",
		CodeSearch: func(_ context.Context, _, query, _ string, _ int) ([]agent.CodeSearchHit, error) {
			return []agent.CodeSearchHit{{Path: "service.go", Symbol: query, StartLine: 5, EndLine: 7, Score: 9.5, Snippet: "func Name() string"}}, nil
		},
		SymbolInspect: func(_ context.Context, _, _, sym string, _ bool) (string, error) {
			return "Definition: service.go:5-7 (" + sym + ")", nil
		},
		Diagnostics: func(_ context.Context, _, _ string) string {
			diagCount.Add(1)
			return ""
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				toolResults = append(toolResults, evt.ToolName+":"+evt.Text)
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	updated, err := os.ReadFile(svcFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `"v2"`) || !strings.Contains(string(updated), `"beta"`) {
		t.Fatalf("expected multiedit to update both hunks atomically, got:\n%s", string(updated))
	}
	if diagCount.Load() != 1 {
		t.Fatalf("expected multiedit to run Diagnostics once, got %d", diagCount.Load())
	}
	allOut := strings.Join(toolResults, "\n")
	for _, want := range []string{"code_search:service.go:5-7", "symbol_inspect:Definition: service.go:5-7", "task:version is currently v1"} {
		if !strings.Contains(allOut, want) {
			t.Fatalf("missing expected tool output %q in:\n%s", want, allOut)
		}
	}
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
