package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func toolPart(id, tool, status, errText string) MessagePart {
	return MessagePart{ID: id, Type: "tool", Tool: tool, State: &ToolState{Status: status, Error: errText, Input: map[string]interface{}{"tool": "edit"}}}
}

func TestToolBreakerStopsTurnAfterRepeatedInvalidArguments(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "store.json"))
	ap := &activeProcess{sessionID: "ses_loop", done: make(chan struct{})}
	svc.procs["ses_loop"] = ap

	bad := func(i int) MessagePart {
		return toolPart(fmt.Sprintf("prt_%d", i), "edit", "error", "The edit tool was called with invalid arguments: missing filePath.")
	}
	svc.noteToolOutcome("ses_loop", bad(1))
	svc.noteToolOutcome("ses_loop", bad(1)) // the same part settling again counts once
	svc.noteToolOutcome("ses_loop", bad(2))
	if ap.wasAborted() {
		t.Fatal("two distinct failures must not stop the turn")
	}
	svc.noteToolOutcome("ses_loop", bad(3))
	if !ap.wasAborted() {
		t.Fatal("the third consecutive invalid call must stop the turn")
	}
	err := ap.stall.abortError()
	if err == nil || err.Name != repeatedToolErrorName || !strings.Contains(err.Data.Message, "edit") {
		t.Fatalf("unexpected turn error: %+v", err)
	}
}

func TestToolBreakerStreakResetsOnProgress(t *testing.T) {
	var st toolFailureStreak
	invalid := func(id string) MessagePart {
		return toolPart(id, "invalid", "completed", "")
	}
	st.note(invalid("a"))
	st.note(invalid("b"))
	if st.note(toolPart("c", "read", "completed", "")) {
		t.Fatal("a successful call must not trip the breaker")
	}
	st.note(invalid("d"))
	st.note(toolPart("e", "bash", "error", "exit status 1"))
	st.note(invalid("f"))
	if st.note(invalid("g")) {
		t.Fatal("other failures and successes must reset the streak")
	}
	if !st.note(toolPart("h", "Edit", "error", "<tool_use_error>InputValidationError: file_path is required</tool_use_error>")) {
		t.Fatal("Claude Code validation errors must count")
	}
	if st.note(toolPart("i", "Edit", "running", "")) {
		t.Fatal("running parts are not outcomes")
	}
}
