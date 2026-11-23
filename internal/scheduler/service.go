package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxConcurrentScheduledRuns = 2
	misfireGraceWindow         = 2 * time.Minute
	maxTimerSleep              = 30 * time.Second
)

// Service coordinates scheduled conversation tasks, persistence, and execution.
type Service struct {
	mu              sync.Mutex
	store           *diskStore
	loadErr         error
	seq             uint64
	schedules       map[string]Schedule
	runsCache       map[string][]RunRecord
	running         map[string]context.CancelFunc
	activeBySession map[string]int // sessionID -> count of active scheduled runs
	sem             chan struct{}
	wakeup          chan struct{}
	nowFn           func() time.Time
	host            Host

	loopMu     sync.Mutex
	loopCancel context.CancelFunc
	loopDone   chan struct{}
	execWG     sync.WaitGroup
	stopped    bool
}

// NewService initializes the scheduler store and recovers any interrupted runs.
func NewService(storePath, runsDir string, host Host) *Service {
	ds := newDiskStore(storePath, runsDir)
	doc, err := ds.load()
	now := time.Now()
	nowMs := now.UnixMilli()
	recovered := false
	for id, sch := range doc.Schedules {
		if sch.Running {
			sch.Running = false
			sch.LastOutcome = StatusInterrupted
			sch.LastError = "interrupted by host restart"
			sch.UpdatedAt = nowMs
			recovered = true
		}
		if sch.Enabled && sch.NextRunAt <= 0 {
			if next, nErr := NextFireTime(sch.Trigger, now); nErr == nil {
				sch.NextRunAt = next.UnixMilli()
				recovered = true
			}
		}
		doc.Schedules[id] = sch
	}
	var initSeq uint64
	if err == nil && recovered {
		initSeq = 1
		if saveErr := ds.saveSnapshot(initSeq, doc); saveErr != nil {
			err = saveErr
		}
	}
	return &Service{
		store:           ds,
		loadErr:         err,
		seq:             initSeq,
		schedules:       doc.Schedules,
		runsCache:       make(map[string][]RunRecord),
		running:         make(map[string]context.CancelFunc),
		activeBySession: make(map[string]int),
		sem:             make(chan struct{}, maxConcurrentScheduledRuns),
		wakeup:          make(chan struct{}, 1),
		nowFn:           time.Now,
		host:            host,
	}
}

func (s *Service) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// LoadError returns any startup store load error.
func (s *Service) LoadError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadErr
}

// IsSessionRunningScheduledTurn reports whether sessionID is currently executing
// a turn initiated by the scheduler (used to block recursive schedule mutations).
func (s *Service) IsSessionRunningScheduledTurn(sessionID string) bool {
	sid := strings.TrimSpace(sessionID)
	if s == nil || sid == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeBySession[sid] > 0
}

func (s *Service) signalWakeup() {
	select {
	case s.wakeup <- struct{}{}:
	default:
	}
}

// Start begins the background scheduling loop until ctx is cancelled or Stop is called.
func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.loopMu.Lock()
	if s.loopCancel != nil {
		s.loopMu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.loopCancel = cancel
	s.loopDone = done
	s.loopMu.Unlock()

	s.mu.Lock()
	s.stopped = false
	s.mu.Unlock()

	go func() {
		defer close(done)
		s.runLoop(loopCtx)
	}()
}

// Stop stops the scheduling loop and cancels any in-flight scheduled turns.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.loopMu.Lock()
	cancel := s.loopCancel
	done := s.loopDone
	s.loopCancel = nil
	s.loopDone = nil
	s.loopMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.mu.Lock()
	s.stopped = true
	cancels := make([]context.CancelFunc, 0, len(s.running))
	for _, fn := range s.running {
		cancels = append(cancels, fn)
	}
	s.mu.Unlock()
	for _, fn := range cancels {
		fn()
	}
	if done != nil {
		<-done
	}
	s.execWG.Wait()
}

