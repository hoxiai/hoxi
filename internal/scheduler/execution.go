package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

func (s *Service) runLoop(ctx context.Context) {
	for {
		delay := s.tickOnce(ctx)
		if delay > maxTimerSleep {
			delay = maxTimerSleep
		}
		if delay < 250*time.Millisecond {
			delay = 250 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wakeup:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// tickOnce evaluates due schedules and returns how long until the next wake-up.
func (s *Service) tickOnce(ctx context.Context) time.Duration {
	now := s.now()
	nowMs := now.UnixMilli()

	type dueJob struct {
		id string
	}
	var due []dueJob
	var earliestMs int64
	var persistSeq uint64
	var persistDoc storeDoc
	dirty := false

	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return maxTimerSleep
	}
	for id, sch := range s.schedules {
		if !sch.Enabled {
			continue
		}
		if sch.NextRunAt <= 0 {
			if next, err := NextFireTime(sch.Trigger, now); err == nil {
				sch.NextRunAt = next.UnixMilli()
				s.schedules[id] = sch
				dirty = true
			}
		}
		if sch.NextRunAt > 0 && sch.NextRunAt <= nowMs {
			overdue := time.Duration(nowMs-sch.NextRunAt) * time.Millisecond
			next, err := NextFireTime(sch.Trigger, now)
			if err != nil {
				sch.Enabled = false
				sch.PausedReason = "invalid schedule trigger: " + err.Error()
				sch.NextRunAt = 0
				sch.UpdatedAt = nowMs
				s.schedules[id] = sch
				dirty = true
				continue
			}
			sch.NextRunAt = next.UnixMilli()
			sch.UpdatedAt = nowMs
			s.schedules[id] = sch
			dirty = true

			if overdue > misfireGraceWindow && sch.Trigger.Misfire != MisfireCoalesce {
				// Misfire skip: machine was likely asleep/offline; advance silently.
			} else {
				due = append(due, dueJob{id: id})
			}
		}
		if sch.NextRunAt > nowMs && (earliestMs == 0 || sch.NextRunAt < earliestMs) {
			earliestMs = sch.NextRunAt
		}
	}
	if dirty {
		persistSeq, persistDoc = s.snapshotLocked()
	}
	s.mu.Unlock()

	if dirty {
		_ = s.store.saveSnapshot(persistSeq, persistDoc)
	}
	for _, job := range due {
		s.launchExecution(ctx, job.id, "scheduled")
	}
	if earliestMs <= nowMs {
		return maxTimerSleep
	}
	return time.Duration(earliestMs-nowMs) * time.Millisecond
}

func (s *Service) launchExecution(parentCtx context.Context, id, triggerKind string) *RunRecord {
	now := s.now()
	nowMs := now.UnixMilli()

	s.mu.Lock()
	sch, ok := s.schedules[id]
	if !ok || s.stopped || sch.Running || s.running[id] != nil {
		s.mu.Unlock()
		return nil
	}
	timeout := time.Duration(sch.Execution.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds * time.Second
	}
	runCtx, cancel := context.WithTimeout(parentCtx, timeout)
	s.running[id] = cancel
	sch.Running = true
	sch.LastRunAt = nowMs
	sch.UpdatedAt = nowMs
	s.schedules[id] = sch
	seq, doc := s.snapshotLocked()
	s.execWG.Add(1)
	s.mu.Unlock()

	_ = s.store.saveSnapshot(seq, doc)
	if s.host != nil {
		s.host.BroadcastUpdated(id)
	}

	run := RunRecord{
		ID:         newRandomID(RunIDPrefix),
		ScheduleID: id,
		SessionID:  sch.SessionID,
		Trigger:    triggerKind,
		Status:     StatusRunning,
		StartedAt:  nowMs,
	}
	s.recordRun(run)

	go s.executeAsync(runCtx, cancel, cloneSchedule(sch), run)
	return &run
}

func (s *Service) executeAsync(ctx context.Context, cancel context.CancelFunc, sch Schedule, run RunRecord) {
	defer s.execWG.Done()
	defer cancel()
	defer func() {
		s.mu.Lock()
		delete(s.running, sch.ID)
		s.mu.Unlock()
	}()

	// Acquire global concurrency slot.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		s.finishRun(sch.ID, run, StatusInterrupted, "", "cancelled before starting", false)
		return
	}

	if s.host == nil {
		s.finishRun(sch.ID, run, StatusError, "", "scheduler host is unavailable", false)
		return
	}

	sessionID := strings.TrimSpace(sch.SessionID)
	if sessionID != "" {
		exists, _ := s.host.SessionExists(sessionID)
		if !exists {
			s.finishRun(sch.ID, run, StatusError, "", "bound session was deleted", true)
			return
		}
		if s.host.IsSessionBusy(sessionID) {
			s.finishRun(sch.ID, run, StatusSkippedBusy, "Skipped because bound session is currently busy", "", false)
			return
		}
	} else {
		createdID, err := s.host.CreateSession(ctx, CreateSessionInput{
			ScheduleID:     sch.ID,
			Title:          "[Schedule] " + sch.Name,
			Directory:      sch.Directory,
			Runtime:        sch.Execution.Runtime,
			Mode:           sch.Execution.Mode,
			ProviderID:     sch.Execution.ProviderID,
			ModelID:        sch.Execution.ModelID,
			SandboxMode:    sch.Execution.SandboxMode,
			CommandSandbox: sch.Execution.CommandSandbox,
		})
		if err != nil {
			s.finishRun(sch.ID, run, StatusError, "", diagnostics.RedactSecrets(err.Error()), false)
			return
		}
		sessionID = createdID
		run.SessionID = sessionID
		s.recordRun(run)
	}

	// Track active scheduled turn by sessionID to prevent recursive hoxi_schedule calls.
	s.mu.Lock()
	s.activeBySession[sessionID]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if n := s.activeBySession[sessionID] - 1; n <= 0 {
			delete(s.activeBySession, sessionID)
		} else {
			s.activeBySession[sessionID] = n
		}
		s.mu.Unlock()
	}()

	systemPrompt := BuildTurnSystem(sch, run.Trigger, s.now())
	reply, err := s.host.RunTurn(ctx, sessionID, RunTurnInput{
		ScheduleID:     sch.ID,
		Prompt:         sch.Prompt,
		System:         systemPrompt,
		Mode:           sch.Execution.Mode,
		Runtime:        sch.Execution.Runtime,
		ProviderID:     sch.Execution.ProviderID,
		ModelID:        sch.Execution.ModelID,
		Variant:        sch.Execution.Variant,
		SkillSelection: cloneSkillRef(sch.SkillSelection),
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			s.host.AbortSession(sessionID)
			s.finishRun(sch.ID, run, StatusError, "", "scheduled run timed out", false)
			return
		}
		if errors.Is(err, context.Canceled) {
			s.host.AbortSession(sessionID)
			s.finishRun(sch.ID, run, StatusInterrupted, "", "scheduled run cancelled", false)
			return
		}
		s.finishRun(sch.ID, run, StatusError, "", diagnostics.RedactSecrets(err.Error()), false)
		return
	}

	status, summary := ParseVerdict(reply)
	s.finishRun(sch.ID, run, status, summary, "", false)
}

