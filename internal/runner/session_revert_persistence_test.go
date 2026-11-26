package runner

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestSessionRevertPersistenceFailureLeavesWorktreeIntact(t *testing.T) {
	for _, action := range []string{"revert", "repeat-revert", "unrevert"} {
		t.Run(action, func(t *testing.T) {
			repo := initTestGitRepo(t)
			snapshot, err := worktree.CreateSnapshot(repo)
			if err != nil {
				t.Fatal(err)
			}
			storeDir := t.TempDir()
			svc := NewService(repo, worktree.NewManager(repo), nil, storeDir)
			const id = "ses_cld_checkpoint"
			edit := MessagePart{
				ID: "edit", MessageID: "reply", Type: "tool", Tool: "edit",
				State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": "README.md"}},
			}
			seed := SessionRecord{
				ID: id, Runtime: "claude-code", ProjectDir: repo, WorktreePath: repo, SandboxMode: "local",
				Snapshots: map[string]string{"prompt": snapshot},
				Turns: []MessageTurn{
					{Info: MessageInfo{ID: "prompt", Role: "user"}},
					{Info: MessageInfo{ID: "reply", Role: "assistant"}, Parts: []MessagePart{edit}},
				},
			}
			if err := svc.Store().Put(seed); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(repo, "README.md")
			if err := os.WriteFile(file, []byte("after turn"), 0600); err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			svc.RegisterRoutes(mux)
			request := func(action string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/"+id+"/"+action+"?directory="+repo, strings.NewReader(`{"messageID":"prompt"}`)))
				return w
			}
			if action != "revert" {
				if w := request("revert"); w.Code != 200 {
					t.Fatalf("initial revert: %d: %s", w.Code, w.Body.String())
				}
			}
			const before = "new local edit before request"
			if err := os.WriteFile(file, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			meta := filepath.Join(svc.Store().SessionDir(id), "session.json")
			if err := os.Rename(meta, meta+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(meta, 0700); err != nil {
				t.Fatal(err)
			}
			operation := action
			if operation == "repeat-revert" {
				operation = "revert"
			}
			if w := request(operation); w.Code != 500 {
				t.Fatalf("failed write: %d: %s", w.Code, w.Body.String())
			}
			got, err := os.ReadFile(file)
			if err != nil || string(got) != before {
				t.Fatalf("workspace after failed %s: %q (%v)", action, got, err)
			}
			if err := os.Remove(meta); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(meta+".original", meta); err != nil {
				t.Fatal(err)
			}
			previousRevert := action != "revert"
			for _, store := range []*Store{svc.Store(), NewStore(storeDir)} {
				rec, ok := store.Get(id)
				if !ok || (rec.Revert != nil) != previousRevert || (!previousRevert && len(rec.Turns) != 2) {
					t.Fatalf("inconsistent session after %s", action)
				}
			}
		})
	}
}

func TestUnrevertMissingSnapshotKeepsRevertRecord(t *testing.T) {
	repo := initTestGitRepo(t)
	storeDir := t.TempDir()
	svc := NewService(repo, worktree.NewManager(repo), nil, storeDir)
	const id = "ses_cld_missing_tree"
	rec := SessionRecord{
		ID: id, Runtime: "claude-code", ProjectDir: repo, WorktreePath: repo,
		Revert: &SessionRevert{MessageID: "a"}, RevertScope: revertScopePaths,
		RevertSnapshot: strings.Repeat("1", 40), RevertPaths: []string{"README.md"},
		RevertedTurns: []MessageTurn{{Info: MessageInfo{ID: "a", Role: "user"}}},
	}
	if err := svc.Store().Put(rec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "README.md")
	const edited = "edited while reverted"
	if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.handleSessionUnrevert(w, httptest.NewRequest(http.MethodPost, "/", nil), id)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("unrevert missing tree: %d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != edited {
		t.Fatalf("workspace: %q (%v)", data, err)
	}
	got, ok := NewStore(storeDir).Get(id)
	if !ok || got.Revert == nil || got.RevertSnapshot != rec.RevertSnapshot || len(got.RevertedTurns) != 1 || len(got.Turns) != 0 {
		t.Fatalf("lost revert record after failed restore: %+v", got)
	}
}
