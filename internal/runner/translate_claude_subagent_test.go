package runner

import (
	"strings"
	"testing"
)

// testdata/claude_subagent.ndjson is a real Claude Code (haiku) stream for a
// prompt that launches one foreground `Agent` subagent which runs Bash + Read.
const claudeAgentCall = "toolu_01JmgWEGdMGnxScFoaRDSA9v"

func TestClaudeRecordedAgentSubagentGetsItsOwnChildSession(t *testing.T) {
	tr := NewTurnTranslator("ses_cld", string(RuntimeClaudeCode), "msg_turn", "msg_user", "claude-code", "haiku", "build", "")
	children := &childRecorder{}
	tr.createChild = children.create
	var all []SSEEvent
	for _, line := range readFixtureLines(t, "testdata/claude_subagent.ndjson") {
		all = append(all, tr.FeedLine(line)...)
	}

	task := tr.partsByID["prt_tool_"+claudeAgentCall]
	if task == nil || task.Tool != "task" {
		t.Fatalf("the Agent tool must render as a task card, got %+v", task)
	}
	if len(children.specs) != 1 || children.specs[0].Title != "List functions in calc.py" || !strings.Contains(children.specs[0].Prompt, "Read tool") {
		t.Fatalf("one child session with the subagent prompt expected, got %+v", children.specs)
	}
	if task.State.Metadata["sessionId"] != "ses_agy_child_1" {
		t.Fatalf("task must link its child session: %+v", task.State.Metadata)
	}
	if task.State.Status != "completed" || !strings.HasPrefix(task.State.Output, "`calc.py` defines two functions") || strings.Contains(task.State.Output, "Subagent hand-back") {
		t.Fatalf("task must complete with the unwrapped report, got %q", task.State.Output)
	}

	for _, p := range tr.partsByID {
		if p.Tool == "bash" || p.Tool == "read" {
			t.Fatalf("subagent steps must not be flattened into the parent: %+v", p)
		}
	}
	child := tr.children[claudeAgentCall]
	var tools []string
	for _, p := range child.parts {
		if p.Type == "tool" {
			if p.State.Status != "completed" {
				t.Fatalf("child step must complete from its tool_result: %+v", p.State)
			}
			tools = append(tools, p.Tool)
		}
	}
	if len(tools) != 2 || !child.closed {
		t.Fatalf("child session must hold the Bash and Read steps and be closed, got %v", tools)
	}
	if !hasStatus(all, "ses_agy_child_1", "idle") {
		t.Fatal("child session must go idle once the subagent reports")
	}
	var parentTextParts int
	for _, p := range tr.partsByID {
		if p.Type == "text" {
			parentTextParts++
		}
	}
	if !tr.IsTurnFinished() || !tr.hasTextPart || parentTextParts != 1 {
		t.Fatalf("the parent's own reply must render exactly once, got textParts=%d", parentTextParts)
	}
}

func TestClaudeBackgroundAgentWaitsForTaskNotification(t *testing.T) {
	tr := NewTurnTranslator("ses_cld", string(RuntimeClaudeCode), "msg_turn", "msg_user", "claude-code", "haiku", "build", "")
	tr.createChild = (&childRecorder{}).create
	tr.FeedLine(`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"tu1","name":"Agent","input":{"description":"bg","prompt":"p","run_in_background":true}}]}}`)
	tr.FeedLine(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"Async agent launched"}]}}`)
	if st := tr.partsByID["prt_tool_tu1"].State.Status; st != "running" {
		t.Fatalf("a background launch acknowledgement must not complete the task, got %q", st)
	}
	tr.FeedLine(`{"type":"system","subtype":"task_notification","tool_use_id":"tu1","status":"completed","summary":"all done"}`)
	if p := tr.partsByID["prt_tool_tu1"]; p.State.Status != "completed" || p.State.Output != "all done" {
		t.Fatalf("task_notification must deliver the report, got %+v", p.State)
	}
}

func TestClaudeSubagentReportUnwrapsHandBackFrame(t *testing.T) {
	framed := "[Subagent hand-back] header text. The report follows:\n  line one\n  \n  - item\nagentId: abc (use SendMessage)\n<usage>x</usage>"
	if got := claudeSubagentReport(framed); got != "line one\n\n- item" {
		t.Fatalf("got %q", got)
	}
	if got := claudeSubagentReport("plain report"); got != "plain report" {
		t.Fatalf("got %q", got)
	}
}
