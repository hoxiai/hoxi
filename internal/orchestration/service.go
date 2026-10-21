package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

var (
	ErrPlanNotFound = errors.New("orchestration plan not found")
	ErrPlanActive   = errors.New("orchestration plan is still running")
	ErrPlanStopping = errors.New("a task is still stopping; try again shortly")
)

const interruptedError = "interrupted by a Hoxi restart"

// Sessions runs convoy tasks as Hoxi conversations. The gateway implements it
// over the runner, so convoy sessions share the UI's session plane: they are
// stored and listed, drive the status bar, pass the runtime approval gates and
// the stall watchdog, and can be steered or stopped like any other session.
type Sessions interface {
	// CanIsolate reports whether directory can host one git worktree per task.
	CanIsolate(directory string) bool
	Create(ctx context.Context, req SessionRequest) (string, error)
	// Run sends prompt as one turn and blocks until it ends, returning the
	// final assistant text.
	Run(ctx context.Context, sessionID, prompt string, req PlanRequest) (string, error)
	// Merge folds the uncommitted changes of sources, in order, into target's
	// workspace, failing without side effects on conflict.
	Merge(targetSessionID string, sourceSessionIDs []string) error
	Abort(sessionID string)
}

// SessionRequest describes the conversation that hosts one convoy task.
type SessionRequest struct {
	Directory string
	Title     string
	Mode      string
	Provider  string
	Model     string
	// Isolated asks for a dedicated git worktree instead of Directory itself.
	Isolated bool
}

// Service runs convoy plans: a DAG of tasks, each executed as its own Hoxi
// conversation. When the project is a git repository every task gets its own
// worktree, and a dependent task starts from its prerequisites' merged
// changes; otherwise tasks share the directory and run one at a time.
type Service struct {
	sessions Sessions
	store    *Store
	llm      llm.Client
	mu       sync.Mutex
	plans    map[string]*planRun
}

type planRun struct {
	plan   Plan
	cancel context.CancelFunc
	// epoch advances on every retry. A superseded scheduler or task must not
	// write results into the new run.
	epoch int64
}

// Option configures optional Service dependencies.
type Option func(*Service)

// WithStore persists plans so history and retries survive app restarts.
func WithStore(store *Store) Option {
	return func(s *Service) { s.store = store }
}

// WithLLM enables drafting a convoy plan from a natural-language goal.
func WithLLM(client llm.Client) Option {
	return func(s *Service) { s.llm = client }
}

func NewService(sessions Sessions, options ...Option) *Service {
	s := &Service{sessions: sessions, plans: make(map[string]*planRun)}
	for _, option := range options {
		option(s)
	}
	s.restore()
	return s
}

