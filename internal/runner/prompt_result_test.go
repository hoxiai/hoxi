package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/testutil/execstub"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestPromptResultOriginalTurnAfterRestart(t *testing.T) {
	dir := initTestGitRepo(t)
	state := filepath.Join(t.TempDir(), "sessions")
	bin := t.TempDir()
	t.Setenv("PATH", agyBinPathWithSystem(bin))
	execstub.Write(t, bin, "claude", `#!/bin/sh
cat >/dev/null
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"original visible reply"}]}}' '{"type":"result","result":"original visible reply","is_error":false}'
`)
	svc := NewService(dir, worktree.NewManager(dir), statusbar.NewTracker(), state)
	sid := createSandboxSession(t, svc, string(RuntimeClaudeCode), dir, "local").ID
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/hoxi/runner/sessions/"+sid+"/prompt_async", strings.NewReader(`{"text":"private user prompt","clientRequestId":"receipt-one"}`)))
	if w.Code != 200 {
		t.Fatalf("submit %d %s", w.Code, w.Body.String())
	}
	for deadline := time.Now().Add(4 * time.Second); svc.isSessionBusy(sid) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	svc.store.Update(sid, func(rec *SessionRecord) {
		last := &rec.Turns[len(rec.Turns)-1]
		last.Parts = append(last.Parts, MessagePart{Type: "reasoning", Text: "private reasoning", SessionID: sid, MessageID: last.Info.ID}, MessagePart{Type: "text", Text: "private wrong part", SessionID: sid, MessageID: "other"})
		sibling := MessageTurn{Info: last.Info, Parts: []MessagePart{{Type: "text", Text: "private sibling", MessageID: "other", SessionID: sid}}}
		sibling.Info.ID = "other"
		rec.Turns = append(rec.Turns, sibling)
	})
	svc.store.FlushSession(sid)
	svc.CloseAll()
	svc = NewService(dir, worktree.NewManager(dir), statusbar.NewTracker(), state)
	defer svc.CloseAll()
	mux = http.NewServeMux()
	svc.RegisterRoutes(mux)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/hoxi/requests/receipt-one?kind=prompt&scope="+sid+"&turnResult=1", nil))
	var v RequestInspectResponse
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 || v.Evidence["status"] != "turn_completed" || v.Evidence["text"] != "original visible reply" || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("inspect %d %s", w.Code, w.Body.String())
	}
	for _, ids := range [][]string{{"wrong", "other"}, {"", "other"}, {"missing", "missing"}} {
		ev := svc.inspectPromptTurn(PersistedRequestRecord{Scope: sid, SessionID: sid, Meta: map[string]string{"userMsgID": ids[0], "assistantMsgID": ids[1]}})
		if ev["status"] != "unknown" || ev["text"] != nil {
			t.Fatal(ev)
		}
	}
}
