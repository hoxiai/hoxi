package agent_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

type inspectStreamClient struct {
	proto    llm.StreamProtocol
	lastReq  llm.StreamHTTPRequest
	response string
}

func (c *inspectStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com",
		Protocol:     c.proto,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (c *inspectStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.lastReq = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(c.response)),
		Header:     make(http.Header),
	}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestMCPStdioHelperProcess acts as a real stdio JSON-RPC 2.0 MCP server subprocess when invoked in tests.
func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("HOXI_TEST_MCP_STDIO") != "1" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
			_, _ = os.Stdout.Write(append(resp, '\n'))
		case "tools/call":
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "stdio-mcp-ok"}},
				},
			})
			_, _ = os.Stdout.Write(append(resp, '\n'))
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestPhase3MultimodalDriversAndSummarize(t *testing.T) {
	mediaBlocks := []agent.ContentBlock{
		{Type: agent.ContentText, Text: "Inspect diagram and spec"},
		{Type: agent.ContentMedia, MimeType: "image/png", Data: "iVBORw0KGgo=", Filename: "arch.png"},
		{Type: agent.ContentMedia, MimeType: "application/pdf", Data: "JVBERi0xLjQK", Filename: "spec.pdf"},
	}

	cases := []struct {
		proto    llm.StreamProtocol
		sse      string
		mustHave []string
	}{
		{
			proto:    llm.ProtocolAnthropic,
			sse:      "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n",
			mustHave: []string{`"type":"image"`, `"media_type":"image/png"`, `"type":"document"`, `"media_type":"application/pdf"`},
		},
		{
			proto:    llm.ProtocolOpenAI,
			sse:      "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n",
			mustHave: []string{`"type":"image_url"`, `"data:image/png;base64,iVBORw0KGgo="`, `"type":"file"`, `"filename":"spec.pdf"`},
		},
		{
			proto:    llm.ProtocolOpenAIResponses,
			sse:      "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":1}}}\n\n",
			mustHave: []string{`"type":"input_image"`, `"type":"input_file"`, `"filename":"spec.pdf"`},
		},
		{
			proto:    llm.ProtocolGoogle,
			sse:      "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n",
			mustHave: []string{`"inlineData"`, `"mimeType":"image/png"`, `"mimeType":"application/pdf"`},
		},
	}

	for _, tc := range cases {
		client := &inspectStreamClient{proto: tc.proto, response: tc.sse}
		eng := agent.NewDefaultEngine(client)
		err := eng.RunTurn(context.Background(), agent.TurnRequest{
			SessionID:   "ses_mm",
			WorktreeDir: t.TempDir(),
			Mode:        "build",
			ProviderID:  "test",
			ModelID:     "model",
			UserBlocks:  mediaBlocks,
		})
		if err != nil {
			t.Fatalf("protocol %s RunTurn failed: %v", tc.proto, err)
		}
		rawBody := string(client.lastReq.Body)
		for _, sub := range tc.mustHave {
			if !strings.Contains(rawBody, sub) {
				t.Fatalf("protocol %s request body missing %q:\n%s", tc.proto, sub, rawBody)
			}
		}
	}

	sumClient := &inspectStreamClient{
		proto:    llm.ProtocolOpenAI,
		response: "data: {\"choices\":[{\"delta\":{\"content\":\"## Goal\\nShip Native Phase 3\"}}]}\ndata: [DONE]\n",
	}
	sumEng := agent.NewDefaultEngine(sumClient)
	gotSummary, err := sumEng.Summarize(context.Background(), "openai", "gpt-5", "[USER]\nBuild Go Agent")
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if !strings.Contains(gotSummary, "Ship Native Phase 3") {
		t.Fatalf("unexpected summary: %q", gotSummary)
	}
}

