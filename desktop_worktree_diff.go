package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type worktreeFileDiff struct {
	File      string `json:"file"`
	Patch     string `json:"patch,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status,omitempty"`
}

func (m *worktreeManager) resolveSessionMeta(sessionID string) worktreeMeta {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if sessionID != "" && sessionID != "__root__" {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return worktreeMeta{
					SessionID: sessionID,
					Isolated:  true,
					Directory: candidate,
					Branch:    "hoxi/" + sessionID,
				}
			}
		}
		return worktreeMeta{
			SessionID: sessionID,
			Isolated:  false,
			Directory: m.rootDir,
		}
	}
	return meta
}

func (m *worktreeManager) Diff(sessionID string) ([]worktreeFileDiff, error) {
	meta := m.resolveSessionMeta(sessionID)
	if meta.Directory == "" {
		return []worktreeFileDiff{}, nil
	}

	diffTarget := "HEAD"
	if meta.Isolated && strings.TrimSpace(meta.BaseCommit) != "" {
		diffTarget = strings.TrimSpace(meta.BaseCommit)
	}

	numstat, err := runGit(meta.Directory, "diff", "--numstat", diffTarget)
	if err != nil && diffTarget != "HEAD" {
		diffTarget = "HEAD"
		numstat, _ = runGit(meta.Directory, "diff", "--numstat", "HEAD")
	}

	nameStatusRaw, _ := runGit(meta.Directory, "diff", "--name-status", diffTarget)
	statusByFile := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(nameStatusRaw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		code := fields[0]
		file := fields[len(fields)-1]
		switch {
		case strings.HasPrefix(code, "A"):
			statusByFile[file] = "added"
		case strings.HasPrefix(code, "D"):
			statusByFile[file] = "deleted"
		default:
			statusByFile[file] = "modified"
		}
	}

	results := make([]worktreeFileDiff, 0)
	seen := make(map[string]struct{})

	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		file := fields[len(fields)-1]
		if file == "" || strings.HasPrefix(file, ".hoxi/") {
			continue
		}
		add, _ := strconv.Atoi(fields[0])
		del, _ := strconv.Atoi(fields[1])
		patch, _ := runGitRaw(meta.Directory, "diff", "--no-color", "-U3", diffTarget, "--", file)

		st := statusByFile[file]
		if st == "" {
			st = "modified"
		}
		seen[file] = struct{}{}
		results = append(results, worktreeFileDiff{
			File:      file,
			Patch:     strings.TrimSpace(patch),
			Additions: add,
			Deletions: del,
			Status:    st,
		})
	}

	porcelain, _ := runGitRaw(meta.Directory, "status", "--porcelain")
	for _, rawLine := range strings.Split(porcelain, "\n") {
		if !strings.HasPrefix(rawLine, "?? ") {
			continue
		}
		rel := strings.TrimSpace(rawLine[3:])
		if rel == "" || strings.HasPrefix(rel, ".hoxi/") {
			continue
		}
		if _, exists := seen[rel]; exists {
			continue
		}
		full := filepath.Join(meta.Directory, rel)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() > 256*1024 {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil || bytes.IndexByte(data, 0) != -1 {
			continue
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.TrimSuffix(text, "\n")
		lines := []string{}
		if len(data) > 0 {
			lines = strings.Split(text, "\n")
		}
		var patchBuf strings.Builder
		patchBuf.WriteString(fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", rel, rel, rel))
		if len(lines) > 0 {
			patchBuf.WriteString(fmt.Sprintf("@@ -0,0 +1,%d @@\n", len(lines)))
			for _, l := range lines {
				patchBuf.WriteString("+" + l + "\n")
			}
		}
		seen[rel] = struct{}{}
		results = append(results, worktreeFileDiff{
			File:      rel,
			Patch:     strings.TrimSpace(patchBuf.String()),
			Additions: len(lines),
			Deletions: 0,
			Status:    "added",
		})
	}

	return results, nil
}
