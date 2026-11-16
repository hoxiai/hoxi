package forge

import (
	"context"
	"testing"
)

func TestDetailAttachesReviewThreads(t *testing.T) {
	svc := testService(t, `if [ "$1" = "pr" ]; then
cat <<'JSON'
{
  "number": 42,
  "url": "https://github.com/hoxiai/hoxi/pull/42",
  "title": "Add PR issues",
  "state": "OPEN",
  "headRefName": "feat/pr-issues",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "FAILURE"}
  ]
}
JSON
exit 0
fi
cat <<'JSON'
{
  "data": {
    "repository": {
      "pullRequest": {
        "reviewThreads": {
          "nodes": [
            {
              "isResolved": false,
              "isOutdated": false,
              "path": "internal/forge/status.go",
              "line": 42,
              "comments": {"nodes": [{"author": {"login": "reviewer"}, "body": "Handle a missing branch."}]}
            },
            {
              "isResolved": true,
              "isOutdated": false,
              "path": "web/src/services/forge.ts",
              "line": 8,
              "comments": {"nodes": [{"author": {"login": "author"}, "body": "Done."}]}
            }
          ]
        }
      }
    }
  }
}
JSON`)

	response := svc.Detail(context.Background(), t.TempDir(), "feat/pr-issues")
	if !response.Available || response.PullRequest == nil {
		t.Fatalf("expected available detail, got %+v", response)
	}
	pr := response.PullRequest
	if pr.UnresolvedThreads != 1 {
		t.Fatalf("expected one unresolved thread, got %d", pr.UnresolvedThreads)
	}
	if len(pr.ReviewThreads) != 2 {
		t.Fatalf("expected two threads, got %+v", pr.ReviewThreads)
	}
	if pr.ReviewThreads[0].Path != "internal/forge/status.go" || pr.ReviewThreads[0].Line != 42 {
		t.Fatalf("unexpected thread anchor: %+v", pr.ReviewThreads[0])
	}
	if pr.ReviewThreads[0].Comments[0].Author != "reviewer" {
		t.Fatalf("unexpected reviewer: %+v", pr.ReviewThreads[0].Comments[0])
	}
}

func TestDetailDegradesWhenThreadLookupFails(t *testing.T) {
	svc := testService(t, `if [ "$1" = "pr" ]; then
cat <<'JSON'
{"number": 5, "url": "https://github.com/hoxiai/hoxi/pull/5", "title": "No threads", "state": "OPEN", "headRefName": "feat/none", "statusCheckRollup": []}
JSON
exit 0
fi
echo 'graphql unavailable' >&2
exit 1`)

	response := svc.Detail(context.Background(), t.TempDir(), "feat/none")
	if !response.Available || response.PullRequest == nil {
		t.Fatalf("expected status to survive thread failure, got %+v", response)
	}
	if response.PullRequest.UnresolvedThreads != 0 || len(response.PullRequest.ReviewThreads) != 0 {
		t.Fatalf("expected no threads, got %+v", response.PullRequest)
	}
}

func TestRepoFromPullRequestURL(t *testing.T) {
	owner, repo, ok := repoFromPullRequestURL("https://github.com/hoxiai/hoxi/pull/42")
	if !ok || owner != "hoxiai" || repo != "hoxi" {
		t.Fatalf("unexpected repo parse: %q %q %v", owner, repo, ok)
	}
	if _, _, ok := repoFromPullRequestURL("https://gitlab.com/hoxiai/hoxi/-/merge_requests/4"); ok {
		t.Fatal("expected non-GitHub URL to be rejected")
	}
}
