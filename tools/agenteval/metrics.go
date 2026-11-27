package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TaskResult records objective outcome and engine telemetry for a single task.
type TaskResult struct {
	TaskID                 string   `json:"taskId"`
	Language               string   `json:"language"`
	Category               string   `json:"category"`
	Passed                 bool     `json:"passed"`
	Skipped                bool     `json:"skipped,omitempty"`
	SkipReason             string   `json:"skipReason,omitempty"`
	Error                  string   `json:"error,omitempty"`
	Steps                  int      `json:"steps"`
	ToolCalls              int      `json:"toolCalls"`
	ToolErrors             int      `json:"toolErrors"`
	EditPatchCalls         int      `json:"editPatchCalls"`
	EditPatchErrors        int      `json:"editPatchErrors"`
	DiagnosticsTriggered   int      `json:"diagnosticsTriggered"`
	DiagnosticErrorsCaught int      `json:"diagnosticErrorsCaught"`
	DiagnosticTimeouts     int      `json:"diagnosticTimeouts,omitempty"`
	InputTokens            int      `json:"inputTokens"`
	OutputTokens           int      `json:"outputTokens"`
	ReasoningTokens        int      `json:"reasoningTokens"`
	CacheReadTokens        int      `json:"cacheReadTokens"`
	CacheWriteTokens       int      `json:"cacheWriteTokens"`
	ReportedCacheInput     int      `json:"reportedCacheInput,omitempty"`
	CacheHitRate           *float64 `json:"cacheHitRate"`
	CostUSD                float64  `json:"costUSD,omitempty"`
	CostIncomplete         bool     `json:"costIncomplete,omitempty"`
	DurationMs             int64    `json:"durationMs"`
	VerifyOutput           string   `json:"verifyOutput,omitempty"`
}

// Summary aggregates benchmark metrics across all executed tasks.
type Summary struct {
	TotalTasks             int      `json:"totalTasks"`
	ExecutedTasks          int      `json:"executedTasks"`
	PassedTasks            int      `json:"passedTasks"`
	SkippedTasks           int      `json:"skippedTasks"`
	PassRate               float64  `json:"passRate"`
	AvgSteps               float64  `json:"avgSteps"`
	TotalToolCalls         int      `json:"totalToolCalls"`
	TotalToolErrors        int      `json:"totalToolErrors"`
	TotalEditPatchCalls    int      `json:"totalEditPatchCalls"`
	TotalEditPatchErrors   int      `json:"totalEditPatchErrors"`
	DiagnosticsTriggered   int      `json:"diagnosticsTriggered"`
	DiagnosticErrorsCaught int      `json:"diagnosticErrorsCaught"`
	DiagnosticTimeouts     int      `json:"diagnosticTimeouts,omitempty"`
	InputTokens            int      `json:"inputTokens"`
	OutputTokens           int      `json:"outputTokens"`
	ReasoningTokens        int      `json:"reasoningTokens"`
	CacheReadTokens        int      `json:"cacheReadTokens"`
	CacheWriteTokens       int      `json:"cacheWriteTokens"`
	TotalTokens            int      `json:"totalTokens"`
	ReportedCacheInput     int      `json:"reportedCacheInput,omitempty"`
	CacheHitRate           *float64 `json:"cacheHitRate"`
	TotalCostUSD           float64  `json:"totalCostUSD,omitempty"`
	CostIncomplete         bool     `json:"costIncomplete,omitempty"`
	TotalDurationMs        int64    `json:"totalDurationMs"`
}

const evalMetricsVersion = 3

// Report is the top-level JSON artifact emitted by agenteval.
type Report struct {
	MetricsVersion   int          `json:"metricsVersion,omitempty"`
	CreatedAt        string       `json:"createdAt"`
	Mode             string       `json:"mode"`
	ProviderID       string       `json:"providerId"`
	ModelID          string       `json:"modelId"`
	Effort           string       `json:"effort,omitempty"`
	ExecutorProvider string       `json:"executorProvider,omitempty"`
	ExecutorModel    string       `json:"executorModel,omitempty"`
	Summary          Summary      `json:"summary"`
	Results          []TaskResult `json:"results"`
}

