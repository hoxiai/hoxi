package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSessionSummarizeForwardsToOpenCodeAndRefreshesTurns(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	var summarizeBody map[string]string
	summarized := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, _ := r.BasicAuth()
		if user != "opencode" || password != "secret" {
			t.Error("missing daemon auth")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_upstream/summarize":
			_ = json.NewDecoder(r.Body).Decode(&summarizeBody)
			summarized = true
			writeJSON(w, http.StatusOK, true)
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_upstream/message":
			mu.Lock()
			done := summarized
			mu.Unlock()
			turnCount := 1
			if done {
				turnCount = 2
			}
			turns := make([]MessageTurn, 0, turnCount)
			turns = append(turns, MessageTurn{Info: MessageInfo{ID: "msg_old", SessionID: "ses_upstream", Role: "user"}, Parts: []MessagePart{{ID: "prt_old", SessionID: "ses_upstream", MessageID: "msg_old", Type: "text", Text: "old"}}})
			if done {
				turns = append(turns, MessageTurn{Info: MessageInfo{ID: "msg_summary", SessionID: "ses_upstream", Role: "assistant"}, Parts: []MessagePart{{ID: "prt_summary", SessionID: "ses_upstream", MessageID: "msg_summary", Type: "text", Text: "summary"}}})
			}
			writeJSON(w, http.StatusOK, turns)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(server.URL, "secret")
	rec := SessionRecord{
		ID: "ses_hoxi_test", Runtime: "opencode", ExternalID: "ses_upstream",
		ProjectDir: root, WorktreePath: root, ProviderID: "old-provider", ModelID: "old-model",
		Turns: []MessageTurn{{Info: MessageInfo{ID: "msg_old", SessionID: "ses_hoxi_test", Role: "user"}}},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	body := `{"providerID":"ainode","modelID":"deepseek-flash"}`
	svc.handleSessionSummarize(w, httptest.NewRequest(http.MethodPost, "/summarize", strings.NewReader(body)), rec.ID)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "true" {
		t.Fatalf("summarize: %d %s", w.Code, w.Body.String())
	}
	if summarizeBody["providerID"] != "ainode" || summarizeBody["modelID"] != "deepseek-flash" {
		t.Fatalf("summarize body = %+v", summarizeBody)
	}
	updated, _ := svc.store.Get(rec.ID)
	if len(updated.Turns) != 2 || updated.Turns[1].Info.ID != "msg_summary" || updated.Turns[1].Info.SessionID != rec.ID {
		t.Fatalf("turns not refreshed: %+v", updated.Turns)
	}
}

func TestSessionSummarizeSkipsNativeRuntimes(t *testing.T) {
	root := t.TempDir()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.NotFound(w, r)
	}))
	defer server.Close()

	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(server.URL, "secret")
	rec := SessionRecord{ID: "ses_cld_test", Runtime: "claude-code", ProjectDir: root, WorktreePath: root}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	svc.handleSessionSummarize(w, httptest.NewRequest(http.MethodPost, "/summarize", strings.NewReader("{}")), rec.ID)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "false" {
		t.Fatalf("native summarize: %d %s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("native runtime reached OpenCode")
	}
}
