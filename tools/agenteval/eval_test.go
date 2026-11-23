package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllTasksMaterializeAndFailBaseline(t *testing.T) {
	tasks := BuiltinTasks()
	if len(tasks) < 5 {
		t.Fatalf("expected at least 5 builtin tasks, got %d", len(tasks))
	}
	executed := 0
	for _, task := range tasks {
		task := task
		t.Run(task.ID, func(t *testing.T) {
			if task.RequireTool != "" {
				if _, err := exec.LookPath(task.RequireTool); err != nil {
					t.Skipf("tool %q not installed on host", task.RequireTool)
				}
			}
			executed++
			dir := t.TempDir()
			if err := Materialize(task, dir); err != nil {
				t.Fatalf("Materialize(%s): %v", task.ID, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			passed, out, err := VerifyTask(ctx, task, dir)
			if err != nil {
				t.Fatalf("VerifyTask(%s) errored: %v", task.ID, err)
			}
			if passed {
				t.Fatalf("baseline task %s unexpectedly passed before agent edits!\n%s", task.ID, out)
			}
		})
	}
	if executed == 0 {
		t.Fatal("expected at least one baseline task to execute")
	}
}

func TestSmokeEvalEndToEndWithDiagnosticsSelfHeal(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain required for smoke eval")
	}
	tasks := BuiltinTasks()[:1]
	rep := RunSuite(context.Background(), tasks, RunOptions{
		ProviderID:  "smoke",
		ModelID:     "scripted-native",
		MaxSteps:    10,
		TaskTimeout: 60 * time.Second,
		Smoke:       true,
	})

	if rep.Summary.ExecutedTasks != 1 || rep.Summary.PassedTasks != 1 {
		t.Fatalf("expected smoke eval to pass 1/1, got summary=%+v results=%+v", rep.Summary, rep.Results)
	}
	r := rep.Results[0]
	if r.Steps < 4 {
		t.Fatalf("expected >=4 engine steps, got %d", r.Steps)
	}
	if r.EditPatchCalls < 2 {
		t.Fatalf("expected >=2 edit calls (broken + self-heal), got %d", r.EditPatchCalls)
	}
	if r.DiagnosticsTriggered < 2 || r.DiagnosticErrorsCaught < 1 {
		t.Fatalf("expected post-edit diagnostics to trigger >=2 and catch >=1 error, got triggered=%d caught=%d",
			r.DiagnosticsTriggered, r.DiagnosticErrorsCaught)
	}
	if r.CacheHitRate <= 0 {
		t.Fatalf("expected positive prompt cache hit rate, got %f", r.CacheHitRate)
	}

	table := FormatTable(rep)
	if !strings.Contains(table, "go_retry_backoff") || !strings.Contains(table, "PASS") {
		t.Fatalf("unexpected formatted table:\n%s", table)
	}

	reportPath := filepath.Join(t.TempDir(), "eval-report.json")
	if err := WriteReport(reportPath, rep); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	loaded, err := ReadReport(reportPath)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if loaded.Summary.PassedTasks != 1 || loaded.Results[0].TaskID != "go_retry_backoff" {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}

	// Simulate an improved run and verify baseline comparison formatting.
	improved := loaded
	improved.Summary.TotalTokens -= 500
	improved.Summary.AvgSteps -= 1.0
	diffText := FormatComparison(loaded, improved)
	if !strings.Contains(diffText, "-500") || !strings.Contains(diffText, "Comparison vs baseline:") {
		t.Fatalf("unexpected comparison output:\n%s", diffText)
	}

	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("expected report file to exist: %v", err)
	}
}

func TestFilterTasksValidation(t *testing.T) {
	all := BuiltinTasks()
	if got, err := filterTasks(all, "", true); err != nil || len(got) != 1 || got[0].ID != "go_retry_backoff" {
		t.Fatalf("smoke default filter mismatch: got=%v err=%v", got, err)
	}
	if got, err := filterTasks(all, "go_lru_cache,py_rate_limiter", false); err != nil || len(got) != 2 {
		t.Fatalf("subset filter mismatch: got=%v err=%v", got, err)
	}
	if _, err := filterTasks(all, "does_not_exist", false); err == nil {
		t.Fatal("expected unknown task ID to fail")
	}
}
