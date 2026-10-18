package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func extractPromptFromOpenAIMessages(rawBody []byte) (prompt, model string, stream bool) {
	var payload struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string      `json:"role"`
			Content interface{} `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(rawBody, &payload) != nil {
		return "", "", false
	}
	model = strings.TrimSpace(payload.Model)
	stream = payload.Stream

	type msgItem struct {
		role string
		text string
	}
	var items []msgItem
	for _, m := range payload.Messages {
		if m.Role == "system" {
			continue
		}
		txt := extractCodexMessageText(m.Content)
		if txt == "" {
			txt = asString(m.Content)
		}
		txt = strings.TrimSpace(txt)
		if txt != "" {
			items = append(items, msgItem{role: m.Role, text: txt})
		}
	}
	if len(items) == 0 {
		return "", model, stream
	}
	if len(items) == 1 {
		return items[0].text, model, stream
	}

	// Keep a bounded recent context window (max 6 prior messages, <= 1200 runes each)
	// so Antigravity CLI never truncates step_index 0 or loses the latest user request.
	latest := items[len(items)-1]
	prior := items[:len(items)-1]
	const maxPriorTurns = 6
	if len(prior) > maxPriorTurns {
		prior = prior[len(prior)-maxPriorTurns:]
	}
	var ctxParts []string
	for _, p := range prior {
		ctxParts = append(ctxParts, fmt.Sprintf("[%s]: %s", p.role, truncateRunes(p.text, 1200)))
	}
	return fmt.Sprintf(
		"Recent conversation context:\n%s\n\nCurrent user request:\n%s",
		strings.Join(ctxParts, "\n\n"),
		latest.text,
	), model, stream
}

func writeOpenAIChatResponse(w http.ResponseWriter, idPrefix, model, content string, stream bool) {
	if !stream {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":      idPrefix + fmt.Sprintf("-%d", time.Now().UnixNano()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": content,
					},
					"finish_reason": "stop",
				},
			},
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	chunk := map[string]interface{}{
		"id":      idPrefix + fmt.Sprintf("-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
	}
	b, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", string(b))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func recoverAntigravityTranscriptReply(conversationID, rootDir string) string {
	if strings.TrimSpace(conversationID) == "" {
		return ""
	}
	detail, err := loadGeminiAntigravitySessionDetail(conversationID, rootDir, rootDir)
	if err != nil || detail == nil {
		return ""
	}
	for i := len(detail.Turns) - 1; i >= 0; i-- {
		t := detail.Turns[i]
		if t.Role == "assistant" {
			if strings.TrimSpace(t.Text) != "" {
				return strings.TrimSpace(t.Text)
			}
			if strings.TrimSpace(t.Reasoning) != "" {
				return strings.TrimSpace(t.Reasoning)
			}
		}
	}
	return strings.TrimSpace(detail.LastTurnSummary)
}

func (g *desktopGateway) handleAntigravityCLIBridge(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/api/hoxi/bridge/gemini-antigravity")
	if subPath == "/v1/models" && r.Method == http.MethodGet {
		_, _, models := readGeminiLocalKeyAndModels()
		items := make([]map[string]interface{}, 0, len(models))
		for _, m := range models {
			items = append(items, map[string]interface{}{
				"id":       m,
				"object":   "model",
				"owned_by": "antigravity",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": items})
		return
	}

	// Model requests have no trusted session directory, and agy exposes no
	// documented tool-disable option. Permission prompts alone are not isolation.
	writeJSONError(w, http.StatusServiceUnavailable, "Antigravity CLI bridge disabled: text-only execution is not supported; use a direct API provider")
}

func lastJSONObjectLine(raw []byte) string {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			return line
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func (g *desktopGateway) handleClaudeCLIBridge(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/api/hoxi/bridge/claude-code")
	if subPath == "/v1/models" && r.Method == http.MethodGet {
		_, _, _, models := readClaudeSettingsEnv()
		items := make([]map[string]interface{}, 0, len(models))
		for _, m := range models {
			items = append(items, map[string]interface{}{
				"id":       m,
				"object":   "model",
				"owned_by": "claude-code",
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": items})
		return
	}

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	prompt, model, stream := extractPromptFromOpenAIMessages(rawBody)
	if prompt == "" {
		prompt = "hi"
	}
	claudeBin := resolveClaudeBinary()
	if claudeBin == "" {
		writeJSONError(w, http.StatusBadGateway, "Claude CLI binary not found")
		return
	}
	// This is a text model adapter, not a session-bound agent runner. Disable
	// built-ins, MCP tools and hooks rather than relying on permission defaults.
	args := []string{"-p", prompt, "--output-format", "json",
		"--tools", "", "--disallowedTools", "*",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands", "--settings", `{"disableAllHooks":true}`}
	if model != "" {
		args = append(args, "--model", model)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.WaitDelay = time.Second
	out, cmdErr := cmd.Output()
	var respDoc struct {
		Type              string   `json:"type"`
		Subtype           string   `json:"subtype"`
		IsError           bool     `json:"is_error"`
		Result            string   `json:"result"`
		Errors            []string `json:"errors"`
		PermissionDenials []struct {
			ToolName string `json:"tool_name"`
		} `json:"permission_denials"`
	}
	lastLine := lastJSONObjectLine(out)
	if json.Unmarshal([]byte(lastLine), &respDoc) == nil &&
		(respDoc.Type != "" || respDoc.Subtype != "" || respDoc.Result != "" || respDoc.IsError) {
		if respDoc.IsError {
			errText := firstNonEmpty(strings.TrimSpace(respDoc.Result), strings.Join(respDoc.Errors, "; "), "Claude CLI execution error")
			writeJSONError(w, http.StatusUnauthorized, errText)
			return
		}
		reply := strings.TrimSpace(respDoc.Result)
		if reply == "" && len(respDoc.PermissionDenials) > 0 {
			var denied []string
			for _, d := range respDoc.PermissionDenials {
				if d.ToolName != "" {
					denied = append(denied, d.ToolName)
				}
			}
			reply = "Claude CLI permission denied for tool(s): " + strings.Join(denied, ", ")
		}
		if reply == "" && len(respDoc.Errors) > 0 {
			reply = "Claude CLI error: " + strings.Join(respDoc.Errors, "; ")
		}
		writeOpenAIChatResponse(w, "chatcmpl-claude", model, reply, stream)
		return
	}
	if len(bytes.TrimSpace(out)) == 0 {
		if cmdErr != nil {
			writeJSONError(w, http.StatusBadGateway, "Claude CLI failed: "+cmdErr.Error())
			return
		}
		writeJSONError(w, http.StatusBadGateway, "Claude CLI returned empty output")
		return
	}
	writeOpenAIChatResponse(w, "chatcmpl-claude", model, strings.TrimSpace(string(out)), stream)
}
