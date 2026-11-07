package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func handoffTestTurns(sessionID string, toolState ToolState) []MessageTurn {
	state := toolState
	return []MessageTurn{
		{
			Info:  MessageInfo{ID: "msg_h1", SessionID: sessionID, Role: "user"},
			Parts: []MessagePart{{ID: "prt_h1", Type: "text", Text: "run the tests"}},
		},
		{
			Info: MessageInfo{ID: "msg_h2", SessionID: sessionID, Role: "assistant"},
			Parts: []MessagePart{
				{ID: "prt_h2", Type: "text", Text: "Running the test suite."},
				{ID: "prt_h3", Type: "tool", Tool: "bash", State: &state},
			},
		},
	}
}

// A tool aborted before a runtime switch is finalized later. The frozen history
// OpenCode resends every turn must already show that final state.
func TestRuntimeContextStableWhenFrozenToolIsFinalizedLater(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	input := map[string]interface{}{"command": "go test ./..."}
	base := SessionRecord{
		ID:               "ses_hoxi_handoff_settle",
		Runtime:          "opencode",
		ExternalID:       "ses_opc_handoff_settle",
		ProjectDir:       dir,
		WorktreePath:     dir,
		ContextTurnCount: 2,
	}

	running := base
	running.Turns = handoffTestTurns(base.ID, ToolState{Status: "running", Input: input, Time: &PartTime{Start: 100}})
	before := runtimeContext(running)

	finalized := base
	finalized.Turns = handoffTestTurns(base.ID, ToolState{Status: "error", Input: input, Error: interruptedToolError, Time: &PartTime{Start: 100, End: 200}})
	after := runtimeContext(finalized)

	if before == "" {
		t.Fatal("expected handoff context for the frozen history")
	}
	if before != after {
		t.Fatalf("frozen history changed after the tool was finalized:\nbefore: %s\nafter:  %s", before, after)
	}
	if !strings.Contains(before, "Tool bash [error]") || !strings.Contains(before, interruptedToolError) {
		t.Fatalf("expected the unfinished tool to read as interrupted, got: %s", before)
	}
	if got := running.Turns[1].Parts[1].State.Status; got != "running" {
		t.Fatalf("runtimeContext must not change the session record, tool status is %q", got)
	}
}

func TestSettledHandoffTurnsKeepsTaskAndFinishedTools(t *testing.T) {
	t.Parallel()
	turns := []MessageTurn{{Parts: []MessagePart{
		{Type: "tool", Tool: "task", State: &ToolState{Status: "running"}},
		{Type: "tool", Tool: "read", State: &ToolState{Status: "completed"}},
	}}}
	out := settledHandoffTurns(turns)
	if &out[0] != &turns[0] {
		t.Fatal("expected the turns to be returned as is when nothing is in flight")
	}
	if out[0].Parts[0].State.Status != "running" || out[0].Parts[1].State.Status != "completed" {
		t.Fatalf("unexpected statuses: %q, %q", out[0].Parts[0].State.Status, out[0].Parts[1].State.Status)
	}
}

// A steer must send the same system prompt bytes as the turn it joins, or the
// provider's prompt cache misses for the whole conversation.
func TestSteerOpenCodeTurnSendsSameSystemAsStart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	rec := SessionRecord{
		ID:               "ses_hoxi_steer_system",
		Runtime:          "opencode",
		ExternalID:       "ses_opc_steer_system",
		ProjectDir:       root,
		WorktreePath:     root,
		SandboxMode:      "local",
		ProviderID:       "test",
		ModelID:          "model",
		ContextTurnCount: 2,
		Turns:            handoffTestTurns("ses_hoxi_steer_system", ToolState{Status: "completed", Input: map[string]interface{}{"command": "go test ./..."}, Output: "ok"}),
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	startSystem := make(chan string, 1)
	steerSystem := make(chan string, 1)
	releaseMsg := make(chan struct{})
	var statusBusy atomic.Bool
	statusBusy.Store(true)
	readSystem := func(r *http.Request) string {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		system, _ := body["system"].(string)
		return system
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/session/ses_opc_steer_system/message":
			if r.Method == http.MethodPost {
				startSystem <- readSystem(r)
				<-releaseMsg
				writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
				return
			}
			writeJSON(w, http.StatusOK, []MessageTurn{})
		case "/session/ses_opc_steer_system/prompt_async":
			steerSystem <- readSystem(r)
			writeJSON(w, http.StatusOK, true)
		case "/session/status":
			status := map[string]SessionStatus{}
			if statusBusy.Load() {
				status[rec.ExternalID] = SessionStatus{Type: "busy"}
			}
			writeJSON(w, http.StatusOK, status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")

	ch, unsub := svc.Subscribe(rec.ID)
	defer unsub()

	const baseSystem = "Base system rules."
	parts := []MessagePart{{Type: "text", Text: "continue"}}
	if err := svc.startOpenCodeTurn(rec, "build", "model", "", baseSystem, MessageInfo{ID: "msg_s1", SessionID: rec.ID, Role: "user"}, parts, parts); err != nil {
		t.Fatal(err)
	}
	started := <-startSystem

	current, _ := svc.store.Get(rec.ID)
	steerParts := []MessagePart{{Type: "text", Text: "also check lint"}}
	if _, _, err := svc.steerOpenCodeTurn(context.Background(), current, "build", "model", "test", "", baseSystem, "also check lint", steerParts, nil); err != nil {
		t.Fatal(err)
	}
	steered := <-steerSystem

	close(releaseMsg)
	statusBusy.Store(false)

	if !strings.Contains(started, "<session_handoff_memory>") {
		t.Fatalf("expected the start system prompt to carry the handoff history, got: %q", started)
	}
	if steered != started {
		t.Fatalf("steer system prompt differs from start:\nstart: %q\nsteer: %q", started, steered)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if st, ok := evt.Properties["status"].(SessionStatus); ok && evt.Type == "session.status" && st.Type == "idle" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the turn to settle")
		}
	}
}
