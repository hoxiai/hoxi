package runner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/agy_subagents.ndjson is a real `agy` (gemini-3.8-flash-low) stream
// for a prompt that dispatches two reviewer subagents; the transcript fixtures
// are the matching brain logs.
const (
	agyParentConv  = "e8ee1879-66f6-4bf6-9feb-bd47c729070f"
	agyBugConv     = "ca5c164f-82f2-40af-8cb9-73a64a5d7ae5"
	agyDocsConv    = "b71b0905-4721-4402-8fd2-726deb547e14"
	agyBugRole     = "Code Bug Reviewer"
	agyDocsRole    = "Documentation Reviewer"
	agyTaskPartID  = "prt_msg_turn_tool_2"
	agyRecordedRun = "testdata/agy_subagents.ndjson"
)

func readFixtureLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func fixtureTranscriptLookup(t *testing.T) func(string, int) string {
	rows := map[int]string{}
	for _, line := range readFixtureLines(t, "testdata/agy_subagents_parent_transcript.jsonl") {
		var row agyTranscriptRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		rows[row.StepIndex] = row.Content
	}
	return func(conv string, step int) string {
		if conv != agyParentConv {
			t.Errorf("transcript lookup used conversation %q, want parent", conv)
		}
		return rows[step]
	}
}

func newAgyTestTranslator(lookup func(string, int) string) *TurnTranslator {
	tr := NewTurnTranslator("ses_agy", string(RuntimeAntigravity), "msg_turn", "msg_user", "antigravity", "gemini-3.8-flash-low", "build", "")
	tr.lookupThink = nil
	tr.lookupStep = lookup
	return tr
}

func stepOf(line string) (int, string, string) {
	var env agyRawEvent
	_ = json.Unmarshal([]byte(line), &env)
	if env.StepUpdate == nil {
		return -1, "", env.Event
	}
	return env.StepUpdate.StepIndex, env.StepUpdate.State, env.StepUpdate.StepType
}

func subagentMeta(t *testing.T, part *MessagePart) (reported, total int) {
	t.Helper()
	if part == nil || part.State == nil || part.State.Metadata == nil {
		t.Fatalf("task part has no subagent metadata: %+v", part)
	}
	return part.State.Metadata["reported"].(int), part.State.Metadata["total"].(int)
}

func TestAgyRecordedSubagentTurnStaysRunningUntilReportsArrive(t *testing.T) {
	tr := newAgyTestTranslator(fixtureTranscriptLookup(t))
	for _, line := range readFixtureLines(t, agyRecordedRun) {
		tr.FeedLine(line)
		step, state, kind := stepOf(line)
		task := tr.partsByID[agyTaskPartID]
		switch {
		case step == 2 && state == "DONE":
			if task == nil || task.Tool != "task" {
				t.Fatalf("subagent dispatch must become a task part, got %+v", task)
			}
			// Dispatch is async: DONE arrives in ~40ms while the subagents keep working.
			if task.State.Status != "running" {
				t.Fatalf("task must stay running after async dispatch, got %q", task.State.Status)
			}
			if task.State.Title != agyBugRole+", "+agyDocsRole {
				t.Fatalf("unexpected task title %q", task.State.Title)
			}
			if r, n := subagentMeta(t, task); r != 0 || n != 2 {
				t.Fatalf("want 0/2 reported, got %d/%d", r, n)
			}
			if tr.subagentByConv[agyBugConv] == nil || tr.subagentByConv[agyDocsConv] == nil {
				t.Fatal("subagent conversation IDs from the DONE dispatch must be tracked")
			}
		case step == 4 && kind == "system_message":
			report := tr.partsByID["prt_msg_turn_report_4"]
			if report == nil || report.Tool != "agent_report" || report.State.Title != agyDocsRole {
				t.Fatalf("docs report must render as a collapsed report part, got %+v", report)
			}
			if strings.Contains(report.State.Output, "SYSTEM_MESSAGE") || !strings.Contains(report.State.Output, "README.md") {
				t.Fatalf("report body not extracted: %q", report.State.Output)
			}
			if r, _ := subagentMeta(t, task); r != 1 || task.State.Status != "running" {
				t.Fatalf("one report in: want running 1/2, got %s %d", task.State.Status, r)
			}
		case step == 6 && state == "DONE":
			if p := tr.partsByID["prt_msg_turn_tool_6"]; p == nil || p.Tool != "agent_status" {
				t.Fatalf("manage_subagents must not render as another task, got %+v", p)
			}
		case step == 8 && kind == "system_message":
			if task.State.Status != "completed" || task.State.Time.End == 0 {
				t.Fatalf("task must complete once both subagents reported, got %+v", task.State)
			}
			if report := tr.partsByID["prt_msg_turn_report_8"]; report == nil || report.State.Title != agyBugRole {
				t.Fatalf("bug report must be attributed by sender, got %+v", report)
			}
		}
	}
	if !tr.IsTurnFinished() || tr.ExternalID() != agyParentConv {
		t.Fatalf("finished=%v externalID=%q", tr.IsTurnFinished(), tr.ExternalID())
	}
	if !strings.HasPrefix(tr.partsByID["prt_msg_turn_text_9"].Text, "Both subagent reports have been received") {
		t.Fatal("final summary text missing")
	}
}

