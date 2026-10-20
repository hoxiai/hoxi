package forge

import (
	"context"
	"encoding/json"
	"strings"
)

// List returns the most relevant pull request for every branch in a
// repository. One gh call serves the whole session list.
func (s *Service) List(ctx context.Context, directory string) ListResponse {
	dir, reason := resolveDirectory(directory)
	if reason != "" {
		return ListResponse{Available: false, Reason: reason, PullRequests: map[string]PullRequest{}}
	}

	result := s.runGH(ctx, dir,
		"pr", "list",
		"--state", "all",
		"--limit", "100",
		"--json", prJSONFields,
	)
	if result.err != nil {
		available, failure := classifyGHFailure(result)
		return ListResponse{Available: available, Reason: failure, PullRequests: map[string]PullRequest{}}
	}

	var rows []json.RawMessage
	if err := json.Unmarshal(result.stdout, &rows); err != nil {
		return ListResponse{Available: false, Reason: "invalid_response", PullRequests: map[string]PullRequest{}}
	}

	pullRequests := make(map[string]PullRequest, len(rows))
	for _, row := range rows {
		pr, err := parsePullRequest(row)
		if err != nil || pr.HeadRefName == "" {
			continue
		}
		current, exists := pullRequests[pr.HeadRefName]
		if !exists || preferPullRequest(pr, current) {
			pullRequests[pr.HeadRefName] = pr
		}
	}
	return ListResponse{Available: true, PullRequests: pullRequests}
}

// preferPullRequest keeps open work visible, then the newest PR number.
func preferPullRequest(candidate, current PullRequest) bool {
	candidateRank := pullRequestStateRank(candidate.State)
	currentRank := pullRequestStateRank(current.State)
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	return candidate.Number > current.Number
}

func pullRequestStateRank(state string) int {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "OPEN":
		return 3
	case "MERGED":
		return 2
	case "CLOSED":
		return 1
	default:
		return 0
	}
}