func (s *Service) snapshotLocked() (uint64, storeDoc) {
	s.seq++
	cp := make(map[string]Schedule, len(s.schedules))
	for id, sch := range s.schedules {
		cp[id] = cloneSchedule(sch)
	}
	return s.seq, storeDoc{Version: storeVersion, Schedules: cp}
}

// List returns schedules optionally filtered by projectID and/or directory.
func (s *Service) List(projectID, directory string) []Schedule {
	wantProj := strings.TrimSpace(projectID)
	wantDir := cleanCompareDir(directory)
	s.mu.Lock()
	out := make([]Schedule, 0, len(s.schedules))
	for _, sch := range s.schedules {
		if wantProj != "" && strings.TrimSpace(sch.ProjectID) != wantProj {
			if wantDir == "" || cleanCompareDir(sch.Directory) != wantDir {
				continue
			}
		} else if wantProj == "" && wantDir != "" && cleanCompareDir(sch.Directory) != wantDir {
			continue
		}
		out = append(out, cloneSchedule(sch))
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns a single schedule by ID.
func (s *Service) Get(id string) (Schedule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sch, ok := s.schedules[strings.TrimSpace(id)]
	if !ok {
		return Schedule{}, false
	}
	return cloneSchedule(sch), true
}

// Create validates and persists a new schedule.
func (s *Service) Create(in UpsertInput) (Schedule, error) {
	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return Schedule{}, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	if len(s.schedules) >= MaxTotalSchedules {
		s.mu.Unlock()
		return Schedule{}, ErrTooManySchedules
	}
	s.mu.Unlock()

	now := s.now()
	nowMs := now.UnixMilli()
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	injectSummary := true
	if in.InjectLastSummary != nil {
		injectSummary = *in.InjectLastSummary
	}
	maxFails := DefaultMaxFails
	if in.MaxConsecutiveFails != nil {
		maxFails = *in.MaxConsecutiveFails
	}

	sch := Schedule{
		ID:                  newRandomID(ScheduleIDPrefix),
		Name:                in.Name,
		Enabled:             enabled,
		ProjectID:           in.ProjectID,
		Directory:           in.Directory,
		SessionID:           in.SessionID,
		Trigger:             in.Trigger,
		Execution:           in.Execution,
		SkillSelection:      cloneSkillRef(in.SkillSelection),
		Prompt:              in.Prompt,
		InjectLastSummary:   injectSummary,
		NotifyOn:            in.NotifyOn,
		MaxConsecutiveFails: maxFails,
		CreatedAt:           nowMs,
		UpdatedAt:           nowMs,
	}
	if err := s.resolveBindings(&sch); err != nil {
		return Schedule{}, err
	}
	if err := normalizeScheduleFields(&sch); err != nil {
		return Schedule{}, err
	}
	if err := s.resolveSkillBinding(&sch); err != nil {
		return Schedule{}, err
	}
	if err := validateModelSelection(sch); err != nil {
		return Schedule{}, err
	}
	if sch.Enabled {
		next, err := NextFireTime(sch.Trigger, now)
		if err != nil {
			return Schedule{}, fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
		}
		sch.NextRunAt = next.UnixMilli()
	}

	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return Schedule{}, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	s.schedules[sch.ID] = cloneSchedule(sch)
	seq, doc := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, doc); err != nil {
		s.mu.Lock()
		delete(s.schedules, sch.ID)
		s.mu.Unlock()
		return Schedule{}, err
	}
	s.signalWakeup()
	if s.host != nil {
		s.host.BroadcastUpdated(sch.ID)
	}
	return sch, nil
}

// Update modifies an existing schedule atomically.
func (s *Service) Update(id string, patch PatchInput) (Schedule, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return Schedule{}, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	existing, ok := s.schedules[id]
	s.mu.Unlock()
	if !ok {
		return Schedule{}, ErrScheduleNotFound
	}

	updated := cloneSchedule(existing)
	if patch.Name != nil {
		updated.Name = *patch.Name
	}
	if patch.Enabled != nil {
		updated.Enabled = *patch.Enabled
		if updated.Enabled {
			updated.PausedReason = ""
			updated.ConsecutiveFails = 0
		}
	}
	if patch.ProjectID != nil {
		updated.ProjectID = *patch.ProjectID
	}
	if patch.Directory != nil {
		updated.Directory = *patch.Directory
	}
	if patch.SessionID != nil {
		updated.SessionID = *patch.SessionID
	}
	if patch.Trigger != nil {
		updated.Trigger = *patch.Trigger
	}
	if patch.Execution != nil {
		updated.Execution = *patch.Execution
	}
	if patch.ClearSkill {
		updated.SkillSelection = nil
	} else if patch.SkillSelection != nil {
		updated.SkillSelection = cloneSkillRef(patch.SkillSelection)
	}
	if patch.Prompt != nil {
		updated.Prompt = *patch.Prompt
	}
	if patch.InjectLastSummary != nil {
		updated.InjectLastSummary = *patch.InjectLastSummary
	}
	if patch.NotifyOn != nil {
		updated.NotifyOn = *patch.NotifyOn
	}
	if patch.MaxConsecutiveFails != nil {
		updated.MaxConsecutiveFails = *patch.MaxConsecutiveFails
	}

	if err := s.resolveBindings(&updated); err != nil {
		return Schedule{}, err
	}
	if err := normalizeScheduleFields(&updated); err != nil {
		return Schedule{}, err
	}
	if err := s.resolveSkillBinding(&updated); err != nil {
		return Schedule{}, err
	}
	if err := validateModelSelection(updated); err != nil {
		return Schedule{}, err
	}

	now := s.now()
	nowMs := now.UnixMilli()
	updated.UpdatedAt = nowMs
	if updated.Enabled {
		if existing.Enabled && existing.Trigger == updated.Trigger && existing.NextRunAt > nowMs {
			updated.NextRunAt = existing.NextRunAt
		} else {
			next, err := NextFireTime(updated.Trigger, now)
			if err != nil {
				return Schedule{}, fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
			}
			updated.NextRunAt = next.UnixMilli()
		}
	} else {
		updated.NextRunAt = 0
	}

	s.mu.Lock()
	cur, ok := s.schedules[id]
	if !ok {
		s.mu.Unlock()
		return Schedule{}, ErrScheduleNotFound
	}
	carryRuntimeState(&updated, existing, cur, patch)
	s.schedules[id] = cloneSchedule(updated)
	seq, doc := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, doc); err != nil {
		s.mu.Lock()
		s.schedules[id] = cur
		s.mu.Unlock()
		return Schedule{}, err
	}
	s.signalWakeup()
	if s.host != nil {
		s.host.BroadcastUpdated(updated.ID)
	}
	return updated, nil
}

