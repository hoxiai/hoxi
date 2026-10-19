package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func (s *Service) recordTurnSnapshot(sessionID, messageID string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return
	}
	rec, ok := s.store.Meta(sessionID)
	if !ok || strings.TrimSpace(rec.WorktreePath) == "" {
		return
	}
	snap, err := worktree.CreateSnapshot(rec.WorktreePath)
	_, _, _ = s.store.Update(sessionID, func(r *SessionRecord) {
		r.Revert = nil
		r.RevertedTurns = nil
		r.RevertSnapshot = ""
		if err == nil && snap != "" {
			if r.Snapshots == nil {
				r.Snapshots = make(map[string]string)
			}
			r.Snapshots[messageID] = snap
		}
	})
}

func (s *Service) handleSessionRevert(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		MessageID string `json:"messageID"`
		PartID    string `json:"partID"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil || strings.TrimSpace(body.MessageID) == "" {
		writeJSONError(w, http.StatusBadRequest, "messageID is required")
		return
	}
	body.MessageID = strings.TrimSpace(body.MessageID)
	body.PartID = strings.TrimSpace(body.PartID)

	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap != nil {
		s.abortActiveProcess(sessionID)
		select {
		case <-ap.done:
		case <-time.After(5 * time.Second):
		}
	}

	rec, ok := s.store.Get(sessionID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	if rec.Subagent {
		writeJSONError(w, http.StatusConflict, errSubagentSessionReadOnly.Error())
		return
	}

	allTurns := rec.Turns
	if len(rec.RevertedTurns) > len(allTurns) {
		allTurns = rec.RevertedTurns
	}

	if rec.Revert == nil {
		if snap, err := worktree.CreateSnapshot(rec.WorktreePath); err == nil && snap != "" {
			rec.RevertSnapshot = snap
		}
		rec.RevertedTurns = cloneTurns(allTurns)
	}

	targetSnap := ""
	if rec.Snapshots != nil {
		targetSnap = rec.Snapshots[body.MessageID]
	}
	if targetSnap == "" {
		if headSnap, err := worktree.HeadTree(rec.WorktreePath); err == nil {
			targetSnap = headSnap
		}
	}
	if targetSnap != "" && strings.TrimSpace(rec.WorktreePath) != "" {
		_ = worktree.RestoreSnapshot(rec.WorktreePath, targetSnap)
	}

	rec.Turns = truncateTurnsForRevert(allTurns, body.MessageID, body.PartID)
	rec.Revert = &SessionRevert{
		MessageID: body.MessageID,
		PartID:    body.PartID,
	}
	rec.UpdatedAt = time.Now().UnixMilli()

	if rec.Runtime == "opencode" && rec.ExternalID != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		payload := map[string]interface{}{"messageID": body.MessageID}
		if body.PartID != "" {
			payload["partID"] = body.PartID
		}
		err := s.openCodeJSON(ctx, http.MethodPost, "/session/"+rec.ExternalID+"/revert", rec.WorktreePath, payload, nil)
		cancel()
		if err != nil {
			rec.ExternalID = ""
			rec.ContextPending = len(rec.Turns) > 0
			rec.ContextTurnCount = len(rec.Turns)
		}
	} else {
		rec.ExternalID = ""
		rec.ContextPending = len(rec.Turns) > 0
		rec.ContextTurnCount = len(rec.Turns)
	}

	if err := s.store.Put(rec); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := rec.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.updated",
		Properties: map[string]interface{}{"info": info},
	})
	writeJSON(w, http.StatusOK, info)
}

func (s *Service) handleSessionUnrevert(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	rec, ok := s.store.Get(sessionID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	if rec.Subagent {
		writeJSONError(w, http.StatusConflict, errSubagentSessionReadOnly.Error())
		return
	}

	if rec.RevertSnapshot != "" && strings.TrimSpace(rec.WorktreePath) != "" {
		_ = worktree.RestoreSnapshot(rec.WorktreePath, rec.RevertSnapshot)
	}
	if len(rec.RevertedTurns) > 0 {
		rec.Turns = cloneTurns(rec.RevertedTurns)
	}
	rec.Revert = nil
	rec.RevertedTurns = nil
	rec.RevertSnapshot = ""
	rec.UpdatedAt = time.Now().UnixMilli()

	if rec.Runtime == "opencode" && rec.ExternalID != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		err := s.openCodeJSON(ctx, http.MethodPost, "/session/"+rec.ExternalID+"/unrevert", rec.WorktreePath, map[string]interface{}{}, nil)
		cancel()
		if err != nil {
			rec.ExternalID = ""
			rec.ContextPending = len(rec.Turns) > 0
			rec.ContextTurnCount = len(rec.Turns)
		}
	} else {
		rec.ExternalID = ""
		rec.ContextPending = len(rec.Turns) > 0
		rec.ContextTurnCount = len(rec.Turns)
	}

	if err := s.store.Put(rec); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := rec.ToSessionInfo()
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.updated",
		Properties: map[string]interface{}{"info": info},
	})
	writeJSON(w, http.StatusOK, info)
}

func (s *Service) handleSessionFork(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		MessageID string `json:"messageID"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
	messageID := strings.TrimSpace(body.MessageID)

	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	rec, ok := s.store.Get(sessionID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	if rec.Subagent {
		writeJSONError(w, http.StatusConflict, errSubagentSessionReadOnly.Error())
		return
	}

	allTurns := rec.Turns
	if len(rec.RevertedTurns) > len(allTurns) {
		allTurns = rec.RevertedTurns
	}

	forkID := GenerateRunnerSessionID(rec.Runtime)
	now := time.Now().UnixMilli()
	forkTurns := sliceTurnsForFork(allTurns, messageID, forkID)

	title := strings.TrimSpace(rec.Title)
	if title == "" {
		title = "Forked Session"
	} else if !strings.HasSuffix(title, " (fork)") {
		title = title + " (fork)"
	}

	forkRec := SessionRecord{
		ID:               forkID,
		Runtime:          rec.Runtime,
		ProjectDir:       rec.ProjectDir,
		SandboxMode:      rec.SandboxMode,
		ModelID:          rec.ModelID,
		ProviderID:       rec.ProviderID,
		Mode:             rec.Mode,
		Title:            title,
		CreatedAt:        now,
		UpdatedAt:        now,
		ContextPending:   len(forkTurns) > 0,
		ContextTurnCount: len(forkTurns),
		Turns:            forkTurns,
	}
	if err := s.provisionSessionDirectory(&forkRec); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rec.SandboxMode == "worktree" && strings.TrimSpace(rec.WorktreePath) != "" && strings.TrimSpace(forkRec.WorktreePath) != "" {
		if snap, err := worktree.CreateSnapshot(rec.WorktreePath); err == nil && snap != "" {
			_ = worktree.RestoreSnapshot(forkRec.WorktreePath, snap)
		}
	}

	if err := s.store.Put(forkRec); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := forkRec.ToSessionInfo()
	s.broadcast(forkID, SSEEvent{
		Type:       "session.created",
		Properties: map[string]interface{}{"info": info},
	})
	writeJSON(w, http.StatusOK, info)
}

func truncateTurnsForRevert(turns []MessageTurn, messageID, partID string) []MessageTurn {
	if messageID == "" {
		return cloneTurns(turns)
	}
	idx := -1
	for i := range turns {
		if turns[i].Info.ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		out := make([]MessageTurn, 0, len(turns))
		for _, t := range turns {
			if t.Info.ID < messageID {
				out = append(out, t)
			}
		}
		return cloneTurns(out)
	}
	if partID == "" {
		return cloneTurns(turns[:idx])
	}
	boundary := turns[idx]
	pIdx := -1
	for i := range boundary.Parts {
		if boundary.Parts[i].ID == partID {
			pIdx = i
			break
		}
	}
	parts := boundary.Parts
	if pIdx >= 0 {
		parts = boundary.Parts[:pIdx]
	}
	out := make([]MessageTurn, 0, idx+1)
	out = append(out, turns[:idx]...)
	out = append(out, MessageTurn{Info: boundary.Info, Parts: parts})
	return cloneTurns(out)
}

func sliceTurnsForFork(turns []MessageTurn, messageID, newSessionID string) []MessageTurn {
	var subset []MessageTurn
	if messageID == "" {
		subset = turns
	} else {
		idx := -1
		for i := range turns {
			if turns[i].Info.ID == messageID {
				idx = i
				break
			}
		}
		if idx >= 0 {
			if turns[idx].Info.Role == "user" {
				subset = turns[:idx]
			} else {
				subset = turns[:idx+1]
			}
		} else {
			subset = turns
		}
	}
	cloned := cloneTurns(subset)
	for i := range cloned {
		cloned[i].Info.SessionID = newSessionID
		for j := range cloned[i].Parts {
			cloned[i].Parts[j].SessionID = newSessionID
		}
	}
	return cloned
}

func cloneTurns(turns []MessageTurn) []MessageTurn {
	if len(turns) == 0 {
		return []MessageTurn{}
	}
	raw, err := json.Marshal(turns)
	if err != nil {
		return append([]MessageTurn(nil), turns...)
	}
	var out []MessageTurn
	if json.Unmarshal(raw, &out) != nil {
		return append([]MessageTurn(nil), turns...)
	}
	return out
}
