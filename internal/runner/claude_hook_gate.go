package runner

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/hoxiai/hoxi/internal/agyhook"
)

// `claude -p --permission-prompts none` denies every tool that would prompt,
// so Claude Code turns get a PreToolUse HTTP hook through `--settings`.
// The hook is per process: nothing is written to ~/.claude, and a Claude
// Code session started outside Hoxi never sees it. An `allow` reply grants
// the call in headless mode, `deny` blocks it and the reason reaches the
// model. If the gate is unreachable or replies with something else, Claude
// falls back to its own policy, which denies the call (fail-closed).
// docs/claude-code-approval-hook.md records the probe results.

const (
	claudeHookPreToolPath = "/api/hoxi/runner/hook/claude-pretool"
	claudeHookTokenEnv    = "HOXI_HOOK_TOKEN"
	claudeHookSessionEnv  = "HOXI_RUNNER_SESSION_ID"
)

type claudeHookPayload struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	HookEventName  string `json:"hook_event_name"`
	ToolName       string `json:"tool_name"`
	ToolUseID      string `json:"tool_use_id"`
	ToolInput      struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	} `json:"tool_input"`
	// rawInput is tool_input verbatim, for repeat detection.
	rawInput json.RawMessage
}

type claudeHookOutput struct {
	HookSpecificOutput *claudeHookDecision `json:"hookSpecificOutput,omitempty"`
}

type claudeHookDecision struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

// claudeHookSettings is the inline `--settings` document. Claude Code
// expands the header variables from its own environment, so the token never
// appears in argv.
func claudeHookSettings(hookURL string) string {
	return string(marshalJSON(map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{map[string]interface{}{
				// Every tool is seen for doom-loop detection; only Bash is gated.
				"matcher": "*",
				"hooks": []interface{}{map[string]interface{}{
					"type":    "http",
					"url":     hookURL,
					"timeout": agyhook.TimeoutSeconds,
					"headers": map[string]string{
						"Authorization":       "Bearer $" + claudeHookTokenEnv,
						agyhook.HeaderSession: "$" + claudeHookSessionEnv,
					},
					"allowedEnvVars": []string{claudeHookTokenEnv, claudeHookSessionEnv},
				}},
			}},
		},
	}, false))
}

// issueClaudeHook returns the extra argv and environment for one Claude Code
// turn. Read-only modes (plan, ask) keep Claude's own read-only policy, so they
// get no hook.
func (s *Service) issueClaudeHook(mode, sessionID, messageID string) ([]string, []string, func()) {
	if isReadOnlyMode(mode) {
		return nil, nil, func() {}
	}
	base, token, revoke := s.issueHookCredential(string(RuntimeClaudeCode), sessionID, messageID)
	if base == "" {
		return nil, nil, revoke
	}
	args := []string{"--settings", claudeHookSettings(base + claudeHookPreToolPath)}
	env := withLoopbackNoProxy([]string{
		claudeHookTokenEnv + "=" + token,
		claudeHookSessionEnv + "=" + sessionID,
	})
	return args, env, revoke
}

// withLoopbackNoProxy keeps a user's HTTP(S)_PROXY from routing the hook
// callback away from 127.0.0.1, which would deny every gated command.
func withLoopbackNoProxy(env []string) []string {
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		cur := os.Getenv(key)
		if cur != "" {
			cur += ","
		}
		env = append(env, key+"="+cur+"127.0.0.1,localhost,::1")
	}
	return env
}

