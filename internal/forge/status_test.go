package forge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

func TestMain(m *testing.M) {
	execstub.Main()
	os.Exit(m.Run())
}

func testService(t *testing.T, script string) *Service {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script gh stub is not available on Windows")
	}
	path := execstub.Write(t, t.TempDir(), "gh", script+"\n")
	return &Service{ghPath: path, timeout: defaultTimeout}
}

func TestStatusParsesPullRequestAndChecks(t *testing.T) {
	svc := testService(t, `cat <<'JSON'
{
  "number": 42,
  "url": "https://github.com/hoxiai/hoxi/pull/42",
  "title": "Add PR badge",
  "state": "OPEN",
  "isDraft": false,
  "reviewDecision": "APPROVED",
  "mergeStateStatus": "CLEAN",
  "mergeable": "MERGEABLE",
  "additions": 120,
  "deletions": 8,
  "changedFiles": 3,
  "baseRefName": "main",
  "headRefName": "feat/pr-badge",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "build", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "https://github.com/actions/1"},
    {"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "FAILURE", "detailsUrl": "https://github.com/actions/2"},
    {"__typename": "StatusContext", "context": "deploy", "state": "PENDING", "targetUrl": "https://ci/3"}
  ]
}
JSON`)

	resp := svc.Status(context.Background(), t.TempDir(), "feat/pr-badge")
	if !resp.Available || resp.PullRequest == nil {
		t.Fatalf("expected available PR, got %+v", resp)
	}
	pr := resp.PullRequest
	if pr.Number != 42 || pr.URL == "" || pr.Title != "Add PR badge" {
		t.Fatalf("unexpected PR identity: %+v", pr)
	}
	if pr.ChecksTotal != 3 || pr.ChecksPassed != 1 || pr.ChecksFailed != 1 || pr.ChecksPending != 1 {
		t.Fatalf("unexpected checks: %+v", pr)
	}
	if pr.ChecksState != "failure" {
		t.Fatalf("expected failure checks state, got %q", pr.ChecksState)
	}
	if pr.Checks[2].Name != "deploy" || pr.Checks[2].URL != "https://ci/3" {
		t.Fatalf("status context was not normalized: %+v", pr.Checks[2])
	}
}

func TestStatusBranchWithoutPullRequest(t *testing.T) {
	svc := testService(t, `echo 'no pull requests found for branch "feat/none"' >&2
exit 1`)

	resp := svc.Status(context.Background(), t.TempDir(), "feat/none")
	if !resp.Available || resp.PullRequest != nil {
		t.Fatalf("expected available without PR, got %+v", resp)
	}
	if resp.Branch != "feat/none" {
		t.Fatalf("expected branch to be preserved, got %q", resp.Branch)
	}
}

func TestStatusReportsNotGitHub(t *testing.T) {
	svc := testService(t, `echo 'could not determine base repo' >&2
exit 1`)

	resp := svc.Status(context.Background(), t.TempDir(), "main")
	if resp.Available || resp.Reason != "not_github" {
		t.Fatalf("expected not_github, got %+v", resp)
	}
}

func TestStatusReportsMissingGH(t *testing.T) {
	svc := &Service{ghPath: "hoxi-gh-that-does-not-exist", timeout: defaultTimeout}
	resp := svc.Status(context.Background(), t.TempDir(), "main")
	if resp.Available || resp.Reason != "gh_missing" {
		t.Fatalf("expected gh_missing, got %+v", resp)
	}
}

func TestStatusRejectsInvalidDirectory(t *testing.T) {
	svc := testService(t, `exit 0`)
	resp := svc.Status(context.Background(), filepath.Join(t.TempDir(), "missing"), "main")
	if resp.Available || resp.Reason != "invalid_directory" {
		t.Fatalf("expected invalid_directory, got %+v", resp)
	}
}

func TestNormalizeCheckPrefersConclusionForCheckRuns(t *testing.T) {
	check := normalizeCheck(ghCheck{Name: "test", Status: "COMPLETED", Conclusion: "NEUTRAL"})
	if check.State != "neutral" {
		t.Fatalf("expected neutral, got %+v", check)
	}
	check = normalizeCheck(ghCheck{Context: "legacy", State: "ERROR"})
	if check.Name != "legacy" || check.State != "failure" {
		t.Fatalf("expected legacy failure, got %+v", check)
	}
}