// carryRuntimeState copies execution state that a run (finishRun, tickOnce)
// recorded while Update was validating outside the lock, so an edit never
// rolls back a run's outcome or an auto-pause the user did not override.
func carryRuntimeState(updated *Schedule, existing, cur Schedule, patch PatchInput) {
	updated.Running = cur.Running
	updated.LastRunAt = cur.LastRunAt
	updated.LastOutcome = cur.LastOutcome
	updated.LastSummary = cur.LastSummary
	updated.LastError = cur.LastError
	updated.LastSessionID = cur.LastSessionID
	updated.AlertUnseen = cur.AlertUnseen
	if patch.Enabled != nil {
		// An explicit enable/disable is the user's latest decision; enabling
		// already reset the failure count and pause reason.
		if !*patch.Enabled {
			updated.ConsecutiveFails = cur.ConsecutiveFails
		}
		return
	}
	updated.ConsecutiveFails = cur.ConsecutiveFails
	updated.PausedReason = cur.PausedReason
	if existing.Enabled && !cur.Enabled {
		updated.Enabled = false
		updated.NextRunAt = 0
	}
}

// Delete cancels any running execution and removes a schedule and its run history.
func (s *Service) Delete(id string) error {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	prev, ok := s.schedules[id]
	if !ok {
		s.mu.Unlock()
		return ErrScheduleNotFound
	}
	cancel := s.running[id]
	delete(s.schedules, id)
	delete(s.runsCache, id)
	seq, doc := s.snapshotLocked()
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if err := s.store.saveSnapshot(seq, doc); err != nil {
		s.mu.Lock()
		s.schedules[id] = prev
		s.mu.Unlock()
		return err
	}
	s.store.deleteRuns(id)
	s.signalWakeup()
	if s.host != nil {
		s.host.BroadcastUpdated(id)
	}
	return nil
}

