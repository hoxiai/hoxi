package sessionstore

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	applyPatchHeaderRe    = regexp.MustCompile(`(?m)^\*\*\*\s+(?:Update|Add|Delete)\s+File:\s*(.+)$`)
	applyPatchMoveToRe    = regexp.MustCompile(`(?m)^\*\*\*\s+Move to:\s*(.+)$`)
	taskOutputSessionIDRe = regexp.MustCompile(`session_id:\s*([a-zA-Z0-9_-]+)`)
)

// Only tool edit/link contributions belong here, never text/reasoning payloads.
// Counts let a corrected part remove its old path without dropping another
// part's edit to the same file.
type editContribution struct{ paths, children []string }

type transcriptEdits struct {
	parts    map[partKey]editContribution
	paths    map[string]int
	children map[string]int
}

func editRelevantPart(part *MessagePart) bool {
	return part != nil && part.Type == "tool" && (IsRunnerEditingTool(part.Tool) || IsRunnerTaskTool(part.Tool))
}

func uniqueEditValues(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}

func (e *transcriptEdits) apply(rec SessionRecord, part MessagePart) bool {
	key := partKey{part.MessageID, part.ID}
	var next editContribution
	if part.Type == "tool" && part.State != nil {
		if IsRunnerTaskTool(part.Tool) {
			for _, id := range ExtractChildSessionIDsFromPart(part) {
				if id = strings.TrimSpace(id); id != "" {
					next.children = append(next.children, id)
				}
			}
		} else if part.State.Status == "completed" && IsRunnerEditingTool(part.Tool) {
			for _, path := range ExtractEditedPathsFromPart(part) {
				if path = NormalizeSessionRelPath(path, rec.ProjectDir, rec.WorktreePath); path != "" {
					next.paths = append(next.paths, path)
				}
			}
		}
	}
	next.paths, next.children = uniqueEditValues(next.paths), uniqueEditValues(next.children)
	prev := e.parts[key]
	if slices.Equal(prev.paths, next.paths) && slices.Equal(prev.children, next.children) {
		return false
	}
	adjust := func(counts map[string]int, values []string, delta int) {
		for _, value := range values {
			counts[value] += delta
			if counts[value] <= 0 {
				delete(counts, value)
			}
		}
	}
	adjust(e.paths, prev.paths, -1)
	adjust(e.children, prev.children, -1)
	adjust(e.paths, next.paths, 1)
	adjust(e.children, next.children, 1)
	if len(next.paths)+len(next.children) == 0 {
		delete(e.parts, key)
	} else {
		e.parts[key] = next
	}
	return true
}

type SessionEditsSnapshot struct {
	Record   SessionRecord
	Paths    []string
	Children []string
}

func (s *Store) SessionEditsSnapshot(id string) (SessionEditsSnapshot, bool) {
	ss := s.lockSession(id, false)
	if ss == nil {
		return SessionEditsSnapshot{}, false
	}
	defer s.finishSession(ss)
	if !ss.turnsLoaded {
		ss.diskMu.Lock()
		s.applyPersistResultLocked(ss)
		paths, children, err := s.readIndexedEditSummary(ss.rec)
		ss.diskMu.Unlock()
		if err == nil {
			return SessionEditsSnapshot{Record: metadataOnly(ss.rec), Paths: paths, Children: children}, true
		}
	}
	if ss.edits == nil {
		s.ensureTurnsLocked(ss)
		ss.edits = &transcriptEdits{parts: make(map[partKey]editContribution), paths: make(map[string]int), children: make(map[string]int)}
		for _, turn := range ss.rec.Turns {
			for _, part := range turn.Parts {
				ss.edits.apply(ss.rec, part)
			}
		}
	}
	out := SessionEditsSnapshot{Record: metadataOnly(ss.rec)}
	for path := range ss.edits.paths {
		out.Paths = append(out.Paths, path)
	}
	for child := range ss.edits.children {
		out.Children = append(out.Children, child)
	}
	return out, true
}

func IsRunnerEditingTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	if t == "" || t == "todowrite" || t == "todoread" || t == "glob" || t == "grep" || t == "read" || strings.HasSuffix(t, "_read") {
		return false
	}
	return strings.Contains(t, "edit") || strings.Contains(t, "write") || strings.Contains(t, "patch") || strings.Contains(t, "replace")
}

func IsRunnerTaskTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	return t == "task" || t == "agent" || strings.Contains(t, "subagent")
}

func ExtractChildSessionIDsFromPart(part MessagePart) []string {
	var ids []string
	add := func(v interface{}) {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			ids = append(ids, strings.TrimSpace(s))
		}
	}
	if part.State != nil {
		if part.State.Metadata != nil {
			add(part.State.Metadata["sessionId"])
			add(part.State.Metadata["sessionID"])
		}
		if part.State.Input != nil {
			add(part.State.Input["task_id"])
		}
		if m := taskOutputSessionIDRe.FindStringSubmatch(part.State.Output); len(m) > 1 {
			add(m[1])
		}
	}
	return ids
}

