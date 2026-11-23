package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

const orchestratorExecutorMaxSteps = 48

// OrchestratorConfig enables dual-model Orchestrator + Executor execution on a
// native turn: the turn's primary model plans, makes architectural decisions,
// and reviews results, while write/shell/heavy execution runs on the Executor.
type OrchestratorConfig struct {
	ExecutorProviderID string
	ExecutorModelID    string
	ExecutorEffort     string
}

func isOrchestratorActive(req TurnRequest) bool {
	return req.SubtaskDepth == 0 &&
		req.Orchestrator != nil &&
		strings.TrimSpace(req.Orchestrator.ExecutorProviderID) != "" &&
		strings.TrimSpace(req.Orchestrator.ExecutorModelID) != ""
}

func hasPendingParentSteer(src SteerSource) bool {
	if src == nil {
		return false
	}
	ws, ok := src.(SteerWaitSource)
	return ok && ws.HasPending()
}

func isOrchestratorDirectExecutionTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write", "edit", "multiedit", "apply_patch", "bash":
		return true
	default:
		return false
	}
}

const orchestratorProtocolGuidance = `<orchestrator_protocol>
- You are running in Orchestrator mode: you are the planner, architect, and reviewer, paired with a dedicated Executor model invoked via task(subagent_type="executor", ...).
- Direct file-write and shell tools (write, edit, multiedit, apply_patch, bash) are intentionally routed to the Executor so implementation, build, and test loops run on the faster/cheaper Executor model.
- For pure questions, explanations, or read-only reviews that require no edits or commands, answer directly using your read-only inspection tools (hoxi_code_index, read, glob, grep) without spawning the Executor.
- For code modifications, builds, tests, or broad multi-file implementation:
  1. Inspect key architecture/interfaces yourself as needed, then delegate a clear, self-contained specification (goal, target files/symbols, constraints, and required verification commands) via task(subagent_type="executor", ...).
  2. The session's sticky Executor thread is resumed automatically across follow-up calls so its context and prompt cache stay warm; pass fresh=true only when starting an unrelated task.
  3. Always review the factual <executor_receipt> (modified files and command exit statuses) and inspect changed code if needed before declaring success. If issues remain, send concise corrective feedback to task(subagent_type="executor", ...).
</orchestrator_protocol>`

const orchestratorExecutorPrompt = "You are the Executor model in Orchestrator mode. Carry out the Orchestrator's plan accurately and efficiently: locate target code, apply clean edits, run the requested builds/tests, fix introduced errors, and return a concise factual summary citing exact modified paths and verification outcomes."

const executorYieldNotice = "[Executor yielded early so Orchestrator can handle new user steering input]"

type executorCommandRecord struct {
	Command string
	Failed  bool
	Step    int
}

type executorReceiptTracker struct {
	mu              sync.Mutex
	worktreeDir     string
	maxStep         int
	editedFiles     []string
	editedSet       map[string]bool
	commands        []executorCommandRecord
	yieldedForSteer bool
}

func newExecutorReceiptTracker(worktreeDir string) *executorReceiptTracker {
	return &executorReceiptTracker{
		worktreeDir: filepath.Clean(strings.TrimSpace(worktreeDir)),
		editedSet:   make(map[string]bool),
	}
}

func (t *executorReceiptTracker) normalizePath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	if t.worktreeDir != "" && filepath.IsAbs(p) {
		if rel, err := filepath.Rel(t.worktreeDir, filepath.Clean(p)); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			p = rel
		}
	}
	return filepath.ToSlash(filepath.Clean(p))
}

func (t *executorReceiptTracker) RecordEditedPaths(paths []string) {
	if t == nil || len(paths) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, raw := range paths {
		p := t.normalizePath(raw)
		if p == "" || t.editedSet[p] {
			continue
		}
		t.editedSet[p] = true
		t.editedFiles = append(t.editedFiles, p)
	}
}

func (t *executorReceiptTracker) RecordToolEvent(evt EngineEvent) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if evt.Step > t.maxStep {
		t.maxStep = evt.Step
	}
	if evt.Kind != EngineToolCompleted && evt.Kind != EngineToolError {
		return
	}
	name := strings.ToLower(strings.TrimSpace(evt.ToolName))
	switch name {
	case "write", "edit", "multiedit":
		if p := t.normalizePath(extractStringField(evt.Input, "filePath", "path")); p != "" && !t.editedSet[p] {
			t.editedSet[p] = true
			t.editedFiles = append(t.editedFiles, p)
		}
	case "bash":
		cmd := strings.TrimSpace(extractStringField(evt.Input, "command", "cmd"))
		if cmd == "" {
			return
		}
		if len(cmd) > 160 {
			cmd = cmd[:157] + "..."
		}
		if len(t.commands) >= 12 {
			t.commands = append(t.commands[1:], executorCommandRecord{
				Command: cmd,
				Failed:  evt.Kind == EngineToolError,
				Step:    evt.Step,
			})
		} else {
			t.commands = append(t.commands, executorCommandRecord{
				Command: cmd,
				Failed:  evt.Kind == EngineToolError,
				Step:    evt.Step,
			})
		}
	}
}

func (t *executorReceiptTracker) MarkYieldedForSteer() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.yieldedForSteer = true
	t.mu.Unlock()
}

func (t *executorReceiptTracker) FormatReceipt(providerID, modelID string, resumed bool) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	var b strings.Builder
	b.WriteString("<executor_receipt>\n")
	if providerID != "" && modelID != "" {
		fmt.Fprintf(&b, "model: %s/%s\n", providerID, modelID)
	}
	if t.maxStep > 0 {
		fmt.Fprintf(&b, "steps: %d\n", t.maxStep)
	}
	if resumed {
		b.WriteString("thread: resumed\n")
	} else {
		b.WriteString("thread: fresh\n")
	}
	if t.yieldedForSteer {
		b.WriteString("status: yielded_for_user_steer\n")
	}
	if len(t.editedFiles) > 0 {
		limit := len(t.editedFiles)
		if limit > 30 {
			limit = 30
		}
		fmt.Fprintf(&b, "modified_files (%d): %s", len(t.editedFiles), strings.Join(t.editedFiles[:limit], ", "))
		if len(t.editedFiles) > limit {
			fmt.Fprintf(&b, " (+%d more)", len(t.editedFiles)-limit)
		}
		b.WriteByte('\n')
	} else {
		b.WriteString("modified_files: none\n")
	}
	if len(t.commands) > 0 {
		b.WriteString("commands:\n")
		for _, c := range t.commands {
			status := "ok"
			if c.Failed {
				status = "error"
			}
			fmt.Fprintf(&b, "- [%s] %s\n", status, c.Command)
		}
	}
	b.WriteString("</executor_receipt>")
	return b.String()
}

func extractStringField(m map[string]any, keys ...string) string {
	if len(m) == 0 {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
