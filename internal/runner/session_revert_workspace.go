package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type revertWorkspaceChange struct {
	dir, target, before, scope string
	paths                      []string
}

func (c revertWorkspaceChange) restore(snapshot string) error {
	if c.scope == revertScopeWorkspace {
		return worktree.RestoreSnapshot(c.dir, snapshot)
	}
	return worktree.RestoreSnapshotPaths(c.dir, snapshot, c.paths)
}

// Both directions compensate only the paths they actually touched, using the
// state immediately before this request even for repeated reverts.
func (s *Service) commitSessionRevert(ctx context.Context, previous SessionRecord, rec *SessionRecord, change revertWorkspaceChange, action string, payload map[string]interface{}) error {
	touchesFiles := change.dir != "" && change.target != "" && (change.scope == revertScopeWorkspace || len(change.paths) > 0)
	if touchesFiles && change.before == "" {
		before, err := worktree.CreateSnapshot(change.dir)
		if err != nil {
			return fmt.Errorf("capture workspace before %s: %w", action, err)
		}
		change.before = before
	}
	// Persist a resumable local conversation before changing the external
	// session. If OpenCode is offline it can be rebuilt from these turns.
	externalID, pending, turnCount := rec.ExternalID, rec.ContextPending, rec.ContextTurnCount
	rec.ExternalID, rec.ContextPending, rec.ContextTurnCount = "", len(rec.Turns) > 0, len(rec.Turns)
	if err := s.store.Put(*rec); err != nil {
		return err
	}
	if touchesFiles {
		if err := change.restore(change.target); err != nil {
			failure := fmt.Errorf("restore workspace snapshot: %w", err)
			if rollbackErr := change.restore(change.before); rollbackErr != nil {
				failure = errors.Join(failure, fmt.Errorf("restore pre-request workspace: %w", rollbackErr))
			}
			if rollbackErr := s.store.Put(previous); rollbackErr != nil {
				failure = errors.Join(failure, fmt.Errorf("restore pre-request session: %w", rollbackErr))
			}
			return failure
		}
	}
	if rec.Runtime != "opencode" || externalID == "" {
		return nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.openCodeJSON(requestCtx, http.MethodPost, "/session/"+externalID+"/"+action, rec.WorktreePath, payload, nil); err != nil {
		return nil
	}
	updated, ok, err := s.store.UpdateMetadata(rec.ID, func(meta *SessionMetadata) {
		meta.ExternalID, meta.ContextPending, meta.ContextTurnCount = externalID, pending, turnCount
	})
	if err != nil {
		diagnostics.Record(diagnostics.Entry{Kind: "system", Level: "error", Source: "runner", Message: "reattach reverted OpenCode session: " + err.Error()})
	} else if ok {
		rec.ExternalID, rec.ContextPending, rec.ContextTurnCount = updated.ExternalID, updated.ContextPending, updated.ContextTurnCount
		rec.UpdatedAt = updated.UpdatedAt
	}
	return nil
}
