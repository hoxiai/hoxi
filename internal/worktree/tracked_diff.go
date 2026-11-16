package worktree

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Plan before asking Git to materialize patch bodies. In particular, lockfiles
// and generated bundles must not allocate megabytes only to be discarded later.
func planTrackedPatches(repoDir, numstat string) (inline []string, deferred []string) {
	pathBytes := 0
	for _, line := range strings.Split(strings.TrimSuffix(numstat, "\x00"), "\x00") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || fields[2] == "" || strings.HasPrefix(fields[2], ".hoxi/") {
			continue
		}
		if len(inline)+len(deferred) >= maxTotalDiffFiles {
			break
		}
		file := fields[2]
		add, _ := strconv.Atoi(fields[0])
		del, _ := strconv.Atoi(fields[1])
		generated := classifyGeneratedFile(file)
		large := (generated && add+del > 300) || len(inline) >= 64 || pathBytes+len(file)+1 > 48*1024
		if info, err := os.Lstat(filepath.Join(repoDir, filepath.FromSlash(file))); err == nil && info.Mode().IsRegular() {
			large = large || info.Size() > maxSingleFilePatchBytes || (generated && info.Size() > maxGeneratedInlinePatchLen)
		}
		if large {
			deferred = append(deferred, file)
		} else {
			inline = append(inline, file)
			pathBytes += len(file) + 1
		}
	}
	return inline, deferred
}

func trackedPatchArgs(diffTarget string, cached bool, files []string, nameStatus bool) []string {
	args := []string{"-c", "core.quotepath=false", "--literal-pathspecs", "diff", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color"}
	if nameStatus {
		args = append(args, "--name-status", "-z")
	} else {
		args = append(args, "-U3")
	}
	if cached {
		args = append(args, "--cached")
	} else {
		args = append(args, diffTarget)
	}
	return append(append(args, "--"), files...)
}

func deferredTrackedStatuses(repoDir, diffTarget string, cached bool, files []string) map[string]string {
	statuses := make(map[string]string, len(files))
	if len(files) == 0 {
		return statuses
	}
	for _, file := range files {
		statuses[filepath.ToSlash(file)] = "modified"
	}
	// Names/statuses are cheap and contain no patch bodies. Query them together
	// without a potentially unbounded argv for the deferred file set.
	raw, err := runGitRaw(repoDir, trackedPatchArgs(diffTarget, cached, nil, true)...)
	if err != nil {
		return statuses
	}
	entries := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	for i := 0; i+1 < len(entries); i += 2 {
		status := "modified"
		switch entries[i] {
		case "A":
			status = "added"
		case "D":
			status = "deleted"
		}
		file := filepath.ToSlash(entries[i+1])
		if _, deferred := statuses[file]; deferred {
			statuses[file] = status
		}
	}
	return statuses
}

func patchHeaderFile(chunk string) string {
	for _, line := range strings.SplitN(chunk, "\n", 12) {
		var path string
		if strings.HasPrefix(line, "+++ ") {
			path = strings.TrimPrefix(line, "+++ ")
		} else if strings.HasPrefix(line, "--- ") {
			path = strings.TrimPrefix(line, "--- ")
		} else {
			continue
		}
		// Git appends a separator tab to unquoted header paths containing spaces.
		path = strings.TrimSuffix(path, "\t")
		if strings.HasPrefix(path, `"`) {
			unquoted, err := strconv.Unquote(path)
			if err != nil {
				continue
			}
			path = unquoted
		}
		if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
			return filepath.ToSlash(path[2:])
		}
	}
	firstLine := strings.SplitN(chunk, "\n", 2)[0]
	// Binary patches have no ---/+++ headers. Git quotes both paths when a
	// filename contains control characters or quotes, even with quotepath=false.
	if strings.HasPrefix(firstLine, `"`) {
		for i := 1; i < len(firstLine); i++ {
			if firstLine[i] == '\\' {
				i++
				continue
			}
			if firstLine[i] == '"' {
				path, err := strconv.Unquote(strings.TrimSpace(firstLine[i+1:]))
				if err == nil && strings.HasPrefix(path, "b/") {
					return filepath.ToSlash(path[2:])
				}
				break
			}
		}
	}
	if idx := strings.Index(firstLine, " b/"); idx >= 0 {
		return filepath.ToSlash(strings.TrimSpace(firstLine[idx+3:]))
	}
	return ""
}