// restore loads persisted plans. Plans that were mid-flight when Hoxi exited
// can no longer resume their child processes, so they surface as failed tasks
// that the user can retry explicitly.
func (s *Service) restore() {
	if s.store == nil {
		return
	}
	plans, err := s.store.Load()
	if err != nil {
		return
	}
	now := time.Now().UnixMilli()
	for _, plan := range plans {
		if !terminal(plan.Status) {
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
			_ = s.store.Save(plan)
		}
		s.plans[plan.ID] = &planRun{plan: plan}
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/orchestration/plans", s.handlePlans)
	mux.HandleFunc("/api/hoxi/orchestration/plans/", s.handlePlan)
	mux.HandleFunc("/api/hoxi/orchestration/draft", s.HandleDraft)
}

func (s *Service) Create(request PlanRequest) (Plan, error) {
	directory, err := canonicalDirectory(request.Directory)
	if err != nil {
		return Plan{}, err
	}
	request.Directory = directory
	request, err = normalizePlan(request)
	if err != nil {
		return Plan{}, err
	}
	if s.sessions == nil {
		return Plan{}, errors.New("convoy sessions are unavailable")
	}
	isolated := s.sessions.CanIsolate(directory)
	if !isolated {
		// Without per-task worktrees, parallel agents would edit the same files.
		request.Concurrency = 1
	}
	plan := Plan{
		Isolated:    isolated,
		ID:          newPlanID(),
		Title:       request.Title,
		Directory:   directory,
		Mode:        request.Mode,
		Provider:    request.Provider,
		Model:       request.Model,
		Concurrency: request.Concurrency,
		Status:      StatusRunning,
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
		Tasks:       make([]TaskState, len(request.Tasks)),
	}
	for i, task := range request.Tasks {
		plan.Tasks[i] = TaskState{
			ID: task.ID, Prompt: task.Prompt, DependsOn: task.DependsOn, Status: StatusQueued,
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &planRun{plan: plan, cancel: cancel, epoch: 1}
	s.mu.Lock()
	s.plans[plan.ID] = run
	if err := s.persistLocked(run); err != nil {
		delete(s.plans, plan.ID)
		s.mu.Unlock()
		cancel()
		return Plan{}, fmt.Errorf("could not persist orchestration plan: %w", err)
	}
	s.pruneLocked(directory)
	epoch := run.epoch
	s.mu.Unlock()
	go s.run(ctx, plan.ID, request, epoch)
	snapshot, _ := s.Get(plan.ID)
	return snapshot, nil
}

func (s *Service) Get(id string) (Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.plans[id]
	if run == nil {
		return Plan{}, false
	}
	plan := run.plan
	plan.Tasks = append([]TaskState(nil), run.plan.Tasks...)
	return plan, true
}

func (s *Service) List(directory string) []Plan {
	directory = strings.TrimSpace(directory)
	s.mu.Lock()
	defer s.mu.Unlock()
	plans := make([]Plan, 0, len(s.plans))
	for _, run := range s.plans {
		if directory != "" && run.plan.Directory != directory {
			continue
		}
		plan := run.plan
		plan.Tasks = append([]TaskState(nil), run.plan.Tasks...)
		plans = append(plans, plan)
	}
	sortPlans(plans)
	return plans
}

// ListBySession finds the plans that own sessionID as one of their task
// sessions. The UI uses it to keep the plan visible while a task's isolated
// worktree directory is the active one.
func (s *Service) ListBySession(sessionID string) []Plan {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return []Plan{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plans := make([]Plan, 0, 1)
	for _, run := range s.plans {
		owned := false
		for _, task := range run.plan.Tasks {
			if task.SessionID == sessionID {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		plan := run.plan
		plan.Tasks = append([]TaskState(nil), run.plan.Tasks...)
		plans = append(plans, plan)
	}
	sortPlans(plans)
	return plans
}

func sortPlans(plans []Plan) {
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].CreatedAt == plans[j].CreatedAt {
			return plans[i].ID > plans[j].ID
		}
		return plans[i].CreatedAt > plans[j].CreatedAt
	})
}

func (s *Service) Cancel(id string) (Plan, error) {
	s.mu.Lock()
	run := s.plans[id]
	if run == nil {
		s.mu.Unlock()
		return Plan{}, ErrPlanNotFound
	}
	if !terminal(run.plan.Status) {
		run.plan.Status = StatusCancelled
		for i := range run.plan.Tasks {
			if run.plan.Tasks[i].Status == StatusQueued {
				run.plan.Tasks[i].Status = StatusCancelled
			}
		}
		run.plan.UpdatedAt = time.Now().UnixMilli()
		_ = s.persistLocked(run)
	}
	cancel := run.cancel
	plan := run.plan
	plan.Tasks = append([]TaskState(nil), run.plan.Tasks...)
	// Collect live sessions before cancelling: a cancelled task marks itself
	// cancelled at once and would otherwise drop out of the abort list.
	var live []string
	for _, task := range run.plan.Tasks {
		if task.SessionID != "" && task.Status == StatusRunning {
			live = append(live, task.SessionID)
		}
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	go func() {
		for _, sessionID := range live {
			s.sessions.Abort(sessionID)
		}
	}()
	return plan, nil
}

// Retry restarts a terminal plan's unfinished tasks. Tasks that already
// succeeded keep their sessions and results; failed, blocked and cancelled
// tasks run again with fresh sessions, in dependency order.
func (s *Service) Retry(id string) (Plan, error) {
	s.mu.Lock()
	run := s.plans[id]
	if run == nil {
		s.mu.Unlock()
		return Plan{}, ErrPlanNotFound
	}
	if !terminal(run.plan.Status) {
		s.mu.Unlock()
		return Plan{}, ErrPlanActive
	}
	for _, task := range run.plan.Tasks {
		if task.Status == StatusRunning {
			s.mu.Unlock()
			return Plan{}, ErrPlanStopping
		}
	}
	request := PlanRequest{
		Directory:   run.plan.Directory,
		Title:       run.plan.Title,
		Mode:        run.plan.Mode,
		Model:       run.plan.Model,
		Provider:    run.plan.Provider,
		Concurrency: run.plan.Concurrency,
		Tasks:       make([]TaskSpec, 0, len(run.plan.Tasks)),
	}
	for i := range run.plan.Tasks {
		task := &run.plan.Tasks[i]
		request.Tasks = append(request.Tasks, TaskSpec{ID: task.ID, Prompt: task.Prompt, DependsOn: task.DependsOn})
		if task.Status == StatusSucceeded {
			continue
		}
		task.Status = StatusQueued
		task.Summary = ""
		task.Error = ""
		task.StartedAt = 0
		task.EndedAt = 0
	}
	run.plan.Status = StatusRunning
	run.plan.Error = ""
	run.plan.UpdatedAt = time.Now().UnixMilli()
	run.epoch++
	epoch := run.epoch
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	if err := s.persistLocked(run); err != nil {
		run.plan.Status = StatusFailed
		run.plan.Error = "could not persist retry: " + err.Error()
		s.mu.Unlock()
		cancel()
		return Plan{}, fmt.Errorf("could not persist orchestration plan: %w", err)
	}
	plan := run.plan
	plan.Tasks = append([]TaskState(nil), run.plan.Tasks...)
	s.mu.Unlock()
	go s.run(ctx, id, request, epoch)
	return plan, nil
}

func terminal(status string) bool {
	return status == StatusSucceeded || status == StatusFailed || status == StatusCancelled
}

func (s *Service) run(ctx context.Context, id string, request PlanRequest, epoch int64) {
	running := make(map[string]bool)
	for {
		s.mu.Lock()
		run := s.plans[id]
		if run == nil || run.epoch != epoch {
			s.mu.Unlock()
			return
		}
		if run.plan.Status == StatusCancelled {
			s.mu.Unlock()
			return
		}
		ready := sortedReady(&run.plan)
		capacity := run.plan.Concurrency - len(running)
		if capacity < 0 {
			capacity = 0
		}
		var launch []int
		for _, index := range ready {
			if len(launch) >= capacity {
				break
			}
			task := &run.plan.Tasks[index]
			if running[task.ID] {
				continue
			}
			task.Status = StatusRunning
			task.StartedAt = time.Now().UnixMilli()
			run.plan.UpdatedAt = task.StartedAt
			running[task.ID] = true
			launch = append(launch, index)
			capacity--
		}
		finished := len(running) == 0 && len(launch) == 0
		if finished {
			s.finish(id, epoch)
			s.mu.Unlock()
			return
		}
		_ = s.persistLocked(run)
		s.mu.Unlock()

		var wg sync.WaitGroup
		for _, index := range launch {
			spec := request.Tasks[index]
			wg.Add(1)
			go func(index int, spec TaskSpec) {
				defer wg.Done()
				s.executeTask(ctx, id, index, spec, request, epoch)
				s.mu.Lock()
				delete(running, spec.ID)
				s.mu.Unlock()
			}(index, spec)
		}
		wg.Wait()
	}
}

func (s *Service) executeTask(ctx context.Context, planID string, index int, spec TaskSpec, request PlanRequest, epoch int64) {
	taskID := spec.ID
	s.mu.Lock()
	run := s.plans[planID]
	if run == nil || run.epoch != epoch {
		s.mu.Unlock()
		return
	}
	isolated := run.plan.Isolated
	var prerequisites []string
	for _, dep := range spec.DependsOn {
		for _, task := range run.plan.Tasks {
			if task.ID == dep && task.SessionID != "" {
				prerequisites = append(prerequisites, task.SessionID)
			}
		}
	}
	s.mu.Unlock()

	sessionID, err := s.sessions.Create(ctx, SessionRequest{
		Directory: request.Directory,
		Title:     fmt.Sprintf("Convoy · %s · %s", planID, taskID),
		Mode:      request.Mode,
		Provider:  request.Provider,
		Model:     request.Model,
		Isolated:  isolated,
	})
	if err != nil {
		s.completeTask(planID, epoch, taskID, "", "", err)
		return
	}
	s.mu.Lock()
	if run := s.plans[planID]; run != nil && run.epoch == epoch {
		run.plan.Tasks[index].SessionID = sessionID
		run.plan.UpdatedAt = time.Now().UnixMilli()
		_ = s.persistLocked(run)
	} else {
		s.mu.Unlock()
		// A retry superseded this task while its session was starting.
		s.sessions.Abort(sessionID)
		return
	}
	s.mu.Unlock()
	if ctx.Err() != nil {
		s.completeTask(planID, epoch, taskID, sessionID, "", ctx.Err())
		return
	}
	if isolated && len(prerequisites) > 0 {
		if err := s.sessions.Merge(sessionID, prerequisites); err != nil {
			s.completeTask(planID, epoch, taskID, sessionID, "", fmt.Errorf("could not combine prerequisite changes: %w", err))
			return
		}
	}
	summary, err := s.sessions.Run(ctx, sessionID, convoyPrompt(spec, isolated), request)
	s.completeTask(planID, epoch, taskID, sessionID, clip(summary, 2000), err)
}

func (s *Service) completeTask(planID string, epoch int64, taskID, sessionID, summary string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.plans[planID]
	if run == nil || run.epoch != epoch {
		return
	}
	for i := range run.plan.Tasks {
		task := &run.plan.Tasks[i]
		if task.ID != taskID {
			continue
		}
		if sessionID != "" {
			task.SessionID = sessionID
		}
		task.EndedAt = time.Now().UnixMilli()
		task.Summary = summary
		switch {
		case run.plan.Status == StatusCancelled || errors.Is(err, context.Canceled):
			task.Status = StatusCancelled
			task.Error = "cancelled"
		case err != nil:
			task.Status = StatusFailed
			task.Error = err.Error()
		default:
			task.Status = StatusSucceeded
		}
		break
	}
	run.plan.UpdatedAt = time.Now().UnixMilli()
	_ = s.persistLocked(run)
}

func (s *Service) finish(planID string, epoch int64) {
	run := s.plans[planID]
	if run == nil || run.epoch != epoch {
		return
	}
	status := StatusSucceeded
	for _, task := range run.plan.Tasks {
		switch task.Status {
		case StatusFailed:
			status = StatusFailed
		case StatusBlocked:
			if status == StatusSucceeded {
				status = StatusFailed
			}
		case StatusRunning, StatusQueued:
			if run.plan.Status == StatusCancelled {
				status = StatusCancelled
			}
		}
	}
	if run.plan.Status == StatusCancelled {
		status = StatusCancelled
	}
	run.plan.Status = status
	run.plan.UpdatedAt = time.Now().UnixMilli()
	_ = s.persistLocked(run)
}

func (s *Service) persistLocked(run *planRun) error {
	if s.store == nil || run == nil {
		return nil
	}
	snapshot := run.plan
	snapshot.Tasks = append([]TaskState(nil), run.plan.Tasks...)
	return s.store.Save(snapshot)
}

const maxPersistedPlansPerDirectory = 50

// pruneLocked keeps recent history bounded without ever dropping an active
// plan or a plan from another workspace.
func (s *Service) pruneLocked(directory string) {
	if s.store == nil {
		return
	}
	var finished []*planRun
	for _, run := range s.plans {
		if run.plan.Directory == directory && terminal(run.plan.Status) {
			finished = append(finished, run)
		}
	}
	if len(finished) <= maxPersistedPlansPerDirectory {
		return
	}
	sort.Slice(finished, func(i, j int) bool {
		return finished[i].plan.CreatedAt < finished[j].plan.CreatedAt
	})
	for _, run := range finished[:len(finished)-maxPersistedPlansPerDirectory] {
		delete(s.plans, run.plan.ID)
		_ = s.store.Delete(run.plan.ID)
	}
}

func convoyPrompt(spec TaskSpec, isolated bool) string {
	var b strings.Builder
	b.WriteString("Hoxi convoy task `")
	b.WriteString(spec.ID)
	b.WriteString("`.\n")
	if len(spec.DependsOn) > 0 {
		b.WriteString("Completed prerequisites: ")
		b.WriteString(strings.Join(spec.DependsOn, ", "))
		b.WriteString(". Their changes are already in this workspace.\n")
	}
	b.WriteString("Work only within the workspace, report the files you changed, and end with a short hand-off summary.\n")
	if isolated {
		b.WriteString("Do not commit: leave your changes uncommitted so dependent tasks can build on them.\n")
	}
	b.WriteString("\n")
	b.WriteString(spec.Prompt)
	return b.String()
}

func canonicalDirectory(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", errors.New("directory is required")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", errors.New("directory does not exist")
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

func newPlanID() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return "plan_" + strconv.FormatInt(time.Now().UnixMilli(), 36) + "_" + hex.EncodeToString(buf[:])
}

func clip(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
