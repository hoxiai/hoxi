package runner

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const restartContinuationPrompt = "Hoxi restarted while your previous turn was in progress (any tool call still running at shutdown was interrupted). Inspect the current workspace state if needed and continue from where you left off to complete the user's request."

var (
	// restartEngineWaitTimeout bounds how long OpenCode turn resumption waits
	// on boot for the supervised `opencode serve` daemon to report healthy.
	restartEngineWaitTimeout    = 25 * time.Second
	restartEnginePollIntervalMs atomic.Int64
)

func init() {
	restartEnginePollIntervalMs.Store(150)
}

// resumeFlight deduplicates concurrent boot-resume attempts for the same
// session (e.g. ResumeInterruptedTurns vs Convoy ResumeOrRunPromptAndWait) and
// allows user Stop/Delete/Archive to cancel engine-readiness waits immediately.
type resumeFlight struct {
	done       chan struct{}
	cancel     context.CancelFunc
	starting   atomic.Bool
	aborted    atomic.Bool
	mu         sync.Mutex
	restarting bool
	started    bool
	err        error
}

func (f *resumeFlight) markRestarting() {
	f.mu.Lock()
	f.restarting = true
	f.mu.Unlock()
}

func (f *resumeFlight) wasRestarting() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restarting
}

func (s *Service) hasPendingOrActiveResume(sessionID string) bool {
	if s == nil {
		return false
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false
	}
	if _, ok := s.pendingResumes.Load(sid); ok {
		return true
	}
	f, ok := s.resumingFlights.Load(sid)
	return ok && f != nil && f.starting.Load() && !f.aborted.Load()
}

func (s *Service) pendingResumeSessionIDs() []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	s.pendingResumes.Range(func(sid string, _ InflightTurnEntry) bool {
		if sid != "" && !seen[sid] {
			seen[sid] = true
			out = append(out, sid)
		}
		return true
	})
	s.resumingFlights.Range(func(sid string, f *resumeFlight) bool {
		if sid != "" && !seen[sid] && f != nil && f.starting.Load() && !f.aborted.Load() {
			seen[sid] = true
			out = append(out, sid)
		}
		return true
	})
	return out
}

func (s *Service) recordInflightTurn(e InflightTurnEntry) {
	if s == nil || s.ledger == nil {
		return
	}
	sid := strings.TrimSpace(e.SessionID)
	s.pendingResumes.Delete(sid)
	s.ledger.putEntry(e)
}

func (s *Service) updateInflightExternalID(sessionID, externalID string) {
	if s == nil || s.ledger == nil {
		return
	}
	s.ledger.updateExternalID(sessionID, externalID)
}

func (s *Service) updateInflightSteer(sessionID, userMsgID, mode, providerID, modelID, effort, promptText string) {
	if s == nil || s.ledger == nil {
		return
	}
	s.ledger.updateSteer(sessionID, userMsgID, mode, providerID, modelID, effort, promptText)
}

func (s *Service) clearInflightTurn(sessionID string) {
	s.clearInflightTurnWithGuard(sessionID, "", true)
}

func (s *Service) clearInflightTurnForUserMsg(sessionID, userMsgID string) {
	s.clearInflightTurnWithGuard(sessionID, userMsgID, false)
}

