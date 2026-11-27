package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
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
	if r.CacheHitRate == nil || *r.CacheHitRate <= 0 {
		t.Fatalf("expected positive prompt cache hit rate, got %s", formatCacheRate(r.CacheHitRate))
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

// Interrupt the scripted coding task after its first edit, then return a metered
// summary. The remaining steps still have to fix the code and pass the real tests.
type overflowEvalStream struct {
	*smokeStreamClient
	requests int
}

func (s *overflowEvalStream) OpenStream(ctx context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	s.requests++
	switch s.requests {
	case 3:
		return nil, &llm.StreamHTTPError{StatusCode: 400, Message: "prompt is too long for the context window"}
	case 4:
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(buildAnthropicTextSSE(77, 11, 19, 3, "Continue fixing retry.go.")))}, nil
	}
	return s.smokeStreamClient.OpenStream(ctx, req)
}

func TestCodingEvalCountsCompactionUsage(t *testing.T) {
	rep := RunSuite(context.Background(), BuiltinTasks()[:1], RunOptions{
		ProviderID: "smoke", ModelID: "scripted-native", StreamClient: &overflowEvalStream{smokeStreamClient: newSmokeStreamClient()},
	})
	if rep.Summary.PassedTasks != 1 || rep.Summary.TotalTokens != 12460+77+11+19+3 {
		t.Fatalf("expected all five coding steps and the compaction call in usage: %+v", rep)
	}
}

func TestFilterTasksValidation(t *testing.T) {
	all := BuiltinTasks()
	if got, err := filterTasks(all, "", true); err != nil || len(got) != 3 || got[0].ID != "go_retry_backoff" {
		t.Fatalf("smoke default filter mismatch: got=%v err=%v", got, err)
	}
	if got, err := filterTasks(all, "go_lru_cache,py_rate_limiter", false); err != nil || len(got) != 2 {
		t.Fatalf("subset filter mismatch: got=%v err=%v", got, err)
	}
	if _, err := filterTasks(all, "does_not_exist", false); err == nil {
		t.Fatal("expected unknown task ID to fail")
	}
}

func TestCheckerTimeoutIsNotACodeError(t *testing.T) {
	if isDiagnosticFailure("Timed out: static check did not finish. [tsc --noEmit --project tsconfig.json]") {
		t.Fatal("a checker timeout was counted as a source-code error")
	}
}

func TestIncompleteMetricsAndMismatchedBaselines(t *testing.T) {
	if isDiagnosticFailure("[go vet]\nCheck exited with code 1 and no diagnostic output.") {
		t.Fatal("a checker exit without findings was counted as a code error")
	}
	base := Report{Mode: "smoke", MetricsVersion: evalMetricsVersion, Results: []TaskResult{{TaskID: "one"}}}
	if got := formatCacheRate(computeCacheHitRate(50, 50, 0, 50)); got != "n/a" {
		t.Fatal("partial cache coverage", got)
	}
	if got := formatCacheRate(computeCacheHitRate(100, 0, 0, 100)); got != "0.0%" {
		t.Fatal("explicit miss", got)
	}
	if got := evalCost(Summary{TotalCostUSD: 1, CostIncomplete: true}); got != "unavailable" {
		t.Fatal("partially priced comparison", got)
	}
	for _, other := range []Report{
		{Mode: "live", MetricsVersion: evalMetricsVersion, Results: base.Results},
		{Mode: "smoke", MetricsVersion: evalMetricsVersion, Results: []TaskResult{{TaskID: "two"}}},
		{Mode: "smoke", Results: base.Results},
	} {
		if !strings.Contains(FormatComparison(base, other), "Comparison unavailable") {
			t.Fatal("incompatible comparison", other)
		}
	}
}

// Offline baseline for the two scripted workloads. Model quality and provider
// cache rates are intentionally outside this deterministic pass/fail contract.
func TestMultiTurnSmokeBaseline(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node required")
	}
	tasks := []Task{taskKeyMigration(), taskReportReplay()}
	rep := RunSuite(context.Background(), tasks, RunOptions{Smoke: true, ProviderID: "smoke", ModelID: "scripted-native"})
	for _, r := range rep.Results {
		if !r.Passed || r.Steps != 6 || r.ToolCalls != 4 || r.EditPatchCalls != 2 || r.EditPatchErrors != 0 || r.ToolErrors != 0 || r.DiagnosticTimeouts != 0 {
			t.Errorf("offline regression: %+v", r)
		}
	}
	budget := RunSuite(context.Background(), tasks[:1], RunOptions{Smoke: true, MaxSteps: 3})
	if budget.Results[0].Passed || !strings.Contains(budget.Results[0].Error, "step budget") {
		t.Fatal("follow-up ignored the task-wide step budget", budget.Results[0])
	}
}
