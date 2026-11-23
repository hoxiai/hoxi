package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resolveHookHost runs onResolve while Update is validating bindings outside
// the service lock, which is where a concurrent run can finish.
type resolveHookHost struct {
	*fakeHost
	mu        sync.Mutex
	onResolve func()
}

func (h *resolveHookHost) ResolveWorkspaceDirectory(projectID, directory string) (string, string, error) {
	h.mu.Lock()
	fn := h.onResolve
	h.onResolve = nil
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
	return h.fakeHost.ResolveWorkspaceDirectory(projectID, directory)
}

func TestUpdateKeepsRunOutcomeAndAutoPauseRecordedDuringValidation(t *testing.T) {
	tmp := t.TempDir()
	host := &resolveHookHost{fakeHost: newFakeHost()}
	host.turnReply = func(context.Context, string, RunTurnInput) (string, error) {
		return "", errors.New("upstream 500")
	}
	svc := NewService(filepath.Join(tmp, "schedules.json"), filepath.Join(tmp, "runs"), host)
	defer svc.Stop()
	maxFails := 1
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

	host.mu.Lock()
	host.onResolve = func() {
		svc.finishRun(sch.ID, RunRecord{ID: "run_race", ScheduleID: sch.ID, Status: StatusRunning}, StatusError, "", "upstream 500", false)
	}
	host.mu.Unlock()

	renamed := "Renamed Check"
	got, err := svc.Update(sch.ID, PatchInput{Name: &renamed})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := svc.Get(sch.ID)
	for label, s := range map[string]Schedule{"returned": got, "stored": stored} {
		if s.Name != renamed {
			t.Errorf("%s: name = %q, want %q", label, s.Name, renamed)
		}
		if s.Enabled || s.NextRunAt != 0 || !strings.Contains(s.PausedReason, "consecutive failures") {
			t.Errorf("%s: concurrent auto-pause was rolled back: %+v", label, s)
		}
		if s.LastOutcome != StatusError || s.LastError != "upstream 500" || s.ConsecutiveFails != 1 || !s.AlertUnseen {
			t.Errorf("%s: concurrent run outcome was lost: %+v", label, s)
		}
	}

	// An explicit re-enable is the user's latest decision and clears the pause.
	enable := true
	reenabled, err := svc.Update(sch.ID, PatchInput{Enabled: &enable})
	if err != nil {
		t.Fatal(err)
	}
	if !reenabled.Enabled || reenabled.PausedReason != "" || reenabled.ConsecutiveFails != 0 || reenabled.NextRunAt == 0 {
		t.Fatalf("explicit enable must clear the auto-pause: %+v", reenabled)
	}
}
