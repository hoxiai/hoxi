package handoff

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func resolveAgyBinary() string {
	if gRoot := geminiRootDir(); gRoot != "" {
		candidate := filepath.Join(gRoot, "bin", "agy")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		if os.Getenv("GEMINI_HOME") != "" || os.Getenv("ANTIGRAVITY_HOME") != "" {
			return ""
		}
	}
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	return ""
}

func resolveClaudeBinary() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	if home, _ := os.UserHomeDir(); home != "" {
		candidate := filepath.Join(home, ".local", "bin", "claude")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

var (
	agyModelsMu          sync.Mutex
	agyCachedBinary      string
	agyCachedAt          time.Time
	agyCachedSlugs       []string
	agyCachedDisplayMap  map[string]string
	agyCachedReverseSlug map[string]string
)

func parseAgyModelsOutput(raw string) (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	displayBySlug = map[string]string{}
	slugByDisplayLower = map[string]string{}
	for _, rawLine := range strings.Split(raw, "\n") {
		segments := strings.Split(rawLine, "\r")
		line := strings.TrimSpace(segments[len(segments)-1])
		if line == "" || strings.Contains(line, "Fetching available models") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		slug := strings.TrimSpace(fields[0])
		if slug == "" || strings.ContainsAny(slug, " ()") || !strings.Contains(slug, "-") {
			continue
		}
		display := strings.TrimSpace(strings.TrimPrefix(line, slug))
		slugs = appendUniqueString(slugs, slug)
		if display != "" {
			displayBySlug[slug] = display
			slugByDisplayLower[strings.ToLower(display)] = slug
		}
	}
	return slugs, displayBySlug, slugByDisplayLower
}

func discoverAntigravityCLIModels() (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	agyBin := resolveAgyBinary()
	if agyBin == "" {
		return nil, nil, nil
	}
	agyModelsMu.Lock()
	defer agyModelsMu.Unlock()
	if agyCachedBinary == agyBin && len(agyCachedSlugs) > 0 && time.Since(agyCachedAt) < 10*time.Minute {
		return agyCachedSlugs, agyCachedDisplayMap, agyCachedReverseSlug
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, agyBin, "models")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return agyCachedSlugs, agyCachedDisplayMap, agyCachedReverseSlug
	}
	s, dMap, rMap := parseAgyModelsOutput(string(out))
	if len(s) > 0 {
		agyCachedBinary = agyBin
		agyCachedAt = time.Now()
		agyCachedSlugs = s
		agyCachedDisplayMap = dMap
		agyCachedReverseSlug = rMap
	}
	return agyCachedSlugs, agyCachedDisplayMap, agyCachedReverseSlug
}

func normalizeAntigravityModelID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if _, _, rev := discoverAntigravityCLIModels(); len(rev) > 0 {
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