func computeCacheHitRate(input, cacheRead, cacheWrite, reportedInput int) *float64 {
	denom := input + cacheRead + cacheWrite
	if denom <= 0 || reportedInput != denom {
		return nil
	}
	rate := float64(cacheRead) / float64(denom)
	return &rate
}

func formatCacheRate(rate *float64) string {
	if rate == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", *rate*100)
}

// ComputeSummary rolls up TaskResult rows into aggregate metrics.
func ComputeSummary(results []TaskResult) Summary {
	var s Summary
	s.TotalTasks = len(results)
	var totalSteps int
	for _, r := range results {
		if r.Skipped {
			s.SkippedTasks++
			continue
		}
		s.ExecutedTasks++
		if r.Passed {
			s.PassedTasks++
		}
		totalSteps += r.Steps
		s.TotalToolCalls += r.ToolCalls
		s.TotalToolErrors += r.ToolErrors
		s.TotalEditPatchCalls += r.EditPatchCalls
		s.TotalEditPatchErrors += r.EditPatchErrors
		s.DiagnosticsTriggered += r.DiagnosticsTriggered
		s.DiagnosticErrorsCaught += r.DiagnosticErrorsCaught
		s.DiagnosticTimeouts += r.DiagnosticTimeouts
		s.InputTokens += r.InputTokens
		s.OutputTokens += r.OutputTokens
		s.ReasoningTokens += r.ReasoningTokens
		s.CacheReadTokens += r.CacheReadTokens
		s.CacheWriteTokens += r.CacheWriteTokens
		s.ReportedCacheInput += r.ReportedCacheInput
		s.TotalCostUSD += r.CostUSD
		s.CostIncomplete = s.CostIncomplete || r.CostIncomplete
		s.TotalDurationMs += r.DurationMs
	}
	s.TotalTokens = s.InputTokens + s.OutputTokens + s.ReasoningTokens + s.CacheReadTokens + s.CacheWriteTokens
	if s.ExecutedTasks > 0 {
		s.PassRate = float64(s.PassedTasks) / float64(s.ExecutedTasks)
		s.AvgSteps = float64(totalSteps) / float64(s.ExecutedTasks)
	}
	s.CacheHitRate = computeCacheHitRate(s.InputTokens, s.CacheReadTokens, s.CacheWriteTokens, s.ReportedCacheInput)
	return s
}

// FormatTable renders a human-readable report table.
func FormatTable(rep Report) string {
	var b strings.Builder
	target := rep.ProviderID + "/" + rep.ModelID
	if rep.ExecutorModel != "" {
		target += fmt.Sprintf(" (orchestrator -> %s/%s)", rep.ExecutorProvider, rep.ExecutorModel)
	}
	fmt.Fprintf(&b, "Hoxi Native Agent Eval [%s]  target=%s\n", rep.Mode, target)
	b.WriteString(strings.Repeat("-", 104) + "\n")
	fmt.Fprintf(&b, "%-20s %-8s %5s %9s %9s %9s %10s %8s %8s\n",
		"TASK", "STATUS", "STEPS", "TOOLS/ERR", "EDIT/ERR", "DIAG/HIT", "TOKENS", "CACHE%", "TIME")
	b.WriteString(strings.Repeat("-", 104) + "\n")
	for _, r := range rep.Results {
		status := "FAIL"
		if r.Skipped {
			status = "SKIP"
		} else if r.Passed {
			status = "PASS"
		}
		toks := r.InputTokens + r.OutputTokens + r.ReasoningTokens + r.CacheReadTokens + r.CacheWriteTokens
		fmt.Fprintf(&b, "%-20s %-8s %5d %4d/%-4d %4d/%-4d %4d/%-4d %10d %8s %7.1fs\n",
			r.TaskID,
			status,
			r.Steps,
			r.ToolCalls, r.ToolErrors,
			r.EditPatchCalls, r.EditPatchErrors,
			r.DiagnosticsTriggered, r.DiagnosticErrorsCaught,
			toks,
			formatCacheRate(r.CacheHitRate),
			float64(r.DurationMs)/1000.0,
		)
		if r.Error != "" {
			fmt.Fprintf(&b, "  error: %s\n", r.Error)
		} else if r.SkipReason != "" {
			fmt.Fprintf(&b, "  skip: %s\n", r.SkipReason)
		}
	}
	b.WriteString(strings.Repeat("-", 104) + "\n")
	s := rep.Summary
	fmt.Fprintf(&b, "Summary: %d/%d passed (%.1f%%) | skipped=%d | avg_steps=%.1f | tools=%d (err=%d) | diag_caught=%d | diag_timeouts=%d | tokens=%d (cache=%s) | time=%.1fs\n",
		s.PassedTasks, s.ExecutedTasks, s.PassRate*100,
		s.SkippedTasks, s.AvgSteps,
		s.TotalToolCalls, s.TotalToolErrors,
		s.DiagnosticErrorsCaught, s.DiagnosticTimeouts,
		s.TotalTokens, formatCacheRate(s.CacheHitRate),
		float64(s.TotalDurationMs)/1000.0,
	)
	return b.String()
}