func TestAgySubagentReportsWithoutTranscriptStillSettleTask(t *testing.T) {
	tr := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range readFixtureLines(t, agyRecordedRun) {
		tr.FeedLine(line)
		if step, _, kind := stepOf(line); step == 4 && kind == "system_message" {
			if r, _ := subagentMeta(t, tr.partsByID[agyTaskPartID]); r != 1 {
				t.Fatalf("system message must count as a report, got %d", r)
			}
		}
	}
	if task := tr.partsByID[agyTaskPartID]; task.State.Status != "completed" {
		t.Fatalf("task must complete, got %q", task.State.Status)
	}
}

func agyDispatchLines() []string {
	return []string{
		`{"event":"init","conversation_id":"` + agyParentConv + `","init":{"model":"gemini-4-argon-high"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"Delegating."}}`,
		`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"invoke_subagent","tool_info":{"name":"invoke_subagent","parameters":{"Subagents":[{"Role":"Deep Audit A","TypeName":"research","Model":"gemini-4-argon-high","Prompt":"Audit backend"},{"Role":"Deep Audit B","TypeName":"research","Prompt":"Audit frontend"}]}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":2,"state":"DONE","step_type":"tool","tool_name":"invoke_subagent","subagent_info":{"subagents":[{"role":"Deep Audit A","conversation_id":"` + agyBugConv + `"},{"role":"Deep Audit B","conversation_id":"` + agyDocsConv + `"}]}}}`,
	}
}

func TestAgyToolShapedDispatchAndScheduleWait(t *testing.T) {
	tr := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	task := tr.partsByID[agyTaskPartID]
	if task == nil || task.Tool != "task" || task.State.Status != "running" || task.State.Title != "Deep Audit A, Deep Audit B" {
		t.Fatalf("tool-shaped invoke_subagent must become a running task, got %+v", task)
	}
	subs := task.State.Input["subagents"].([]map[string]interface{})
	if subs[0]["prompt"] != "Audit backend" || subs[0]["model"] != "gemini-4-argon-high" {
		t.Fatalf("subagent prompt/model missing from input: %+v", subs)
	}

	tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":3,"state":"DONE","step_type":"tool","tool_name":"schedule","tool_info":{"name":"schedule","parameters":{"DurationSeconds":30,"Prompt":"Check if subagent has responded"}}}}`)
	wait := tr.partsByID["prt_msg_turn_tool_3"]
	if wait == nil || wait.Tool != "agent_wait" || wait.State.Metadata["waitingFor"] != "subagents" {
		t.Fatalf("schedule must render as waiting for subagents, got %+v", wait)
	}
	if wait.State.Title != "Check if subagent has responded" {
		t.Fatalf("unexpected schedule title %q", wait.State.Title)
	}

	evts := tr.FeedLine(`{"event":"result","result":{"conversation_id":"` + agyParentConv + `","status":"SUCCESS","response":"done"}}`)
	if task.State.Status != "completed" {
		t.Fatalf("turn end must settle the task, got %q", task.State.Status)
	}
	found := false
	for _, e := range evts {
		if p, ok := e.Properties["part"].(MessagePart); ok && p.ID == agyTaskPartID {
			found = true
		}
	}
	if !found {
		t.Fatal("result must broadcast the settled task part")
	}
}