func ExtractPatchTextPaths(patchText string) []string {
	if strings.TrimSpace(patchText) == "" {
		return nil
	}
	var out []string
	for _, m := range applyPatchHeaderRe.FindAllStringSubmatch(patchText, -1) {
		if len(m) > 1 && strings.TrimSpace(m[1]) != "" {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	for _, m := range applyPatchMoveToRe.FindAllStringSubmatch(patchText, -1) {
		if len(m) > 1 && strings.TrimSpace(m[1]) != "" {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(patchText, "\r\n", "\n"), "\n") {
		var raw string
		switch {
		case strings.HasPrefix(line, "--- ") && !strings.HasPrefix(line, "--- /dev/null"):
			raw = strings.TrimSpace(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "+++ /dev/null"):
			raw = strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
		default:
			continue
		}
		if tab := strings.IndexByte(raw, '\t'); tab >= 0 {
			raw = strings.TrimSpace(raw[:tab])
		}
		raw = strings.TrimPrefix(strings.TrimPrefix(raw, "a/"), "b/")
		if raw != "" && raw != "/dev/null" {
			out = append(out, raw)
		}
	}
	return out
}

func addLegacyPatchSummaryFile(raw string, add func(interface{})) {
	s := strings.TrimSpace(raw)
	if len(s) > 2 && s[1] == ' ' {
		switch s[0] {
		case 'A', 'M', 'D':
			add(strings.TrimSpace(s[2:]))
			return
		case 'R':
			rest := strings.TrimSpace(s[2:])
			if oldP, newP, ok := strings.Cut(rest, " -> "); ok {
				add(oldP)
				add(newP)
				return
			}
		}
	}
	add(s)
}

func firstNonEmptyMapString(vals ...interface{}) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func ExtractEditedPathsFromPart(part MessagePart) []string {
	if part.State == nil {
		return nil
	}
	var out []string
	add := func(v interface{}) {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	addFileObj := func(v map[string]interface{}) {
		if v == nil {
			return
		}
		rel, _ := v["relativePath"].(string)
		abs, _ := v["filePath"].(string)
		if rel != "" && !strings.HasPrefix(rel, "..") {
			add(rel)
		} else if abs != "" {
			add(abs)
		} else {
			add(v["file"])
			add(v["path"])
		}
		if movePath, _ := v["movePath"].(string); movePath != "" {
			if abs != "" {
				add(abs)
			}
			add(movePath)
		}
	}
	if in := part.State.Input; in != nil {
		for _, k := range []string{"filePath", "file_path", "Path", "AbsolutePath", "path", "file", "filename", "targetFile", "TargetFile", "target_file", "notebook_path"} {
			add(in[k])
		}
		for _, pk := range []string{"patchText", "patch", "diff"} {
			if patchText, ok := in[pk].(string); ok && patchText != "" {
				for _, p := range ExtractPatchTextPaths(patchText) {
					add(p)
				}
			}
		}
	}
	if meta := part.State.Metadata; meta != nil {
		add(meta["filePath"])
		add(meta["filepath"])
		if fd, ok := meta["filediff"].(map[string]interface{}); ok {
			add(firstNonEmptyMapString(fd["file"], fd["path"], fd["filePath"]))
		}
		switch files := meta["files"].(type) {
		case []interface{}:
			for _, item := range files {
				switch v := item.(type) {
				case string:
					addLegacyPatchSummaryFile(v, add)
				case map[string]interface{}:
					addFileObj(v)
				}
			}
		case []map[string]interface{}:
			for _, v := range files {
				addFileObj(v)
			}
		case []string:
			for _, v := range files {
				addLegacyPatchSummaryFile(v, add)
			}
		}
	}
	return out
}

func NormalizeSessionRelPath(raw, projectDir, worktreeDir string) string {
	trimmed := strings.TrimSpace(filepath.ToSlash(raw))
	if trimmed == "" {
		return ""
	}
	for _, root := range []string{worktreeDir, projectDir} {
		r := strings.TrimRight(filepath.ToSlash(strings.TrimSpace(root)), "/")
		if r != "" && (trimmed == r || strings.HasPrefix(trimmed, r+"/")) {
			trimmed = strings.TrimLeft(trimmed[len(r):], "/")
			break
		}
	}
	trimmed = strings.TrimPrefix(trimmed, "./")
	if strings.HasPrefix(trimmed, ".hoxi/worktrees/") {
		rest := strings.TrimPrefix(trimmed, ".hoxi/worktrees/")
		if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			trimmed = strings.TrimLeft(rest[idx+1:], "/")
		}
	}
	return trimmed
}

func MatchesEditedFileSet(candidate, projectDir, worktreeDir string, edited map[string]struct{}) bool {
	norm := NormalizeSessionRelPath(candidate, projectDir, worktreeDir)
	if norm == "" {
		return false
	}
	if _, ok := edited[norm]; ok {
		return true
	}
	for k := range edited {
		if strings.HasSuffix(norm, "/"+k) || strings.HasSuffix(k, "/"+norm) {
			return true
		}
	}
	return false
}
