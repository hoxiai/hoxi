package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSessions records what the scheduler asks of the session plane.
type fakeSessions struct {
	isolate bool

	mu       sync.Mutex
	nextID   int
	created  []SessionRequest
	prompts  map[string]string
	merges   map[string][]string
	aborted  []string
	running  int
	peak     int
	block    chan struct{}
	mergeErr error
}

func newFakeSessions(isolate bool) *fakeSessions {
	return &fakeSessions{isolate: isolate, prompts: map[string]string{}, merges: map[string][]string{}}
}

func (f *fakeSessions) CanIsolate(string) bool { return f.isolate }

// blockAll makes Run block until the returned function is called. Calling the
// returned function more than once is safe.
func (f *fakeSessions) blockAll() func() {
	block := make(chan struct{})
	f.mu.Lock()
	f.block = block
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			if f.block == block {
				f.block = nil
			}
			f.mu.Unlock()
			close(block)
		})
	}
}

func (f *fakeSessions) Create(_ context.Context, req SessionRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.created = append(f.created, req)
	return fmt.Sprintf("ses_hoxi_%d", f.nextID), nil
}

func (f *fakeSessions) Run(ctx context.Context, sessionID, prompt string, _ PlanRequest) (string, error) {
	f.mu.Lock()
	f.prompts[sessionID] = prompt
	f.running++
	if f.running > f.peak {
		f.peak = f.running
	}
	block := f.block
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	} else {
		time.Sleep(20 * time.Millisecond)
	}
	return "finished " + sessionID, nil
}

func (f *fakeSessions) Merge(target string, sources []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.merges[target] = append([]string(nil), sources...)
	return f.mergeErr
}

func (f *fakeSessions) Abort(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted = append(f.aborted, sessionID)
}

func waitTerminal(t *testing.T, svc *Service, id string) Plan {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if plan, ok := svc.Get(id); ok && terminal(plan.Status) {
			return plan
		}
		time.Sleep(10 * time.Millisecond)
	}
	plan, _ := svc.Get(id)
	t.Fatalf("plan did not finish: %#v", plan)
	return plan
}

func taskByID(plan Plan, id string) TaskState {
	for _, task := range plan.Tasks {
		if task.ID == id {
			return task
		}
	}
	return TaskState{}
}

