package forge

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestListPrefersOpenPullRequestPerBranch(t *testing.T) {
	svc := testService(t, `cat <<'JSON'
[
  {"number": 7, "url": "https://github.com/hoxiai/hoxi/pull/7", "title": "old", "state": "CLOSED", "headRefName": "feat/a", "statusCheckRollup": []},
  {"number": 9, "url": "https://github.com/hoxiai/hoxi/pull/9", "title": "current", "state": "OPEN", "headRefName": "feat/a", "statusCheckRollup": []},
  {"number": 4, "url": "https://github.com/hoxiai/hoxi/pull/4", "title": "merged", "state": "MERGED", "headRefName": "feat/b", "statusCheckRollup": []}
]
JSON`)

	response := svc.List(context.Background(), t.TempDir())
	if !response.Available {
		t.Fatalf("expected available list, got %+v", response)
	}
	if response.PullRequests["feat/a"].Number != 9 {
		t.Fatalf("expected open PR 9, got %+v", response.PullRequests["feat/a"])
	}
	if response.PullRequests["feat/b"].Number != 4 {
		t.Fatalf("expected merged PR 4, got %+v", response.PullRequests["feat/b"])
	}
}

func TestListReportsMissingGH(t *testing.T) {
	svc := &Service{ghPath: "hoxi-gh-that-does-not-exist", timeout: defaultTimeout}
	response := svc.List(context.Background(), t.TempDir())
	if response.Available || response.Reason != "gh_missing" {
		t.Fatalf("expected gh_missing, got %+v", response)
	}
}

func TestListCachesAcrossWorktreesAndInvalidatesOnCreate(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "gh-list-count")
	svc := testService(t, `if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  echo x >> "`+countFile+`"
  sleep 0.03
  cat <<'JSON'
[{"number": 12, "url": "https://github.com/hoxiai/hoxi/pull/12", "title": "wt pr", "state": "OPEN", "headRefName": "hoxi/ses_1", "statusCheckRollup": []}]
JSON
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "https://github.com/hoxiai/hoxi/pull/13"
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo '{}'
  exit 1
fi
exit 1`)

	repoDir := t.TempDir()
	wt1 := filepath.Join(repoDir, ".hoxi", "worktrees", "ses_1")
	wt2 := filepath.Join(repoDir, ".hoxi", "worktrees", "ses_2")
	if err := os.MkdirAll(wt1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt2, 0o755); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for _, d := range []string{repoDir, wt1, wt2} {
		d := d
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := svc.List(context.Background(), d)
			if !resp.Available || resp.PullRequests["hoxi/ses_1"].Number != 12 {
				t.Errorf("unexpected list response for %s: %+v", d, resp)
			}
		}()
	}
	wg.Wait()

	// Cached follow-up call from another worktree must also reuse the single run.
	if resp := svc.List(context.Background(), wt2); !resp.Available || resp.PullRequests["hoxi/ses_1"].Number != 12 {
		t.Fatalf("unexpected cached response: %+v", resp)
	}
	rawCount, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := len(rawCount) / 2; lines != 1 {
		t.Fatalf("gh pr list invocations = %d, want 1", lines)
	}

	// Creating a PR from a worktree invalidates the parent repository's PR list cache.
	createResp := svc.Create(context.Background(), CreateRequest{
		Directory: wt1,
		Title:     "new pr",
		Body:      "body",
		Base:      "main",
		Head:      "hoxi/ses_2",
	})
	if !createResp.OK {
		t.Fatalf("Create failed: %+v", createResp)
	}
	_ = svc.List(context.Background(), repoDir)
	rawCountAfter, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := len(rawCountAfter) / 2; lines != 2 {
		t.Fatalf("gh pr list invocations after Create = %d, want 2", lines)
	}
}
