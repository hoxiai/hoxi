package runner

import (
	"slices"
	"strings"
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
	return part != nil && part.Type == "tool" && (isRunnerEditingTool(part.Tool) || isRunnerTaskTool(part.Tool))
}

func uniqueEditValues(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}

func (e *transcriptEdits) apply(rec SessionRecord, part MessagePart) bool {
	key := partKey{part.MessageID, part.ID}
	var next editContribution
	if part.Type == "tool" && part.State != nil {
		if isRunnerTaskTool(part.Tool) {
			for _, id := range extractChildSessionIDsFromPart(part) {
				if id = strings.TrimSpace(id); id != "" {
					next.children = append(next.children, id)
				}
			}
		} else if part.State.Status == "completed" && isRunnerEditingTool(part.Tool) {
			for _, path := range extractEditedPathsFromPart(part) {
				if path = normalizeSessionRelPath(path, rec.ProjectDir, rec.WorktreePath); path != "" {
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

type sessionEditsSnapshot struct {
	record   SessionRecord
	paths    []string
	children []string
}

func (s *Store) sessionEditsSnapshot(id string) (sessionEditsSnapshot, bool) {
	ss := s.lockSession(id, false)
	if ss == nil {
		return sessionEditsSnapshot{}, false
	}
	defer s.finishSession(ss)
	if !ss.turnsLoaded {
		ss.diskMu.Lock()
		s.applyPersistResultLocked(ss)
		paths, children, err := s.readIndexedEditSummary(ss.rec)
		ss.diskMu.Unlock()
		if err == nil {
			return sessionEditsSnapshot{record: metadataOnly(ss.rec), paths: paths, children: children}, true
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
	out := sessionEditsSnapshot{record: metadataOnly(ss.rec)}
	for path := range ss.edits.paths {
		out.paths = append(out.paths, path)
	}
	for child := range ss.edits.children {
		out.children = append(out.children, child)
	}
	return out, true
}
