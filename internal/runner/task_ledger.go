package runner

import (
	"context"
	"time"

	"github.com/hoxiai/hoxi/internal/runner/taskledger"
)

const (
	taskLedgerFile           = taskledger.TaskLedgerFile
	taskLedgerVersion        = taskledger.TaskLedgerVersion
	maxAutoRestartRecoveries = taskledger.MaxAutoRestartRecoveries
)

var (
	// updateStepDrainTimeout bounds how long PrepareForRestart waits for an
	// actively running tool step to finish cleanly before stopping child processes.
	updateStepDrainTimeout = 12 * time.Second
	// stepDrainPollInterval is the polling cadence while draining active tool steps.
	stepDrainPollInterval = 50 * time.Millisecond
	// restartResumeMaxAge bounds how old a graceful-restart checkpoint may be
	// and still auto-resume on the next Hoxi launch.
	restartResumeMaxAge = 30 * time.Minute
)

type (
	InflightTurnEntry = taskledger.InflightTurnEntry
	PromptReceipt     = taskledger.PromptReceipt
	taskLedgerDoc     = taskledger.Document
)

type taskLedgerStore struct {
	*taskledger.Store
	loadErr error
}

func newTaskLedgerStore(customStorePath string) *taskLedgerStore {
	st := taskledger.NewStore(customStorePath)
	return &taskLedgerStore{
		Store:   st,
		loadErr: st.LoadErr(),
	}
}

func (s *taskLedgerStore) peekRequest(kind, scope, clientRequestID, digest string, meta map[string]string) (RequestAdmissionOutcome, bool) {
	if s == nil {
		return RequestAdmissionOutcome{}, false
	}
	return s.Store.PeekRequest(kind, scope, clientRequestID, digest, meta)
}

func (s *taskLedgerStore) admitRequest(kind, scope, sessionID, clientRequestID, digest string, meta map[string]string) RequestAdmissionOutcome {
	if s == nil {
		return RequestAdmissionOutcome{State: RequestAdmissionNew}
	}
	return s.Store.AdmitRequest(kind, scope, sessionID, clientRequestID, digest, meta)
}

func (s *taskLedgerStore) completeRequest(kind, scope, sessionID, clientRequestID, digest string, httpStatus int, responseBody []byte, meta map[string]string) {
	if s != nil {
		s.Store.CompleteRequest(kind, scope, sessionID, clientRequestID, digest, httpStatus, responseBody, meta)
	}
}

func (s *taskLedgerStore) abortUncommittedRequest(kind, scope, clientRequestID string) {
	if s != nil {
		s.Store.AbortUncommittedRequest(kind, scope, clientRequestID)
	}
}

func (s *taskLedgerStore) markRequestUnknown(kind, scope, clientRequestID, message string) {
	if s != nil {
		s.Store.MarkRequestUnknown(kind, scope, clientRequestID, message)
	}
}

func (s *Service) hydrateReceiptsFromLedger() {}

func (s *Service) hasInFlightRunningToolPart(sessionID string) bool {
	if s == nil || sessionID == "" {
		return false
	}
	running := false
	s.store.VisitTurns(sessionID, func(rec SessionRecord) {
		for i := len(rec.Turns) - 1; i >= 0; i-- {
			t := rec.Turns[i]
			if t.Info.Role == "user" && !isInternalRecoveryMessageID(t.Info.ID) {
				break
			}
			for _, p := range t.Parts {
				if p.Type == "tool" && p.State != nil && p.State.Status == "running" {
					running = true
					return
				}
			}
		}
	})
	return running
}

// WaitForToolStepDrain waits up to maxWait for any actively executing tool call
// (excluding sessions paused on user approval/questions) to finish its current
// step before an update or restart stops child processes.
func (s *Service) WaitForToolStepDrain(ctx context.Context, maxWait time.Duration) bool {
	if s == nil || maxWait <= 0 {
		return !s.anyActiveToolStepRunning()
	}
	if !s.anyActiveToolStepRunning() {
		return true
	}
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(stepDrainPollInterval)
	defer ticker.Stop()
	for {
		if !s.anyActiveToolStepRunning() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return !s.anyActiveToolStepRunning()
		case <-ticker.C:
		}
	}
}

func (s *Service) anyActiveToolStepRunning() bool {
	s.mu.Lock()
	active := make([]*activeProcess, 0, len(s.procs))
	for _, ap := range s.procs {
		if ap != nil && !ap.wasAborted() && !ap.hasPendingError() {
			active = append(active, ap)
		}
	}
	s.mu.Unlock()
	for _, ap := range active {
		if s.awaitingUser(ap) {
			continue
		}
		if s.hasInFlightRunningToolPart(ap.sessionID) || s.hasRunningToolSubprocess(ap) {
			return true
		}
	}
	return false
}

// PrepareForRestart drains any fast in-flight tool step, records a graceful
// restart checkpoint for every still-running or boot-resuming turn on disk,
// and cleanly stops child processes while preserving their ledger checkpoints.
func (s *Service) PrepareForRestart(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.WaitForToolStepDrain(ctx, updateStepDrainTimeout)

	extBySession := make(map[string]string)

	s.pendingResumes.Range(func(sid string, e InflightTurnEntry) bool {
		extBySession[sid] = e.ExternalID
		return true
	})
	s.resumingFlights.Range(func(sid string, flight *resumeFlight) bool {
		if flight != nil {
			flight.markRestarting()
			flight.cancel()
		}
		if rec, ok := s.store.Meta(sid); ok {
			extBySession[sid] = rec.ExternalID
		} else {
			extBySession[sid] = ""
		}
		return true
	})

	s.mu.Lock()
	for _, ap := range s.procs {
		if ap != nil && !ap.wasAborted() && !ap.hasPendingError() {
			ap.markRestarting()
			extID := ""
			if rec, ok := s.store.Meta(ap.sessionID); ok {
				extID = rec.ExternalID
			}
			extBySession[ap.sessionID] = extID
		}
	}
	s.mu.Unlock()

	if len(extBySession) > 0 && s.ledger != nil {
		s.ledger.MarkGracefulRestart(extBySession)
	}

	s.CloseAll()
}
