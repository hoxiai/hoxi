package runner

import (
	"os"
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

// ShouldGeminiCLIUseAgy reports whether a `gemini-cli` session turn should
// execute via the Antigravity (`agy`) CLI instead of standalone `gemini`.
// Google retired personal Google OAuth (`free-tier` / "Gemini Code Assist for
// individuals") in `@google/gemini-cli` (`IneligibleTierError: UNSUPPORTED_CLIENT`)
// and moved Gemini 4 Argon / 3.8 Flash and personal OAuth accounts to `agy`.
func ShouldGeminiCLIUseAgy(modelID string) bool {
	if strings.TrimSpace(os.Getenv("GEMINI_BIN")) != "" {
		return false
	}
	agyBin := ResolveAgyBinary()
	if agyBin == "" {
		return false
	}
	gemBin := agentcli.ResolveStandaloneGeminiBinary()
	if gemBin == "" || gemBin == agyBin {
		return true
	}
	if !agentcli.HasGeminiCLIDirectAuth() {
		return true
	}
	if agentcli.IsAgyOnlyGeminiModel(modelID) {
		return true
	}
	return false
}

// ResolveGeminiTurnBinary selects the CLI executable (`agy` or `gemini`) for a
// `gemini-cli` turn.
func ResolveGeminiTurnBinary(modelID string) (binPath string, useAgy bool) {
	if ShouldGeminiCLIUseAgy(modelID) {
		return ResolveAgyBinary(), true
	}
	bin := ResolveGeminiBinary()
	if isAgyBinary(bin) {
		return bin, true
	}
	return bin, false
}

// The Windows resolver may return agy.exe or an npm/Scoop command shim.
// Recognize the executable name before choosing the protocol and hook.
func isAgyBinary(path string) bool {
	name := path
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToLower(name)
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		if strings.HasSuffix(name, ext) {
			name = strings.TrimSuffix(name, ext)
			break
		}
	}
	return name == "agy"
}

// ResolveGeminiAgyModelAndEffort maps a `gemini-cli` model slug and effort to
// the slug and effort flag accepted by `agy`.
func ResolveGeminiAgyModelAndEffort(modelID, effort string) (string, string) {
	m := strings.TrimSpace(modelID)
	lower := strings.ToLower(m)
	switch {
	case lower == "" || lower == "auto":
		m = ""
	case strings.Contains(lower, "flash"):
		if !strings.HasPrefix(lower, "gemini-3.8-") {
			m = "gemini-3.8-flash"
		}
	case strings.HasPrefix(lower, "gemini-") && !strings.Contains(lower, "argon") && !strings.HasPrefix(lower, "gemini-4-"):
		m = "gemini-4-argon"
	}
	return ResolveAgyModelAndEffort(m, effort)
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
