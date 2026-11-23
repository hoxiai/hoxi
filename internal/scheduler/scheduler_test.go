package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHost struct {
	mu             sync.Mutex
	sessions       map[string]string // sessionID -> dir
	busy           map[string]bool
	created        []CreateSessionInput
	turns          []RunTurnInput
	turnReply      func(ctx context.Context, sessionID string, req RunTurnInput) (string, error)
	notifications  []string
	broadcastCount int
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		sessions: make(map[string]string),
		busy:     make(map[string]bool),
	}
}

func (f *fakeHost) ResolveWorkspaceDirectory(projectID, directory string) (string, string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", "", errors.New("directory required")
	}
	if projectID == "" {
		projectID = "proj_test"
	}
	return projectID, filepath.Clean(directory), nil
}

func (f *fakeHost) SessionExists(sessionID string) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir, ok := f.sessions[sessionID]
	return ok, dir
}

func (f *fakeHost) IsSessionBusy(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[sessionID]
}

func (f *fakeHost) CreateSession(_ context.Context, req CreateSessionInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, req)
	id := newRandomID("ses_")
	f.sessions[id] = req.Directory
	return id, nil
}

func (f *fakeHost) RunTurn(ctx context.Context, sessionID string, req RunTurnInput) (string, error) {
	f.mu.Lock()
	f.turns = append(f.turns, req)
	fn := f.turnReply
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, sessionID, req)
	}
	return "All checks green.\n[[schedule:ok]] All checks passed.", nil
}

func (f *fakeHost) AbortSession(string) {}

func (f *fakeHost) Notify(title, body, sessionID string) {
	f.mu.Lock()
	f.notifications = append(f.notifications, title+"|"+body+"|"+sessionID)
	f.mu.Unlock()
}

func (f *fakeHost) BroadcastUpdated(string) {
	f.mu.Lock()
	f.broadcastCount++
	f.mu.Unlock()
}

func waitForRunDone(t *testing.T, svc *Service, id string) Schedule {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sch, ok := svc.Get(id)
		if ok && !sch.Running && sch.LastOutcome != "" {
			return sch
		}
		time.Sleep(15 * time.Millisecond)
	}
	sch, _ := svc.Get(id)
	t.Fatalf("timed out waiting for schedule %s to finish (running=%v, lastOutcome=%q)", id, sch.Running, sch.LastOutcome)
	return sch
}

func TestCronParsingAndNextFireTime(t *testing.T) {
	base := time.Date(2026, 11, 23, 8, 30, 0, 0, time.UTC) // Monday
	next, err := NextFireTime(TriggerSpec{Kind: TriggerKindCron, Cron: "0 9 * * 1-5", Timezone: "UTC"}, base)
	if err != nil {
		t.Fatalf("NextFireTime failed: %v", err)
	}
	want := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("got %v, want %v", next, want)
	}

	// Sunday alias 7 and day name
	sunNext, err := NextFireTime(TriggerSpec{Kind: TriggerKindCron, Cron: "15 10 * * 7", Timezone: "UTC"}, base)
	if err != nil {
		t.Fatalf("Sunday cron failed: %v", err)
	}
	if sunNext.Weekday() != time.Sunday || sunNext.Hour() != 10 || sunNext.Minute() != 15 {
		t.Fatalf("unexpected sunday match: %v", sunNext)
	}

	// Reject impossible date (Feb 31)
	if err := ValidateTrigger(TriggerSpec{Kind: TriggerKindCron, Cron: "0 9 31 2 *"}); err == nil {
		t.Fatal("expected Feb 31 cron to fail validation")
	}
	// Reject interval < 5m
	if err := ValidateTrigger(TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 2}); err == nil {
		t.Fatal("expected interval < 5m to fail validation")
	}
}