// FormatComparison renders deltas between a baseline report and a new run.
func FormatComparison(base, curr Report) string {
	executed := func(rep Report) map[string]bool {
		ids := make(map[string]bool)
		for _, row := range rep.Results {
			if !row.Skipped {
				ids[row.TaskID] = true
			}
		}
		return ids
	}
	if base.MetricsVersion != curr.MetricsVersion {
		return "\nComparison unavailable: grading or metrics changed. Rerun the baseline with the current agenteval.\n"
	}
	if base.Mode != curr.Mode || !maps.Equal(executed(base), executed(curr)) {
		return "\nComparison unavailable: choose the same run mode and executed task IDs for both reports.\n"
	}
	var b strings.Builder
	b.WriteString("\nComparison vs baseline:\n")
	passDelta := (curr.Summary.PassRate - base.Summary.PassRate) * 100
	stepDelta := curr.Summary.AvgSteps - base.Summary.AvgSteps
	tokDelta := curr.Summary.TotalTokens - base.Summary.TotalTokens
	durDelta := float64(curr.Summary.TotalDurationMs-base.Summary.TotalDurationMs) / 1000.0
	fmt.Fprintf(&b, "  Pass rate:   %5.1f%% -> %5.1f%% (%+.1f pp)\n", base.Summary.PassRate*100, curr.Summary.PassRate*100, passDelta)
	fmt.Fprintf(&b, "  Avg steps:   %5.1f  -> %5.1f  (%+.1f)\n", base.Summary.AvgSteps, curr.Summary.AvgSteps, stepDelta)
	fmt.Fprintf(&b, "  Total tokens:%8d -> %8d (%+d)\n", base.Summary.TotalTokens, curr.Summary.TotalTokens, tokDelta)
	before, after := base.Summary.CacheHitRate, curr.Summary.CacheHitRate
	fmt.Fprintf(&b, "  Cache hit:   %s -> %s", formatCacheRate(before), formatCacheRate(after))
	if before != nil && after != nil {
		fmt.Fprintf(&b, " (%+.1f pp)", (*after-*before)*100)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  Edit errors: %d -> %d\n", base.Summary.TotalEditPatchErrors, curr.Summary.TotalEditPatchErrors)
	fmt.Fprintf(&b, "  Diagnostic findings / timeouts: %d / %d -> %d / %d\n", base.Summary.DiagnosticErrorsCaught, base.Summary.DiagnosticTimeouts, curr.Summary.DiagnosticErrorsCaught, curr.Summary.DiagnosticTimeouts)
	fmt.Fprintf(&b, "  Estimated cost (USD): %s -> %s\n", evalCost(base.Summary), evalCost(curr.Summary))
	fmt.Fprintf(&b, "  Wall time:   %5.1fs -> %5.1fs (%+.1fs)\n", float64(base.Summary.TotalDurationMs)/1000.0, float64(curr.Summary.TotalDurationMs)/1000.0, durDelta)
	return b.String()
}

func evalCost(s Summary) string {
	if s.TotalCostUSD <= 0 || s.CostIncomplete {
		return "unavailable"
	}
	return fmt.Sprintf("%.6f", s.TotalCostUSD)
}

// WriteReport persists a Report as indented JSON atomically.
func WriteReport(path string, rep Report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agenteval-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// ReadReport reads a persisted JSON Report from disk.
func ReadReport(path string) (Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Report{}, err
	}
	return rep, nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
