package opencode

import (
	"encoding/json"
	"fmt"
)

// builtinMode is a Hoxi-owned OpenCode agent. Hoxi injects these through the
// daemon's inline config overlay, which OpenCode applies with the highest
// precedence, so user or project config files cannot widen their permissions.
type builtinMode struct {
	Name        string
	Description string
	Prompt      string
	// Permission replaces the agent's permission block when non-nil.
	// Nil keeps the rules inherited from the global configuration.
	Permission map[string]string
}

const askModePrompt = "You are Ask mode in Hoxi. Answer questions about the user's codebase accurately, " +
	"citing file paths and line numbers you actually read. You are read-only: do not edit, create, move, " +
	"or delete files, and do not run shell commands. If the user asks for a change, explain the change " +
	"that would be needed and suggest switching to Build mode."

const debugModePrompt = "You are Debug mode in Hoxi. Troubleshoot the reported failure systematically: " +
	"reproduce it, form hypotheses, and gather evidence from logs, code, and tool output. Identify the root " +
	"cause before changing anything. Make the smallest fix that addresses the cause, then verify it by " +
	"re-running the failing check. Report the root cause, the fix, and the verification result."

var builtinModes = []builtinMode{
	{
		Name:        "ask",
		Description: "Read-only answers about the codebase. Never edits files or runs shell commands.",
		Prompt:      askModePrompt,
		Permission:  map[string]string{"edit": "deny", "bash": "deny"},
	},
	{
		Name:        "debug",
		Description: "Troubleshoots failures: reproduces, traces the root cause, and applies a minimal verified fix.",
		Prompt:      debugModePrompt,
	},
}

// mergeBuiltinModes writes Hoxi's built-in agents into the inline config map.
// User-defined fields such as a dedicated model survive; the built-in
// description, prompt, mode, and (for ask) permission always win.
func mergeBuiltinModes(config map[string]json.RawMessage) error {
	agents := map[string]json.RawMessage{}
	if raw, exists := config["agent"]; exists {
		if err := json.Unmarshal(raw, &agents); err != nil || agents == nil {
			return fmt.Errorf("OPENCODE_CONFIG_CONTENT agent must be an object")
		}
	}
	for _, mode := range builtinModes {
		entry := map[string]json.RawMessage{}
		if raw, exists := agents[mode.Name]; exists {
			if err := json.Unmarshal(raw, &entry); err != nil || entry == nil {
				return fmt.Errorf("OPENCODE_CONFIG_CONTENT agent.%s must be an object", mode.Name)
			}
		}
		description, err := json.Marshal(mode.Description)
		if err != nil {
			return fmt.Errorf("encode built-in mode %s: %w", mode.Name, err)
		}
		prompt, err := json.Marshal(mode.Prompt)
		if err != nil {
			return fmt.Errorf("encode built-in mode %s: %w", mode.Name, err)
		}
		entry["mode"] = json.RawMessage(`"primary"`)
		entry["description"] = description
		entry["prompt"] = prompt
		if mode.Permission != nil {
			permission, err := json.Marshal(mode.Permission)
			if err != nil {
				return fmt.Errorf("encode built-in mode %s permission: %w", mode.Name, err)
			}
			entry["permission"] = permission
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("encode built-in mode %s: %w", mode.Name, err)
		}
		agents[mode.Name] = encoded
	}
	encoded, err := json.Marshal(agents)
	if err != nil {
		return fmt.Errorf("encode agent overlay: %w", err)
	}
	config["agent"] = encoded
	return nil
}
