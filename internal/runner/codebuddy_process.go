package runner

import (
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

// BuildCodeBuddyArgs assembles the stream-json argv for one CodeBuddy CLI turn.
func BuildCodeBuddyArgs(mode, modelID, effort, externalSessionID string) []string {
	permMode := "acceptEdits"
	if isReadOnlyMode(mode) {
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
