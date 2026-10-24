package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	geminiHookPreToolPath   = "/api/hoxi/runner/hook/gemini-pretool"
	geminiSystemSettingsEnv = "GEMINI_CLI_SYSTEM_SETTINGS_PATH"
)

type geminiHookPayload struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolUseID     string          `json:"tool_use_id"`
	ToolID        string          `json:"tool_id"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

func geminiHookSettings(exe string) []byte {
	return marshalJSON(map[string]interface{}{
		"hooksConfig": map[string]interface{}{
			"enabled": true,
		},
		"tools": map[string]interface{}{
			"enableHooks": true,
		},
		"hooks": map[string]interface{}{
			"BeforeTool": []interface{}{map[string]interface{}{
				"matcher": ".*",
				"hooks": []interface{}{map[string]interface{}{
					"name":    agyHookName,
					"type":    "command",
					"command": agyHookCommand(exe),
					"timeout": agyhook.TimeoutSeconds * 1000,
				}},
			}},
		},
	}, true)
}

func writeGeminiHookSettingsFile(sessionID, messageID, exe string) (string, error) {
	dir := filepath.Join(hoxipaths.Home(), "sandbox")
	if strings.TrimSpace(hoxipaths.Home()) == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("gemini-settings-%s-%s.json", sessionID, messageID))
	data := append(geminiHookSettings(exe), '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// issueGeminiHook writes a per-turn system settings file wiring Gemini CLI's
// BeforeTool hook to `hoxi agy-hook` and returns the environment variables and
// cleanup function. Read-only modes rely on `--approval-mode plan` and get no hook.
func (s *Service) issueGeminiHook(mode, sessionID, messageID string) ([]string, func()) {
	if isReadOnlyMode(mode) {
		return nil, func() {}
	}
	base, token, revoke := s.issueHookCredential(string(RuntimeGeminiCLI), sessionID, messageID)
	if base == "" {
		return nil, revoke
	}
	exe, err := hookExecutable()
	if err != nil || strings.TrimSpace(exe) == "" {
		revoke()
		return nil, func() {}
	}
	settingsPath, err := writeGeminiHookSettingsFile(sessionID, messageID, exe)
	if err != nil {
		revoke()
		return nil, func() {}
	}
	env := withLoopbackNoProxy([]string{
		geminiSystemSettingsEnv + "=" + settingsPath,
		agyhook.EnvSessionID + "=" + sessionID,
		agyhook.EnvHookURL + "=" + base + geminiHookPreToolPath,
		agyhook.EnvHookToken + "=" + token,
	})
	cleanup := func() {
		revoke()
		_ = os.Remove(settingsPath)
	}
	return env, cleanup
}

func (s *Service) handleGeminiHookPreTool(w http.ResponseWriter, r *http.Request) {
	cred, ok := s.authorizeHook(w, r, string(RuntimeGeminiCLI))
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	var payload geminiHookPayload
	if err != nil || json.Unmarshal(body, &payload) != nil {
		writeHookDecision(w, "deny", "Hoxi could not parse the tool call")
		return
	}
	decision, reason := s.decideGeminiHookCall(r, cred, payload)
	writeHookDecision(w, decision, reason)
}

func (s *Service) decideGeminiHookCall(r *http.Request, cred hookCredential, p geminiHookPayload) (string, string) {
	rec, ok := s.store.Get(cred.sessionID)
	if !ok {
		return "deny", "Hoxi session no longer exists"
	}
	if p.SessionID != "" && rec.ExternalID != "" && p.SessionID != rec.ExternalID {
		return "deny", "Gemini CLI session does not belong to this Hoxi session"
	}
	callID := firstNonEmptyStr(p.ToolUseID, p.ToolID)
	if ok, reason := s.gateDoomLoop(r, cred, p.ToolName, p.ToolInput, callID); !ok {
		return "deny", reason
	}

	var argsMap map[string]interface{}
	if len(p.ToolInput) > 0 {
		_ = json.Unmarshal(p.ToolInput, &argsMap)
	}
	lowerTool := strings.ToLower(strings.TrimSpace(p.ToolName))
	baseDir := firstNonEmptyStr(strings.TrimSpace(p.Cwd), rec.WorktreePath)

	if lowerTool == "run_shell_command" || lowerTool == "bash" || lowerTool == "shell" || lowerTool == "run_command" {
		cmd := firstString(argsMap["command"], argsMap["cmd"], argsMap["CommandLine"])
		desc := firstString(argsMap["description"], argsMap["toolSummary"])
		dir := firstNonEmptyStr(
			resolveHookToolPath(baseDir, map[string]interface{}{
				"dir_path":  argsMap["dir_path"],
				"dirPath":   argsMap["dirPath"],
				"directory": argsMap["directory"],
			}),
			p.Cwd,
		)
		approved, reason := s.gateCommand(r, cred, rec, approvalAsk{
			command:     cmd,
			cwd:         dir,
			description: desc,
			callID:      callID,
			source:      string(RuntimeGeminiCLI),
		})
		if approved {
			return "allow", ""
		}
		return "deny", reason
	}

	switch {
	case lowerTool == "write_file" || lowerTool == "replace" || lowerTool == "edit" || lowerTool == "write":
		path := resolveHookToolPath(baseDir, argsMap)
		if path != "" && pathWithin(rec.WorktreePath, path) && !isProtectedWorktreePath(rec.WorktreePath, path) &&
			s.policyApprovesWorktreeEdit(rec, path) {
			return "allow", ""
		}
		if approved, reason := s.gateToolCallIn(r, cred, p.ToolName, argsMap, callID, p.Cwd); approved {
			return "allow", ""
		} else {
			return "deny", reason
		}

	case lowerTool == "read_file" || lowerTool == "read_many_files":
		if multi := stringList(argsMap["paths"]); len(multi) > 0 {
			allSafe := true
			for _, item := range multi {
				rp := resolveHookToolPath(baseDir, map[string]interface{}{"path": item})
				if rp == "" || !pathWithin(rec.WorktreePath, rp) || isSecretFilePath(rp) {
					allSafe = false
					break
				}
			}
			if allSafe {
				return "allow", ""
			}
		} else {
			path := resolveHookToolPath(baseDir, argsMap)
			if path != "" && pathWithin(rec.WorktreePath, path) && !isSecretFilePath(path) {
				return "allow", ""
			}
		}
		if approved, reason := s.gateToolCallIn(r, cred, p.ToolName, argsMap, callID, p.Cwd); approved {
			return "allow", ""
		} else {
			return "deny", reason
		}

	case lowerTool == "list_directory" || lowerTool == "glob" || lowerTool == "search_file_content" || lowerTool == "grep_search" || lowerTool == "grep" || lowerTool == "ls":
		path := resolveHookToolPath(baseDir, argsMap)
		cwdOK := strings.TrimSpace(p.Cwd) == "" || pathWithin(rec.WorktreePath, p.Cwd)
		if cwdOK && !hasEscapingSearchPattern(argsMap) && (path == "" || pathWithin(rec.WorktreePath, path)) {
			return "allow", ""
		}
		if approved, reason := s.gateToolCallIn(r, cred, p.ToolName, argsMap, callID, p.Cwd); approved {
			return "allow", ""
		} else {
			return "deny", reason
		}

	case isClaudePassthroughTool(lowerTool):
		return "allow", ""
	}

	if approved, reason := s.gateToolCallIn(r, cred, p.ToolName, argsMap, callID, p.Cwd); approved {
		return "allow", ""
	} else {
		return "deny", reason
	}
}
