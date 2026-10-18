package worktree

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLocalSessionCommitRejectsAnotherProject(t *testing.T) {
	root, manager, base := sessionFixture(t)
	other, _, _ := sessionFixture(t)
	if err := manager.UseLocalDirectory("ses_local_bound"); err != nil {
		t.Fatal(err)
	}
	binding, known, err := manager.BindingForSession("ses_local_bound")
	if err != nil || !known || binding.Isolated {
		t.Fatalf("missing local binding: %+v %v", binding, err)
	}
	after := strings.Replace(base, "one\n", "SESSION\n", 1)
	sessionTestWrite(t, root, "file.txt", after)
	sessionTestWrite(t, other, "file.txt", after)
	headA, headB := sessionTestGit(t, root, "rev-parse", "HEAD"), sessionTestGit(t, other, "rev-parse", "HEAD")
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)
	for _, sessionDiffs := range []bool{true, false} {
		for _, selector := range []string{"body", "query"} {
			body := map[string]interface{}{"sessionID": "ses_local_bound", "message": "bound"}
			if sessionDiffs {
				body["sessionDiffs"] = []SessionDiff{sessionTestDiff("file.txt", base, after, "modified")}
			}
			endpoint := "/api/hoxi/worktree/commit"
			if selector == "body" {
				body["directory"] = other
			} else {
				endpoint += "?directory=" + url.QueryEscape(other)
			}
			data, _ := json.Marshal(body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data)))
			if w.Code != http.StatusForbidden {
				t.Fatalf("cross-project commit admitted: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if sessionTestGit(t, root, "rev-parse", "HEAD") != headA || sessionTestGit(t, other, "rev-parse", "HEAD") != headB {
		t.Fatal("rejected request changed a repository")
	}
	if err := manager.ForDirectory(other).UseLocalDirectory("ses_local_bound"); err != nil {
		t.Fatal(err)
	}
	if _, known, err := manager.BindingForSession("ses_local_bound"); err == nil || !known {
		t.Fatal("ambiguous local bindings were accepted")
	}
}