func TestFreshSessionRunAndLastSummaryInjection(t *testing.T) {
	tmp := t.TempDir()
	host := newFakeHost()
	calls := 0
	host.turnReply = func(_ context.Context, _ string, _ RunTurnInput) (string, error) {
		calls++
		if calls == 1 {
			return "Found 2 failing checks.\n[[schedule:alert]] 2 PR checks failed on main", nil
		}
		return "Recovered.\n[[schedule:ok]] All PR checks green", nil
	}
	svc := NewService(filepath.Join(tmp, "schedules.json"), filepath.Join(tmp, "runs"), host)
	defer svc.Stop()

	// Guardrail: native unbound schedule without explicit model or with virtual provider must fail.
	_, err := svc.Create(UpsertInput{
		Name:      "No Model",
		Directory: tmp,
		Trigger:   TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 30},
		Prompt:    "Check repo",
	})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("expected ErrModelRequired, got %v", err)
	}
	for _, virt := range []string{"auto", "hoxi-auto", "hoxi-orchestrator"} {
		_, err = svc.Create(UpsertInput{
			Name:      "Virtual Provider",
			Directory: tmp,
			Trigger:   TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 30},
			Execution: ExecutionSpec{ProviderID: virt, ModelID: "auto"},
			Prompt:    "Check repo",
		})
		if !errors.Is(err, ErrModelRequired) {
			t.Fatalf("expected ErrModelRequired for virtual provider %q, got %v", virt, err)
		}
	}

	created, err := svc.Create(UpsertInput{
		Name:      "PR Patrol",
		Directory: tmp,
		Trigger:   TriggerSpec{Kind: TriggerKindCron, Cron: "0 9 * * 1-5"},
		Execution: ExecutionSpec{
			Mode:        "plan",
			SandboxMode: "local",
			Runtime:     "native",
			ProviderID:  "anthropic",
			ModelID:     "claude-sonnet-4-5",
		},
		SkillSelection: &SkillRef{Key: "pr-patrol", Name: "pr-patrol"},
		Prompt:         "Check failing PR checks",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if created.Execution.SandboxMode != "local" || created.Execution.CommandSandbox {
		t.Fatalf("expected default non-sandbox local session, got %+v", created.Execution)
	}

	if _, err := svc.RunNow(created.ID); err != nil {
		t.Fatalf("RunNow #1 failed: %v", err)
	}
	after1 := waitForRunDone(t, svc, created.ID)
	if after1.LastOutcome != StatusAlert || !after1.AlertUnseen {
		t.Fatalf("expected alert with unseen badge, got %+v", after1)
	}
	host.mu.Lock()
	notifCount := len(host.notifications)
	host.mu.Unlock()
	if notifCount != 1 {
		t.Fatalf("expected 1 desktop notification on alert, got %d", notifCount)
	}

	// Acknowledge alert
	acked, err := svc.AcknowledgeAlert(created.ID)
	if err != nil || acked.AlertUnseen {
		t.Fatalf("expected alert acknowledged, got err=%v unseen=%v", err, acked.AlertUnseen)
	}

	// Second run should create a second fresh session and inject previous summary into System.
	if _, err := svc.RunNow(created.ID); err != nil {
		t.Fatalf("RunNow #2 failed: %v", err)
	}
	after2 := waitForRunDone(t, svc, created.ID)
	if after2.LastOutcome != StatusOK {
		t.Fatalf("expected ok on run #2, got %+v", after2)
	}
	host.mu.Lock()
	createdCount := len(host.created)
	secondTurn := host.turns[1]
	host.mu.Unlock()
	if createdCount != 2 {
		t.Fatalf("expected 2 fresh sessions created, got %d", createdCount)
	}
	if !strings.Contains(secondTurn.System, "2 PR checks failed on main") {
		t.Fatalf("expected previous run summary injected in System prompt, got: %s", secondTurn.System)
	}
	if secondTurn.SkillSelection == nil || secondTurn.SkillSelection.Name != "pr-patrol" {
		t.Fatalf("expected skill selection forwarded, got %+v", secondTurn.SkillSelection)
	}
}

func TestBoundSessionBusySkipAndDeleteAutoPause(t *testing.T) {
	tmp := t.TempDir()
	host := newFakeHost()
	host.sessions["ses_bound_1"] = tmp
	host.busy["ses_bound_1"] = true

	svc := NewService(filepath.Join(tmp, "schedules.json"), filepath.Join(tmp, "runs"), host)
	defer svc.Stop()
	sch, err := svc.Create(UpsertInput{
		Name:      "Watch Bound Session",
		Directory: tmp,
		SessionID: "ses_bound_1",
		Trigger:   TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 15},
		Prompt:    "Check deployment status",
	})
	if err != nil {
		t.Fatalf("Create bound schedule failed: %v", err)
	}

	// While bound session is busy, run should skip cleanly without interrupting or incrementing fail count.
	if _, err := svc.RunNow(sch.ID); err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}
	afterSkip := waitForRunDone(t, svc, sch.ID)
	if afterSkip.LastOutcome != StatusSkippedBusy || afterSkip.ConsecutiveFails != 0 {
		t.Fatalf("expected skipped_busy with 0 fails, got %+v", afterSkip)
	}
	host.mu.Lock()
	createdCount := len(host.created)
	turnsCount := len(host.turns)
	host.mu.Unlock()
	if createdCount != 0 || turnsCount != 0 {
		t.Fatalf("bound busy session should neither create a new session nor run a turn (created=%d turns=%d)", createdCount, turnsCount)
	}

	// When bound session is deleted, schedule must auto-pause.
	svc.ForgetSession("ses_bound_1")
	paused, ok := svc.Get(sch.ID)
	if !ok || paused.Enabled || paused.PausedReason == "" {
		t.Fatalf("expected schedule paused after bound session deleted, got %+v", paused)
	}
}

