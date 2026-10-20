package orchestration

import (
	"context"
	"errors"
	"fmt"
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
