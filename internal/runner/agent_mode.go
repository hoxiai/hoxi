package runner

import "strings"

// isReadOnlyMode reports whether a Hoxi agent mode must not write files.
// Ask is read-only by contract. Non-OpenCode runtimes (Claude Code, agy,
// Qoder, CodeBuddy) only understand a plan policy, so every read-only mode is
// mapped onto that policy instead of falling through to accept-edits.
func isReadOnlyMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan", "ask":
		return true
	default:
		return false
	}
}
