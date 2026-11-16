package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreatePullRequestSuccessAndStatusLookup(t *testing.T) {
	svc := testService(t, `if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "https://github.com/hoxiai/hoxi/pull/99"
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  cat <<'JSON'
{
  "number": 99,
  "url": "https://github.com/hoxiai/hoxi/pull/99",
  "title": "feat: add worktree verify",
  "state": "OPEN",
  "isDraft": false,
  "additions": 35,
  "deletions": 4,
  "changedFiles": 2,
  "baseRefName": "main",
  "headRefName": "hoxi/ses_99",
  "statusCheckRollup": []
}
JSON
  exit 0
fi
exit 1`)

	dir := t.TempDir()
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	payload, _ := json.Marshal(CreateRequest{
		Directory: dir,
		Title:     "feat: add worktree verify",
		Body:      "Summary of changes",
		Base:      "main",
		Head:      "hoxi/ses_99",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/forge/pr", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp CreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.OK || !resp.Available || resp.URL != "https://github.com/hoxiai/hoxi/pull/99" {
		t.Fatalf("unexpected create response: %+v", resp)
	}
	if resp.PullRequest == nil || resp.PullRequest.Number != 99 {
		t.Fatalf("expected populated pullRequest #99, got %+v", resp.PullRequest)
	}
}

func TestCreatePullRequestRejectsFlagInjectionRef(t *testing.T) {
	svc := testService(t, `exit 0`)
	resp := svc.Create(context.Background(), CreateRequest{
		Directory: t.TempDir(),
		Head:      "--repo=evil/repo",
	})
	if resp.OK || resp.Reason != "invalid_branch" {
		t.Fatalf("expected invalid_branch rejection, got %+v", resp)
	}
}

func TestCreatePullRequestHandlesAlreadyExists(t *testing.T) {
	svc := testService(t, `if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  echo "a pull request for branch \"hoxi/ses_exists\" into branch \"main\" already exists:" >&2
  echo "https://github.com/hoxiai/hoxi/pull/77" >&2
  exit 1
fi
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  cat <<'JSON'
{
  "number": 77,
  "url": "https://github.com/hoxiai/hoxi/pull/77",
  "title": "Existing PR",
  "state": "OPEN",
  "isDraft": false,
  "additions": 10,
  "deletions": 2,
  "changedFiles": 1,
  "baseRefName": "main",
  "headRefName": "hoxi/ses_exists",
  "statusCheckRollup": []
}
JSON
  exit 0
fi
exit 1`)

	resp := svc.Create(context.Background(), CreateRequest{
		Directory: t.TempDir(),
		Head:      "hoxi/ses_exists",
	})
	if !resp.OK || resp.PullRequest == nil || resp.PullRequest.Number != 77 {
		t.Fatalf("expected existing PR #77 to be returned as OK, got %+v", resp)
	}
}