func (s *Service) handleClaudeHookPreTool(w http.ResponseWriter, r *http.Request) {
	cred, ok := s.authorizeHook(w, r, string(RuntimeClaudeCode))
	if !ok {
		return
	}
	var payload claudeHookPayload
	var raw struct {
		ToolInput json.RawMessage `json:"tool_input"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err == nil {
		err = json.Unmarshal(body, &payload)
	}
	if err != nil || json.Unmarshal(body, &raw) != nil {
		writeClaudeHookDecision(w, "deny", "Hoxi could not parse the tool call")
		return
	}
	payload.rawInput = raw.ToolInput
	decision, reason := s.decideClaudeHookCall(r, cred, payload)
	writeClaudeHookDecision(w, decision, reason)
}

// decideClaudeHookCall returns "allow", "deny", or "" to leave the call to
// Claude Code's own permission mode.
func (s *Service) decideClaudeHookCall(r *http.Request, cred hookCredential, p claudeHookPayload) (string, string) {
	rec, ok := s.store.Get(cred.sessionID)
	if !ok {
		return "deny", "Hoxi session no longer exists"
	}
	if p.SessionID != "" && rec.ExternalID != "" && p.SessionID != rec.ExternalID {
		return "deny", "Claude Code session does not belong to this Hoxi session"
	}
	if ok, reason := s.gateDoomLoop(r, cred, p.ToolName, p.rawInput, p.ToolUseID); !ok {
		return "deny", reason
	}
	if strings.EqualFold(p.PermissionMode, "plan") {
		return "", ""
	}
	if p.ToolName == "Bash" {
		approved, reason := s.gateCommand(r, cred, rec, approvalAsk{
			command:     p.ToolInput.Command,
			cwd:         p.Cwd,
			description: p.ToolInput.Description,
			callID:      p.ToolUseID,
			source:      string(RuntimeClaudeCode),
		})
		if approved {
			return "allow", ""
		}
		return "deny", reason
	}

	var argsMap map[string]interface{}
	if len(p.rawInput) > 0 {
		_ = json.Unmarshal(p.rawInput, &argsMap)
	}
	baseDir := firstNonEmptyStr(strings.TrimSpace(p.Cwd), rec.WorktreePath)
	lowerTool := strings.ToLower(strings.TrimSpace(p.ToolName))

	switch {
	case lowerTool == "edit" || lowerTool == "multiedit" || lowerTool == "write" || lowerTool == "notebookedit":
		path := resolveHookToolPath(baseDir, argsMap)
		if path != "" && pathWithin(rec.WorktreePath, path) && !isProtectedWorktreePath(rec.WorktreePath, path) {
			return "", ""
		}
		if approved, reason := s.gateToolCall(r, cred, p.ToolName, argsMap, p.ToolUseID); approved {
			return "allow", ""
		} else {
			return "deny", reason
		}

	case lowerTool == "read" || lowerTool == "notebookread":
		path := resolveHookToolPath(baseDir, argsMap)
		if path != "" && pathWithin(rec.WorktreePath, path) {
			return "", ""
		}
		if approved, reason := s.gateToolCall(r, cred, p.ToolName, argsMap, p.ToolUseID); approved {
			return "allow", ""
		} else {
			return "deny", reason
		}

	case lowerTool == "glob" || lowerTool == "grep" || lowerTool == "ls":
		path := resolveHookToolPath(baseDir, argsMap)
		cwdOK := strings.TrimSpace(p.Cwd) == "" || pathWithin(rec.WorktreePath, p.Cwd)
		if cwdOK && (path == "" || pathWithin(rec.WorktreePath, path)) {
			return "", ""
		}
		if approved, reason := s.gateToolCall(r, cred, p.ToolName, argsMap, p.ToolUseID); approved {
			return "allow", ""
		} else {
			return "deny", reason
		}

	case lowerTool == "webfetch" || lowerTool == "websearch" || lowerTool == "remotetrigger" ||
		lowerTool == "mcp" || strings.HasPrefix(lowerTool, "mcp__"):
		if approved, reason := s.gateToolCall(r, cred, p.ToolName, argsMap, p.ToolUseID); approved {
			return "allow", ""
		} else {
			return "deny", reason
		}
	}

	return "", ""
}

func writeClaudeHookDecision(w http.ResponseWriter, decision, reason string) {
	out := claudeHookOutput{}
	if decision != "" {
		out.HookSpecificOutput = &claudeHookDecision{
			HookEventName:            "PreToolUse",
			PermissionDecision:       decision,
			PermissionDecisionReason: reason,
		}
	}
	writeJSON(w, http.StatusOK, out)
}
