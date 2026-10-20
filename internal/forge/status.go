package forge

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
)

// Status resolves the pull request attached to branch in directory. A branch
// without a PR is a successful response with no PullRequest.
func (s *Service) Status(ctx context.Context, directory, branch string) Response {
	branch = strings.TrimSpace(branch)
	dir, reason := resolveDirectory(directory)
	if reason != "" {
		return Response{Available: false, Reason: reason, Branch: branch}
	}

	args := []string{"pr", "view"}
	if branch != "" {
		args = append(args, branch)
	}
	args = append(args, "--json", prJSONFields)

	result := s.runGH(ctx, dir, args...)
	if result.err != nil {
		return classifyError(result, branch)
	}

	pr, err := parsePullRequest(result.stdout)
	if err != nil {
		return Response{Available: false, Reason: "invalid_response", Branch: branch}
	}
	if branch == "" {
		branch = pr.HeadRefName
	}
	return Response{Available: true, Branch: branch, PullRequest: &pr}
}

func classifyError(result ghResult, branch string) Response {
	available, reason := classifyGHFailure(result)
	return Response{Available: available, Reason: reason, Branch: branch}
}

func classifyGHFailure(result ghResult) (bool, string) {
	if errors.Is(result.err, exec.ErrNotFound) {
		return false, "gh_missing"
	}
	if result.timedOut {
		return false, "timeout"
	}

	stderr := strings.ToLower(string(result.stderr))
	switch {
	case strings.Contains(stderr, "no pull requests found"):
		return true, ""
	case strings.Contains(stderr, "could not determine"),
		strings.Contains(stderr, "not a git repository"),
		strings.Contains(stderr, "no git remotes"),
		strings.Contains(stderr, "no remotes"),
		strings.Contains(stderr, "http 404"):
		return false, "not_github"
	case strings.Contains(stderr, "auth"),
		strings.Contains(stderr, "login"),
		strings.Contains(stderr, "token"):
		return false, "not_authenticated"
	default:
		return false, "gh_failed"
	}
}

type ghPullRequest struct {
	Number            int       `json:"number"`
	URL               string    `json:"url"`
	Title             string    `json:"title"`
	State             string    `json:"state"`
	IsDraft           bool      `json:"isDraft"`
	ReviewDecision    string    `json:"reviewDecision"`
	MergeStateStatus  string    `json:"mergeStateStatus"`
	Mergeable         string    `json:"mergeable"`
	Additions         int       `json:"additions"`
	Deletions         int       `json:"deletions"`
	ChangedFiles      int       `json:"changedFiles"`
	BaseRefName       string    `json:"baseRefName"`
	HeadRefName       string    `json:"headRefName"`
	StatusCheckRollup []ghCheck `json:"statusCheckRollup"`
}

type ghCheck struct {
	TypeName   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
}

func parsePullRequest(raw []byte) (PullRequest, error) {
	var source ghPullRequest
	if err := json.Unmarshal(raw, &source); err != nil {
		return PullRequest{}, err
	}
	if source.Number <= 0 || strings.TrimSpace(source.URL) == "" {
		return PullRequest{}, errors.New("pull request is missing number or url")
	}

	checks := make([]Check, 0, len(source.StatusCheckRollup))
	passed, failed, pending := 0, 0, 0
	for _, rawCheck := range source.StatusCheckRollup {
		check := normalizeCheck(rawCheck)
		if check.Name == "" {
			continue
		}
		checks = append(checks, check)
		switch check.State {
		case "success", "neutral":
			passed++
		case "failure":
			failed++
		default:
			pending++
		}
	}

	checksState := "none"
	if len(checks) > 0 {
		switch {
		case failed > 0:
			checksState = "failure"
		case pending > 0:
			checksState = "pending"
		default:
			checksState = "success"
		}
	}

	return PullRequest{
		Number:           source.Number,
		URL:              strings.TrimSpace(source.URL),
		Title:            strings.TrimSpace(source.Title),
		State:            strings.ToUpper(strings.TrimSpace(source.State)),
		IsDraft:          source.IsDraft,
		ReviewDecision:   strings.ToUpper(strings.TrimSpace(source.ReviewDecision)),
		MergeStateStatus: strings.ToUpper(strings.TrimSpace(source.MergeStateStatus)),
		Mergeable:        strings.ToUpper(strings.TrimSpace(source.Mergeable)),
		Additions:        source.Additions,
		Deletions:        source.Deletions,
		ChangedFiles:     source.ChangedFiles,
		BaseRefName:      strings.TrimSpace(source.BaseRefName),
		HeadRefName:      strings.TrimSpace(source.HeadRefName),
		Checks:           checks,
		ChecksPassed:     passed,
		ChecksFailed:     failed,
		ChecksPending:    pending,
		ChecksTotal:      len(checks),
		ChecksState:      checksState,
	}, nil
}

func normalizeCheck(source ghCheck) Check {
	name := strings.TrimSpace(source.Name)
	if name == "" {
		name = strings.TrimSpace(source.Context)
	}
	url := strings.TrimSpace(source.DetailsURL)
	if url == "" {
		url = strings.TrimSpace(source.TargetURL)
	}

	state := "pending"
	switch {
	case strings.TrimSpace(source.State) != "":
		switch strings.ToUpper(strings.TrimSpace(source.State)) {
		case "SUCCESS":
			state = "success"
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			state = "failure"
		case "NEUTRAL", "SKIPPED":
			state = "neutral"
		default:
			state = "pending"
		}
	case strings.TrimSpace(source.Conclusion) != "":
		switch strings.ToUpper(strings.TrimSpace(source.Conclusion)) {
		case "SUCCESS":
			state = "success"
		case "NEUTRAL", "SKIPPED":
			state = "neutral"
		case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
			state = "failure"
		default:
			state = "pending"
		}
	}

	return Check{Name: name, State: state, URL: url}
}
