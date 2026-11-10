package orchestration

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type resumableFakeSessions struct {
	*fakeSessions
	mu         sync.Mutex
	resumedIDs []string
	blockID    string
	blocked    chan struct{}
	release    chan struct{}
}

func newResumableFakeSessions(isolate bool, blockTaskPrompt string) *resumableFakeSessions {
	return &resumableFakeSessions{
		fakeSessions: newFakeSessions(isolate),
		blockID:      blockTaskPrompt,
		blocked:      make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (r *resumableFakeSessions) Run(ctx context.Context, sessionID, prompt string, req PlanRequest) (string, error) {
	r.mu.Lock()
	shouldBlock := r.blockID != "" && strings.Contains(prompt, r.blockID)
	r.mu.Unlock()
	if shouldBlock {
		r.fakeSessions.mu.Lock()
		r.fakeSessions.prompts[sessionID] = prompt
		r.fakeSessions.running++
		r.fakeSessions.mu.Unlock()
		defer func() {
			r.fakeSessions.mu.Lock()
			r.fakeSessions.running--
			r.fakeSessions.mu.Unlock()
		}()
		select {
		case <-r.blocked:
		default:
			close(r.blocked)
		}
		select {
		case <-r.release:
			return "finished " + sessionID, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return r.fakeSessions.Run(ctx, sessionID, prompt, req)
}

func (r *resumableFakeSessions) Resume(ctx context.Context, sessionID, prompt string, req PlanRequest) (string, error) {
	r.mu.Lock()
	r.resumedIDs = append(r.resumedIDs, sessionID)
	r.mu.Unlock()
	return r.fakeSessions.Run(ctx, sessionID, prompt, req)
}

func TestGracefulRestartResumesConvoyDAGWithoutRerunningOrDoubleMerging(t *testing.T) {
	workspace := t.TempDir()
	store := NewStore(filepath.Join(t.TempDir(), "plans"))

	sessions1 := newResumableFakeSessions(true, "beta work")
	svc1 := NewService(sessions1, WithStore(store))

	plan, err := svc1.Create(PlanRequest{
		Directory:       workspace,
		ParentSessionID: "ses_parent_root",
		Concurrency:     2,
		Tasks: []TaskSpec{
			{ID: "a", Prompt: "alpha work"},
			{ID: "b", Prompt: "beta work", DependsOn: []string{"a"}},
			{ID: "c", Prompt: "gamma work", DependsOn: []string{"b"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait until "a" succeeded, merged into "b", and "b" is mid-run.
	select {
	case <-sessions1.blocked:
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for task b to block mid-flight")
	}

	midPlan, ok := svc1.Get(plan.ID)
	if !ok {
		t.Fatal("plan missing before restart")
	}
	aSession := taskByID(midPlan, "a").SessionID
	bSession := taskByID(midPlan, "b").SessionID
	if aSession == "" || bSession == "" {
		t.Fatalf("expected sessions assigned for a (%q) and b (%q)", aSession, bSession)
	}
	if !taskByID(midPlan, "b").PrerequisitesMerged {
		t.Fatal("expected task b to record PrerequisitesMerged=true before running")
	}

	// Checkpoint for graceful restart and release old blocked goroutine.
	svc1.PrepareForRestart()
	close(sessions1.release)

	// Boot new Service instance and resume checkpointed plans.
	sessions2 := newResumableFakeSessions(true, "")
	sessions2.nextID = 100
	svc2 := NewService(sessions2, WithStore(store))
	restoredBeforeResume, ok := svc2.Get(plan.ID)
	if !ok || restoredBeforeResume.Status != StatusRunning || !restoredBeforeResume.RestartPending {
		t.Fatalf("expected restored plan waiting for ResumeAfterRestart, got %#v", restoredBeforeResume)
	}

	svc2.ResumeAfterRestart(context.Background())
	final := waitTerminal(t, svc2, plan.ID)
	if final.Status != StatusSucceeded {
		t.Fatalf("resumed plan status = %s (err=%q), want succeeded", final.Status, final.Error)
	}

	// Verify:
	// 1. Task "a" kept its original session and was NOT re-run.
	// 2. Task "b" kept its original session, invoked Resume(), and did NOT re-merge "a".
	// 3. Task "c" got a new session after restart, merged "b", and succeeded.
	if got := taskByID(final, "a").SessionID; got != aSession {
		t.Fatalf("task a session changed: got %q, want %q", got, aSession)
	}
	if got := taskByID(final, "b").SessionID; got != bSession {
		t.Fatalf("task b session changed: got %q, want %q", got, bSession)
	}
	cSession := taskByID(final, "c").SessionID
	if cSession == "" || cSession == aSession || cSession == bSession {
		t.Fatalf("unexpected task c session: %q", cSession)
	}

	sessions2.mu.Lock()
	resumed := append([]string(nil), sessions2.resumedIDs...)
	sessions2.mu.Unlock()
	if len(resumed) != 1 || resumed[0] != bSession {
		t.Fatalf("expected Resume called once for %q, got %v", bSession, resumed)
	}

	sessions2.fakeSessions.mu.Lock()
	defer sessions2.fakeSessions.mu.Unlock()
	if len(sessions2.created) != 1 {
		t.Fatalf("expected only 1 new session created after restart (for task c), got %d", len(sessions2.created))
	}
	if _, mergedAgain := sessions2.merges[bSession]; mergedAgain {
		t.Fatalf("task b must not double-merge prerequisites after restart: %v", sessions2.merges[bSession])
	}
	if got := strings.Join(sessions2.merges[cSession], ","); got != bSession {
		t.Fatalf("expected task c to merge [%s], got %v", bSession, sessions2.merges[cSession])
	}
}

func TestConvoyRestartRecoveryBudgetAndExpiryFailClosed(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "plans"))
	now := time.Now().UnixMilli()

	overBudget := Plan{
		ID:                "plan_over_budget",
		Title:             "Over Budget",
		Directory:         t.TempDir(),
		Status:            StatusRunning,
		RestartPending:    true,
		RestartRecoveries: maxConvoyRestartRecoveries,
		CreatedAt:         now,
		UpdatedAt:         now,
		Tasks: []TaskState{
			{ID: "a", Prompt: "work", Status: StatusRunning, SessionID: "ses_1"},
		},
	}
	expired := Plan{
		ID:                "plan_expired",
		Title:             "Expired",
		Directory:         t.TempDir(),
		Status:            StatusRunning,
		RestartPending:    true,
		RestartRecoveries: 0,
		CreatedAt:         now - 2*time.Hour.Milliseconds(),
		UpdatedAt:         now - 2*time.Hour.Milliseconds(),
		Tasks: []TaskState{
			{ID: "a", Prompt: "work", Status: StatusQueued},
		},
	}
	if err := store.Save(overBudget); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(expired); err != nil {
		t.Fatal(err)
	}

	svc := NewService(newFakeSessions(true), WithStore(store))
	svc.ResumeAfterRestart(context.Background())

	for _, id := range []string{overBudget.ID, expired.ID} {
		got, ok := svc.Get(id)
		if !ok {
			t.Fatalf("plan %s missing", id)
		}
		if got.Status != StatusFailed || got.RestartPending {
			t.Fatalf("plan %s should fail closed, got %#v", id, got)
		}
	}
}
