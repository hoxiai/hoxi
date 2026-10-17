package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type gitDailyPulseResponse struct {
	TodayCommits         int    `json:"todayCommits"`
	CommittedAdditions   int    `json:"committedAdditions"`
	CommittedDeletions   int    `json:"committedDeletions"`
	CommittedFiles       int    `json:"committedFiles"`
	UncommittedAdditions int    `json:"uncommittedAdditions"`
	UncommittedDeletions int    `json:"uncommittedDeletions"`
	UncommittedFiles     int    `json:"uncommittedFiles"`
	Branch               string `json:"branch,omitempty"`
	HeadCommit           string `json:"headCommit,omitempty"`
	HeadSubject          string `json:"headSubject,omitempty"`
}

func (g *desktopGateway) handleDevPulse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
	if dir != "" && g.worktrees != nil {
		g.worktrees.SetRootDir(dir)
	}

	workDir := g.rootDir
	if g.worktrees != nil {
		g.worktrees.mu.RLock()
		if g.worktrees.rootDir != "" {
			workDir = g.worktrees.rootDir
		}
		g.worktrees.mu.RUnlock()
	}
	if dir != "" {
		workDir = dir
	}

	res := gitDailyPulseResponse{}
	if workDir == "" {
		writeJSON(w, http.StatusOK, res)
		return
	}

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	sinceArg := "--since=" + startOfToday.Format(time.RFC3339)

	if logOut, err := runGitRaw(workDir, "log", sinceArg, "--numstat", "--pretty=format:COMMIT:%H"); err == nil {
		committedFileSet := make(map[string]struct{})
		for _, rawLine := range strings.Split(logOut, "\n") {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "COMMIT:") {
				res.TodayCommits++
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			file := fields[len(fields)-1]
			if file == "" || strings.HasPrefix(file, ".hoxi/") {
				continue
			}
			add, errAdd := strconv.Atoi(fields[0])
			del, errDel := strconv.Atoi(fields[1])
			if errAdd == nil {
				res.CommittedAdditions += add
			}
			if errDel == nil {
				res.CommittedDeletions += del
			}
			committedFileSet[file] = struct{}{}
		}
		res.CommittedFiles = len(committedFileSet)
	}

	if g.worktrees != nil {
		targetSession := "__root__"
		if sessionID != "" {
			targetSession = sessionID
		}
		if st, err := g.worktrees.Status(targetSession); err == nil {
			res.UncommittedAdditions = st.Additions
			res.UncommittedDeletions = st.Deletions
			res.UncommittedFiles = st.ChangedFiles
			res.Branch = st.Branch
			res.HeadCommit = st.HeadCommit
			res.HeadSubject = st.HeadSubject
		}
	}

	writeJSON(w, http.StatusOK, res)
}
