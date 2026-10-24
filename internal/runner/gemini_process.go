package runner

import (
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

func ResolveGeminiBinary() string {
	return agentcli.ResolveGeminiBinary()
}

func DiscoverGeminiModels() []agentcli.ModelItem {
	return agentcli.DiscoverGeminiModels()
}

func DiscoverGeminiModelsWithRefresh(forceRefresh bool) []agentcli.ModelItem {
	return agentcli.DiscoverGeminiModelsWithRefresh(forceRefresh)
}

func BuildGeminiArgs(mode, modelID, _, externalSessionID string) []string {
	approvalMode := "yolo"
	if isReadOnlyMode(mode) {
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
