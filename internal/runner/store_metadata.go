package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionMetadata exposes only fields that can change independently of history.
// Transcript, worktree, runtime and revert mutations retain the full Update path.
type SessionMetadata struct {
	Title, ProjectID, ProviderID, ModelID, Mode, ExternalID string
	ArchivedAt                                              int64
	ContextPending                                          bool
}

// UpdateMetadata persists small session changes without loading or cloning turns.
// The returned record is metadata-only, like Meta; callers needing history use Get.
func (s *Store) UpdateMetadata(id string, fn func(*SessionMetadata)) (SessionRecord, bool, error) {
	return s.updateMetadata(id, true, fn)
}

func (s *Store) updateMetadata(id string, touch bool, fn func(*SessionMetadata)) (SessionRecord, bool, error) {
	ss := s.lockSession(id, false)
	if ss == nil {
		return SessionRecord{}, false, nil
	}
	defer s.finishSession(ss)
	next := ss.rec
	meta := SessionMetadata{Title: next.Title, ProjectID: next.ProjectID, ProviderID: next.ProviderID,
		ModelID: next.ModelID, Mode: next.Mode, ExternalID: next.ExternalID, ArchivedAt: next.ArchivedAt,
		ContextPending: next.ContextPending}
	fn(&meta)
	next.Title, next.ProjectID, next.ProviderID = meta.Title, meta.ProjectID, meta.ProviderID
	next.ModelID, next.Mode, next.ExternalID = meta.ModelID, meta.Mode, meta.ExternalID
	next.ArchivedAt, next.ContextPending = meta.ArchivedAt, meta.ContextPending
	if touch {
		next.UpdatedAt = time.Now().UnixMilli()
	}
	ss.diskMu.Lock()
	defer ss.diskMu.Unlock()
	s.applyPersistResultLocked(ss)
	if err := s.writeMetadataLocked(ss, next); err != nil {
		return metadataOnly(ss.rec), true, err
	}
	ss.rec, ss.metaDirty = next, false
	ss.rev++
	s.mutations.Add(1)
	return metadataOnly(next), true, nil
}

func (s *Store) writeMetadataLocked(ss *storedSession, next SessionRecord) error {
	if ss.turnsLoaded || !ss.hasRevertedTurns {
		return s.writeMeta(next)
	}
	// Cold reverted history lives in session.json. Preserve its raw JSON instead
	// of hydrating it just to change a title, including all unknown part fields.
	path := filepath.Join(s.sessionDir(next.ID), sessionMetaFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var previous struct {
		RevertedTurns json.RawMessage `json:"revertedTurns,omitempty"`
	}
	if err := json.Unmarshal(raw, &previous); err != nil {
		if backupErr := writeFileAtomic(fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano()), raw); backupErr != nil {
			return fmt.Errorf("backup invalid session metadata: %w", backupErr)
		}
		return fmt.Errorf("invalid session metadata: %w", err)
	}
	next.Turns, next.RevertedTurns = nil, nil
	encoded, err := json.MarshalIndent(struct {
		SessionRecord
		RevertedTurns json.RawMessage `json:"revertedTurns,omitempty"`
	}{next, previous.RevertedTurns}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, encoded)
}