func (s *Service) clearInflightTurnWithGuard(sessionID, userMsgID string, cancelFlight bool) {
	if s == nil {
		return
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	s.pendingResumes.Delete(sid)
	if cancelFlight {
		if flight, ok := s.resumingFlights.LoadAndDelete(sid); ok && flight != nil {
			flight.aborted.Store(true)
			if flight.cancel != nil {
				flight.cancel()
			}
		}
		if s.status != nil {
			s.mu.Lock()
			stillRunning := s.procs[sid] != nil
			s.mu.Unlock()
			if !stillRunning {
				s.status.SetSessionBusy(sid, false)
			}
		}
	}
	if s.ledger != nil {
		if cancelFlight {
			s.ledger.deleteEntry(sid)
		} else {
			s.ledger.deleteEntryForUserMsg(sid, userMsgID)
		}
	}
}

func (s *Service) forgetLedgerSessions(seen map[string]bool) {
	if s == nil || len(seen) == 0 {
		return
	}
	for sid := range seen {
		s.pendingResumes.Delete(sid)
		if flight, ok := s.resumingFlights.LoadAndDelete(sid); ok && flight != nil {
			flight.aborted.Store(true)
			if flight.cancel != nil {
				flight.cancel()
			}
		}
	}
	if s.ledger != nil {
		s.ledger.forgetSessions(seen)
	}
}

// settleStartupLedger inspects all in-flight turns left on disk by the previous
// Hoxi process. Graceful-restart checkpoints within the freshness and recovery
// budget are queued for automatic resumption; ungraceful crashes or expired
// entries have their dangling tool/assistant states settled cleanly on disk.
func (s *Service) settleStartupLedger() {
	if s == nil || s.ledger == nil {
		return
	}
	nowMs := time.Now().UnixMilli()
	maxAgeMs := restartResumeMaxAge.Milliseconds()
	for _, entry := range s.ledger.listEntries() {
		rec, ok := s.store.Get(entry.SessionID)
		if !ok || rec.Subagent || rec.ArchivedAt > 0 || validateSessionDirectory(rec) != nil {
			s.ledger.deleteEntry(entry.SessionID)
			continue
		}
		fresh := maxAgeMs <= 0 || (entry.UpdatedAt > 0 && nowMs-entry.UpdatedAt <= maxAgeMs)
		canAutoResume := entry.GracefulRestart && fresh && entry.RecoveryCount < maxAutoRestartRecoveries
		if canAutoResume {
			// Settle any interrupted tool/reasoning parts from before the restart
			// while keeping the turn free of fatal error banners so resumption appends cleanly.
			s.abandonOpenCodeInFlightParts(rec.ID, nowMs)
			s.clearInterruptedTurnAbortError(rec.ID)
			s.store.FlushSession(rec.ID)
			s.pendingResumes.Store(rec.ID, entry)
			if s.status != nil {
				s.status.SetSessionBusy(rec.ID, true)
			}
			continue
		}
		s.ledger.deleteEntry(entry.SessionID)
		s.settleCrashedSessionOnDisk(rec.ID, nowMs)
	}
}

func (s *Service) clearInterruptedTurnAbortError(sessionID string) {
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	last := rec.Turns[len(rec.Turns)-1].Info
	if last.Role != "assistant" || last.Error == nil {
		return
	}
	if !strings.Contains(strings.ToLower(last.Error.Name), "abort") {
		return
	}
	last.Error = nil
	if last.Finish == "" || last.Finish == "error" {
		last.Finish = "aborted"
	}
	if last.Time.Completed == 0 {
		last.Time.Completed = time.Now().UnixMilli()
	}
	s.store.UpsertTurnInfo(sessionID, last)
}

func (s *Service) settleCrashedSessionOnDisk(sessionID string, nowMs int64) {
	s.abandonOpenCodeInFlightParts(sessionID, nowMs)
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	last := rec.Turns[len(rec.Turns)-1].Info
	if last.Role == "assistant" && last.Error == nil && (last.Finish == "" || last.Finish == "tool-calls") {
		last.Error = newClassifiedMessageError("RunnerInterruptedError", "Interrupted by a Hoxi restart", 0)
		last.Finish = "error"
		if last.Time.Completed == 0 {
			last.Time.Completed = nowMs
		}
		s.store.UpsertTurnInfo(sessionID, last)
	}
	s.store.FlushSession(sessionID)
}

// ResumeInterruptedTurns asynchronously resumes all turns that were checkpointed
// during a graceful update or restart. Safe to call multiple times; each queued
// session is claimed at most once via singleflight.
func (s *Service) ResumeInterruptedTurns(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var sessionIDs []string
	s.pendingResumes.Range(func(sid string, _ InflightTurnEntry) bool {
		if strings.TrimSpace(sid) != "" {
			sessionIDs = append(sessionIDs, sid)
		}
		return true
	})
	for _, sid := range sessionIDs {
		sessionID := sid
		go func() {
			_, _ = s.ensureSessionResumed(ctx, sessionID)
		}()
	}
}

// ResumeOrRunPromptAndWait attaches to an already-resumed or pending-resume turn
// for sessionID when available, reuses an already-completed turn outcome when
// the session finished cleanly right before shutdown, or runs req fresh otherwise.
func (s *Service) ResumeOrRunPromptAndWait(ctx context.Context, sessionID string, req PromptRequest) (string, error) {
	resumed, err := s.ensureSessionResumed(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if resumed {
		if waitErr := s.waitActiveProcess(ctx, sessionID); waitErr != nil {
			return "", waitErr
		}
		return s.lastTurnOutcome(sessionID)
	}
	s.mu.Lock()
	ap := s.procs[sessionID]
	busy := ap != nil && !ap.hasPendingError() && !ap.wasAborted()
	s.mu.Unlock()
	if busy {
		if waitErr := s.waitActiveProcess(ctx, sessionID); waitErr != nil {
			return "", waitErr
		}
		return s.lastTurnOutcome(sessionID)
	}
	if rec, ok := s.store.Get(sessionID); ok && len(rec.Turns) > 0 {
		if out, outErr := s.lastTurnOutcome(sessionID); outErr == nil && strings.TrimSpace(out) != "" {
			return out, nil
		}
	}
	return s.RunPromptAndWait(ctx, sessionID, req)
}

func (s *Service) waitActiveProcess(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap == nil {
		return nil
	}
	select {
	case <-ap.done:
		return nil
	case <-ctx.Done():
		s.AbortSession(sessionID)
		select {
		case <-ap.done:
		case <-time.After(abortSettleTimeout):
		}
		return ctx.Err()
	}
}

func (s *Service) ensureSessionResumed(ctx context.Context, sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if existing, ok := s.resumingFlights.Load(sessionID); ok && existing != nil {
		select {
		case <-existing.done:
			return existing.started, existing.err
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}

	if _, hasPending := s.pendingResumes.Load(sessionID); !hasPending {
		s.mu.Lock()
		ap := s.procs[sessionID]
		alreadyActive := ap != nil && !ap.hasPendingError() && !ap.wasAborted()
		s.mu.Unlock()
		return alreadyActive, nil
	}

	flightCtx, cancelFlight := context.WithCancel(ctx)
	flight := &resumeFlight{done: make(chan struct{}), cancel: cancelFlight}
	actual, loaded := s.resumingFlights.LoadOrStore(sessionID, flight)
	if loaded && actual != nil {
		cancelFlight()
		select {
		case <-actual.done:
			return actual.started, actual.err
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}

	defer func() {
		cancelFlight()
		s.resumingFlights.Delete(sessionID)
		if !flight.started && s.status != nil {
			s.mu.Lock()
			stillRunning := s.procs[sessionID] != nil
			s.mu.Unlock()
			if !stillRunning {
				s.status.SetSessionBusy(sessionID, false)
			}
		}
		close(flight.done)
	}()

	// Guard against AbortSession deleting pendingResumes right before LoadOrStore.
	if _, stillPending := s.pendingResumes.Load(sessionID); !stillPending {
		flight.aborted.Store(true)
		return false, nil
	}

	// Wait for OpenCode readiness OUTSIDE s.turnMu so user Stop/Delete/Archive
	// never blocks while the supervised daemon is booting.
	if recMeta, ok := s.store.Meta(sessionID); ok && recMeta.Runtime == "opencode" {
		if err := s.waitForOpenCodeReady(flightCtx, restartEngineWaitTimeout); err != nil {
			if flight.wasRestarting() || flight.aborted.Load() {
				return false, nil
			}
			if _, stillQueued := s.pendingResumes.LoadAndDelete(sessionID); !stillQueued {
				return false, nil
			}
			s.clearInflightTurn(sessionID)
			s.settleCrashedSessionOnDisk(sessionID, time.Now().UnixMilli())
			flight.err = err
			return false, err
		}
	}

	unlock := s.turnMu.lock(sessionID)
	defer unlock()

	if flight.aborted.Load() {
		return false, nil
	}

	s.mu.Lock()
	ap := s.procs[sessionID]
	alreadyActive := ap != nil && !ap.hasPendingError() && !ap.wasAborted()
	s.mu.Unlock()
	if alreadyActive {
		s.pendingResumes.Delete(sessionID)
		flight.started = true
		return true, nil
	}

	flight.starting.Store(true)
	defer flight.starting.Store(false)

	entry, ok := s.pendingResumes.LoadAndDelete(sessionID)
	if !ok || flight.aborted.Load() {
		return false, nil
	}
	rec, exists := s.store.Get(sessionID)
	if !exists || rec.Subagent || rec.ArchivedAt > 0 {
		s.clearInflightTurn(sessionID)
		return false, nil
	}
	if err := validateSessionDirectory(rec); err != nil {
		s.clearInflightTurn(sessionID)
		flight.err = err
		return false, err
	}

	if rec.Runtime == "opencode" {
		if err := s.resumeOpenCodeTurnFromLedger(flightCtx, rec, entry); err != nil {
			if !flight.wasRestarting() {
				s.clearInflightTurn(sessionID)
				s.settleCrashedSessionOnDisk(sessionID, time.Now().UnixMilli())
			}
			flight.err = err
			return false, err
		}
		flight.started = true
		return true, nil
	}
	if err := s.resumeCLITurnFromLedger(rec, entry); err != nil {
		if !flight.wasRestarting() {
			s.clearInflightTurn(sessionID)
			s.settleCrashedSessionOnDisk(sessionID, time.Now().UnixMilli())
		}
		flight.err = err
		return false, err
	}
	flight.started = true
	return true, nil
}