func TestAgyInterruptedTurnFailsOutstandingSubagents(t *testing.T) {
	tr := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	tr.FeedLine(`{"event":"result","result":{"conversation_id":"` + agyParentConv + `","status":"ERROR","error":"interrupted"}}`)
	if st := tr.partsByID[agyTaskPartID].State.Status; st != "error" {
		t.Fatalf("interrupted turn must not leave the task running, got %q", st)
	}
}

func TestAgyForeignConversationStepsNeverClobberParent(t *testing.T) {
	tr := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	partCount := len(tr.partOrder)
	// Same step_index as the parent's text part, but from a subagent conversation.
	tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"` + agyBugConv + `","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"subagent text"}}`)
	tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"` + agyBugConv + `","step_index":4,"state":"ACTIVE","step_type":"tool","tool_name":"run_command"}}`)
	tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"unrelated-conv","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"stray"}}`)

	if len(tr.partOrder) != partCount {
		t.Fatalf("foreign steps must not create parts: %v", tr.partOrder)
	}
	if got := tr.partsByID["prt_msg_turn_text_1"].Text; got != "Delegating." {
		t.Fatalf("parent text clobbered: %q", got)
	}
	if tr.ExternalID() != agyParentConv {
		t.Fatalf("externalID must stay pinned to the parent, got %q", tr.ExternalID())
	}
	sub := tr.subagentByConv[agyBugConv]
	if sub.steps != 5 || sub.currentTool != "run_command" {
		t.Fatalf("foreign steps must advance subagent progress, got steps=%d tool=%q", sub.steps, sub.currentTool)
	}
}

func TestPollSubagentActivityTailsTranscriptIncrementally(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), agyBugConv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "transcript.jsonl")
	rows := readFixtureLines(t, "testdata/agy_subagent_child_transcript.jsonl")
	writeRows := func(rs []string, flag int) {
		f, err := os.OpenFile(logPath, flag|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(strings.Join(rs, "\n") + "\n")
		_ = f.Close()
	}
	writeRows(rows[:4], os.O_CREATE|os.O_TRUNC)

	tr := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	evts := tr.PollSubagentActivity()
	if len(evts) != 1 {
		t.Fatalf("first poll must report progress, got %d events", len(evts))
	}
	sub := tr.subagentByConv[agyBugConv]
	if sub.steps != 4 || sub.currentTool != "view_file" || sub.lastActivityMs == 0 {
		t.Fatalf("unexpected progress steps=%d tool=%q", sub.steps, sub.currentTool)
	}
	if evts := tr.PollSubagentActivity(); len(evts) != 0 {
		t.Fatal("an unchanged transcript must not emit updates")
	}

	writeRows(rows[4:], os.O_APPEND)
	if evts := tr.PollSubagentActivity(); len(evts) != 1 {
		t.Fatal("appended rows must emit one update")
	}
	if sub.steps != len(rows) || sub.currentTool != "send_message" {
		t.Fatalf("want %d steps ending in send_message, got %d %q", len(rows), sub.steps, sub.currentTool)
	}
	meta := tr.partsByID[agyTaskPartID].State.Metadata["subagents"].([]map[string]interface{})
	if meta[0]["steps"] != len(rows) {
		t.Fatalf("task metadata not refreshed: %+v", meta[0])
	}

	tr.FeedLine(`{"event":"result","result":{"conversation_id":"` + agyParentConv + `","status":"SUCCESS"}}`)
	if evts := tr.PollSubagentActivity(); evts != nil {
		t.Fatal("polling must stop after the turn finishes")
	}
}

func TestParseAgySystemMessage(t *testing.T) {
	sender, body := parseAgySystemMessage("The following is a <SYSTEM_MESSAGE> not actually sent by the user.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=2026-10-19T03:53:09Z sender=abc-1 priority=MESSAGE_PRIORITY_HIGH content=Report line 1\nline 2\n</SYSTEM_MESSAGE>")
	if sender != "abc-1" || body != "Report line 1\nline 2" {
		t.Fatalf("got sender=%q body=%q", sender, body)
	}
}
