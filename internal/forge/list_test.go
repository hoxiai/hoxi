package forge

import (
	"context"
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
