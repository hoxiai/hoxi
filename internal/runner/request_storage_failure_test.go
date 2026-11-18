package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func unavailableLedgerPath(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, taskLedgerFile)
}

func TestLedgerLoadFailureBlocksPromptApprovalCommitAndInspect(t *testing.T) {
	t.Parallel()
	root := initTestGitRepo(t)
	state := t.TempDir()
	path := filepath.Join(state, taskLedgerFile)
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := worktree.NewManager(root)
	svc := NewService(root, mgr, nil, filepath.Join(state, "sessions"))
	defer svc.CloseAll()

	id := "ses_corrupt_ledger"
	if err := svc.store.Put(SessionRecord{ID: id, Runtime: string(RuntimeNative), ProjectDir: root, WorktreePath: root}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	mgr.RegisterRoutes(mux)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/hoxi/runner/sessions/" + id + "/prompt_async", `{"text":"hi"}`},
		{http.MethodPost, "/api/hoxi/permission/per_rnr_1/reply", `{"reply":"once"}`},
		{http.MethodPost, "/api/hoxi/permission/per_oc_1/reply", `{"reply":"once"}`},
		{http.MethodPost, "/api/hoxi/worktree/commit", `{"message":"test","clientRequestId":"req-1"}`},
		{http.MethodGet, "/api/hoxi/requests/req-1?kind=prompt&scope=" + id, ""},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		assertStorageUnavailable(t, w)
	}
}

func TestPermissionReplyStorageFailureDoesNotAnswerApproval(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(root, "sessions"))
	defer svc.CloseAll()
	goodPath := svc.ledger.Path()
	svc.ledger.SetPathForTest(unavailableLedgerPath(t))

	id := "per_rnr_storage_fail"
	replies := make(chan approvalReply, 1)
	svc.gate.mu.Lock()
	svc.gate.pending[id] = &pendingApproval{
		sid:   "ses_1",
		req:   map[string]interface{}{"id": id, "permission": "bash", "directory": root},
		reply: replies,
	}
	svc.gate.mu.Unlock()

	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+id+"/reply", strings.NewReader(`{"reply":"once","clientRequestId":"request"}`)))
	assertStorageUnavailable(t, w)
	select {
	case reply := <-replies:
		t.Fatalf("approval delivered despite failed admission: %+v", reply)
	default:
	}
	svc.ledger.SetPathForTest(goodPath)
	retry := httptest.NewRecorder()
	mux.ServeHTTP(retry, httptest.NewRequest(http.MethodPost, "/api/hoxi/permission/"+id+"/reply", strings.NewReader(`{"reply":"once","clientRequestId":"request"}`)))
	if retry.Code != http.StatusOK || len(replies) != 1 {
		t.Fatalf("same approval cannot retry after repair: status=%d replies=%d body=%s", retry.Code, len(replies), retry.Body.String())
	}
}

func TestPromptStorageFailureDoesNotStartTurn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(root, "sessions"))
	defer svc.CloseAll()
	svc.ledger.SetPathForTest(unavailableLedgerPath(t))
	id := "ses_storage_failure"
	if err := svc.store.Put(SessionRecord{ID: id, Runtime: string(RuntimeNative), ProjectDir: root, WorktreePath: root}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+id+"/prompt_async", strings.NewReader(`{"text":"must not run","clientRequestId":"request"}`)))
	assertStorageUnavailable(t, w)
	rec, _ := svc.store.Get(id)
	if svc.isSessionBusy(id) || len(rec.Turns) != 0 {
		t.Fatalf("turn started despite failed admission: turns=%d", len(rec.Turns))
	}
}

func TestCommitStorageFailureDoesNotMutateGit(t *testing.T) {
	t.Parallel()
	root := initTestGitRepo(t)
	mgr := worktree.NewManager(root)
	svc := NewService(root, mgr, nil, filepath.Join(t.TempDir(), "sessions"))
	defer svc.CloseAll()
	svc.ledger.SetPathForTest(unavailableLedgerPath(t))
	if err := os.WriteFile(filepath.Join(root, "pending.txt"), []byte("keep uncommitted"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := worktree.ObserveGitHead(root)
	mux := http.NewServeMux()
	mgr.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/commit", strings.NewReader(`{"message":"must not commit","clientRequestId":"request"}`)))
	assertStorageUnavailable(t, w)
	if after := worktree.ObserveGitHead(root); before == "" || after != before {
		t.Fatalf("Git mutated despite failed admission: before=%s after=%s", before, after)
	}
}

func assertStorageUnavailable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusServiceUnavailable || body.Code != ErrCodeRequestStorageUnavailable {
		t.Fatalf("expected 503 storage failure: status=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
}
