package orchestration

import (
	"context"
	"strings"
	"time"
)

const maxConvoyRestartRecoveries = 2

var convoyRestartMaxAge = 30 * time.Minute

// ResumableSessions is an optional extension implemented by session backends
// that can attach to or resume an interrupted conversation turn across a Hoxi
// restart without discarding work already saved in its isolated worktree.
type ResumableSessions interface {
	Resume(ctx context.Context, sessionID, prompt string, req PlanRequest) (string, error)
}

func (s *Service) resumeOrRunSession(ctx context.Context, sessionID, prompt string, req PlanRequest, isResume bool) (string, error) {
	if isResume {
		if rs, ok := s.sessions.(ResumableSessions); ok {
			return rs.Resume(ctx, sessionID, prompt, req)
		}
	}
	return s.sessions.Run(ctx, sessionID, prompt, req)
}

func (s *Service) restorePlan(plan Plan, now int64) *planRun {
	if terminal(plan.Status) {
		return &planRun{plan: plan, seq: 1}
	}
	maxAgeMs := convoyRestartMaxAge.Milliseconds()
	fresh := maxAgeMs <= 0 || (plan.UpdatedAt > 0 && now-plan.UpdatedAt <= maxAgeMs)
	if plan.RestartPending && fresh && plan.RestartRecoveries < maxConvoyRestartRecoveries {
		plan.Status = StatusRunning
		plan.Error = ""
		for i := range plan.Tasks {
			if plan.Tasks[i].Status == StatusRunning {
				// Re-queue while preserving SessionID and PrerequisitesMerged so
				// executeTask resumes the existing conversation and worktree.
				plan.Tasks[i].Status = StatusQueued
			}
		}
		return &planRun{plan: plan, seq: 1}
	}
	plan.RestartPending = false
	plan.Status = StatusFailed
	plan.Error = interruptedError
	for i := range plan.Tasks {
		task := &plan.Tasks[i]
		if task.Status == StatusRunning || task.Status == StatusQueued {
			task.Status = StatusFailed
			task.Error = interruptedError
			task.EndedAt = now
		}
	}
	plan.UpdatedAt = now
	_ = s.store.SaveSeq(plan, 1)
	return &planRun{plan: plan, seq: 1}
}

// PrepareForRestart checkpoints every active convoy plan as RestartPending and
// detaches its current scheduler epoch before child sessions are stopped, so
// shutdown aborts do not mark in-flight tasks as permanently failed.
func (s *Service) PrepareForRestart() {
	if s == nil {
		return
	}
	now := time.Now().UnixMilli()
	var snaps []planSnapshot
	var cancels []context.CancelFunc

	s.mu.Lock()
	for _, run := range s.plans {
		if run == nil || terminal(run.plan.Status) {
			continue
		}
		run.epoch++
		run.plan.RestartPending = true
		run.plan.UpdatedAt = now
		snaps = append(snaps, snapshotLocked(run))
		if run.cancel != nil {
			cancels = append(cancels, run.cancel)
			run.cancel = nil
		}
	}
	s.mu.Unlock()

	for _, snap := range snaps {
		_ = s.persist(snap)
	}
	for _, cancel := range cancels {
		cancel()
	}
}

// ResumeAfterRestart restarts scheduling for any convoy plans that were
// checkpointed via PrepareForRestart prior to the restart.
func (s *Service) ResumeAfterRestart(ctx context.Context) {
	if s == nil || s.sessions == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	type pendingLaunch struct {
		id    string
		req   PlanRequest
		epoch int64
		ctx   context.Context
		snap  planSnapshot
	}
	var launches []pendingLaunch
	now := time.Now().UnixMilli()

	s.mu.Lock()
	for id, run := range s.plans {
		if run == nil || !run.plan.RestartPending || terminal(run.plan.Status) {
			continue
		}
		req := PlanRequest{
			Directory:       run.plan.Directory,
			ParentSessionID: run.plan.ParentSessionID,
			Title:           run.plan.Title,
			Mode:            run.plan.Mode,
			Model:           run.plan.Model,
			Provider:        run.plan.Provider,
			Concurrency:     run.plan.Concurrency,
			Tasks:           make([]TaskSpec, 0, len(run.plan.Tasks)),
		}
		for _, task := range run.plan.Tasks {
			req.Tasks = append(req.Tasks, TaskSpec{
				ID:        strings.TrimSpace(task.ID),
				Prompt:    task.Prompt,
				DependsOn: append([]string(nil), task.DependsOn...),
			})
		}
		run.plan.RestartPending = false
		run.plan.RestartRecoveries++
		run.plan.Status = StatusRunning
		run.plan.Error = ""
		run.plan.UpdatedAt = now
		run.epoch++
		planCtx, cancel := context.WithCancel(ctx)
		run.cancel = cancel
		launches = append(launches, pendingLaunch{
			id:    id,
			req:   req,
			epoch: run.epoch,
			ctx:   planCtx,
			snap:  snapshotLocked(run),
		})
	}
	s.mu.Unlock()

	for _, item := range launches {
		_ = s.persist(item.snap)
		go s.run(item.ctx, item.id, item.req, item.epoch)
	}
}
