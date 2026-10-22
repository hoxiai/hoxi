package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
)

func TestRunnerProxyHidesEngineSessionsAndPreservesStreaming(t *testing.T) {
	root := t.TempDir()
	service := runner.NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := service.Store().Put(runner.SessionRecord{ID: "ses_visible", Runtime: "opencode", ProjectDir: root, WorktreePath: root, SandboxMode: "local", OpenCodeIDs: []string{"ses_engine"}}); err != nil {
		t.Fatal(err)
	}
	service.ConfigureOpenCode("http://127.0.0.1:1", "")
	response := &http.Response{StatusCode: 200, Header: make(http.Header), Request: httptest.NewRequest("GET", "/session", nil), Body: io.NopCloser(strings.NewReader(`[{"id":"ses_engine"},{"id":"ses_other"},{"id":"ses_unregistered","metadata":{"hoxiConversation":"ses_visible"}}]`))}
	if err := filterRunnerResponse(response, service); err != nil {
		t.Fatal(err)
	}
	var list []map[string]interface{}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["id"] != "ses_other" {
		t.Fatalf("engine handles leaked: %v", list)
	}
	response.Header.Set("Content-Type", "text/event-stream")
	input := ": keepalive\n\ndata: {\"payload\":{\"type\":\"message.part.delta\",\"properties\":{\"sessionID\":\"ses_engine\"}}}\n\ndata: {\"payload\":{\"type\":\"message.part.delta\",\"properties\":{\"sessionID\":\"ses_other\",\"delta\":\"hello\"}}}\n\n"
	response.Body = io.NopCloser(strings.NewReader(input))
	if err := filterRunnerResponse(response, service); err != nil {
		t.Fatal(err)
	}
	// Tiny reads exercise partial frames without dropping or duplicating bytes.
	var out strings.Builder
	buf := make([]byte, 3)
	for {
		n, err := response.Body.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(out.String(), "ses_engine") || !strings.Contains(out.String(), "hello") || !strings.Contains(out.String(), ": keepalive") {
		t.Fatalf("bad filtered stream: %s", out.String())
	}
}

func TestRunnerProxyCleansToolDisplayInMessageHistoryAndStreams(t *testing.T) {
	t.Parallel()
	service := runner.NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions"))
	marker := "# hoxi-stop:ses_other:call_1"
	part := runner.MessagePart{
		ID: "prt_1", Type: "tool", Tool: "bash", SessionID: "ses_other", CallID: "call_1",
		State: &runner.ToolState{Status: "running", Input: map[string]interface{}{"command": marker + "\ntrap : EXIT\ngit status"}, Title: strings.TrimPrefix(marker, "# "), Output: marker},
	}
	turn := runner.MessageTurn{Parts: []runner.MessagePart{part}}
	for _, path := range []string{"/session/ses_other/message?directory=/tmp/project", "/session/ses_other/message/msg_1?directory=/tmp/project"} {
		t.Run(path, func(t *testing.T) {
			var body interface{} = []runner.MessageTurn{turn}
			if strings.Contains(path, "/msg_1") {
				body = turn
			}
			raw, _ := json.Marshal(body)
			response := &http.Response{StatusCode: 200, Header: make(http.Header), Request: httptest.NewRequest("GET", path, nil), Body: io.NopCloser(strings.NewReader(string(raw)))}
			if err := filterRunnerResponse(response, service); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), `"command":"git status"`) || !strings.Contains(string(got), `"title":"git status"`) || !strings.Contains(string(got), `"output":"`+marker+`"`) {
				t.Fatalf("history display or real output changed incorrectly: %s", got)
			}
			if response.ContentLength != int64(len(got)) {
				t.Fatal("response length was not updated")
			}
		})
	}
	for _, global := range []bool{false, true} {
		event := map[string]interface{}{"type": "message.part.updated", "properties": map[string]interface{}{"part": part}}
		if global {
			event = map[string]interface{}{"directory": "/tmp/project", "payload": event}
		}
		raw, _ := json.Marshal(event)
		input := ": keepalive\r\n\r\ndata: " + string(raw) + "\r\n\r\n"
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(input))}
		if err := filterRunnerResponse(response, service); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		buf := make([]byte, 3)
		for {
			n, err := response.Body.Read(buf)
			out.Write(buf[:n])
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		got := out.String()
		if !strings.Contains(got, `"command":"git status"`) || !strings.Contains(got, `"callID":"call_1"`) || !strings.Contains(got, `"title":"git status"`) || !strings.Contains(got, `"output":"`+marker+`"`) || !strings.HasSuffix(got, "\r\n\r\n") || !strings.HasPrefix(got, ": keepalive\r\n\r\n") {
			t.Fatalf("stream display, stop identity or boundaries regressed: %q", got)
		}
	}
}
