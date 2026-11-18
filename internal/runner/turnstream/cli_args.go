package turnstream

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/runner/sessionstore"
)

// IsReadOnlyMode reports whether a Hoxi agent mode must not write files.
func IsReadOnlyMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan", "ask":
		return true
	default:
		return false
	}
}

func ValidateWorktreeDir(worktreePath, projectRoot string) error {
	wt := strings.TrimSpace(worktreePath)
	if wt == "" {
		return errors.New("runner worktree path is required")
	}
	absWT, err := filepath.Abs(wt)
	if err != nil {
		return fmt.Errorf("resolve worktree path: %w", err)
	}
	st, err := os.Stat(absWT)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("worktree directory does not exist: %s", absWT)
	}
	slashWT := filepath.ToSlash(filepath.Clean(absWT))
	if !strings.Contains(slashWT, "/.hoxi/worktrees/") {
		return fmt.Errorf("refusing to run CLI outside isolated worktree (.hoxi/worktrees/<session-id>): %s", absWT)
	}
	if home, _ := os.UserHomeDir(); home != "" {
		if sessionstore.NormalizeComparePath(absWT) == sessionstore.NormalizeComparePath(home) {
			return errors.New("refusing to run CLI in user home directory")
		}
	}
	if strings.TrimSpace(projectRoot) != "" {
		if sessionstore.NormalizeComparePath(absWT) == sessionstore.NormalizeComparePath(projectRoot) {
			return errors.New("refusing to run CLI directly in main repository root")
		}
	}
	return nil
}

func BuildAgyArgs(mode, modelID, effort, externalConversationID string) []string {
	agyMode := "accept-edits"
	if IsReadOnlyMode(mode) {
		agyMode = "plan"
	}
	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--sandbox",
		"--mode", agyMode,
	}
	resolvedModel, resolvedEffort := ResolveAgyModelAndEffort(modelID, effort)
	if resolvedModel != "" && !strings.EqualFold(resolvedModel, "auto") {
		args = append(args, "--model", resolvedModel)
	}
	if resolvedEffort != "" {
		args = append(args, "--effort", resolvedEffort)
	}
	if cid := strings.TrimSpace(externalConversationID); cid != "" {
		args = append(args, "--conversation", cid)
	}
	args = append(args, "-p=")
	return args
}

func BuildClaudeArgs(mode, modelID, effort, externalSessionID string) []string {
	permMode := "acceptEdits"
	if IsReadOnlyMode(mode) {
		permMode = "plan"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", permMode,
		"--permission-prompts", "none",
	}
	m := strings.TrimSpace(modelID)
	if m != "" {
		args = append(args, "--model", m)
	}
	if e := agentcli.NormalizeEffortName(effort); e != "" {
		efforts, defEffort, supportsEffort := inferClaudeModelEfforts(m)
		if supportsEffort {
			if clamped := agentcli.ClampEffortToSupported(e, efforts, defEffort); clamped != "" {
				args = append(args, "--effort", clamped)
			}
		}
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "--resume", sid)
	}
	return args
}

func BuildCodeBuddyArgs(mode, modelID, effort, externalSessionID string) []string {
	permMode := "acceptEdits"
	if IsReadOnlyMode(mode) {
		permMode = "plan"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", permMode,
	}
	m := strings.TrimSpace(modelID)
	if m != "" && m != "default-model" {
		args = append(args, "--model", m)
	}
	if e := agentcli.CodeBuddyReasoningEffort(m, effort); e != "" {
		args = append(args, "--effort", e)
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "--resume", sid)
	}
	return args
}

func BuildQoderArgs(mode, modelID, effort, externalSessionID string) []string {
	permMode := "accept_edits"
	if IsReadOnlyMode(mode) {
		permMode = "default"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--permission-mode", permMode,
	}
	if IsReadOnlyMode(mode) {
		args = append(args, "--disallowed-tools", "Edit,Write,MultiEdit")
	}
	_, cleanModel := agentcli.ResolveQoderBinaryForModel(modelID)
	m := strings.TrimSpace(cleanModel)
	if m != "" && !strings.EqualFold(m, "auto") {
		args = append(args, "--model", m)
	}
	if e := agentcli.QoderReasoningEffort(m, effort); e != "" {
		args = append(args, "--reasoning-effort", e)
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "--resume", sid)
	}
	return args
}

func BuildGeminiArgs(mode, modelID, _, externalSessionID string) []string {
	approvalMode := "yolo"
	if IsReadOnlyMode(mode) {
		approvalMode = "plan"
	}
	args := []string{
		"--output-format", "stream-json",
		"--approval-mode", approvalMode,
	}
	m := strings.TrimSpace(modelID)
	if m != "" && !strings.EqualFold(m, "auto") {
		args = append(args, "--model", m)
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "--resume", sid)
	}
	args = append(args, "-p", "")
	return args
}

func BuildCodexArgs(mode, modelID, effort, externalThreadID string) []string {
	m := strings.TrimSpace(modelID)
	eff := agentcli.NormalizeEffortName(effort)
	tid := strings.TrimSpace(externalThreadID)
	if tid != "" {
		args := []string{"exec", "resume", tid, "--json", "--skip-git-repo-check"}
		if m != "" && !strings.EqualFold(m, "auto") {
			args = append(args, "-m", m)
		}
		if eff != "" {
			args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", eff))
		}
		args = append(args, "-")
		return args
	}
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	if IsReadOnlyMode(mode) {
		args = append(args, "-s", "read-only")
	} else {
		args = append(args, "--full-auto")
	}
	if m != "" && !strings.EqualFold(m, "auto") {
		args = append(args, "-m", m)
	}
	if eff != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", eff))
	}
	args = append(args, "-")
	return args
}

func BuildOpenCodeCLIArgs(mode, providerID, modelID, effort, externalSessionID string) []string {
	agentName := "build"
	if IsReadOnlyMode(mode) {
		agentName = "plan"
	}
	args := []string{"run", "--format", "json", "--agent", agentName}
	m := strings.TrimSpace(modelID)
	p := strings.TrimSpace(providerID)
	if m != "" {
		if !strings.Contains(m, "/") && p != "" && p != string(RuntimeOpenCode) {
			m = p + "/" + m
		}
		args = append(args, "-m", m)
	}
	if eff := agentcli.NormalizeEffortName(effort); eff != "" {
		args = append(args, "--variant", eff)
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "-s", sid)
	}
	return args
}
