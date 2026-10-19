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
				"matcher": "Bash",
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
// turn. Plan mode keeps Claude's own read-only policy, so it gets no hook.
func (s *Service) issueClaudeHook(mode, sessionID, messageID string) ([]string, []string, func()) {
	if strings.EqualFold(strings.TrimSpace(mode), "plan") {
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
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&payload); err != nil {
		writeClaudeHookDecision(w, "deny", "Hoxi could not parse the tool call")
		return
	}
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
	if p.ToolName != "Bash" || strings.EqualFold(p.PermissionMode, "plan") {
		return "", ""
	}
	if p.SessionID != "" && rec.ExternalID != "" && p.SessionID != rec.ExternalID {
		return "deny", "Claude Code session does not belong to this Hoxi session"
	}
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