// AcknowledgeAlert clears the unseen alert indicator for a schedule.
func (s *Service) AcknowledgeAlert(id string) (Schedule, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return Schedule{}, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	sch, ok := s.schedules[id]
	if !ok {
		s.mu.Unlock()
		return Schedule{}, ErrScheduleNotFound
	}
	if !sch.AlertUnseen {
		s.mu.Unlock()
		return cloneSchedule(sch), nil
	}
	sch.AlertUnseen = false
	sch.UpdatedAt = s.now().UnixMilli()
	s.schedules[id] = sch
	seq, doc := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, doc); err != nil {
		return Schedule{}, err
	}
	if s.host != nil {
		s.host.BroadcastUpdated(id)
	}
	return cloneSchedule(sch), nil
}

// RunNow triggers an immediate execution asynchronously and returns the pending RunRecord.
func (s *Service) RunNow(id string) (RunRecord, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return RunRecord{}, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}
	sch, ok := s.schedules[id]
	if !ok {
		s.mu.Unlock()
		return RunRecord{}, ErrScheduleNotFound
	}
	if sch.Running || s.running[id] != nil {
		s.mu.Unlock()
		return RunRecord{}, ErrScheduleBusy
	}
	s.mu.Unlock()

	runRec := s.launchExecution(context.Background(), id, "manual")
	if runRec == nil {
		return RunRecord{}, ErrScheduleBusy
	}
	return *runRec, nil
}

// StopRun cancels the currently active execution of schedule id.
func (s *Service) StopRun(id string) error {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	cancel, ok := s.running[id]
	s.mu.Unlock()
	if !ok || cancel == nil {
		return nil
	}
	cancel()
	return nil
}

// History returns bounded run records (newest first) for a schedule.
func (s *Service) History(id string) ([]RunRecord, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	if _, ok := s.schedules[id]; !ok {
		s.mu.Unlock()
		return nil, ErrScheduleNotFound
	}
	cached, ok := s.runsCache[id]
	s.mu.Unlock()
	if !ok {
		loaded := s.store.loadRuns(id)
		s.mu.Lock()
		if _, exists := s.schedules[id]; !exists {
			s.mu.Unlock()
			return nil, ErrScheduleNotFound
		}
		if cur, already := s.runsCache[id]; already {
			cached = cur
		} else {
			s.runsCache[id] = loaded
			cached = loaded
		}
		s.mu.Unlock()
	}
	out := make([]RunRecord, len(cached))
	copy(out, cached)
	return out, nil
}

// ForgetSession pauses any enabled schedule explicitly bound to a deleted session.
func (s *Service) ForgetSession(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if s == nil || sid == "" {
		return
	}
	nowMs := s.now().UnixMilli()
	var changedIDs []string
	var seq uint64
	var doc storeDoc

	s.mu.Lock()
	for id, sch := range s.schedules {
		dirty := false
		if sch.LastSessionID == sid {
			sch.LastSessionID = ""
			dirty = true
		}
		if sch.SessionID == sid {
			if sch.Enabled || sch.PausedReason == "" {
				sch.Enabled = false
				sch.NextRunAt = 0
				sch.PausedReason = "bound session was deleted"
				dirty = true
			}
		}
		if dirty {
			sch.UpdatedAt = nowMs
			s.schedules[id] = sch
			changedIDs = append(changedIDs, id)
		}
	}
	if len(changedIDs) > 0 {
		seq, doc = s.snapshotLocked()
	}
	s.mu.Unlock()

	if len(changedIDs) > 0 {
		_ = s.store.saveSnapshot(seq, doc)
		s.signalWakeup()
		if s.host != nil {
			for _, id := range changedIDs {
				s.host.BroadcastUpdated(id)
			}
		}
	}
}

