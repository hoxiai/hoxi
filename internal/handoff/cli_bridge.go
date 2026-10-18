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
	"time"
)

func resolveAgyBinary() string {
	if gRoot := geminiRootDir(); gRoot != "" {
		candidate := filepath.Join(gRoot, "bin", "agy")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
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
	var parts []string
	for _, m := range payload.Messages {
		if m.Role == "system" {
			continue
		}
		txt := extractCodexMessageText(m.Content)
		if txt == "" {
			txt = asString(m.Content)
		}
		if strings.TrimSpace(txt) != "" {
			if len(payload.Messages) > 2 {
				parts = append(parts, fmt.Sprintf("[%s]: %s", m.Role, strings.TrimSpace(txt)))
			} else {
				parts = append(parts, strings.TrimSpace(txt))
			}
		}
	}
	return strings.Join(parts, "\n\n"), model, stream
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

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	prompt, model, stream := extractPromptFromOpenAIMessages(rawBody)
	if prompt == "" {
		prompt = "hi"
	}
	agyBin := resolveAgyBinary()
	if agyBin == "" {
		writeJSONError(w, http.StatusBadGateway, "Antigravity CLI (agy) binary not found in ~/.gemini/bin/agy")
		return
	}
	args := []string{"-p", prompt, "--output-format", "json"}
	if model != "" && !strings.Contains(model, " ") {
		args = append(args, "--model", model)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, agyBin, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		writeJSONError(w, http.StatusBadGateway, "Antigravity CLI failed: "+err.Error())
		return
	}
	var respDoc struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if json.Unmarshal([]byte(lastLine), &respDoc) == nil && respDoc.Response != "" {
		writeOpenAIChatResponse(w, "chatcmpl-agy", model, respDoc.Response, stream)
		return
	}
	writeOpenAIChatResponse(w, "chatcmpl-agy", model, strings.TrimSpace(string(out)), stream)
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
	args := []string{"-p", prompt, "--output-format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	var respDoc struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &respDoc) == nil {
		if respDoc.IsError {
			writeJSONError(w, http.StatusUnauthorized, respDoc.Result)
			return
		}
		writeOpenAIChatResponse(w, "chatcmpl-claude", model, respDoc.Result, stream)
		return
	}
	if len(bytes.TrimSpace(out)) == 0 {
		writeJSONError(w, http.StatusBadGateway, "Claude CLI returned empty output")
		return
	}
	writeOpenAIChatResponse(w, "chatcmpl-claude", model, strings.TrimSpace(string(out)), stream)
}
