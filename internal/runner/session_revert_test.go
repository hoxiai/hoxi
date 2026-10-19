package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestSessionRevertUnrevertAndFork(t *testing.T) {
	repo := initTestGitRepo(t)
	trackedFile := filepath.Join(repo, "README.md")
	untrackedFile := filepath.Join(repo, "generated.txt")

	snap1, err := worktree.CreateSnapshot(repo)
	if err != nil || snap1 == "" {
		t.Fatalf("CreateSnapshot snap1: %v", err)
	}

	if err := os.WriteFile(trackedFile, []byte("modified in turn 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	snap2, err := worktree.CreateSnapshot(repo)
	if err != nil || snap2 == "" {
		t.Fatalf("CreateSnapshot snap2: %v", err)
	}

	if err := os.WriteFile(trackedFile, []byte("modified in turn 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untrackedFile, []byte("untracked in turn 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var openCodeCalls []string
	oc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		openCodeCalls = append(openCodeCalls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(oc.Close)

	storeDir := filepath.Join(t.TempDir(), "sessions")
	svc := NewService(repo, worktree.NewManager(repo), nil, storeDir)
	svc.ConfigureOpenCode(oc.URL, "pw")

	turns := []MessageTurn{
		{
			Info:  MessageInfo{ID: "msg_u_1", SessionID: "ses_hoxi_1", Role: "user"},
			Parts: []MessagePart{{ID: "prt_u_1", SessionID: "ses_hoxi_1", MessageID: "msg_u_1", Type: "text", Text: "first prompt"}},
		},
		{
			Info:  MessageInfo{ID: "msg_a_1", SessionID: "ses_hoxi_1", Role: "assistant"},
			Parts: []MessagePart{{ID: "prt_a_1", SessionID: "ses_hoxi_1", MessageID: "msg_a_1", Type: "text", Text: "first reply"}},
		},
		{
			Info:  MessageInfo{ID: "msg_u_2", SessionID: "ses_hoxi_1", Role: "user"},
			Parts: []MessagePart{{ID: "prt_u_2", SessionID: "ses_hoxi_1", MessageID: "msg_u_2", Type: "text", Text: "second prompt"}},
		},
		{
			Info:  MessageInfo{ID: "msg_a_2", SessionID: "ses_hoxi_1", Role: "assistant"},
			Parts: []MessagePart{{ID: "prt_a_2", SessionID: "ses_hoxi_1", MessageID: "msg_a_2", Type: "text", Text: "second reply"}},
		},
	}

	rec := SessionRecord{
		ID:           "ses_hoxi_1",
		Runtime:      "opencode",
		ExternalID:   "ses_oc_handle",
		OpenCodeIDs:  []string{"ses_oc_handle"},
		ProjectDir:   repo,
		WorktreePath: repo,
		SandboxMode:  "local",
		Title:        "Revert Test",
		Turns:        turns,
		Snapshots: map[string]string{
			"msg_u_1": snap1,
			"msg_u_2": snap2,
		},
	}
	if err := svc.Store().Put(rec); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	// 1. Revert to msg_u_2: should roll back working tree to snap2, truncate turns.jsonl to 2 turns, and call OpenCode revert.
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_hoxi_1/revert", strings.NewReader(`{"messageID":"msg_u_2"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revert status = %d: %s", w.Code, w.Body.String())
	}
	var revertedInfo SessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &revertedInfo); err != nil {
		t.Fatal(err)
	}
	if revertedInfo.Revert == nil || revertedInfo.Revert.MessageID != "msg_u_2" {
		t.Fatalf("expected revert boundary msg_u_2, got %+v", revertedInfo.Revert)
	}
	if !revertedInfo.Capabilities.Revert || !revertedInfo.Capabilities.Fork || !revertedInfo.Capabilities.Steer {
		t.Fatalf("expected full capabilities on opencode session, got %+v", revertedInfo.Capabilities)
	}

	gotTracked, err := os.ReadFile(trackedFile)
	if err != nil || string(gotTracked) != "modified in turn 1\n" {
		t.Fatalf("expected tracked file rolled back to snap2, got %q (%v)", string(gotTracked), err)
	}
	if _, err := os.Stat(untrackedFile); !os.IsNotExist(err) {
		t.Fatalf("expected untracked file from turn 2 removed on revert, err=%v", err)
	}

	// Verify turns.jsonl on disk was truncated by opening a fresh Store.
	reloaded, ok := NewStore(storeDir).Get("ses_hoxi_1")
	if !ok || len(reloaded.Turns) != 2 || reloaded.Turns[1].Info.ID != "msg_a_1" {
		t.Fatalf("expected turns.jsonl truncated to 2 turns on disk, got %+v", reloaded.Turns)
	}

	// 2. Unrevert: should restore working tree to post-turn-2 state, restore all 4 turns in turns.jsonl, and call OpenCode unrevert.
	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_hoxi_1/unrevert", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unrevert status = %d: %s", w.Code, w.Body.String())
	}
	var unrevertedInfo SessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &unrevertedInfo); err != nil {
		t.Fatal(err)
	}
	if unrevertedInfo.Revert != nil {
		t.Fatalf("expected Revert nil after unrevert, got %+v", unrevertedInfo.Revert)
	}
	gotTracked, _ = os.ReadFile(trackedFile)
	gotUntracked, err := os.ReadFile(untrackedFile)
	if string(gotTracked) != "modified in turn 2\n" || err != nil || string(gotUntracked) != "untracked in turn 2\n" {
		t.Fatalf("expected post-turn-2 files restored after unrevert: tracked=%q untracked=%q (%v)", string(gotTracked), string(gotUntracked), err)
	}
	reloaded, _ = NewStore(storeDir).Get("ses_hoxi_1")
	if len(reloaded.Turns) != 4 {
		t.Fatalf("expected 4 turns restored on disk after unrevert, got %d", len(reloaded.Turns))
	}

	mu.Lock()
	calls := append([]string(nil), openCodeCalls...)
	mu.Unlock()
	if len(calls) != 2 || calls[0] != "POST /session/ses_oc_handle/revert" || calls[1] != "POST /session/ses_oc_handle/unrevert" {
		t.Fatalf("unexpected OpenCode calls: %+v", calls)
	}

	// 3. CLI runtime revert: should clear ExternalID and mark ContextPending.
	cliRec := SessionRecord{
		ID:           "ses_cld_1",
		Runtime:      "claude-code",
		ExternalID:   "cld_ext_1",
		ProjectDir:   repo,
		WorktreePath: repo,
		SandboxMode:  "local",
		Title:        "CLI Revert",
		Turns:        turns,
		Snapshots:    map[string]string{"msg_u_2": snap2},
	}
	if err := svc.Store().Put(cliRec); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_cld_1/revert", strings.NewReader(`{"messageID":"msg_u_2"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cli revert status = %d: %s", w.Code, w.Body.String())
	}
	cliAfter, _ := svc.Store().Get("ses_cld_1")
	if cliAfter.ExternalID != "" || !cliAfter.ContextPending || cliAfter.ContextTurnCount != 2 || len(cliAfter.Turns) != 2 {
		t.Fatalf("expected CLI handle reset + ContextPending with 2 turns, got %+v", cliAfter)
	}

	// 4. Fork from msg_u_2: should create a new session with the first 2 turns and ContextPending.
	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_hoxi_1/fork", strings.NewReader(`{"messageID":"msg_u_2"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("fork status = %d: %s", w.Code, w.Body.String())
	}
	var forkedInfo SessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &forkedInfo); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(forkedInfo.ID, "ses_hoxi_") || forkedInfo.ID == "ses_hoxi_1" || forkedInfo.ParentID != "" {
		t.Fatalf("unexpected forked session info: %+v", forkedInfo)
	}
	forkedRec, ok := NewStore(storeDir).Get(forkedInfo.ID)
	if !ok || len(forkedRec.Turns) != 2 || !forkedRec.ContextPending || forkedRec.Turns[0].Info.SessionID != forkedInfo.ID {
		t.Fatalf("unexpected forked record on disk: %+v", forkedRec)
	}
}

