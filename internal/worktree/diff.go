package worktree

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

	numstat, err := runGitRaw(meta.Directory, "diff", "--no-renames", "--numstat", "-z", diffTarget)
	if err != nil && diffTarget != "HEAD" {
		diffTarget = "HEAD"
		numstat, _ = runGitRaw(meta.Directory, "diff", "--no-renames", "--numstat", "-z", "HEAD")
	}

	patchesRaw, _ := runGitRaw(meta.Directory, "diff", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color", "-U3", diffTarget)
	patches := strings.Split("\n"+patchesRaw, "\ndiff --git ")
	if len(patches) > 0 {
		patches = patches[1:]
	}

	results := make([]worktreeFileDiff, 0)
	seen := make(map[string]struct{})

	for i, line := range strings.Split(strings.TrimSuffix(numstat, "\x00"), "\x00") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		file := fields[2]
		if file == "" || strings.HasPrefix(file, ".hoxi/") {
			continue
		}
		add, _ := strconv.Atoi(fields[0])
		del, _ := strconv.Atoi(fields[1])
		patch := ""
		if i < len(patches) {
			patch = "diff --git " + patches[i]
		}
		st := "modified"
		if strings.Contains(patch, "\nnew file mode ") {
			st = "added"
		} else if strings.Contains(patch, "\ndeleted file mode ") {
			st = "deleted"
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

	untracked, _ := runGitRaw(meta.Directory, "ls-files", "--others", "--exclude-standard", "-z")
	for _, rel := range strings.Split(untracked, "\x00") {
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
