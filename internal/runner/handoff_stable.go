package runner

import "strings"

// interruptedToolError is the error a tool call gets when its turn ends before
// the call finishes.
const interruptedToolError = "Interrupted before completion"

// isInFlightToolPart reports a tool call that will be finalized as interrupted
// once its turn ends. Task calls are left alone: their subagent can still finish.
func isInFlightToolPart(p MessagePart) bool {
	return p.Type == "tool" && p.Tool != "task" && p.State != nil &&
		(p.State.Status == "pending" || p.State.Status == "running")
}

// openCodeTurnSystem builds the system prompt for an OpenCode start, steer or
// recovery request. All of them must produce the same bytes: one changed byte
// invalidates the provider's prompt cache for everything after it.
func openCodeTurnSystem(base string, rec SessionRecord) string {
	contextText := runtimeContext(rec)
	if contextText == "" {
		return base
	}
	return strings.TrimSpace(base + "\n\n" + contextText)
}

// settledHandoffTurns shows tool calls that are still pending or running the way
// they look once finalized as interrupted. OpenCode resends the quoted history
// on every turn, so it must not change when that finalization happens later.
// Only the turns and parts it changes are copied.
func settledHandoffTurns(turns []MessageTurn) []MessageTurn {
	out := turns
	copiedTurns := false
	for i := range turns {
		copiedParts := false
		for j, p := range turns[i].Parts {
			if !isInFlightToolPart(p) {
				continue
			}
			if !copiedTurns {
				out = append([]MessageTurn(nil), turns...)
				copiedTurns = true
			}
			if !copiedParts {
				out[i].Parts = append([]MessagePart(nil), turns[i].Parts...)
				copiedParts = true
			}
			state := *p.State
			state.Status = "error"
			if strings.TrimSpace(state.Error) == "" {
				state.Error = interruptedToolError
			}
			out[i].Parts[j].State = &state
		}
	}
	return out
}