func (s *Service) finishRun(scheduleID string, run RunRecord, status, summary, errMsg string, forcePauseMissingSession bool) {
	nowMs := s.now().UnixMilli()
	run.Status = status
	run.Summary = summary
	run.Error = errMsg
	run.CompletedAt = nowMs

	// Persist run history while sch.Running is still true (skips if schedule was deleted).
	s.recordRun(run)

	var notifyTitle, notifyBody, notifySession string
	s.mu.Lock()
	if sch, ok := s.schedules[scheduleID]; ok {
		shouldNotify := false
		switch sch.NotifyOn {
		case NotifyAlways:
			shouldNotify = status != StatusSkippedBusy
		case NotifyAlertOrError:
			shouldNotify = status == StatusAlert || status == StatusBlocked || status == StatusError
		}
		if shouldNotify {
			notifySession = run.SessionID
			switch status {
			case StatusAlert:
				notifyTitle = "Hoxi Schedule Alert: " + sch.Name
				notifyBody = firstNonEmpty(summary, "Scheduled check reported an alert.")
			case StatusBlocked:
				notifyTitle = "Hoxi Schedule Blocked: " + sch.Name
				notifyBody = firstNonEmpty(summary, "Scheduled check needs your attention.")
			case StatusError:
				notifyTitle = "Hoxi Schedule Failed: " + sch.Name
				notifyBody = firstNonEmpty(errMsg, "Scheduled check failed.")
			default:
				notifyTitle = "Hoxi Schedule Completed: " + sch.Name
				notifyBody = firstNonEmpty(summary, "Scheduled check finished.")
			}
		}
	}
	s.mu.Unlock()

	if notifyTitle != "" && s.host != nil {
		s.host.Notify(notifyTitle, notifyBody, notifySession)
	}

	var seq uint64
	var doc storeDoc
	var updated bool

	s.mu.Lock()
	delete(s.running, scheduleID)
	if sch, ok := s.schedules[scheduleID]; ok {
		sch.Running = false
		sch.LastOutcome = status
		sch.LastError = errMsg
		if run.SessionID != "" {
			sch.LastSessionID = run.SessionID
		}
		if summary != "" {
			sch.LastSummary = summary
		}
		switch status {
		case StatusOK:
			sch.ConsecutiveFails = 0
		case StatusAlert, StatusBlocked:
			sch.ConsecutiveFails = 0
			sch.AlertUnseen = true
		case StatusError:
			sch.ConsecutiveFails++
			sch.AlertUnseen = true
			if forcePauseMissingSession {
				sch.Enabled = false
				sch.NextRunAt = 0
				sch.PausedReason = "bound session was deleted"
			} else if sch.MaxConsecutiveFails > 0 && sch.ConsecutiveFails >= sch.MaxConsecutiveFails {
				sch.Enabled = false
				sch.NextRunAt = 0
				sch.PausedReason = fmt.Sprintf("auto-paused after %d consecutive failures", sch.ConsecutiveFails)
			}
		case StatusSkippedBusy, StatusInterrupted:
			// Neutral: do not count toward consecutive failures.
		}
		sch.UpdatedAt = nowMs
		s.schedules[scheduleID] = sch
		seq, doc = s.snapshotLocked()
		updated = true
	}
	s.mu.Unlock()

	if updated {
		_ = s.store.saveSnapshot(seq, doc)
		if s.host != nil {
			s.host.BroadcastUpdated(scheduleID)
		}
	}
}

