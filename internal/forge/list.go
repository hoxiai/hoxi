package forge

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func cloneListResponse(resp ListResponse) ListResponse {
	cloned := make(map[string]PullRequest, len(resp.PullRequests))
	for branch, pr := range resp.PullRequests {
		if len(pr.Checks) > 0 {
			checks := make([]Check, len(pr.Checks))
			copy(checks, pr.Checks)
			pr.Checks = checks
		}
		cloned[branch] = pr
	}
	resp.PullRequests = cloned
	return resp
}

func (s *Service) effectiveListTTL() time.Duration {
	if s.listTTL > 0 {
		return s.listTTL
	}
	return defaultListCacheTTL
}

func (s *Service) invalidateListCache(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	repoDir := canonicalRepoDir(dir)
	s.listMu.Lock()
	if s.listCache != nil {
		delete(s.listCache, repoDir)
	}
	s.listMu.Unlock()
}

// List returns the most relevant pull request for every branch in a
// repository. One gh call serves the whole session list, coalescing concurrent
// requests and caching results briefly across worktree paths of the same repo.
func (s *Service) List(ctx context.Context, directory string) ListResponse {
	dir, reason := resolveDirectory(directory)
	if reason != "" {
		return ListResponse{Available: false, Reason: reason, PullRequests: map[string]PullRequest{}}
	}
	if err := ctx.Err(); err != nil {
		return ListResponse{Available: true, Reason: "timeout", PullRequests: map[string]PullRequest{}}
	}
	repoDir := canonicalRepoDir(dir)
	now := time.Now()

	s.listMu.Lock()
	if entry, ok := s.listCache[repoDir]; ok {
		if now.Before(entry.expiresAt) {
			cached := cloneListResponse(entry.resp)
			s.listMu.Unlock()
			return cached
		}
		delete(s.listCache, repoDir)
	}
	flight, joined := s.listInflight[repoDir]
	if !joined {
		if s.listInflight == nil {
			s.listInflight = make(map[string]*pullRequestListFlight)
		}
		flight = &pullRequestListFlight{done: make(chan struct{})}
		s.listInflight[repoDir] = flight
		go func() {
			resp, cacheable := s.fetchList(context.Background(), repoDir)
			s.listMu.Lock()
			if cacheable {
				if s.listCache == nil {
					s.listCache = make(map[string]cachedPullRequestList)
				}
				if len(s.listCache) >= maxListCacheEntries {
					var oldestKey string
					var oldestExp time.Time
					for k, v := range s.listCache {
						if oldestKey == "" || v.expiresAt.Before(oldestExp) {
							oldestKey = k
							oldestExp = v.expiresAt
						}
					}
					if oldestKey != "" {
						delete(s.listCache, oldestKey)
					}
				}
				s.listCache[repoDir] = cachedPullRequestList{
					resp:      cloneListResponse(resp),
					expiresAt: time.Now().Add(s.effectiveListTTL()),
				}
			}
			delete(s.listInflight, repoDir)
			flight.resp = resp
			close(flight.done)
			s.listMu.Unlock()
		}()
	}
	s.listMu.Unlock()

	select {
	case <-flight.done:
		return cloneListResponse(flight.resp)
	case <-ctx.Done():
		return ListResponse{Available: true, Reason: "timeout", PullRequests: map[string]PullRequest{}}
	}
}

func (s *Service) fetchList(ctx context.Context, dir string) (ListResponse, bool) {
	result := s.runGH(ctx, dir,
		"pr", "list",
		"--state", "all",
		"--limit", "100",
		"--json", prJSONFields,
	)
	if result.err != nil {
		available, failure := classifyGHFailure(result)
		return ListResponse{Available: available, Reason: failure, PullRequests: map[string]PullRequest{}}, failure != "timeout"
	}

	var rows []json.RawMessage
	if err := json.Unmarshal(result.stdout, &rows); err != nil {
		return ListResponse{Available: false, Reason: "invalid_response", PullRequests: map[string]PullRequest{}}, false
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
	return ListResponse{Available: true, PullRequests: pullRequests}, true
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
