package runner

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type editedFilesEntry struct {
	stamp uint64
	files map[string]struct{}
}

// sessionDiffState caches per-session edited file sets and OpenCode subagent
// transcripts inspected while filtering diffs.
type sessionDiffState struct {
	childTurns  syncMap[childTurnsKey, childTurnsEntry]
	editedFiles syncMap[string, editedFilesEntry]
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
	childIDs, _ := s.store.ChildSessionIDs(sessionID)
	stamp := s.store.EditMutations()
	if entry, hit := s.editedFiles.Load(sessionID); hit && entry.stamp == stamp {
		return entry.files
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
		if snapshot, ok := s.store.SessionEditsSnapshot(sid); ok {
			rec := snapshot.Record
			if sid != sessionID && !rec.Subagent && normalizeComparePath(rec.WorktreePath) != normalizeComparePath(rootRec.WorktreePath) {
				return
			}
			for _, path := range snapshot.Paths {
				edited[path] = struct{}{}
			}
			subChildren = append(subChildren, snapshot.Children...)
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
		postStamp := s.store.EditMutations()
		if postStamp == stamp {
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
	childTurnsTTL      = 3 * time.Second
	childTurnsErrTTL   = 250 * time.Millisecond
	childTurnsStaleTTL = 5 * time.Minute
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
	if entry, ok := s.childTurns.Load(key); ok {
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
	s.childTurns.Range(func(k childTurnsKey, entry childTurnsEntry) bool {
		if k != key && !now.Before(entry.expires) && (len(entry.turns) == 0 || now.Sub(entry.expires) >= childTurnsStaleTTL) {
			s.childTurns.Delete(k)
		}
		return true
	})
	s.childTurns.Store(key, childTurnsEntry{turns: turns, expires: now.Add(ttl)})
	return turns
}
