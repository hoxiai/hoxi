package runner

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBusySessionRevertPreservesActiveTask(t *testing.T) {
	for _, action := range []string{"revert", "unrevert"} {
		for _, childOnly := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/process", true: "/child"}[childOnly], func(t *testing.T) {
				t.Parallel()
				repo := initTestGitRepo(t)
				file := filepath.Join(repo, "README.md")
				if err := os.WriteFile(file, []byte("original task is still editing\n"), 0644); err != nil {
					t.Fatal(err)
				}
				svc := NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
				rec := SessionRecord{
					ID: "ses_hoxi_busy_revert", Runtime: "opencode", ExternalID: "ses_oc_busy",
					ProjectDir: repo, WorktreePath: repo, SandboxMode: "local",
					Turns: []MessageTurn{
						{Info: MessageInfo{ID: "msg_original", Role: "user"}},
						{Info: MessageInfo{ID: "msg_reply", Role: "assistant", ParentID: "msg_original"}},
						{Info: MessageInfo{ID: "msg_immediate", Role: "user"}},
					},
				}
				if err := svc.store.Put(rec); err != nil {
					t.Fatal(err)
				}
				before, _ := svc.store.Get(rec.ID)
				cancelled := false
				ap := &activeProcess{done: make(chan struct{}), cancel: func() { cancelled = true }}
				close(ap.done)
				if childOnly {
					svc.childBusy[rec.ID] = true
				} else {
					svc.procs[rec.ID] = ap
				}
				// No OpenCode client is configured: rejection must precede every
				// upstream call as well as snapshots and process cancellation.
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/"+action, strings.NewReader(`{"messageID":"msg_immediate"}`))
				if action == "revert" {
					svc.handleSessionRevert(w, r, rec.ID)
				} else {
					svc.handleSessionUnrevert(w, r, rec.ID)
				}
				if w.Code != http.StatusConflict {
					t.Fatalf("status = %d: %s", w.Code, w.Body)
				}
				if cancelled || ap.wasAborted() {
					t.Fatal("revert interrupted the active task")
				}
				after, _ := svc.store.Get(rec.ID)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("rejected revert changed conversation state")
				}
				content, err := os.ReadFile(file)
				if err != nil || string(content) != "original task is still editing\n" {
					t.Fatalf("rejected revert changed workspace: %q, %v", content, err)
				}
			})
		}
	}
}