// waitPersistedStatus blocks until the store holds the plan at status. Terminal
// status becomes observable in memory just before the finishing goroutine
// persists the snapshot, so a restart test that reads the file immediately can
// otherwise load a stale "running" plan and report a false interruption.
func waitPersistedStatus(t *testing.T, store *Store, id string, status string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if plans, err := store.Load(); err == nil {
			for _, plan := range plans {
				if plan.ID == id && plan.Status == status {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("plan %s was not persisted as %s", id, status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type flakySessions struct {
	*fakeSessions
	mu       sync.Mutex
	failures int
}

func (f *flakySessions) Run(ctx context.Context, sessionID, prompt string, req PlanRequest) (string, error) {
	f.mu.Lock()
	if f.failures > 0 && strings.Contains(prompt, "flaky work") {
		f.failures--
		f.mu.Unlock()
		return "", errors.New("flaky failure")
	}
	f.mu.Unlock()
	return f.fakeSessions.Run(ctx, sessionID, prompt, req)
}

func TestPlanPersistsAcrossServiceRestart(t *testing.T) {
	workspace := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "plans"))
	svc := NewService(newFakeSessions(true), WithStore(store))
	plan, err := svc.Create(PlanRequest{
		Directory: workspace,
		Mode:      "build",
		Tasks:     []TaskSpec{{ID: "a", Prompt: "alpha work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitTerminal(t, svc, plan.ID)
	if done.Status != StatusSucceeded {
		t.Fatalf("plan status = %s, want succeeded", done.Status)
	}
	waitPersistedStatus(t, store, plan.ID, StatusSucceeded)

	restarted := NewService(newFakeSessions(true), WithStore(store))
	restored, ok := restarted.Get(plan.ID)
	if !ok {
		t.Fatal("persisted plan was not restored")
	}
	if restored.Status != StatusSucceeded || restored.Mode != "build" {
		t.Fatalf("restored plan = %#v", restored)
	}
	if task := taskByID(restored, "a"); task.Status != StatusSucceeded || task.SessionID != done.Tasks[0].SessionID {
		t.Fatalf("restored task = %#v", task)
	}
}

func TestRestoreMarksInterruptedTasksFailed(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "plans"))
	now := time.Now().UnixMilli()
	plan := Plan{
		ID: "plan_interrupted_test", Title: "Interrupted", Directory: t.TempDir(),
		Status: StatusRunning, CreatedAt: now, UpdatedAt: now,
		Tasks: []TaskState{
			{ID: "done", Prompt: "done work", Status: StatusSucceeded, SessionID: "ses_hoxi_done"},
			{ID: "live", Prompt: "live work", Status: StatusRunning, SessionID: "ses_hoxi_live"},
		},
	}
	if err := store.Save(plan); err != nil {
		t.Fatal(err)
	}
	svc := NewService(newFakeSessions(true), WithStore(store))
	restored, ok := svc.Get(plan.ID)
	if !ok {
		t.Fatal("plan was not restored")
	}
	if restored.Status != StatusFailed || restored.Error == "" {
		t.Fatalf("restored plan = %#v", restored)
	}
	if task := taskByID(restored, "done"); task.Status != StatusSucceeded {
		t.Fatalf("completed task was rewritten: %#v", task)
	}
	live := taskByID(restored, "live")
	if live.Status != StatusFailed || live.Error == "" || live.SessionID != "ses_hoxi_live" {
		t.Fatalf("interrupted task = %#v", live)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].Status != StatusFailed {
		t.Fatalf("interrupted state was not persisted: %#v", persisted)
	}
}

func TestListBySessionFindsOwningPlan(t *testing.T) {
	svc := NewService(newFakeSessions(true))
	plan, err := svc.Create(PlanRequest{
		Directory: t.TempDir(),
		Tasks:     []TaskSpec{{ID: "a", Prompt: "alpha work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitTerminal(t, svc, plan.ID)
	sessionID := taskByID(done, "a").SessionID
	found := svc.ListBySession(sessionID)
	if len(found) != 1 || found[0].ID != plan.ID {
		t.Fatalf("ListBySession(%q) = %#v", sessionID, found)
	}
	if other := svc.ListBySession("ses_unknown"); len(other) != 0 {
		t.Fatalf("unknown session returned %#v", other)
	}
}

func TestRetryRerunsOnlyUnfinishedTasks(t *testing.T) {
	fake := newFakeSessions(true)
	flaky := &flakySessions{fakeSessions: fake, failures: 1}
	svc := NewService(flaky)
	plan, err := svc.Create(PlanRequest{
		Directory:   t.TempDir(),
		Concurrency: 2,
		Tasks: []TaskSpec{
			{ID: "stable", Prompt: "stable work"},
			{ID: "flaky", Prompt: "flaky work"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := waitTerminal(t, svc, plan.ID)
	if first.Status != StatusFailed {
		t.Fatalf("first run status = %s, want failed", first.Status)
	}
	stableSession := taskByID(first, "stable").SessionID
	if task := taskByID(first, "flaky"); task.Status != StatusFailed || task.SessionID == "" {
		t.Fatalf("flaky task = %#v", task)
	}
	createdBefore := len(fake.created)

	retried, err := svc.Retry(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task := taskByID(retried, "stable"); task.Status != StatusSucceeded || task.SessionID != stableSession {
		t.Fatalf("succeeded task was reset: %#v", task)
	}
	if task := taskByID(retried, "flaky"); task.Status != StatusQueued || task.Error != "" || task.SessionID != "" {
		t.Fatalf("failed task was not reset cleanly: %#v", task)
	}

	final := waitTerminal(t, svc, plan.ID)
	if final.Status != StatusSucceeded {
		t.Fatalf("retried plan status = %s, want succeeded", final.Status)
	}
	if task := taskByID(final, "stable"); task.SessionID != stableSession {
		t.Fatalf("succeeded task was rerun: %#v", task)
	}
	if len(fake.created) != createdBefore+1 {
		t.Fatalf("sessions created = %d, want %d", len(fake.created), createdBefore+1)
	}
}

func TestRetryRejectsActivePlan(t *testing.T) {
	fake := newFakeSessions(false)
	fake.block = make(chan struct{})
	svc := NewService(fake)
	plan, err := svc.Create(PlanRequest{
		Directory: t.TempDir(),
		Tasks:     []TaskSpec{{ID: "a", Prompt: "alpha work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if current, ok := svc.Get(plan.ID); ok && taskByID(current, "a").Status == StatusRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := svc.Retry(plan.ID); !errors.Is(err, ErrPlanActive) {
		t.Fatalf("retry error = %v, want ErrPlanActive", err)
	}
	_, _ = svc.Cancel(plan.ID)
	close(fake.block)
	waitTerminal(t, svc, plan.ID)
}

func TestIsolatedPlanMergesPrerequisitesIntoDependents(t *testing.T) {
	fake := newFakeSessions(true)
	svc := NewService(fake)
	plan, err := svc.Create(PlanRequest{
		Directory:   t.TempDir(),
		Concurrency: 2,
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha work"},
			{ID: "b", Prompt: "beta work", DependsOn: []string{"a"}},
			{ID: "c", Prompt: "gamma work"},
			{ID: "d", Prompt: "delta work", DependsOn: []string{"b", "c"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Isolated {
		t.Fatal("plan in a git repository should be isolated")
	}
	plan = waitTerminal(t, svc, plan.ID)
	if plan.Status != StatusSucceeded {
		t.Fatalf("plan did not succeed: %#v", plan)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, req := range fake.created {
		if !req.Isolated {
			t.Fatalf("task session not isolated: %#v", req)
		}
	}
	a, b, c, d := taskByID(plan, "a"), taskByID(plan, "b"), taskByID(plan, "c"), taskByID(plan, "d")
	if got := fake.merges[b.SessionID]; strings.Join(got, ",") != a.SessionID {
		t.Fatalf("b merged %v, want [%s]", got, a.SessionID)
	}
	if got := fake.merges[d.SessionID]; strings.Join(got, ",") != b.SessionID+","+c.SessionID {
		t.Fatalf("d merged %v, want [%s %s]", got, b.SessionID, c.SessionID)
	}
	if _, merged := fake.merges[a.SessionID]; merged {
		t.Fatal("a task without prerequisites must not merge")
	}
	beta := fake.prompts[b.SessionID]
	if !strings.Contains(beta, "Completed prerequisites: a") || !strings.Contains(beta, "Do not commit") || !strings.Contains(beta, "beta work") {
		t.Fatalf("dependent prompt missing context: %q", beta)
	}
	if b.Summary != "finished "+b.SessionID {
		t.Fatalf("summary = %q", b.Summary)
	}
}

func TestSharedDirectoryPlanRunsOneTaskAtATime(t *testing.T) {
	fake := newFakeSessions(false)
	svc := NewService(fake)
	plan, err := svc.Create(PlanRequest{
		Directory:   t.TempDir(),
		Concurrency: 3,
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha"},
			{ID: "b", Prompt: "beta"},
			{ID: "c", Prompt: "gamma", DependsOn: []string{"a"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Isolated || plan.Concurrency != 1 {
		t.Fatalf("non-git plan should be serial and shared: isolated=%v concurrency=%d", plan.Isolated, plan.Concurrency)
	}
	plan = waitTerminal(t, svc, plan.ID)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if plan.Status != StatusSucceeded || fake.peak != 1 {
		t.Fatalf("status=%s peak concurrency=%d", plan.Status, fake.peak)
	}
	if len(fake.merges) != 0 {
		t.Fatalf("shared-directory plans must not merge: %v", fake.merges)
	}
}

func TestMergeConflictFailsTaskAndBlocksDependents(t *testing.T) {
	fake := newFakeSessions(true)
	fake.mergeErr = errors.New("merge conflict: f.txt")
	svc := NewService(fake)
	plan, err := svc.Create(PlanRequest{
		Directory:   t.TempDir(),
		Concurrency: 1,
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha"},
			{ID: "b", Prompt: "beta", DependsOn: []string{"a"}},
			{ID: "c", Prompt: "gamma", DependsOn: []string{"b"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan = waitTerminal(t, svc, plan.ID)
	b, c := taskByID(plan, "b"), taskByID(plan, "c")
	if plan.Status != StatusFailed || b.Status != StatusFailed || !strings.Contains(b.Error, "merge conflict") {
		t.Fatalf("expected b to fail on merge conflict: plan=%s b=%#v", plan.Status, b)
	}
	if c.Status != StatusBlocked {
		t.Fatalf("dependent of a failed merge should be blocked: %#v", c)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ran := fake.prompts[b.SessionID]; ran {
		t.Fatal("a task whose merge failed must not run")
	}
}

func TestCancelAbortsRunningSessionsAndQueuedTasks(t *testing.T) {
	fake := newFakeSessions(true)
	fake.block = make(chan struct{})
	t.Cleanup(func() { close(fake.block) })
	svc := NewService(fake)
	plan, err := svc.Create(PlanRequest{
		Directory:   t.TempDir(),
		Concurrency: 1,
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha"},
			{ID: "b", Prompt: "beta", DependsOn: []string{"a"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if current, _ := svc.Get(plan.ID); taskByID(current, "a").SessionID != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancelled, err := svc.Cancel(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != StatusCancelled || taskByID(cancelled, "b").Status != StatusCancelled {
		t.Fatalf("cancel did not mark the plan and queued task: %#v", cancelled)
	}
	runningID := taskByID(cancelled, "a").SessionID
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		aborted := strings.Join(fake.aborted, ",")
		fake.mu.Unlock()
		if strings.Contains(aborted, runningID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("running session %s was not aborted", runningID)
}

func TestCreateWithoutSessionBackendFails(t *testing.T) {
	if _, err := NewService(nil).Create(PlanRequest{Directory: t.TempDir(), Tasks: []TaskSpec{{ID: "a", Prompt: "x"}}}); err == nil {
		t.Fatal("expected an error without a session backend")
	}
}

func TestRetryPersistenceFailureRestoresPreviousTaskState(t *testing.T) {
	fake := newFakeSessions(true)
	flaky := &flakySessions{fakeSessions: fake, failures: 1}
	storeDir := filepath.Join(t.TempDir(), "plans")
	store := NewStore(storeDir)
	svc := NewService(flaky, WithStore(store))
	plan, err := svc.Create(PlanRequest{
		Directory:   t.TempDir(),
		Concurrency: 1,
		Tasks: []TaskSpec{
			{ID: "flaky", Prompt: "flaky work"},
			{ID: "dep", Prompt: "dependent work", DependsOn: []string{"flaky"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitTerminal(t, svc, plan.ID)
	if failed.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", failed.Status)
	}
	oldSession := taskByID(failed, "flaky").SessionID
	if oldSession == "" {
		t.Fatal("expected failed task to have a session ID")
	}
	// Let the finishing persist land before mutating the store directory, or a
	// late snapshot can recreate it after RemoveAll and fail the next WriteFile.
	waitPersistedStatus(t, store, plan.ID, StatusFailed)

	// Replace the store directory with a plain file so persisting the retry fails.
	if err := os.RemoveAll(storeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storeDir, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Retry(plan.ID); err == nil {
		t.Fatal("expected Retry to fail when persistence fails")
	}
	after, ok := svc.Get(plan.ID)
	if !ok {
		t.Fatal("plan missing after failed retry")
	}
	if after.Status != StatusFailed {
		t.Fatalf("plan status after failed retry = %s, want %s", after.Status, StatusFailed)
	}
	flakyTask := taskByID(after, "flaky")
	if flakyTask.Status != StatusFailed || flakyTask.SessionID != oldSession || flakyTask.Error == "" {
		t.Fatalf("failed task state was not restored after retry persist error: %#v", flakyTask)
	}
	depTask := taskByID(after, "dep")
	if depTask.Status != StatusBlocked {
		t.Fatalf("blocked task state was left as %s, want %s", depTask.Status, StatusBlocked)
	}
}

// Two convoys started on the same non-isolated folder must not run at the same
// time: they share one working tree, so concurrent tasks would edit the same
// files. Serializing per plan alone is not enough.
func TestPlansSharingDirectoryRunOneTaskAtATime(t *testing.T) {
	fake := newFakeSessions(false)
	unblock := fake.blockAll()
	defer unblock()
	svc := NewService(fake)
	directory := t.TempDir()

	var wg sync.WaitGroup
	plans := make([]Plan, 0, 2)
	var mu sync.Mutex
	for _, title := range []string{"first", "second"} {
		wg.Add(1)
		go func(title string) {
			defer wg.Done()
			plan, err := svc.Create(PlanRequest{
				Directory: directory,
				Title:     title,
				Tasks:     []TaskSpec{{ID: "a", Prompt: title + " work"}},
			})
			if err != nil {
				t.Errorf("create %s: %v", title, err)
				return
			}
			mu.Lock()
			plans = append(plans, plan)
			mu.Unlock()
		}(title)
	}
	wg.Wait()
	if len(plans) != 2 {
		t.Fatalf("created %d plans, want 2", len(plans))
	}

	// Wait for the first task to start holding the directory, then confirm no
	// second task from the other plan can join it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		running := fake.running
		fake.mu.Unlock()
		if running == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		fake.mu.Lock()
		running, peak := fake.running, fake.peak
		fake.mu.Unlock()
		if running != 1 || peak != 1 {
			t.Fatalf("peak concurrency across plans sharing a directory = %d (running %d), want 1", peak, running)
		}
		time.Sleep(10 * time.Millisecond)
	}

	unblock()
	for _, plan := range plans {
		done := waitTerminal(t, svc, plan.ID)
		if done.Status != StatusSucceeded {
			t.Fatalf("plan %s finished as %s", done.ID, done.Status)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.peak != 1 {
		t.Fatalf("peak concurrency = %d, want 1", fake.peak)
	}
}

// A cancelled waiter must not keep the directory locked for later plans.
func TestCancelledPlanReleasesSharedDirectoryLock(t *testing.T) {
	fake := newFakeSessions(false)
	unblock := fake.blockAll()
	defer unblock()
	svc := NewService(fake)
	directory := t.TempDir()

	first, err := svc.Create(PlanRequest{Directory: directory, Tasks: []TaskSpec{{ID: "a", Prompt: "hold"}}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		running := fake.running
		fake.mu.Unlock()
		if running == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	second, err := svc.Create(PlanRequest{Directory: directory, Tasks: []TaskSpec{{ID: "a", Prompt: "wait"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(second.ID); err != nil {
		t.Fatal(err)
	}
	unblock()

	waitTerminal(t, svc, first.ID)
	third, err := svc.Create(PlanRequest{Directory: directory, Tasks: []TaskSpec{{ID: "a", Prompt: "after"}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan := waitTerminal(t, svc, third.ID); plan.Status != StatusSucceeded {
		t.Fatalf("plan after a cancelled waiter = %s", plan.Status)
	}
}

func TestPlanParentSessionAndMergeLeafTasks(t *testing.T) {
	fake := newFakeSessions(true)
	svc := NewService(fake)
	plan, err := svc.Create(PlanRequest{
		Directory:       t.TempDir(),
		ParentSessionID: "ses_parent_chat",
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha"},
			{ID: "b", Prompt: "beta", DependsOn: []string{"a"}},
			{ID: "c", Prompt: "gamma"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitTerminal(t, svc, plan.ID)
	if done.ParentSessionID != "ses_parent_chat" {
		t.Fatalf("ParentSessionID = %q, want ses_parent_chat", done.ParentSessionID)
	}
	if byParent := svc.ListBySession("ses_parent_chat"); len(byParent) != 1 || byParent[0].ID != plan.ID {
		t.Fatalf("ListBySession(ses_parent_chat) = %#v", byParent)
	}
	if _, err := svc.Merge(plan.ID, "ses_wrong_target"); !errors.Is(err, ErrTargetSessionMismatch) {
		t.Fatalf("Merge with mismatched target session = %v, want ErrTargetSessionMismatch", err)
	}
	merged, err := svc.Merge(plan.ID, "")
	if err != nil {
		t.Fatalf("Merge into parent session: %v", err)
	}
	if merged.MergedAt == 0 {
		t.Fatal("expected MergedAt to be set after Merge")
	}
	fake.mu.Lock()
	gotSources := fake.merges["ses_parent_chat"]
	fake.mu.Unlock()
	bID := taskByID(done, "b").SessionID
	cID := taskByID(done, "c").SessionID
	if len(gotSources) != 2 || gotSources[0] != bID || gotSources[1] != cID {
		t.Fatalf("merged leaf sources = %v, want [%s %s]", gotSources, bID, cID)
	}
}

func TestMergeRejectsNonIsolatedOrFailedPlan(t *testing.T) {
	sharedSvc := NewService(newFakeSessions(false))
	sharedPlan, err := sharedSvc.Create(PlanRequest{
		Directory:       t.TempDir(),
		ParentSessionID: "ses_parent",
		Tasks:           []TaskSpec{{ID: "a", Prompt: "shared work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, sharedSvc, sharedPlan.ID)
	if _, err := sharedSvc.Merge(sharedPlan.ID, "ses_parent"); !errors.Is(err, ErrPlanNotIsolated) {
		t.Fatalf("Merge non-isolated plan = %v, want ErrPlanNotIsolated", err)
	}
}
