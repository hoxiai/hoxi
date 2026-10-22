package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/worktree"
)

// Revert scope kinds. An unrevert may restore the whole exclusive worktree or
// only the exact path set captured for a shared checkout. An empty path set is
// a conversation-only revert and must never fall back to a whole-tree restore.
const (
	revertScopeWorkspace = "workspace"
	revertScopePaths     = "paths"
)

func mergeRevertScope(current, next string) string {
	if current == revertScopeWorkspace || next == revertScopeWorkspace {
		return revertScopeWorkspace
	}
	return revertScopePaths
}

// revertScopeFor resolves the persisted scope, including records written before
// RevertScope existed. Local checkouts default to paths so an empty set can
// never be mistaken for a full restore.
func revertScopeFor(rec SessionRecord) string {
	switch strings.TrimSpace(rec.RevertScope) {
	case revertScopeWorkspace:
		return revertScopeWorkspace
	case revertScopePaths:
		return revertScopePaths
	}
	if isIsolatedWorktree(rec.WorktreePath) {
		return revertScopeWorkspace
	}
	return revertScopePaths
}

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
		r.RevertPaths = nil
		r.RevertScope = ""
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

	unlock := s.turnMu.lock(sessionID)
	defer unlock()

	preRec, ok := s.store.Get(sessionID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	if preRec.Subagent {
		writeJSONError(w, http.StatusConflict, errSubagentSessionReadOnly.Error())
		return
	}
	preTurns := preRec.Turns
	if len(preRec.RevertedTurns) > len(preTurns) {
		preTurns = preRec.RevertedTurns
	}
	preTargetIdx := messageTurnIndex(preTurns, body.MessageID)
	if preTargetIdx < 0 {
		writeJSONError(w, http.StatusNotFound, "message not found in session")
		return
	}
	if body.PartID != "" && messagePartIndex(preTurns[preTargetIdx], body.PartID) < 0 {
		writeJSONError(w, http.StatusBadRequest, "partID not found in message")
		return
	}

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
	targetIdx := messageTurnIndex(allTurns, body.MessageID)
	if targetIdx < 0 {
		writeJSONError(w, http.StatusNotFound, "message not found in session")
		return
	}
	if body.PartID != "" && messagePartIndex(allTurns[targetIdx], body.PartID) < 0 {
		writeJSONError(w, http.StatusBadRequest, "partID not found in message")
		return
	}

	createdSnapshot := ""
	if rec.Revert == nil {
		if snap, err := worktree.CreateSnapshot(rec.WorktreePath); err == nil && snap != "" {
			createdSnapshot = snap
		}
	}

	targetSnap := resolveRevertSnapshot(rec, allTurns, targetIdx)
	if rec.Revert == nil && targetSnap != "" && createdSnapshot == "" {
		writeJSONError(w, http.StatusInternalServerError, "failed to capture workspace snapshot for revert")
		return
	}
	// baseSnap is the workspace tree before this revert was applied. A revert
	// with no diff against the checkpoint only rolled back conversation turns.
	baseSnap := createdSnapshot
	if rec.Revert != nil {
		baseSnap = strings.TrimSpace(rec.RevertSnapshot)
	}
	codeChanged := targetSnap != "" && baseSnap != "" && baseSnap != targetSnap
	var revertPaths []string
	// Default to paths so a revert that restores nothing (no checkpoint, or a
	// conversation-only revert) can never widen into a whole-tree restore.
	revertScope := revertScopePaths
	if targetSnap != "" && strings.TrimSpace(rec.WorktreePath) != "" {
		if scopePaths, scoped, scopeErr := s.resolveLocalRevertPaths(rec, allTurns, targetIdx, body.PartID, targetSnap, baseSnap); scopeErr != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to restore workspace snapshot: %v", scopeErr))
			return
		} else if scoped {
			if err := worktree.RestoreSnapshotPaths(rec.WorktreePath, targetSnap, scopePaths); err != nil {
				writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to restore workspace snapshot: %v", err))
				return
			}
			revertPaths = scopePaths
			codeChanged = len(scopePaths) > 0
		} else {
			if err := worktree.RestoreSnapshot(rec.WorktreePath, targetSnap); err != nil {
				writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to restore workspace snapshot: %v", err))
				return
			}
			revertScope = revertScopeWorkspace
		}
	}

	if rec.Revert == nil {
		rec.RevertSnapshot = createdSnapshot
		rec.RevertedTurns = cloneTurns(allTurns)
		rec.RevertPaths = revertPaths
		rec.RevertScope = revertScope
	} else {
		rec.RevertScope = mergeRevertScope(rec.RevertScope, revertScope)
		if len(revertPaths) > 0 {
			rec.RevertPaths = mergeStringLists(rec.RevertPaths, revertPaths)
		}
	}

	rec.Turns = truncateTurnsForRevert(allTurns, body.MessageID, body.PartID)
	rec.Revert = &SessionRevert{
		MessageID:   body.MessageID,
		PartID:      body.PartID,
		CodeChanged: codeChanged,
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
	unlock := s.turnMu.lock(sessionID)
	defer unlock()

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
		var err error
		if revertScopeFor(rec) == revertScopeWorkspace {
			err = worktree.RestoreSnapshot(rec.WorktreePath, rec.RevertSnapshot)
		} else {
			// A paths-scoped revert (including an empty set) must never widen to
			// a whole-tree restore: later user work would be deleted.
			err = worktree.RestoreSnapshotPaths(rec.WorktreePath, rec.RevertSnapshot, rec.RevertPaths)
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to restore workspace snapshot: %v", err))
			return
		}
	}
	if len(rec.RevertedTurns) > 0 {
		rec.Turns = cloneTurns(rec.RevertedTurns)
	}
	rec.Revert = nil
	rec.RevertedTurns = nil
	rec.RevertSnapshot = ""
	rec.RevertPaths = nil
	rec.RevertScope = ""
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

	unlock := s.turnMu.lock(sessionID)
	defer unlock()

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
	if messageID != "" && messageTurnIndex(allTurns, messageID) < 0 {
		writeJSONError(w, http.StatusNotFound, "message not found in session")
		return
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

	forkProjectID := rec.ProjectID
	if forkProjectID == "" {
		forkProjectID, _ = s.ensureProjectID(rec.ProjectDir)
	}
	forkRec := SessionRecord{
		ID:               forkID,
		ProjectID:        forkProjectID,
		Runtime:          rec.Runtime,
		ProjectDir:       rec.ProjectDir,
		SandboxMode:      rec.SandboxMode,
		CommandSandbox:   rec.CommandSandbox,
		ModelID:          rec.ModelID,
		ProviderID:       rec.ProviderID,
		Mode:             rec.Mode,
		Title:            title,
		CreatedAt:        now,
		UpdatedAt:        now,
		ContextPending:   len(forkTurns) > 0,
		ContextTurnCount: len(forkTurns),
		Turns:            forkTurns,
		Snapshots:        copyTurnSnapshots(rec.Snapshots, forkTurns),
	}
	if err := s.provisionSessionDirectory(&forkRec); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if rec.SandboxMode == "worktree" && strings.TrimSpace(rec.WorktreePath) != "" && strings.TrimSpace(forkRec.WorktreePath) != "" {
		snap, snapErr := worktree.CreateSnapshot(rec.WorktreePath)
		if snapErr != nil || snap == "" {
			_ = s.worktrees.ForDirectory(rec.ProjectDir).RemoveWithOptions(forkID, true, false)
			msg := "failed to snapshot source workspace"
			if snapErr != nil {
				msg = fmt.Sprintf("%s: %v", msg, snapErr)
			}
			writeJSONError(w, http.StatusInternalServerError, msg)
			return
		}
		if err := worktree.RestoreSnapshot(forkRec.WorktreePath, snap); err != nil {
			_ = s.worktrees.ForDirectory(rec.ProjectDir).RemoveWithOptions(forkID, true, false)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to copy workspace snapshot: %v", err))
			return
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

func messageTurnIndex(turns []MessageTurn, messageID string) int {
	for i := range turns {
		if turns[i].Info.ID == messageID {
			return i
		}
	}
	return -1
}

func messagePartIndex(turn MessageTurn, partID string) int {
	for i := range turn.Parts {
		if turn.Parts[i].ID == partID {
			return i
		}
	}
	return -1
}

// resolveRevertSnapshot returns the workspace checkpoint for the boundary.
// Checkpoints are captured per user prompt; a tool-level undo targets an
// assistant message, so it falls back to the checkpoint that opened its exchange
// instead of resetting the worktree to HEAD.
func resolveRevertSnapshot(rec SessionRecord, turns []MessageTurn, targetIdx int) string {
	if targetIdx < 0 || targetIdx >= len(turns) {
		return ""
	}
	if snap := strings.TrimSpace(rec.Snapshots[turns[targetIdx].Info.ID]); snap != "" {
		return snap
	}
	if turns[targetIdx].Info.Role != "assistant" {
		return ""
	}
	for i := targetIdx - 1; i >= 0; i-- {
		if turns[i].Info.Role == "user" {
			return strings.TrimSpace(rec.Snapshots[turns[i].Info.ID])
		}
	}
	return ""
}

func copyTurnSnapshots(snapshots map[string]string, turns []MessageTurn) map[string]string {
	if len(snapshots) == 0 {
		return nil
	}
	out := make(map[string]string)
	for _, turn := range turns {
		if snap := strings.TrimSpace(snapshots[turn.Info.ID]); snap != "" {
			out[turn.Info.ID] = snap
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// resolveLocalRevertPaths scopes file restoration in shared ("local") checkouts
// so reverting one session does not wipe out unrelated files created or edited
// by another session or by the user. Isolated `.hoxi/worktrees/<id>` sessions
// own their worktree exclusively and continue to restore the full snapshot.
//
// In a shared checkout only paths this session recorded through structured edit
// tool calls (or patch parts) are attributable. Shell edits, unrecorded changes
// and the user's own edits cannot be told apart from a snapshot diff, so a
// revert without explicit attribution restores conversation state only.
func (s *Service) resolveLocalRevertPaths(
	rec SessionRecord,
	turns []MessageTurn,
	targetIdx int,
	partID, targetSnap, baseSnap string,
) ([]string, bool, error) {
	if isIsolatedWorktree(rec.WorktreePath) {
		return nil, false, nil
	}
	if baseSnap == "" {
		if snap, err := worktree.CreateSnapshot(rec.WorktreePath); err == nil {
			baseSnap = snap
		}
	}
	changed, err := worktree.SnapshotChangedPaths(rec.WorktreePath, targetSnap, baseSnap)
	if err != nil {
		return nil, false, err
	}
	if len(changed) == 0 {
		return nil, true, nil
	}
	changedSet := make(map[string]bool, len(changed))
	for _, p := range changed {
		changedSet[p] = true
	}
	explicit := collectTurnEditedRelPaths(rec.WorktreePath, turns[targetIdx:], partID)
	var scoped []string
	for _, p := range explicit {
		if changedSet[p] {
			scoped = append(scoped, p)
		}
	}
	return scoped, true, nil
}

func collectTurnEditedRelPaths(worktreePath string, turns []MessageTurn, startPartID string) []string {
	seen := make(map[string]bool)
	var out []string
	addPath := func(raw string) {
		rel := normalizeWorktreeRelPath(worktreePath, raw)
		if rel != "" && !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for idx, turn := range turns {
		start := 0
		if idx == 0 && startPartID != "" {
			if pIdx := messagePartIndex(turn, startPartID); pIdx >= 0 {
				start = pIdx
			}
		}
		for _, part := range turn.Parts[start:] {
			if part.Type == "patch" {
				if files, ok := part.Metadata["files"]; ok {
					for _, f := range stringList(files) {
						addPath(f)
					}
				}
			}
			if part.Type != "tool" || part.State == nil {
				continue
			}
			toolLower := strings.ToLower(strings.TrimSpace(part.Tool))
			if !editToolNames[toolLower] {
				continue
			}
			for _, key := range editTargetKeys {
				for _, f := range stringList(part.State.Input[key]) {
					addPath(f)
				}
			}
			if diff, ok := part.State.Metadata["filediff"].(map[string]interface{}); ok {
				for _, f := range stringList(firstString(diff["file"], diff["path"], diff["filePath"])) {
					addPath(f)
				}
			}
		}
	}
	return out
}

func normalizeWorktreeRelPath(worktreePath, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		rel, err := filepath.Rel(filepath.Clean(worktreePath), filepath.Clean(trimmed))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
			return ""
		}
		return filepath.ToSlash(rel)
	}
	cleaned := filepath.ToSlash(filepath.Clean(trimmed))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}

func mergeStringLists(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, s := range append(append([]string(nil), a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
