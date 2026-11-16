package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

func sanitizeGitRefArg(ref string) (string, bool) {
	cleaned := strings.TrimSpace(ref)
	if cleaned == "" {
		return "", true
	}
	if strings.HasPrefix(cleaned, "-") || strings.Contains(cleaned, "..") || strings.ContainsAny(cleaned, " \t\r\n~^:?*[\\") {
		return "", false
	}
	return cleaned, true
}

func extractCreatedPullRequestURL(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "http://") {
			return trimmed
		}
	}
	return ""
}

func defaultPullRequestBodyFromCommits(ctx context.Context, dir, base, head, title string) string {
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var logArgs []string
	if base != "" && head != "" {
		logArgs = []string{"-C", dir, "log", "--no-merges", "--reverse", "--max-count=10", "--format=- %s", base + ".." + head}
	} else {
		logArgs = []string{"-C", dir, "log", "-1", "--format=%B", "HEAD"}
	}
	cmd := subproc.CommandContext(gitCtx, "git", logArgs...)
	cmd.Dir = dir
	cmd.Env = forgeEnv()
	stdout := &limitedBuffer{max: 16 << 10}
	cmd.Stdout = stdout
	if err := cmd.Run(); err == nil {
		if summary := strings.TrimSpace(string(stdout.Bytes())); summary != "" {
			return summary
		}
	}
	return strings.TrimSpace(title)
}

// Create creates a pull request via `gh pr create` and returns the resulting PR metadata.
func (s *Service) Create(ctx context.Context, req CreateRequest) CreateResponse {
	head, headOK := sanitizeGitRefArg(req.Head)
	base, baseOK := sanitizeGitRefArg(req.Base)
	if !headOK || !baseOK {
		return CreateResponse{
			OK:        false,
			Available: false,
			Reason:    "invalid_branch",
			Error:     "invalid branch reference",
			Branch:    strings.TrimSpace(req.Head),
		}
	}
	dir, reason := resolveDirectory(req.Directory)
	if reason != "" {
		return CreateResponse{
			OK:        false,
			Available: false,
			Reason:    reason,
			Branch:    head,
		}
	}

	args := []string{"pr", "create"}
	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if body == "" {
		body = defaultPullRequestBodyFromCommits(ctx, dir, base, head, title)
	}
	if title != "" {
		args = append(args, "--title", title, "--body", body)
	} else {
		args = append(args, "--fill")
		if body != "" {
			args = append(args, "--body", body)
		}
	}
	if base != "" {
		args = append(args, "--base", base)
	}
	if head != "" {
		args = append(args, "--head", head)
	}
	if req.Draft {
		args = append(args, "--draft")
	}

	result := s.runGH(ctx, dir, args...)
	if result.err != nil {
		stderrStr := strings.TrimSpace(string(result.stderr))
		if strings.Contains(strings.ToLower(stderrStr), "already exists") {
			s.invalidateListCache(dir)
			if status := s.Status(ctx, dir, head); status.Available && status.PullRequest != nil {
				return CreateResponse{
					OK:          true,
					Available:   true,
					URL:         status.PullRequest.URL,
					Branch:      status.Branch,
					PullRequest: status.PullRequest,
				}
			}
			if existingURL := extractCreatedPullRequestURL(stderrStr); existingURL != "" {
				return CreateResponse{
					OK:        true,
					Available: true,
					URL:       existingURL,
					Branch:    head,
				}
			}
		}
		available, failReason := classifyGHFailure(result)
		if stderrStr == "" {
			stderrStr = strings.TrimSpace(string(result.stdout))
		}
		return CreateResponse{
			OK:        false,
			Available: available,
			Reason:    failReason,
			Error:     stderrStr,
			Branch:    head,
		}
	}

	s.invalidateListCache(dir)
	createdURL := extractCreatedPullRequestURL(string(result.stdout))
	status := s.Status(ctx, dir, head)
	var pr *PullRequest
	if status.Available && status.PullRequest != nil {
		pr = status.PullRequest
		if createdURL == "" {
			createdURL = pr.URL
		}
		if head == "" {
			head = status.Branch
		}
	}
	return CreateResponse{
		OK:          true,
		Available:   true,
		URL:         createdURL,
		Branch:      head,
		PullRequest: pr,
	}
}

func (s *Service) handleCreatePullRequest(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, CreateResponse{
			OK:     false,
			Reason: "invalid_request",
			Error:  "invalid request body",
		})
		return
	}
	if strings.TrimSpace(req.Directory) == "" {
		req.Directory = r.URL.Query().Get("directory")
	}
	resp := s.Create(r.Context(), req)
	writeJSON(w, http.StatusOK, resp)
}
