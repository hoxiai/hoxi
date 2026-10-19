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