// RelinkProject updates directories of schedules belonging to a relocated project folder.
func (s *Service) RelinkProject(projectID, oldPath, newPath string) {
	if s == nil {
		return
	}
	proj := strings.TrimSpace(projectID)
	oldClean := cleanCompareDir(oldPath)
	newTrim := strings.TrimSpace(newPath)
	if newTrim == "" {
		return
	}
	nowMs := s.now().UnixMilli()
	var changed []string
	var seq uint64
	var doc storeDoc

	s.mu.Lock()
	for id, sch := range s.schedules {
		matchProj := proj != "" && sch.ProjectID == proj
		matchPath := oldClean != "" && cleanCompareDir(sch.Directory) == oldClean
		if (matchProj && (oldClean == "" || matchPath)) || matchPath {
			sch.Directory = newTrim
			sch.UpdatedAt = nowMs
			s.schedules[id] = sch
			changed = append(changed, id)
		}
	}
	if len(changed) > 0 {
		seq, doc = s.snapshotLocked()
	}
	s.mu.Unlock()

	if len(changed) > 0 {
		_ = s.store.saveSnapshot(seq, doc)
		if s.host != nil {
			for _, id := range changed {
				s.host.BroadcastUpdated(id)
			}
		}
	}
}

func (s *Service) resolveBindings(sch *Schedule) error {
	if s.host == nil {
		if strings.TrimSpace(sch.Directory) == "" {
			return fmt.Errorf("%w: directory is required", ErrInvalidSchedule)
		}
		return nil
	}
	if sid := strings.TrimSpace(sch.SessionID); sid != "" {
		exists, sessDir := s.host.SessionExists(sid)
		if !exists {
			return ErrBoundSessionMissing
		}
		if strings.TrimSpace(sch.Directory) == "" && strings.TrimSpace(sessDir) != "" {
			sch.Directory = sessDir
		}
	}
	projID, dir, err := s.host.ResolveWorkspaceDirectory(sch.ProjectID, sch.Directory)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
	}
	sch.ProjectID = projID
	sch.Directory = dir
	return nil
}

func (s *Service) resolveSkillBinding(sch *Schedule) error {
	if sch.SkillSelection == nil {
		return nil
	}
	if resolver, ok := s.host.(SkillResolver); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resolved, err := resolver.ResolveSkill(ctx, sch.Directory, sch.SessionID, sch.Execution.Runtime, *sch.SkillSelection)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
		}
		sch.SkillSelection = &resolved
		return nil
	}
	if strings.TrimSpace(sch.SkillSelection.Name) == "" {
		return fmt.Errorf("%w: skillSelection.name is required", ErrInvalidSchedule)
	}
	return nil
}

func validateModelSelection(sch Schedule) error {
	// Guardrail #6: background LLM execution never silently falls back to an
	// unchosen provider. Require an explicit provider/model for native runs
	// unless bound to an existing session that already owns its model config.
	if strings.TrimSpace(sch.SessionID) != "" {
		return nil
	}
	if sch.Execution.Runtime != "native" {
		return nil
	}
	if sch.Execution.ProviderID == "" || sch.Execution.ModelID == "" ||
		sch.Execution.ProviderID == "auto" ||
		sch.Execution.ProviderID == "hoxi-auto" ||
		sch.Execution.ProviderID == "hoxi-orchestrator" {
		return ErrModelRequired
	}
	return nil
}

func cleanCompareDir(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return ""
	}
	return filepath.ToSlash(strings.ToLower(filepath.Clean(trimmed)))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