func TestPhase3ApplyPatchTodoQuestionImpactWebfetchAndMCP(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("remove me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agent.SetMCPHTTPTransportForTest(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var rpc struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		var out map[string]any
		switch rpc.Method {
		case "tools/list":
			out = map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "ping_docs", "description": "Query docs", "inputSchema": map[string]any{"type": "object"}},
					},
				},
			}
		case "tools/call":
			out = map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "mcp-pong-ok"}},
				},
			}
		}
		b, _ := json.Marshal(out)
		hdr := make(http.Header)
		hdr.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: http.StatusOK, Header: hdr, Body: io.NopCloser(bytes.NewReader(b))}, nil
	}))
	defer agent.SetMCPHTTPTransportForTest(nil)

	todoArgs, _ := json.Marshal(map[string]any{
		"todos": []map[string]any{
			{"id": "1", "content": "Check impact", "status": "completed"},
			{"id": "2", "content": "Apply multi-file patch", "status": "in_progress"},
		},
	})
	qArgs, _ := json.Marshal(map[string]any{
		"questions": []map[string]any{
			{"header": "Target", "question": "Which greeting?", "options": []map[string]any{{"label": "hello-world"}}},
		},
	})
	impArgs, _ := json.Marshal(map[string]any{"symbol": "Greet"})
	badPatchArgs, _ := json.Marshal(map[string]any{
		"patch": "*** Begin Patch\n*** Delete File: old.txt\n*** Update File: app.go\n@@\n-\tnon_existent_line()\n+\treturn \"broken\"\n*** End Patch",
	})
	goodPatchArgs, _ := json.Marshal(map[string]any{
		"patch": "*** Begin Patch\n*** Add File: added.go\n+package app\n+\n+const Tag = \"v3\"\n*** Delete File: old.txt\n*** Update File: app.go\n@@\n func Greet() string {\n-\treturn \"hi\"\n+\treturn \"hello-world\"\n }\n*** End Patch",
	})
	blockedFetchArgs, _ := json.Marshal(map[string]any{"url": "http://localhost:8080/internal"})
	mcpListArgs, _ := json.Marshal(map[string]any{"server": "docs"})
	mcpCallArgs, _ := json.Marshal(map[string]any{"server": "docs", "tool": "ping_docs", "arguments": map[string]any{"q": "hoxi"}})
	stdioCallArgs, _ := json.Marshal(map[string]any{"server": "localtool", "tool": "echo", "arguments": map[string]any{}})

	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"td1","function":{"name":"todowrite","arguments":` + mustJSONString(string(todoArgs)) + `}},` +
			`{"index":1,"id":"qu1","function":{"name":"question","arguments":` + mustJSONString(string(qArgs)) + `}},` +
			`{"index":2,"id":"im1","function":{"name":"impact_analysis","arguments":` + mustJSONString(string(impArgs)) + `}},` +
			`{"index":3,"id":"wf1","function":{"name":"webfetch","arguments":` + mustJSONString(string(blockedFetchArgs)) + `}},` +
			`{"index":4,"id":"ml1","function":{"name":"mcp_list_tools","arguments":` + mustJSONString(string(mcpListArgs)) + `}},` +
			`{"index":5,"id":"mc1","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(string(mcpCallArgs)) + `}},` +
			`{"index":6,"id":"ms1","function":{"name":"mcp_call_tool","arguments":` + mustJSONString(string(stdioCallArgs)) + `}},` +
			`{"index":7,"id":"bp1","function":{"name":"apply_patch","arguments":` + mustJSONString(string(badPatchArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sse2 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"gp1","function":{"name":"apply_patch","arguments":` + mustJSONString(string(goodPatchArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sse3 := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"All Phase 3 tools verified."}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sse2, sse3}}
	eng := agent.NewDefaultEngine(sc)

	completed := make(map[string]agent.EngineEvent)
	errored := make(map[string]agent.EngineEvent)

	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_phase3",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Run full phase 3 suite",
		AskQuestion: func(_ context.Context, callID, _ string) (string, map[string]any, error) {
			return `User has answered your questions: "Which greeting?"="hello-world".`, map[string]any{"callID": callID, "answers": [][]string{{"hello-world"}}}, nil
		},
		ImpactInspect: func(_ context.Context, _, _, sym string, _, _ int) (string, error) {
			return "Symbol: " + sym + "\nDirect Callers:\n- cmd/main.go:10", nil
		},
		MCPConfig: func() map[string]any {
			return map[string]any{
				"mcp": map[string]any{
					"docs": map[string]any{"type": "remote", "url": "https://mcp.example.com/rpc", "enabled": true},
					"localtool": map[string]any{
						"type":        "local",
						"command":     []any{os.Args[0], "-test.run=TestMCPStdioHelperProcess"},
						"environment": map[string]any{"HOXI_TEST_MCP_STDIO": "1"},
						"enabled":     true,
					},
				},
			}
		},
		Diagnostics: func(_ context.Context, _, target string) string {
			return "[vet ok " + filepath.Base(target) + "]"
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			switch evt.Kind {
			case agent.EngineToolCompleted:
				completed[evt.CallID] = evt
			case agent.EngineToolError:
				errored[evt.CallID] = evt
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if _, ok := errored["bp1"]; !ok {
		t.Fatal("expected bad apply_patch (bp1) to fail atomically")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(statErr) {
		t.Fatal("expected good apply_patch to delete old.txt")
	}
	addedBytes, err := os.ReadFile(filepath.Join(dir, "added.go"))
	if err != nil || !strings.Contains(string(addedBytes), `const Tag = "v3"`) {
		t.Fatalf("expected added.go content, got err=%v body=%q", err, string(addedBytes))
	}
	appBytes, err := os.ReadFile(filepath.Join(dir, "app.go"))
	if err != nil || !strings.Contains(string(appBytes), `"hello-world"`) {
		t.Fatalf("expected updated app.go, got err=%v body=%q", err, string(appBytes))
	}
	if !strings.Contains(completed["gp1"].Text, "[vet ok added.go]") || !strings.Contains(completed["gp1"].Text, "[vet ok app.go]") {
		t.Fatalf("expected batch diagnostics on apply_patch, got:\n%s", completed["gp1"].Text)
	}

	if _, ok := errored["wf1"]; !ok || !strings.Contains(errored["wf1"].ErrorText, "blocked") {
		t.Fatalf("expected webfetch localhost block, got: %+v", errored["wf1"])
	}
	if completed["td1"].Metadata["todos"] == nil {
		t.Fatal("expected todos metadata on todowrite event")
	}
	if !strings.Contains(completed["qu1"].Text, "hello-world") {
		t.Fatalf("unexpected question output: %q", completed["qu1"].Text)
	}
	if !strings.Contains(completed["im1"].Text, "Direct Callers:") {
		t.Fatalf("unexpected impact_analysis output: %q", completed["im1"].Text)
	}
	if !strings.Contains(completed["ml1"].Text, "ping_docs") {
		t.Fatalf("unexpected mcp_list_tools output: %q", completed["ml1"].Text)
	}
	if strings.TrimSpace(completed["mc1"].Text) != "mcp-pong-ok" {
		t.Fatalf("unexpected remote mcp_call_tool output: %q", completed["mc1"].Text)
	}
	if strings.TrimSpace(completed["ms1"].Text) != "stdio-mcp-ok" {
		t.Fatalf("unexpected stdio mcp_call_tool output: %q", completed["ms1"].Text)
	}
}

type overflowStreamClient struct {
	calls []string
}

func (c *overflowStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com",
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (c *overflowStreamClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	body := string(req.Body)
	c.calls = append(c.calls, body)
	switch len(c.calls) {
	case 1:
		return nil, errors.New(`HTTP 400: {"error":{"message":"This model's maximum context length is 128000 tokens.","code":"context_length_exceeded"}}`)
	case 2:
		sse := "data: {\"choices\":[{\"delta\":{\"content\":\"## Checkpoint\\nRetained key context.\"}}]}\ndata: [DONE]\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
	default:
		sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Recovered after reactive compaction.\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
	}
}

func TestPhase3ReactiveContextOverflowCompaction(t *testing.T) {
	oc := &overflowStreamClient{}
	eng := agent.NewDefaultEngine(oc)

	var finalOut string
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_overflow",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		History: []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "Initial architecture design"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "Completed phase 1 and phase 2."}}},
			{Role: agent.RoleUser, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "Run diagnostics"}}},
			{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
				{Type: agent.ContentToolUse, ID: "call_99", Name: "read", Input: json.RawMessage(`{"filePath":"main.go"}`)},
			}},
			{Role: agent.RoleUser, Content: []agent.ContentBlock{
				{Type: agent.ContentToolResult, ToolUseID: "call_99", Text: "package main\nfunc main() {}"},
			}},
		},
		UserPrompt: "Finish implementation",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineTextEnd {
				finalOut = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected reactive compaction recovery, got error: %v", err)
	}
	if len(oc.calls) != 3 {
		t.Fatalf("expected 3 upstream calls (overflow -> summarize -> retry), got %d", len(oc.calls))
	}
	if !strings.Contains(oc.calls[2], "context_checkpoint") || !strings.Contains(oc.calls[2], "Retained key context.") {
		t.Fatalf("expected compacted checkpoint in retried request body:\n%s", oc.calls[2])
	}
	if finalOut != "Recovered after reactive compaction." {
		t.Fatalf("unexpected final output: %q", finalOut)
	}
}

type subtaskConcurrencyClient struct {
	inFlight    int32
	maxInFlight int32
	mu          chan struct{}
	parentDone  bool
}

func (c *subtaskConcurrencyClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com",
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (c *subtaskConcurrencyClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	body := string(req.Body)
	c.mu <- struct{}{}
	isParentFirst := !c.parentDone
	if isParentFirst {
		c.parentDone = true
	}
	<-c.mu

	if isParentFirst {
		t1, _ := json.Marshal(map[string]any{"description": "sub-1", "prompt": "inspect pkg A"})
		t2, _ := json.Marshal(map[string]any{"description": "sub-2", "prompt": "inspect pkg B"})
		t3, _ := json.Marshal(map[string]any{"description": "sub-3", "prompt": "inspect pkg C"})
		sse := strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[` +
				`{"index":0,"id":"sub_call_1","function":{"name":"task","arguments":` + mustJSONString(string(t1)) + `}},` +
				`{"index":1,"id":"sub_call_2","function":{"name":"task","arguments":` + mustJSONString(string(t2)) + `}},` +
				`{"index":2,"id":"sub_call_3","function":{"name":"task","arguments":` + mustJSONString(string(t3)) + `}}` +
				`]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}, "\n") + "\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
	}
	if strings.Contains(body, "sub-1-done") && strings.Contains(body, "sub-2-done") && strings.Contains(body, "sub-3-done") {
		sse := "data: {\"choices\":[{\"delta\":{\"content\":\"All subtasks merged.\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
	}

	c.mu <- struct{}{}
	c.inFlight++
	if c.inFlight > c.maxInFlight {
		c.maxInFlight = c.inFlight
	}
	<-c.mu
	defer func() {
		c.mu <- struct{}{}
		c.inFlight--
		<-c.mu
	}()

	tag := "sub-unknown"
	switch {
	case strings.Contains(body, "inspect pkg A"):
		tag = "sub-1-done"
	case strings.Contains(body, "inspect pkg B"):
		tag = "sub-2-done"
	case strings.Contains(body, "inspect pkg C"):
		tag = "sub-3-done"
	}
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"" + tag + "\"}}]}\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
}

func TestPhase3SubtaskBoundedConcurrencyAndParentCallID(t *testing.T) {
	sc := &subtaskConcurrencyClient{mu: make(chan struct{}, 1)}
	eng := agent.NewDefaultEngine(sc)
	eng.SetMaxConcurrentSubtasks(2)

	seenStarts := make(map[string]bool)
	seenEnds := make(map[string]bool)
	evMu := make(chan struct{}, 1)

	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_sub_conc",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Fan out 3 subagents",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			evMu <- struct{}{}
			if evt.Kind == agent.EngineSubtaskStart && evt.ParentCallID != "" {
				seenStarts[evt.ParentCallID] = true
			}
			if evt.Kind == agent.EngineSubtaskEnd && evt.ParentCallID != "" {
				seenEnds[evt.ParentCallID] = true
			}
			<-evMu
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if sc.maxInFlight > 2 {
		t.Fatalf("expected bounded subtask concurrency <= 2, got %d", sc.maxInFlight)
	}
	for _, cid := range []string{"sub_call_1", "sub_call_2", "sub_call_3"} {
		if !seenStarts[cid] || !seenEnds[cid] {
			t.Fatalf("missing child lifecycle events for %s: starts=%v ends=%v", cid, seenStarts, seenEnds)
		}
	}
}
