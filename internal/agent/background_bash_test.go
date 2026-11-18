package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestBashBackgroundLifecycleIncrementalOutputAndSessionIsolation(t *testing.T) {
	wsDir := t.TempDir()
	defer agent.DeleteSessionBackgroundProcs("ses_bg_main")
	defer agent.DeleteSessionBackgroundProcs("ses_bg_other")

	startArgs, _ := json.Marshal(map[string]any{
		"command":           "printf 'SERVER_BOOT_OK\\n'; sleep 0.15; printf 'REQUEST_HANDLED_200\\n'; sleep 20",
		"run_in_background": true,
	})
	pollArgs, _ := json.Marshal(map[string]any{
		"action": "output",
		"procId": "bg_1",
		"waitMs": 2500,
	})
	listArgs, _ := json.Marshal(map[string]any{
		"action": "list",
	})
	stopArgs, _ := json.Marshal(map[string]any{
		"action": "stop",
		"procId": "bg_1",
	})

	sse1 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_start","function":{"name":"bash","arguments":` + mustJSONString(string(startArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sse2 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[` +
			`{"index":0,"id":"c_poll","function":{"name":"bash","arguments":` + mustJSONString(string(pollArgs)) + `}},` +
			`{"index":1,"id":"c_list","function":{"name":"bash","arguments":` + mustJSONString(string(listArgs)) + `}}` +
			`]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sse3 := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_stop","function":{"name":"bash","arguments":` + mustJSONString(string(stopArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	sseDone := "data: {\"choices\":[{\"delta\":{\"content\":\"all verified\"},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"

	outputs := map[string]string{}
	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse1, sse2, sse3, sseDone}}
	if err := agent.NewDefaultEngine(sc).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_bg_main",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Start server, inspect logs, then stop",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				outputs[evt.CallID] = evt.Text
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if !strings.Contains(outputs["c_start"], "procId=bg_1") || !strings.Contains(outputs["c_start"], "SERVER_BOOT_OK") {
		t.Fatalf("expected initial background startup banner with procId=bg_1, got: %q", outputs["c_start"])
	}
	// If REQUEST_HANDLED_200 arrived after the 450ms grace or during the grace, it appears in either c_start or c_poll without losing bytes.
	combined := outputs["c_start"] + "\n" + outputs["c_poll"]
	if !strings.Contains(combined, "REQUEST_HANDLED_200") {
		t.Fatalf("expected incremental output to capture REQUEST_HANDLED_200, got start=%q poll=%q", outputs["c_start"], outputs["c_poll"])
	}
	if !strings.Contains(outputs["c_list"], "bg_1: running") {
		t.Fatalf("expected list output to show bg_1 running, got: %q", outputs["c_list"])
	}
	if !strings.Contains(outputs["c_stop"], "status=exited") {
		t.Fatalf("expected stop output to confirm exited state, got: %q", outputs["c_stop"])
	}

	// Rejection path 1: another session must NOT be able to read or stop ses_bg_main's procId.
	var crossErr string
	scCross := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sse3, sseDone}}
	if err := agent.NewDefaultEngine(scCross).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_bg_other",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Try stopping another session's procId",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError {
				crossErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("cross-session check turn failed: %v", err)
	}
	if !strings.Contains(crossErr, "not found in this session") {
		t.Fatalf("expected cross-session procId access to be rejected, got: %q", crossErr)
	}

	// Rejection path 2: immediate startup failure exits non-zero right away.
	failArgs, _ := json.Marshal(map[string]any{
		"command":           "printf 'EADDRINUSE: port 5173 busy\\n' >&2; exit 7",
		"run_in_background": true,
	})
	sseFail := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c_fail","function":{"name":"bash","arguments":` + mustJSONString(string(failArgs)) + `}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n") + "\n"
	var failOut, failErr string
	scFail := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseFail, sseDone}}
	if err := agent.NewDefaultEngine(scFail).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_bg_main",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Start failing server",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError && evt.CallID == "c_fail" {
				failOut = evt.Text
				failErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("fail check turn failed: %v", err)
	}
	if !strings.Contains(failErr, "code 7") || !strings.Contains(failErr, "EADDRINUSE") {
		t.Fatalf("expected immediate startup failure to surface exit code 7 and stderr, got err=%q out=%q", failErr, failOut)
	}

	// Regression: polling bash(action="output"|"list") must NOT count as a workspace mutation that resets identical-read loop blocking.
	sampleFile := filepath.Join(wsDir, "note.txt")
	if err := os.WriteFile(sampleFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]any{"filePath": "note.txt"})
	mkReadAndPollStep := func(stepIdx int) string {
		return strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[` +
				`{"index":0,"id":"r_` + string(rune('0'+stepIdx)) + `","function":{"name":"read","arguments":` + mustJSONString(string(readArgs)) + `}},` +
				`{"index":1,"id":"l_` + string(rune('0'+stepIdx)) + `","function":{"name":"bash","arguments":"{\"action\":\"list\"}"}}` +
				`]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}, "\n") + "\n"
	}
	var blockedReadErr string
	scLoop := &stubStreamClient{
		proto:     llm.ProtocolOpenAI,
		responses: []string{mkReadAndPollStep(1), mkReadAndPollStep(2), mkReadAndPollStep(3), mkReadAndPollStep(4), sseDone},
	}
	if err := agent.NewDefaultEngine(scLoop).RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_bg_main",
		WorktreeDir: wsDir,
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "Read repeatedly with bash list in between",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolError && evt.ToolName == "read" {
				blockedReadErr = evt.ErrorText
			}
		}),
	}); err != nil {
		t.Fatalf("loop guard turn failed: %v", err)
	}
	if !strings.Contains(blockedReadErr, "[loop guard] Blocked repeated read call") {
		t.Fatalf("expected identical read #4 to stay blocked despite intervening bash(action=list), got: %q", blockedReadErr)
	}
}
