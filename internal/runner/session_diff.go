package runner

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	applyPatchHeaderRe    = regexp.MustCompile(`(?m)^\*\*\*\s+(?:Update|Add|Delete)\s+File:\s*(.+)$`)
	taskOutputSessionIDRe = regexp.MustCompile(`session_id:\s*([a-zA-Z0-9_-]+)`)
)

type editedFilesEntry struct {
	stamp int64
	files map[string]struct{}
}

func (s *Service) handleSessionDiff(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	rec, ok := s.store.Meta(sessionID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "runner session not found")
		return
	}
	projDir := firstNonEmptyStr(rec.ProjectDir, rec.WorktreePath, r.URL.Query().Get("directory"), s.rootDir)
	if rec.SandboxMode == "worktree" {
		diffs, err := s.worktrees.ForDirectory(projDir).Diff(sessionID)
		if err != nil || len(diffs) == 0 {
			writeJSON(w, http.StatusOK, []struct{}{})
			return
		}
		writeJSON(w, http.StatusOK, diffs)
		return
	}

	edited := s.sessionEditedFileSet(sessionID)
	if len(edited) == 0 {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	wtDiffs, err := s.worktrees.ForDirectory(projDir).DiffForFiles(sessionID, edited)
	if err != nil || len(wtDiffs) == 0 {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	filtered := wtDiffs[:0]
	for _, d := range wtDiffs {
		if matchesEditedFileSet(d.File, projDir, rec.WorktreePath, edited) {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		writeJSON(w, http.StatusOK, []struct{}{})
		return
	}
	writeJSON(w, http.StatusOK, filtered)
}

func (s *Service) filterLocalSessionDiffEvent(sessionID string, rawDiff interface{}) interface{} {
	rec, ok := s.store.Meta(sessionID)
	if !ok || rec.SandboxMode == "worktree" {
		return rawDiff
	}
	items, ok := rawDiff.([]interface{})
	if !ok {
		return []interface{}{}
	}
	edited := s.sessionEditedFileSet(sessionID)
	if len(edited) == 0 {
		return []interface{}{}
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		file, _ := m["file"].(string)
		if matchesEditedFileSet(file, rec.ProjectDir, rec.WorktreePath, edited) {
			out = append(out, item)
		}
	}
	return out
}

func (s *Service) sessionEditedFileSet(sessionID string) map[string]struct{} {
	rootRec, ok := s.store.Meta(sessionID)
	if !ok {
		return nil
	}
	childIDs, stamp := s.store.ChildSessionIDs(sessionID)
	if stamp != 0 {
		if cached, hit := s.editedFiles.Load(sessionID); hit {
			entry := cached.(editedFilesEntry)
			if entry.stamp == stamp {
				return entry.files
			}
		}
	}

	edited := make(map[string]struct{})
	visited := make(map[string]bool)
	hasExternalChild := false

	var walkSession func(sid string)
	walkTurns := func(turns []MessageTurn, projectDir, worktreePath string, discoveredChildren *[]string) {
		for i := range turns {
			parts := turns[i].Parts
			for j := range parts {
				part := parts[j]
				if part.Type != "tool" || part.State == nil {
					continue
				}
				if isRunnerTaskTool(part.Tool) {
					*discoveredChildren = append(*discoveredChildren, extractChildSessionIDsFromPart(part)...)
					continue
				}
				if part.State.Status != "completed" {
					continue
				}
				if isRunnerEditingTool(part.Tool) {
					for _, rawPath := range extractEditedPathsFromPart(part) {
						if norm := normalizeSessionRelPath(rawPath, projectDir, worktreePath); norm != "" {
							edited[norm] = struct{}{}
						}
					}
				}
			}
		}
	}

	walkSession = func(sid string) {
		sid = strings.TrimSpace(sid)
		if sid == "" || visited[sid] {
			return
		}
		visited[sid] = true
		var subChildren []string
		if s.store.VisitTurns(sid, func(rec SessionRecord) {
			walkTurns(rec.Turns, rec.ProjectDir, rec.WorktreePath, &subChildren)
		}) {
			for _, cid := range subChildren {
				walkSession(cid)
			}
			return
		}
		hasExternalChild = true
		if s.openCode.Available() {
			dir := firstNonEmptyStr(rootRec.WorktreePath, rootRec.ProjectDir, s.rootDir)
			if turns := s.openCodeChildTurns(sid, dir); len(turns) > 0 {
				walkTurns(turns, rootRec.ProjectDir, rootRec.WorktreePath, &subChildren)
				for _, cid := range subChildren {
					walkSession(cid)
				}
			}
		}
	}

	walkSession(sessionID)
	for _, cid := range childIDs {
		walkSession(cid)
	}
	if !hasExternalChild {
		_, postStamp := s.store.ChildSessionIDs(sessionID)
		if postStamp != 0 {
			s.editedFiles.Store(sessionID, editedFilesEntry{stamp: postStamp, files: edited})
		}
	}
	return edited
}

// Session diff events arrive in bursts on the broadcast path; cache each child
// transcript briefly so one event burst never fans out into blocking OpenCode
// requests per subagent, while keeping negative-cache backoff short (and
// serving last-known turns on transient failure) so the changed-files shelf
// does not drop entries under load jitter.
const (
	childTurnsTTL    = 3 * time.Second
	childTurnsErrTTL = 250 * time.Millisecond
)

type childTurnsKey struct {
	dir string
	sid string
}

type childTurnsEntry struct {
	turns   []MessageTurn
	expires time.Time
}

func (s *Service) openCodeChildTurns(sid, dir string) []MessageTurn {
	cleanDir := strings.TrimSpace(dir)
	if cleanDir != "" {
		cleanDir = filepath.Clean(cleanDir)
	}
	key := childTurnsKey{dir: cleanDir, sid: strings.TrimSpace(sid)}
	now := time.Now()
	var staleTurns []MessageTurn
	if v, ok := s.childTurns.Load(key); ok {
		entry := v.(childTurnsEntry)
		if now.Before(entry.expires) {
			return entry.turns
		}
		staleTurns = entry.turns
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var turns []MessageTurn
	ttl := childTurnsTTL
	if err := s.openCodeJSON(ctx, http.MethodGet, "/session/"+url.PathEscape(key.sid)+"/message", dir, nil, &turns); err != nil {
		turns = staleTurns
		ttl = childTurnsErrTTL
	}
	s.childTurns.Range(func(k, v any) bool {
		entry := v.(childTurnsEntry)
		if k != key && !now.Before(entry.expires) && len(entry.turns) == 0 {
			s.childTurns.Delete(k)
		}
		return true
	})
	s.childTurns.Store(key, childTurnsEntry{turns: turns, expires: now.Add(ttl)})
	return turns
}

func isRunnerEditingTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	if t == "" || t == "todowrite" || t == "todoread" || t == "glob" || t == "grep" || t == "read" || strings.HasSuffix(t, "_read") {
		return false
	}
	return strings.Contains(t, "edit") || strings.Contains(t, "write") || strings.Contains(t, "patch") || strings.Contains(t, "replace")
}

func isRunnerTaskTool(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	return t == "task" || t == "agent" || strings.Contains(t, "subagent")
}

func extractChildSessionIDsFromPart(part MessagePart) []string {
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

func extractEditedPathsFromPart(part MessagePart) []string {
	if part.State == nil {
		return nil
	}
	var out []string
	add := func(v interface{}) {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	if in := part.State.Input; in != nil {
		for _, k := range []string{"filePath", "file_path", "Path", "AbsolutePath", "path", "file", "filename", "targetFile", "TargetFile", "target_file", "notebook_path"} {
			add(in[k])
		}
		if patchText, ok := in["patchText"].(string); ok && patchText != "" {
			for _, m := range applyPatchHeaderRe.FindAllStringSubmatch(patchText, -1) {
				if len(m) > 1 {
					add(m[1])
				}
			}
		}
	}
	if meta := part.State.Metadata; meta != nil {
		add(meta["filePath"])
		add(meta["filepath"])
		if files, ok := meta["files"].([]interface{}); ok {
			for _, item := range files {
				switch v := item.(type) {
				case string:
					add(v)
				case map[string]interface{}:
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
				}
			}
		}
	}
	return out
}

func normalizeSessionRelPath(raw, projectDir, worktreeDir string) string {
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

func matchesEditedFileSet(candidate, projectDir, worktreeDir string, edited map[string]struct{}) bool {
	norm := normalizeSessionRelPath(candidate, projectDir, worktreeDir)
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
