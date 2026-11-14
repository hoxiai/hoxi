package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	defaultTimeout      = 20 * time.Second
	defaultListCacheTTL = 15 * time.Second
	maxListCacheEntries = 64
	maxOutputBytes      = 1 << 20
	prJSONFields        = "number,url,title,state,isDraft,reviewDecision,mergeStateStatus,mergeable,additions,deletions,changedFiles,baseRefName,headRefName,statusCheckRollup"
)

type cachedPullRequestList struct {
	resp      ListResponse
	expiresAt time.Time
}

type pullRequestListFlight struct {
	done chan struct{}
	resp ListResponse
}

// Service reads pull request status through the GitHub CLI.
type Service struct {
	ghPath       string
	timeout      time.Duration
	listTTL      time.Duration
	listMu       sync.Mutex
	listCache    map[string]cachedPullRequestList
	listInflight map[string]*pullRequestListFlight
}

// NewService returns a forge service that shells out to `gh`.
func NewService() *Service {
	return &Service{ghPath: "gh", timeout: defaultTimeout}
}

// RegisterRoutes mounts the read-only pull request status endpoints.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/forge/pr", s.handlePullRequest)
	mux.HandleFunc("/api/hoxi/forge/pr/detail", s.handlePullRequestDetail)
	mux.HandleFunc("/api/hoxi/forge/prs", s.handlePullRequests)
}

func (s *Service) handlePullRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		response := s.Status(r.Context(), r.URL.Query().Get("directory"), r.URL.Query().Get("branch"))
		writeJSON(w, http.StatusOK, response)
	case http.MethodPost:
		s.handleCreatePullRequest(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Service) handlePullRequestDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	response := s.Detail(r.Context(), r.URL.Query().Get("directory"), r.URL.Query().Get("branch"))
	writeJSON(w, http.StatusOK, response)
}

func (s *Service) handlePullRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	response := s.List(r.Context(), r.URL.Query().Get("directory"))
	writeJSON(w, http.StatusOK, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type ghResult struct {
	stdout   []byte
	stderr   []byte
	err      error
	timedOut bool
}

func (s *Service) runGH(ctx context.Context, dir string, args ...string) ghResult {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := subproc.CommandContext(ctx, s.ghPath, args...)
	cmd.Dir = dir
	cmd.Env = forgeEnv()

	stdout := &limitedBuffer{max: maxOutputBytes}
	stderr := &limitedBuffer{max: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	return ghResult{
		stdout:   stdout.Bytes(),
		stderr:   stderr.Bytes(),
		err:      err,
		timedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
}

// forgeEnv keeps gh non-interactive and prevents it from waiting on editors or
// credential prompts while a session is running.
func forgeEnv() []string {
	blocked := map[string]bool{
		"GIT_ASKPASS": true,
		"SSH_ASKPASS": true,
		"GIT_EDITOR":  true,
		"EDITOR":      true,
		"VISUAL":      true,
	}
	env := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		if blocked[key] {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"GH_PAGER=cat",
		"NO_COLOR=1",
		"TERM=dumb",
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
}

type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if b.max <= 0 || b.buf.Len() >= b.max {
		return original, nil
	}
	remaining := b.max - b.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return original, nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func resolveDirectory(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "invalid_directory"
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", "invalid_directory"
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", "invalid_directory"
	}
	return abs, ""
}

// canonicalRepoDir maps an isolated Hoxi worktree path (<repo>/.hoxi/worktrees/<id>)
// back to its parent repository root when that root exists on disk, so all
// worktrees of the same repository share one PR list cache entry and subprocess.
func canonicalRepoDir(absDir string) string {
	slashed := filepath.ToSlash(filepath.Clean(absDir))
	const marker = "/.hoxi/worktrees/"
	idx := strings.Index(slashed, marker)
	if idx <= 0 {
		return absDir
	}
	parent := filepath.FromSlash(slashed[:idx])
	if info, err := os.Stat(parent); err == nil && info.IsDir() {
		return parent
	}
	return absDir
}
