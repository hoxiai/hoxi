package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestForegroundLogKeepsMiddleFailureAndReadablePage(t *testing.T) {
	const failure = "orders_test.go:123: unexpected rounding result"
	row := strings.Repeat("ordinary output 01234567890123456789012\n", 4000)
	content := row + failure + "\n" + row
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test-output.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := "cat test-output.txt; exit 7"
	if runtime.GOOS == "windows" {
		cmd = "type test-output.txt & exit 7"
		if sh, _ := defaultShellCommand(""); !strings.EqualFold(sh, "cmd") {
			cmd = "cat test-output.txt; exit 7"
		}
	}
	reg := newToolRegistry()
	tc := ToolContext{SessionID: "ses_logs", WorktreeDir: dir, commandLogs: &turnCommandLogs{}}
	run := func(tc ToolContext, args map[string]any) (ToolOutput, error) {
		raw, _ := json.Marshal(args)
		return reg.Execute(context.Background(), tc, ToolCall{Name: "bash", Input: raw}, nil)
	}
	out, err := run(tc, map[string]any{"command": cmd})
	id, _ := out.Metadata["logId"].(string)
	if err == nil || id == "" || !strings.Contains(out.Output, failure) || !strings.Contains(out.Output, id) {
		t.Fatalf("lost diagnostic or log ID (%v, %s): %s", err, id, out.Output)
	}
	args := map[string]any{"action": "output", "procId": id, "offset": len(row)}
	page, err := run(tc, args)
	if err != nil || !strings.HasPrefix(page.Output, failure) || page.Metadata["nextOffset"] != len(row)+commandLogPageBytes {
		t.Fatalf("unexpected page %+v, %v", page, err)
	}
	if _, err := run(ToolContext{commandLogs: &turnCommandLogs{}}, args); err == nil {
		t.Fatal("foreign turn read log")
	}
	args["offset"] = -1
	if _, err := run(tc, args); err == nil {
		t.Fatal("accepted invalid offset")
	}
}

func TestCommandLogRetentionAndUTF8PageBoundaries(t *testing.T) {
	s := &turnCommandLogs{}
	data := bytes.Repeat([]byte("日"), maxCommandLogBytes/3+5)
	var first, last string
	for i := 0; i <= maxTurnCommandLogs; i++ {
		c := &commandLogCapture{}
		_, _ = c.Write(data)
		out := ToolOutput{Output: "bounded preview"}
		s.finish(c, nil, &out)
		last = out.Metadata["logId"].(string)
		if i == 0 {
			first = last
		}
	}
	if _, err := s.page(first, 0); err == nil {
		t.Fatal("retained evicted output")
	}
	page, err := s.page(last, 0)
	if err != nil || page.Metadata["nextOffset"] != commandLogPageBytes/3*3 || strings.Contains(page.Output, "�") {
		t.Fatalf("split UTF-8 page: %+v", page.Metadata)
	}
	end, err := s.page(last, maxCommandLogBytes)
	if err != nil || !strings.Contains(end.Output, "4194304/4194318 bytes") {
		t.Fatalf("missing capture boundary: %s %v", end.Output, err)
	}
}

type scriptedBashLogClient struct {
	step     int
	nextCall func(step int, body []byte) string
}

func (c *scriptedBashLogClient) ResolveStreamTarget(provider, model string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   provider,
		ModelID:      model,
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
	}
}

func (c *scriptedBashLogClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	c.step++
	body := c.nextCall(c.step, req.Body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestEngineRunTurnForegroundBashLogPaging(t *testing.T) {
	const failure = "billing_test.go:42: expected 100 got 99"
	row := strings.Repeat("ordinary output 01234567890123456789012\n", 4000)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(row+failure+"\n"+row), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := "cat long.txt; exit 7"
	if runtime.GOOS == "windows" {
		cmd = "type long.txt & exit 7"
		if sh, _ := defaultShellCommand(""); !strings.EqualFold(sh, "cmd") {
			cmd = "cat long.txt; exit 7"
		}
	}

	var capturedLogID, pagedOutput string
	client := &scriptedBashLogClient{
		nextCall: func(step int, _ []byte) string {
			switch step {
			case 1:
				argsJSON, _ := json.Marshal(map[string]any{"command": cmd})
				argStr, _ := json.Marshal(string(argsJSON))
				return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_run\",\"function\":{\"name\":\"bash\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n", argStr)
			case 2:
				if capturedLogID == "" {
					t.Fatalf("Engine.RunTurn failed to capture foreground bash logId")
				}
				argsJSON, _ := json.Marshal(map[string]any{
					"action": "output",
					"procId": capturedLogID,
					"offset": len(row),
				})
				argStr, _ := json.Marshal(string(argsJSON))
				return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_page\",\"function\":{\"name\":\"bash\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\ndata: [DONE]\n", argStr)
			default:
				return "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"
			}
		},
	}

	err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_runturn_log",
		WorktreeDir: dir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-test",
		UserPrompt:  "run long command and page output",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.CallID == "call_run" && (evt.Kind == EngineToolError || evt.Kind == EngineToolCompleted) {
				if id, ok := evt.Metadata["logId"].(string); ok {
					capturedLogID = id
				}
			}
			if evt.CallID == "call_page" && evt.Kind == EngineToolCompleted {
				pagedOutput = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if capturedLogID == "" {
		t.Fatal("expected foreground bash run to record a logId inside Engine.RunTurn")
	}
	if !strings.HasPrefix(pagedOutput, failure) {
		t.Fatalf("expected paged bash output starting with %q, got %q", failure, pagedOutput)
	}
}
