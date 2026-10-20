package runner

import (
	"fmt"
	"strings"
	"sync"
)

// A model that keeps re-issuing tool calls with arguments that fail schema
// validation makes no progress, and every retry costs a full model call. A
// turn is stopped after repeatedToolFailureLimit such failures in a row; any
// successful tool call or any other kind of failure resets the streak.

const (
	repeatedToolFailureLimit = 3
	repeatedToolErrorName    = "RepeatedToolFailureError"
)

type toolFailureStreak struct {
	mu     sync.Mutex
	failed map[string]bool // part IDs in the current streak
	last   string          // tool name of the latest failure
}

// note records a tool part's outcome and reports whether the streak tripped.
func (st *toolFailureStreak) note(part MessagePart) bool {
	if part.Type != "tool" || part.State == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	switch part.State.Status {
	case "completed", "error":
	default:
		return false
	}
	if !isInvalidToolInput(part) {
		st.failed = nil
		return false
	}
	if st.failed == nil {
		st.failed = make(map[string]bool)
	}
	// A part is reported several times while it settles; count it once.
	st.failed[part.ID] = true
	st.last = invalidToolName(part)
	if len(st.failed) < repeatedToolFailureLimit {
		return false
	}
	st.failed = nil
	return true
}

// isInvalidToolInput matches schema-validation failures: OpenCode routes them
// to its `invalid` tool or a ToolInvalidArgumentsError, Claude Code reports an
// InputValidationError.
func isInvalidToolInput(part MessagePart) bool {
	if part.Tool == "invalid" {
		return true
	}
	if part.State.Status != "error" {
		return false
	}
	text := part.State.Error + "\n" + part.State.Output
	return strings.Contains(text, "was called with invalid arguments") ||
		strings.Contains(text, "InputValidationError")
}

func invalidToolName(part MessagePart) string {
	if part.Tool == "invalid" {
		if name, ok := part.State.Input["tool"].(string); ok && name != "" {
			return name
		}
	}
	return part.Tool
}

// noteToolOutcome feeds a tool part of the session's active turn into its
// streak and stops the turn once the model is stuck on malformed input.
func (s *Service) noteToolOutcome(sessionID string, part MessagePart) {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap == nil || !ap.toolFails.note(part) {
		return
	}
	ap.toolFails.mu.Lock()
	tool := ap.toolFails.last
	ap.toolFails.mu.Unlock()
	msg := fmt.Sprintf("Stopped after %d tool calls in a row with invalid arguments (latest: %s). The model kept re-sending malformed input, so Hoxi ended the run to save tokens. Rephrase the task or switch models.", repeatedToolFailureLimit, tool)
	s.abortTurn(ap, &MessageError{Name: repeatedToolErrorName, Data: &MessageErrorData{Message: msg}})
}

// noteToolEvents scans translated CLI events for tool outcomes.
func (s *Service) noteToolEvents(sessionID string, evts []SSEEvent) {
	for _, evt := range evts {
		if evt.Type != "message.part.updated" {
			continue
		}
		if part, ok := evt.Properties["part"].(MessagePart); ok {
			s.noteToolOutcome(sessionID, part)
		} else if part, ok := evt.Properties["part"].(*MessagePart); ok && part != nil {
			s.noteToolOutcome(sessionID, *part)
		}
	}
}
