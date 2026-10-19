package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func switchRuntime(t *testing.T, s *Service, id, rt, dir string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	body := fmt.Sprintf(`{"runtime":%q,"providerID":%q,"modelID":"model"}`, rt, rt)
	s.handleRuntimeSwitch(w, httptest.NewRequest("POST", "/runtime?directory="+dir, strings.NewReader(body)), id)
	return w
}

func TestRuntimeSwitchPreservesConversationAndSandbox(t *testing.T) {
	root := initTestGitRepo(t)
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	meta, err := svc.worktrees.Create("ses_original")
	if err != nil {
		t.Fatal(err)
	}
	origin := SessionInfo{ID: "ses_original", Directory: root, Title: "Existing chat", Time: SessionTime{Created: 10, Updated: 20}}
	turns := []MessageTurn{{Info: MessageInfo{ID: "msg_old", SessionID: origin.ID, Role: "user"}, Parts: []MessagePart{{ID: "prt_old", SessionID: origin.ID, MessageID: "msg_old", Type: "text", Text: "Remember project requirement"}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, _ := r.BasicAuth()
		if user != "opencode" || password != "secret" {
			t.Error("missing daemon auth")
		}
		if r.URL.Query().Get("directory") != meta.Directory {
			t.Errorf("wrong directory: %s", r.URL)
		}
		switch r.URL.Path {
		case "/session/ses_original":
			writeJSON(w, 200, origin)
		case "/session/status":
			writeJSON(w, 200, map[string]SessionStatus{})
		case "/session/ses_original/message":
			writeJSON(w, 200, turns)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	for _, rt := range []string{"claude-code", "antigravity", "opencode"} {
		w := switchRuntime(t, svc, origin.ID, rt, meta.Directory)
		if w.Code != 200 {
			t.Fatalf("switch %s: %d %s", rt, w.Code, w.Body)
		}
		rec, ok := svc.store.Get(origin.ID)
		if !ok || rec.Runtime != rt || len(rec.Turns) != 1 || rec.WorktreePath != meta.Directory || rec.ProjectDir != root || rec.Branch != meta.Branch || rec.BaseCommit != meta.BaseCommit {
			t.Fatalf("lost conversation/sandbox: %+v", rec)
		}
		if rec.ExternalID != "" || !rec.ContextPending || !strings.Contains(runtimeContext(rec), "Remember project requirement") {
			t.Fatal("missing context handoff")
		}
		_, _, _ = svc.store.Update(origin.ID, func(r *SessionRecord) { r.ExternalID = "engine-resume-id" })
	}
	restored := NewService(root, nil, nil, svc.store.filePath)
	restored.ConfigureOpenCode(server.URL, "secret")
	rec, _ := restored.store.Get(origin.ID)
	if rec.Runtime != "opencode" || len(restored.store.List(root)) != 1 || !restored.IsManagedOpenCodeSession(origin.ID) {
		t.Fatal("runtime adoption was not restored")
	}
	svc.mu.Lock()
	svc.procs[origin.ID] = &activeProcess{}
	svc.mu.Unlock()
	if w := switchRuntime(t, svc, origin.ID, "claude-code", meta.Directory); w.Code != 409 {
		t.Fatal("switched a running session")
	}
	svc.mu.Lock()
	delete(svc.procs, origin.ID)
	svc.mu.Unlock()
	if w := switchRuntime(t, svc, origin.ID, "claude-code", t.TempDir()); w.Code != 409 {
		t.Fatal("accepted another directory")
	}
}

func TestManagedOpenCodeTurnStreamsPersistsAndResumes(t *testing.T) {
	root := t.TempDir()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	svc := NewService(root, nil, nil, storePath)
	info := createSandboxSession(t, svc, "claude-code", root, "local")
	_, _, _ = svc.store.Update(info.ID, func(r *SessionRecord) {
		r.Turns = []MessageTurn{{Info: MessageInfo{ID: "msg_history", SessionID: info.ID, Role: "assistant"}, Parts: []MessagePart{{Type: "text", Text: "Previously agreed constraint"}}}}
	})
	var mu sync.Mutex
	var turns []MessageTurn
	var systems []string
	streamEvents := make(chan SSEEvent, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("directory") != root {
			t.Errorf("wrong prompt directory: %s", r.URL)
		}
		switch r.URL.Path {
		case "/session":
			writeJSON(w, 200, SessionInfo{ID: "ses_engine", Directory: root})
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			for {
				select {
				case <-r.Context().Done():
					return
				case event := <-streamEvents:
					raw, _ := json.Marshal(event)
					fmt.Fprintf(w, "data: %s\n\n", raw)
					w.(http.Flusher).Flush()
				}
			}
		case "/session/ses_engine/message":
			if r.Method == "GET" {
				mu.Lock()
				defer mu.Unlock()
				writeJSON(w, 200, turns)
				return
			}
			var input struct {
				MessageID string `json:"messageID"`
				System    string `json:"system"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			mu.Lock()
			systems = append(systems, input.System)
			n := len(systems)
			mu.Unlock()
			msg := MessageInfo{ID: fmt.Sprintf("msg_answer%d", n), SessionID: "ses_engine", Role: "assistant", Time: MessageTime{Created: time.Now().UnixMilli(), Completed: time.Now().UnixMilli()}, Finish: "stop"}
			part := MessagePart{ID: fmt.Sprintf("prt_answer%d", n), MessageID: msg.ID, SessionID: "ses_engine", Type: "text", Text: ""}
			streamEvents <- SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": msg}}
			streamEvents <- SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": part}}
			streamEvents <- SSEEvent{Type: "message.part.delta", Properties: map[string]interface{}{"sessionID": "ses_engine", "messageID": msg.ID, "partID": part.ID, "delta": "streamed reply"}}
			// Wait until the delta reaches the controller before completing the HTTP call.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				rec, _ := svc.store.Get(info.ID)
				found := false
				for _, turn := range rec.Turns {
					for _, p := range turn.Parts {
						if p.ID == part.ID && p.Text == "streamed reply" {
							found = true
						}
					}
				}
				if found {
					break
				}
				time.Sleep(time.Millisecond)
			}
			part.Text = "streamed reply"
			mu.Lock()
			turns = append(turns, MessageTurn{Info: MessageInfo{ID: input.MessageID, SessionID: "ses_engine", Role: "user"}, Parts: []MessagePart{{ID: input.MessageID + "_part", MessageID: input.MessageID, SessionID: "ses_engine", Type: "text", Text: "next"}}}, MessageTurn{Info: msg, Parts: []MessagePart{part}})
			mu.Unlock()
			writeJSON(w, 200, map[string]interface{}{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	if w := switchRuntime(t, svc, info.ID, "opencode", root); w.Code != 200 {
		t.Fatalf("switch: %s", w.Body)
	}
	ch, unsub := svc.Subscribe(info.ID)
	defer unsub()
	for attempt := 0; attempt < 2; attempt++ {
		w := httptest.NewRecorder()
		svc.handleSessionPrompt(w, httptest.NewRequest("POST", "/prompt", strings.NewReader(`{"text":"next","model":{"providerID":"provider","modelID":"model"}}`)), info.ID)
		if w.Code != 200 {
			t.Fatalf("prompt: %d %s", w.Code, w.Body)
		}
		sawDelta := false
		deadline := time.After(5 * time.Second)
	wait:
		for {
			select {
			case evt := <-ch:
				if evt.Type == "message.part.delta" {
					sawDelta = true
					if evt.Properties["sessionID"] != info.ID {
						t.Fatal("leaked backing ID")
					}
				}
				if evt.Type == "session.status" && evt.Properties["status"].(SessionStatus).Type == "idle" {
					break wait
				}
			case <-deadline:
				t.Fatal("managed turn did not finish")
			}
		}
		if !sawDelta {
			t.Fatal("no incremental SSE before completion")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(systems) != 2 || !strings.Contains(systems[0], "Previously agreed constraint") || !strings.Contains(systems[1], "Previously agreed constraint") {
		t.Fatalf("lost initial context on resume: %v", systems)
	}
	rec, _ := NewStore(storePath).Get(info.ID)
	if rec.ID != info.ID || len(rec.Turns) != 5 || rec.Runtime != "opencode" || rec.ProviderID != "provider" {
		t.Fatalf("lost managed history: %+v", rec)
	}
	if svc.AllowOpenCodeSSELine(`data: {"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_engine"}}}`) {
		t.Fatal("backing event leaked to global feed")
	}
	if !svc.AllowOpenCodeSSELine(`data: {"payload":{"type":"message.part.delta","properties":{"sessionID":"ses_other"}}}`) {
		t.Fatal("another session was filtered")
	}
}

func TestManagedOpenCodeStopAbortsBackingSession(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	rec := SessionRecord{ID: "ses_stable_stop", Runtime: "opencode", ExternalID: "ses_engine_stop", ProjectDir: root, WorktreePath: root, SandboxMode: "local", ProviderID: "test", ModelID: "model"}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	started, aborted := make(chan struct{}), make(chan struct{})
	steered := make(chan string, 1)
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("directory") != root {
			t.Errorf("unscoped stop: %s", r.URL)
		}
		switch r.URL.Path {
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/session/ses_engine_stop/message":
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			<-r.Context().Done()
		case "/session/ses_engine_stop/abort":
			once.Do(func() { close(aborted) })
			writeJSON(w, 200, true)
		case "/session/ses_engine_stop/prompt_async":
			var payload struct {
				Parts []MessagePart `json:"parts"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			steered <- payload.Parts[0].Text
			writeJSON(w, 200, true)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	if err := svc.startOpenCodeTurn(rec, "build", "model", "", "", MessageInfo{ID: "msg_stop", SessionID: rec.ID, Role: "user"}, []MessagePart{{Type: "text", Text: "long task"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt never started")
	}
	if w := switchRuntime(t, svc, rec.ID, "opencode", root); w.Code != 200 {
		t.Fatalf("same-runtime steering blocked: %s", w.Body)
	}
	steerResponse := httptest.NewRecorder()
	svc.handleSessionPrompt(steerResponse, httptest.NewRequest("POST", "/prompt", strings.NewReader(`{"text":"correct course"}`)), rec.ID)
	if steerResponse.Code != 200 {
		t.Fatalf("steer: %s", steerResponse.Body)
	}
	select {
	case text := <-steered:
		if text != "correct course" {
			t.Fatal("incorrect steering input")
		}
	case <-time.After(time.Second):
		t.Fatal("steering did not reach existing engine")
	}
	svc.mu.Lock()
	ap := svc.procs[rec.ID]
	svc.mu.Unlock()
	w := httptest.NewRecorder()
	svc.handleSessionAbort(w, httptest.NewRequest("POST", "/abort", strings.NewReader(`{}`)), rec.ID)
	select {
	case <-aborted:
	case <-time.After(3 * time.Second):
		t.Fatal("backing engine was not aborted")
	}
	select {
	case <-ap.done:
	case <-time.After(3 * time.Second):
		t.Fatal("managed execution did not stop")
	}
	if w.Code != 200 || svc.ActiveProcessCount() != 0 {
		t.Fatal("stop left an active execution")
	}
}

func TestNativeRuntimeHandoffUsesHistoryWithoutReusingOtherEngineResumeID(t *testing.T) {
	root := t.TempDir()
	binRoot := t.TempDir()
	binDir := filepath.Join(binRoot, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("GEMINI_HOME", binRoot)
	inputLog, argsLog := filepath.Join(t.TempDir(), "input"), filepath.Join(t.TempDir(), "args")
	t.Setenv("TEST_INPUT", inputLog)
	t.Setenv("TEST_ARGS", argsLog)
	for name, result := range map[string]string{"agy": `{"event":"result","result":{"response":"OK","status":"DONE"}}`, "claude": `{"type":"result","result":"OK","is_error":false}`} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$TEST_ARGS\"\n/bin/cat > \"$TEST_INPUT\"\nprintf '%s\\n' '" + result + "'\n"
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	defer svc.CloseAll()
	info := createSandboxSession(t, svc, "claude-code", root, "local")
	_, _, _ = svc.store.Update(info.ID, func(r *SessionRecord) {
		r.ExternalID = "previous-engine-resume"
		r.Turns = []MessageTurn{{Info: MessageInfo{ID: "msg_previous", SessionID: info.ID, Role: "user"}, Parts: []MessagePart{{Type: "text", Text: "historical task requirement"}}}}
	})
	for _, rt := range []string{"antigravity", "claude-code"} {
		if w := switchRuntime(t, svc, info.ID, rt, root); w.Code != 200 {
			t.Fatalf("switch: %s", w.Body)
		}
		w := httptest.NewRecorder()
		svc.handleSessionPrompt(w, httptest.NewRequest("POST", "/prompt", strings.NewReader(`{"text":"current user request"}`)), info.ID)
		if w.Code != 200 {
			t.Fatalf("prompt: %s", w.Body)
		}
		svc.mu.Lock()
		ap := svc.procs[info.ID]
		svc.mu.Unlock()
		if ap != nil {
			select {
			case <-ap.done:
			case <-time.After(3 * time.Second):
				t.Fatal("CLI did not finish")
			}
		}
		input, _ := os.ReadFile(inputLog)
		args, _ := os.ReadFile(argsLog)
		if !strings.Contains(string(input), "historical task requirement") || !strings.Contains(string(input), "current user request") || strings.Contains(string(args), "previous-engine-resume") {
			t.Fatalf("bad handoff: %s %s", args, input)
		}
		rec, _ := svc.store.Get(info.ID)
		for _, turn := range rec.Turns {
			if turn.Info.Role == "user" && turn.Info.ID != "msg_previous" {
				if len(turn.Parts) == 0 || turn.Parts[0].Text != "current user request" {
					t.Fatal("context handoff leaked into visible user text")
				}
			}
		}
		_, _, _ = svc.store.Update(info.ID, func(r *SessionRecord) { r.ExternalID = "previous-engine-resume" })
	}
	if len(svc.store.List(root)) != 1 {
		t.Fatal("runtime switch created another conversation")
	}
}

func TestDeleteManagedConversationRemovesAllScopedEngineHandles(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	rec := SessionRecord{ID: "ses_delete_adopted", Runtime: "claude-code", ProjectDir: root, WorktreePath: root, SandboxMode: "local", OpenCodeIDs: []string{"ses_original_delete", "ses_backing_delete"}, OpenCodeDirectories: map[string]string{"ses_original_delete": root, "ses_backing_delete": root + "/old-sandbox"}}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	deleted := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deleted[r.URL.Path] = r.URL.Query().Get("directory")
		writeJSON(w, 200, true)
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	w := httptest.NewRecorder()
	svc.handleSingleSession(w, httptest.NewRequest("DELETE", "/session", nil), rec.ID)
	if w.Code != 204 {
		t.Fatalf("delete: %s", w.Body)
	}
	if _, ok := NewStore(svc.store.filePath).Get(rec.ID); ok {
		t.Fatal("conversation not deleted")
	}
	if deleted["/session/ses_original_delete"] != root || deleted["/session/ses_backing_delete"] != root+"/old-sandbox" {
		t.Fatalf("unscoped engine cleanup: %v", deleted)
	}
}
