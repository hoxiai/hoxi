package runner

import (
	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/runner/turnstream"
)

func isReadOnlyMode(mode string) bool           { return turnstream.IsReadOnlyMode(mode) }
func isAgyBinary(path string) bool              { return turnstream.IsAgyBinary(path) }
func geminiHomeDir() string                     { return agentcli.GeminiHomeDir() }
func ResolveAgyBinary() string                  { return turnstream.ResolveAgyBinary() }
func ResolveClaudeBinary() string               { return turnstream.ResolveClaudeBinary() }
func ResolveGeminiBinary() string               { return turnstream.ResolveGeminiBinary() }
func ShouldGeminiCLIUseAgy(modelID string) bool { return turnstream.ShouldGeminiCLIUseAgy(modelID) }
func ResolveGeminiTurnBinary(modelID string) (string, bool) {
	return turnstream.ResolveGeminiTurnBinary(modelID)
}
func ResolveAgyModelAndEffort(modelID, effort string) (string, string) {
	return turnstream.ResolveAgyModelAndEffort(modelID, effort)
}
func ResolveGeminiAgyModelAndEffort(modelID, effort string) (string, string) {
	return turnstream.ResolveGeminiAgyModelAndEffort(modelID, effort)
}
func ParseAgyModelsOutput(raw string) ([]string, map[string]string, map[string]string) {
	return turnstream.ParseAgyModelsOutput(raw)
}
func DiscoverAgyModels() ([]agentcli.ModelItem, map[string]string, map[string]string) {
	return turnstream.DiscoverAgyModels()
}
func DiscoverAgyModelsWithRefresh(forceRefresh bool) ([]agentcli.ModelItem, map[string]string, map[string]string) {
	return turnstream.DiscoverAgyModelsWithRefresh(forceRefresh)
}
func DiscoverClaudeModels() []agentcli.ModelItem { return turnstream.DiscoverClaudeModels() }
func DiscoverGeminiModels() []agentcli.ModelItem { return turnstream.DiscoverGeminiModels() }
func DiscoverGeminiModelsWithRefresh(forceRefresh bool) []agentcli.ModelItem {
	return turnstream.DiscoverGeminiModelsWithRefresh(forceRefresh)
}
func inferOfficialRunnerModelSpec(slug string) (int, int, float64, float64, bool, bool) {
	return turnstream.InferOfficialRunnerModelSpec(slug)
}
func inferClaudeModelEfforts(slug string) ([]string, string, bool) {
	return turnstream.InferClaudeModelEfforts(slug)
}
func ValidateWorktreeDir(worktreePath, projectRoot string) error {
	return turnstream.ValidateWorktreeDir(worktreePath, projectRoot)
}
func BuildAgyArgs(mode, modelID, effort, externalConversationID string) []string {
	return turnstream.BuildAgyArgs(mode, modelID, effort, externalConversationID)
}
func BuildClaudeArgs(mode, modelID, effort, externalSessionID string) []string {
	return turnstream.BuildClaudeArgs(mode, modelID, effort, externalSessionID)
}
func BuildCodeBuddyArgs(mode, modelID, effort, externalSessionID string) []string {
	return turnstream.BuildCodeBuddyArgs(mode, modelID, effort, externalSessionID)
}
func BuildQoderArgs(mode, modelID, effort, externalSessionID string) []string {
	return turnstream.BuildQoderArgs(mode, modelID, effort, externalSessionID)
}
func BuildGeminiArgs(mode, modelID, effort, externalSessionID string) []string {
	return turnstream.BuildGeminiArgs(mode, modelID, effort, externalSessionID)
}
func BuildCodexArgs(mode, modelID, effort, externalThreadID string) []string {
	return turnstream.BuildCodexArgs(mode, modelID, effort, externalThreadID)
}
func BuildOpenCodeCLIArgs(mode, providerID, modelID, effort, externalSessionID string) []string {
	return turnstream.BuildOpenCodeCLIArgs(mode, providerID, modelID, effort, externalSessionID)
}
func EncodeStreamInputTurn(runtime, promptText string) ([]byte, error) {
	return turnstream.EncodeStreamInputTurn(runtime, promptText)
}
func EncodeStreamInputTurnWithParts(runtime, promptText string, parts []MessagePart) ([]byte, error) {
	return turnstream.EncodeStreamInputTurnWithParts(runtime, promptText, parts)
}
func parseBase64DataURL(dataURL, fallbackMime string) (string, string, []byte, bool) {
	return turnstream.ParseBase64DataURL(dataURL, fallbackMime)
}
