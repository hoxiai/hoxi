// Command agenteval runs objective, real-toolchain coding benchmarks directly
// against Hoxi's in-process Go Native Agent Engine (internal/agent) and reports
// pass rate, step count, edit/patch retry errors, post-edit diagnostics catches,
// token usage, prompt cache hit rate, and baseline diffs.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/provider"
)

func main() {
	listFlag := flag.Bool("list", false, "list available evaluation tasks and exit")
	tasksFlag := flag.String("tasks", "", "comma-separated subset of task IDs to execute")
	providerFlag := flag.String("provider", "", "provider ID configured in ~/.hoxi/models.json (required unless -smoke)")
	modelFlag := flag.String("model", "", "model ID configured in ~/.hoxi/models.json (required unless -smoke)")
	effortFlag := flag.String("effort", "", "optional reasoning effort variant (e.g. medium, high, max)")
	execProviderFlag := flag.String("executor-provider", "", "optional executor provider ID to benchmark Orchestrator+Executor mode")
	execModelFlag := flag.String("executor-model", "", "optional executor model ID to benchmark Orchestrator+Executor mode")
	execEffortFlag := flag.String("executor-effort", "", "optional executor reasoning effort")
	maxStepsFlag := flag.Int("max-steps", defaultMaxEvalSteps, "maximum agent steps per task")
	timeoutFlag := flag.Duration("timeout", defaultEvalTimeout, "per-task execution timeout")
	outFlag := flag.String("out", "", "optional path to write structured JSON report")
	compareFlag := flag.String("compare", "", "optional path to a baseline JSON report for delta comparison")
	smokeFlag := flag.Bool("smoke", false, "run offline deterministic smoke check through the real engine, diagnostics, and go test")
	verboseFlag := flag.Bool("verbose", false, "log each tool call as tasks execute")
	flag.Parse()

	allTasks := BuiltinTasks()
	if *listFlag {
		for _, task := range allTasks {
			fmt.Printf("%-20s %-11s %-12s %s\n", task.ID, task.Language, task.Category, task.Description)
		}
		return
	}

	selected, err := filterTasks(allTasks, *tasksFlag, *smokeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[agenteval] %v\n", err)
		os.Exit(2)
	}

	opts := RunOptions{
		ProviderID:         strings.TrimSpace(*providerFlag),
		ModelID:            strings.TrimSpace(*modelFlag),
		Effort:             strings.TrimSpace(*effortFlag),
		ExecutorProviderID: strings.TrimSpace(*execProviderFlag),
		ExecutorModelID:    strings.TrimSpace(*execModelFlag),
		ExecutorEffort:     strings.TrimSpace(*execEffortFlag),
		MaxSteps:           *maxStepsFlag,
		TaskTimeout:        *timeoutFlag,
		Verbose:            *verboseFlag,
		Smoke:              *smokeFlag,
		LogWriter:          os.Stderr,
	}

	if *smokeFlag {
		if opts.ProviderID == "" {
			opts.ProviderID = "smoke"
		}
		if opts.ModelID == "" {
			opts.ModelID = "scripted-native"
		}
	} else {
		cwd, _ := os.Getwd()
		provSvc := provider.NewService(0, "", cwd, nil)
		if opts.ProviderID == "" || opts.ModelID == "" {
			pid, mid := resolveDefaultEvalModel(provSvc)
			if opts.ProviderID == "" {
				opts.ProviderID = pid
			}
			if opts.ModelID == "" {
				opts.ModelID = mid
			}
		}
		if opts.ProviderID == "" || opts.ModelID == "" {
			fmt.Fprintln(os.Stderr, "[agenteval] specify -provider and -model (or configure default model in ~/.hoxi/models.json), or use -smoke for offline verification")
			os.Exit(2)
		}
		opts.StreamClient = provSvc
	}

	rep := RunSuite(context.Background(), selected, opts)
	fmt.Print(FormatTable(rep))

	if *compareFlag != "" {
		base, err := ReadReport(*compareFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[agenteval] failed to read baseline %s: %v\n", *compareFlag, err)
			os.Exit(2)
		}
		fmt.Print(FormatComparison(base, rep))
	}
	if *outFlag != "" {
		if err := WriteReport(*outFlag, rep); err != nil {
			fmt.Fprintf(os.Stderr, "[agenteval] failed to write report %s: %v\n", *outFlag, err)
			os.Exit(2)
		}
		fmt.Printf("Report written to %s\n", *outFlag)
	}
	if rep.Summary.ExecutedTasks == 0 || rep.Summary.PassedTasks < rep.Summary.ExecutedTasks {
		os.Exit(1)
	}
}

func filterTasks(all []Task, filter string, smoke bool) ([]Task, error) {
	trimmed := strings.TrimSpace(filter)
	if trimmed == "" {
		if smoke {
			return all[:1], nil
		}
		return all, nil
	}
	byID := make(map[string]Task, len(all))
	for _, task := range all {
		byID[task.ID] = task
	}
	var out []Task
	seen := make(map[string]bool)
	for _, raw := range strings.Split(trimmed, ",") {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		task, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown task ID %q (run with -list to see valid tasks)", id)
		}
		seen[id] = true
		out = append(out, task)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tasks selected")
	}
	return out, nil
}

func resolveDefaultEvalModel(provSvc *provider.Service) (string, string) {
	doc := provSvc.LoadHoxiConfigDoc()
	if rawModel, ok := doc["model"].(string); ok {
		if pid, mid := llm.SplitModelRef(strings.TrimSpace(rawModel)); pid != "" && mid != "" {
			return pid, mid
		}
	}
	return "", ""
}