func (s *Service) recordRun(run RunRecord) {
	s.mu.Lock()
	if _, exists := s.schedules[run.ScheduleID]; !exists {
		s.mu.Unlock()
		return
	}
	existing, loaded := s.runsCache[run.ScheduleID]
	s.mu.Unlock()
	if !loaded {
		existing = s.store.loadRuns(run.ScheduleID)
	}

	s.mu.Lock()
	if _, exists := s.schedules[run.ScheduleID]; !exists {
		s.mu.Unlock()
		return
	}
	list := s.runsCache[run.ScheduleID]
	if len(list) == 0 && len(existing) > 0 {
		list = existing
	}
	replaced := false
	for i := range list {
		if list[i].ID == run.ID {
			list[i] = run
			replaced = true
			break
		}
	}
	if !replaced {
		list = append([]RunRecord{run}, list...)
	}
	if len(list) > MaxStoredRunsPerJob {
		list = list[:MaxStoredRunsPerJob]
	}
	snapshot := make([]RunRecord, len(list))
	copy(snapshot, list)
	s.runsCache[run.ScheduleID] = snapshot
	s.mu.Unlock()

	_ = s.store.saveRuns(run.ScheduleID, snapshot)

	// Guard against Delete racing between unlock and saveRuns.
	s.mu.Lock()
	_, stillExists := s.schedules[run.ScheduleID]
	if !stillExists {
		delete(s.runsCache, run.ScheduleID)
	}
	s.mu.Unlock()
	if !stillExists {
		s.store.deleteRuns(run.ScheduleID)
	}
}