func TestCorruptStoreIsBackedUpAndBlocksWrites(t *testing.T) {
	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "schedules.json")
	if err := os.WriteFile(storePath, []byte("{corrupt json"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(storePath, filepath.Join(tmp, "runs"), newFakeHost())
	if svc.LoadError() == nil {
		t.Fatal("expected load error for corrupt schedules.json")
	}
	// Backup file must exist
	matches, _ := filepath.Glob(storePath + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("expected 1 corrupt backup file, found %v", matches)
	}
	// Writes must be refused instead of clobbering
	_, err := svc.Create(UpsertInput{
		Name:      "Blocked",
		Directory: tmp,
		Trigger:   TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 10},
		Execution: ExecutionSpec{ProviderID: "anthropic", ModelID: "claude"},
		Prompt:    "hi",
	})
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("expected ErrStorageUnavailable, got %v", err)
	}
}

func TestConsecutiveFailuresAutoPause(t *testing.T) {
	tmp := t.TempDir()
	host := newFakeHost()
	host.turnReply = func(context.Context, string, RunTurnInput) (string, error) {
		return "", errors.New("upstream 500")
	}
	svc := NewService(filepath.Join(tmp, "schedules.json"), filepath.Join(tmp, "runs"), host)
	defer svc.Stop()
	maxFails := 2
	sch, err := svc.Create(UpsertInput{
		Name:                "Flaky Check",
		Directory:           tmp,
		Trigger:             TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 10},
		Execution:           ExecutionSpec{ProviderID: "anthropic", ModelID: "claude"},
		Prompt:              "Check",
		MaxConsecutiveFails: &maxFails,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.RunNow(sch.ID); err != nil {
			t.Fatalf("RunNow #%d failed: %v", i+1, err)
		}
		waitForRunDone(t, svc, sch.ID)
	}
	got, _ := svc.Get(sch.ID)
	if got.Enabled || !strings.Contains(got.PausedReason, "2 consecutive failures") {
		t.Fatalf("expected auto-pause after 2 consecutive failures, got %+v", got)
	}
}