func TestUnifiedPermissionEndpointMergesAndRoutesReplies(t *testing.T) {
	repo := initTestGitRepo(t)
	var repliedEngineID string
	var repliedBody string
	oc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/permission":
			_, _ = io.WriteString(w, `[{"id":"per_oc_1","sessionID":"ses_oc_backing","permission":"bash","patterns":["rm -rf dist"]}]`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/permission/"):
			repliedEngineID = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/permission/"), "/reply")
			raw, _ := io.ReadAll(r.Body)
			repliedBody = string(raw)
			_, _ = io.WriteString(w, `true`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(oc.Close)

	svc := NewService(repo, worktree.NewManager(repo), nil, filepath.Join(t.TempDir(), "sessions"))
	svc.ConfigureOpenCode(oc.URL, "pw")
	if err := svc.Store().Put(SessionRecord{
		ID:           "ses_hoxi_owner",
		Runtime:      "opencode",
		ExternalID:   "ses_oc_backing",
		OpenCodeIDs:  []string{"ses_oc_backing"},
		ProjectDir:   repo,
		WorktreePath: repo,
		SandboxMode:  "local",
	}); err != nil {
		t.Fatal(err)
	}

	replyCh := make(chan approvalReply, 1)
	svc.gate.mu.Lock()
	svc.gate.pending["per_rnr_test1"] = &pendingApproval{
		sid: "ses_hoxi_owner",
		req: map[string]interface{}{
			"id":         "per_rnr_test1",
			"sessionID":  "ses_hoxi_owner",
			"permission": "bash",
			"patterns":   []string{"python3 x.py"},
		},
		reply: replyCh,
	}
	svc.gate.mu.Unlock()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/permission?directory="+repo, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/hoxi/permission = %d: %s", w.Code, w.Body.String())
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0]["id"] != "per_oc_1" || list[0]["sessionID"] != "ses_hoxi_owner" || list[1]["id"] != "per_rnr_test1" {
		t.Fatalf("expected merged permissions with rewritten sessionID, got %+v", list)
	}

	// Reply to runner permission
	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/per_rnr_test1/reply", bytes.NewReader([]byte(`{"reply":"once"}`)))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reply runner permission = %d: %s", w.Code, w.Body.String())
	}
	ans := <-replyCh
	if ans.Reply != "once" {
		t.Fatalf("expected runner reply 'once', got %+v", ans)
	}

	// Reply to OpenCode engine permission
	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/per_oc_1/reply?directory="+repo, bytes.NewReader([]byte(`{"reply":"always"}`)))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || repliedEngineID != "per_oc_1" || !strings.Contains(repliedBody, `"always"`) {
		t.Fatalf("reply engine permission = %d id=%q body=%q", w.Code, repliedEngineID, repliedBody)
	}
}
