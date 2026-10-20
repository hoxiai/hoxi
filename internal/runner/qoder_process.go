package runner

import (
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

// BuildQoderArgs assembles the stream-json argv for one Qoder CLI turn.
func BuildQoderArgs(mode, modelID, effort, externalSessionID string) []string {
	permMode := "accept_edits"
	if isReadOnlyMode(mode) {
		permMode = "default"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--permission-mode", permMode,
	}
	if isReadOnlyMode(mode) {
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
