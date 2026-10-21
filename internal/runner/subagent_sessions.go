package runner

import (
	"errors"
	"time"
)

// Subagent child sessions share their parent's worktree and are written only
// by the parent's turn; they never own a worktree, a sandbox or a CLI process.

var errSubagentSessionReadOnly = errors.New("subagent sessions are read-only; continue in the parent conversation")

// childSessionFactory returns the translator hook that persists a subagent's
// child session record next to its parent.
func (s *Service) childSessionFactory(parent SessionRecord, modelID, mode string) func(ChildSessionSpec) (SessionInfo, bool) {
	return func(spec ChildSessionSpec) (SessionInfo, bool) {
		now := time.Now().UnixMilli()
		child := SessionRecord{
			ID:           GenerateRunnerSessionID(parent.Runtime),
			ProjectID:    parent.ProjectID,
			ParentID:     parent.ID,
			Subagent:     true,
			Runtime:      parent.Runtime,
			ExternalID:   spec.ExternalID,
			ProjectDir:   parent.ProjectDir,
			WorktreePath: parent.WorktreePath,
			Branch:       parent.Branch,
			SandboxMode:  parent.SandboxMode,
			ModelID:      firstNonEmptyStr(spec.ModelID, modelID),
			ProviderID:   parent.ProviderID,
			Mode:         mode,
			Title:        firstNonEmptyStr(spec.Title, "Subagent"),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.store.Put(child); err != nil {
			return SessionInfo{}, false
		}
		return child.ToSessionInfo(), true
	}
}

// broadcastRouted delivers a turn's events to the session each belongs to and
// keeps child busy state in step with their session.status events.
func (s *Service) broadcastRouted(defaultID string, evts []SSEEvent) {
	for _, evt := range evts {
		sid := eventSessionID(evt)
		if sid == "" {
			sid = defaultID
		}
		if sid != defaultID && evt.Type == "session.status" {
			if st, ok := evt.Properties["status"].(SessionStatus); ok {
				s.setChildBusy(sid, st.Type == "busy")
			}
		}
		s.broadcast(sid, evt)
	}
}

func (s *Service) setChildBusy(id string, busy bool) {
	s.mu.Lock()
	if busy {
		s.childBusy[id] = true
	} else {
		delete(s.childBusy, id)
	}
	s.mu.Unlock()
	if !busy {
		s.store.FlushSession(id)
	}
	s.store.SetActive(id, busy)
}

// busySessionIDs lists sessions with a CLI process plus running subagents.
func (s *Service) busySessionIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.procs)+len(s.childBusy))
	for id := range s.procs {
		ids = append(ids, id)
	}
	for id := range s.childBusy {
		ids = append(ids, id)
	}
	return ids
}

// deleteChildSessions removes the subagent sessions of a deleted parent so
// they cannot linger as orphans.
func (s *Service) deleteChildSessions(parentID string) {
	for _, info := range s.store.List("") {
		if info.ParentID != parentID {
			continue
		}
		if rec, ok := s.store.Meta(info.ID); ok && rec.Subagent {
			s.setChildBusy(info.ID, false)
			_ = s.store.Delete(info.ID)
			s.broadcast(info.ID, SSEEvent{Type: "session.deleted", Properties: map[string]interface{}{"sessionID": info.ID}})
		}
	}
}
