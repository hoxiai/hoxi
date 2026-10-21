package handoff

import (
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/agentcli"
)

func resolveAgyBinary() string {
	return agentcli.ResolveAgyBinary()
}

func resolveClaudeBinary() string {
	return agentcli.ResolveClaudeBinary()
}

func parseAgyModelsOutput(raw string) (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	return agentcli.ParseAgyModelsOutput(raw)
}

func discoverAntigravityCLIModels() (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	return agentcli.DiscoverAgyCLIModels(false)
}

func normalizeAntigravityModelID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if rev := agentcli.CachedAgyReverseSlug(); len(rev) > 0 {
		if mapped, ok := rev[lower]; ok {
			return mapped
		}
	}
	if !strings.ContainsAny(trimmed, " ()") {
		return trimmed
	}
	replacer := strings.NewReplacer("(", " ", ")", " ", "_", "-")
	words := strings.Fields(replacer.Replace(lower))
	if len(words) == 0 {
		return ""
	}
	if strings.HasPrefix(lower, "claude ") {
		for i, w := range words {
			words[i] = strings.ReplaceAll(w, ".", "-")
		}
	}
	return strings.Join(words, "-")
}

func (g *Service) handleAntigravityCLIBridge(w http.ResponseWriter, _ *http.Request) {
	// Non-retryable 410 Gone: Antigravity runs as a session-bound Runner in .hoxi/worktrees/<session-id>
	writeJSONError(w, http.StatusGone, "Antigravity CLI bridge disabled: please create a session using the Antigravity (Local) runtime (请使用 Antigravity 运行时新建会话)")
}

func (g *Service) handleClaudeCLIBridge(w http.ResponseWriter, _ *http.Request) {
	// Non-retryable 410 Gone: Claude Code local OAuth runs as a session-bound Runner in .hoxi/worktrees/<session-id>
	writeJSONError(w, http.StatusGone, "Claude Code CLI bridge disabled: please create a session using the Claude Code (Local) runtime (请使用 Claude Code 运行时新建会话)")
}