func TestRoutesRejectRemoteCallers(t *testing.T) {
	tmp := t.TempDir()
	svc := NewService(filepath.Join(tmp, "schedules.json"), filepath.Join(tmp, "runs"), newFakeHost())
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux, func(r *http.Request) bool {
		return r.Header.Get("X-Test-Remote") == "1"
	})

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/hoxi/schedules"},
		{http.MethodPost, "/api/hoxi/schedules"},
		{http.MethodPost, "/api/hoxi/schedules/preview-cron"},
		{http.MethodPost, "/api/hoxi/schedules/sch_1/run"},
		{http.MethodDelete, "/api/hoxi/schedules/sch_1"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(`{}`))
		req.Header.Set("X-Test-Remote", "1")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("remote %s %s expected 403, got %d", tc.method, tc.path, rec.Code)
		}
	}

	// Local preview-cron works
	body, _ := json.Marshal(map[string]any{
		"trigger": map[string]any{"kind": "cron", "cron": "0 9 * * *"},
		"count":   3,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/schedules/preview-cron", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("local preview-cron expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBoundSessionPreservesEmptyModeAndUpdatePreservesNextRunAt(t *testing.T) {
	tmp := t.TempDir()
	host := newFakeHost()
	host.sessions["ses_plan"] = tmp
	svc := NewService(filepath.Join(tmp, "schedules.json"), filepath.Join(tmp, "runs"), host)
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return base }

	bound, err := svc.Create(UpsertInput{
		Name:      "Bound Plan Session",
		Directory: tmp,
		SessionID: "ses_plan",
		Trigger:   TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 30},
		Prompt:    "Check status",
	})
	if err != nil {
		t.Fatalf("Create bound schedule failed: %v", err)
	}
	if bound.Execution.Mode != "" {
		t.Fatalf("expected bound schedule to keep empty Mode so session mode is inherited, got %q", bound.Execution.Mode)
	}
	origNext := bound.NextRunAt

	// Advance clock by 5m and edit only name/notifyOn: NextRunAt must stay preserved.
	svc.nowFn = func() time.Time { return base.Add(5 * time.Minute) }
	newName := "Bound Plan Session (Renamed)"
	updated, err := svc.Update(bound.ID, PatchInput{Name: &newName})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updated.NextRunAt != origNext {
		t.Fatalf("expected NextRunAt preserved at %d when trigger unchanged, got %d", origNext, updated.NextRunAt)
	}
	if updated.Execution.Mode != "" {
		t.Fatalf("expected Mode to remain empty after rename, got %q", updated.Execution.Mode)
	}
}

func TestDeleteRunningScheduleDoesNotRecreateRunsFile(t *testing.T) {
	tmp := t.TempDir()
	runsDir := filepath.Join(tmp, "runs")
	host := newFakeHost()
	started := make(chan struct{})
	finished := make(chan struct{})
	host.turnReply = func(ctx context.Context, _ string, _ RunTurnInput) (string, error) {
		close(started)
		<-ctx.Done()
		defer close(finished)
		return "", ctx.Err()
	}
	svc := NewService(filepath.Join(tmp, "schedules.json"), runsDir, host)
	defer svc.Stop()
	sch, err := svc.Create(UpsertInput{
		Name:      "Delete While Running",
		Directory: tmp,
		Trigger:   TriggerSpec{Kind: TriggerKindInterval, IntervalMinutes: 15},
		Execution: ExecutionSpec{ProviderID: "anthropic", ModelID: "claude"},
		Prompt:    "Long running check",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunNow(sch.ID); err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}
	<-started
	if err := svc.Delete(sch.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	<-finished
	// Give finishRun a moment to return cleanly after ctx cancellation.
	time.Sleep(50 * time.Millisecond)

	runsFile := filepath.Join(runsDir, sch.ID+".runs.json")
	if _, err := os.Stat(runsFile); !os.IsNotExist(err) {
		t.Fatalf("expected deleted schedule runs file %s not to exist, stat err=%v", runsFile, err)
	}
	if hist, err := svc.History(sch.ID); !errors.Is(err, ErrScheduleNotFound) || len(hist) != 0 {
		t.Fatalf("expected ErrScheduleNotFound and empty history for deleted schedule, got err=%v len=%d", err, len(hist))
	}
}
