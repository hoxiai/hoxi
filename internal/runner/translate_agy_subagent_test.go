package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	agyBugTaskID   = "prt_msg_turn_tool_2_0"
	agyDocsTaskID  = "prt_msg_turn_tool_2_1"
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

// childRecorder stands in for the host's child session factory.
type childRecorder struct{ specs []ChildSessionSpec }

func (r *childRecorder) create(spec ChildSessionSpec) (SessionInfo, bool) {
	r.specs = append(r.specs, spec)
	return SessionInfo{ID: fmt.Sprintf("ses_agy_child_%d", len(r.specs)), ParentID: "ses_agy", Title: spec.Title}, true
}

func newAgyTestTranslator(lookup func(string, int) string) (*TurnTranslator, *childRecorder) {
	tr := NewTurnTranslator("ses_agy", string(RuntimeAntigravity), "msg_turn", "msg_user", "antigravity", "gemini-3.8-flash-low", "build", "")
	tr.lookupThink = nil
	tr.lookupStep = lookup
	rec := &childRecorder{}
	tr.createChild = rec.create
	return tr, rec
}

func stepOf(line string) (int, string, string) {
	var env agyRawEvent
	_ = json.Unmarshal([]byte(line), &env)
	if env.StepUpdate == nil {
		return -1, "", env.Event
	}
	return env.StepUpdate.StepIndex, env.StepUpdate.State, env.StepUpdate.StepType
}

func eventsFor(evts []SSEEvent, sessionID string) []SSEEvent {
	var out []SSEEvent
	for _, e := range evts {
		if eventSessionID(e) == sessionID {
			out = append(out, e)
		}
	}
	return out
}

func hasStatus(evts []SSEEvent, sessionID, status string) bool {
	for _, e := range evts {
		if st, ok := e.Properties["status"].(SessionStatus); ok && e.Type == "session.status" && eventSessionID(e) == sessionID && st.Type == status {
			return true
		}
	}
	return false
}

func TestAgyRecordedSubagentsBecomeChildSessionsWithReports(t *testing.T) {
	t.Parallel()
	tr, children := newAgyTestTranslator(fixtureTranscriptLookup(t))
	var all []SSEEvent
	for _, line := range readFixtureLines(t, agyRecordedRun) {
		evts := tr.FeedLine(line)
		all = append(all, evts...)
		step, state, kind := stepOf(line)
		bug, docs := tr.partsByID[agyBugTaskID], tr.partsByID[agyDocsTaskID]
		switch {
		case step == 2 && state == "ACTIVE":
			if bug == nil || docs == nil || bug.Tool != "task" || bug.State.Status != "pending" {
				t.Fatalf("each dispatched subagent needs its own pending task part, got %+v %+v", bug, docs)
			}
			if bug.State.Title != agyBugRole || !strings.Contains(bug.State.Input["prompt"].(string), "calc.py") {
				t.Fatalf("task part must show role and prompt: %+v", bug.State)
			}
		case step == 2 && state == "DONE":
			// Dispatch is async: DONE arrives in ~40ms while the subagents keep working.
			if bug.State.Status != "running" || docs.State.Status != "running" {
				t.Fatalf("tasks must run after async dispatch, got %q %q", bug.State.Status, docs.State.Status)
			}
			if len(children.specs) != 2 || children.specs[0].ExternalID != agyBugConv || children.specs[1].Title != agyDocsRole {
				t.Fatalf("one child session per subagent expected, got %+v", children.specs)
			}
			if bug.State.Metadata["sessionId"] != "ses_agy_child_1" {
				t.Fatalf("task must link its child session: %+v", bug.State.Metadata)
			}
			if !hasStatus(evts, "ses_agy_child_1", "busy") || len(eventsFor(evts, "ses_agy_child_2")) < 5 {
				t.Fatal("child sessions must be announced busy with their prompt message")
			}
		case step == 4 && kind == "system_message":
			if docs.State.Status != "completed" || !strings.Contains(docs.State.Output, "README.md") || strings.Contains(docs.State.Output, "SYSTEM_MESSAGE") {
				t.Fatalf("docs report must land on the docs task, got %+v", docs.State)
			}
			if bug.State.Status != "running" {
				t.Fatal("bug reviewer is still working")
			}
			if !hasStatus(evts, "ses_agy_child_2", "idle") {
				t.Fatal("a reported subagent's child session must go idle")
			}
		case step == 6 && state == "DONE":
			if p := tr.partsByID["prt_msg_turn_tool_6"]; p == nil || p.Tool != "agent_status" {
				t.Fatalf("manage_subagents must not render as another task, got %+v", p)
			}
		case step == 8 && kind == "system_message":
			if bug.State.Status != "completed" || !strings.Contains(bug.State.Output, "ZeroDivisionError") {
				t.Fatalf("bug report must land on the bug task, got %+v", bug.State)
			}
		}
	}
	for _, id := range []string{"prt_msg_turn_report_4", "prt_msg_turn_report_8"} {
		if tr.partsByID[id] != nil {
			t.Fatal("attributed reports belong on their task part, not in a separate card")
		}
	}
	if !tr.IsTurnFinished() || tr.ExternalID() != agyParentConv {
		t.Fatalf("finished=%v externalID=%q", tr.IsTurnFinished(), tr.ExternalID())
	}
	for _, e := range all {
		if p, ok := e.Properties["part"].(MessagePart); ok && p.SessionID == "ses_agy" && p.MessageID != "msg_turn" {
			t.Fatalf("parent part with foreign message id: %+v", p)
		}
	}
}

func TestAgySubagentReportsWithoutTranscriptFallBackToDispatchOrder(t *testing.T) {
	t.Parallel()
	tr, _ := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range readFixtureLines(t, agyRecordedRun) {
		tr.FeedLine(line)
		if step, _, kind := stepOf(line); step == 4 && kind == "system_message" {
			if tr.partsByID[agyBugTaskID].State.Status != "completed" || tr.partsByID[agyDocsTaskID].State.Status != "running" {
				t.Fatal("an unattributed report settles the first outstanding subagent")
			}
		}
	}
	if tr.partsByID[agyDocsTaskID].State.Status != "completed" {
		t.Fatal("both subagents must settle")
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
	t.Parallel()
	tr, children := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	task := tr.partsByID[agyBugTaskID]
	if task == nil || task.State.Status != "running" || task.State.Title != "Deep Audit A" || task.State.Input["model"] != "gemini-4-argon-high" {
		t.Fatalf("tool-shaped invoke_subagent must become running tasks, got %+v", task)
	}
	if len(children.specs) != 2 || children.specs[0].Prompt != "Audit backend" {
		t.Fatalf("child sessions must carry the prompts, got %+v", children.specs)
	}

	tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"` + agyParentConv + `","step_index":3,"state":"ACTIVE","step_type":"tool","tool_name":"schedule","tool_info":{"name":"schedule","parameters":{"DurationSeconds":30,"Prompt":"Check if subagent has responded"}}}}`)
	wait := tr.partsByID["prt_msg_turn_tool_3"]
	if wait == nil || wait.Tool != "agent_wait" || wait.State.Metadata["waitingFor"] != "subagents" || wait.State.Title != "Check if subagent has responded" {
		t.Fatalf("schedule must render as waiting for subagents, got %+v", wait)
	}

	evts := tr.FeedLine(`{"event":"result","result":{"conversation_id":"` + agyParentConv + `","status":"SUCCESS","response":"done"}}`)
	if task.State.Status != "completed" || !hasStatus(evts, "ses_agy_child_1", "idle") || !hasStatus(evts, "ses_agy_child_2", "idle") {
		t.Fatal("turn end must settle tasks and idle their child sessions")
	}
}

func TestAgyInterruptedTurnFailsOutstandingSubagents(t *testing.T) {
	t.Parallel()
	tr, _ := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	tr.FeedLine(`{"event":"result","result":{"conversation_id":"` + agyParentConv + `","status":"ERROR","error":"interrupted"}}`)
	if st := tr.partsByID[agyBugTaskID].State.Status; st != "error" {
		t.Fatalf("interrupted turn must not leave the task running, got %q", st)
	}
	if c := tr.children[agyBugTaskID]; !c.closed || c.asst.Finish != "aborted" {
		t.Fatalf("child session must end aborted, got %+v", c.asst)
	}
}

func TestAgyAbandonedTurnClosesChildren(t *testing.T) {
	t.Parallel()
	tr, _ := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	evts := tr.AbandonTurn(1)
	if tr.partsByID[agyDocsTaskID].State.Status != "error" || !hasStatus(evts, "ses_agy_child_2", "idle") {
		t.Fatal("a CLI crash must fail outstanding subagents and idle their sessions")
	}
	if len(tr.ChildSessionIDs()) != 2 {
		t.Fatal("child IDs must be reported for busy cleanup")
	}
}

func TestAgyForeignConversationStepsNeverClobberParent(t *testing.T) {
	t.Parallel()
	tr, _ := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	partCount := len(tr.partOrder)
	// Same step_index as the parent's text part, but from a subagent conversation.
	tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"` + agyBugConv + `","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"subagent text"}}`)
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
}

func TestPollSubagentActivityMirrorsTranscriptIntoChildSession(t *testing.T) {
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
	// Rows 0-3: instructions, run_command, its result, a view_file still running.
	writeRows(rows[:4], os.O_CREATE|os.O_TRUNC)

	tr, _ := newAgyTestTranslator(func(string, int) string { return "" })
	for _, line := range agyDispatchLines() {
		tr.FeedLine(line)
	}
	evts := tr.PollSubagentActivity()
	child := tr.children[agyBugTaskID]
	var tools []*MessagePart
	collect := func() {
		tools = tools[:0]
		for _, p := range child.parts {
			if p.Type == "tool" {
				tools = append(tools, p)
			}
		}
	}
	collect()
	if len(eventsFor(evts, "ses_agy_child_1")) == 0 || len(tools) != 2 {
		t.Fatalf("first poll must mirror the subagent's tool calls, got %d tools", len(tools))
	}
	shell := child.parts[child.partID("tool_1_0")]
	if shell.Tool != "bash" || shell.State.Status != "completed" || shell.State.Input["command"] != "ls -la" || strings.HasPrefix(shell.State.Output, "Created At") {
		t.Fatalf("run_command must be a completed bash step with decoded args: %+v", shell.State)
	}
	if read := child.parts[child.partID("tool_3_0")]; read.Tool != "read" || read.State.Status != "running" {
		t.Fatalf("view_file without a result yet must still run: %+v", read.State)
	}
	if evts := tr.PollSubagentActivity(); len(evts) != 0 {
		t.Fatal("an unchanged transcript must not emit updates")
	}

	writeRows(rows[4:], os.O_APPEND)
	tr.PollSubagentActivity()
	collect()
	if len(tools) != 5 || child.parts[child.partID("tool_9_0")].Tool != "send_message" {
		t.Fatalf("appended rows must add the remaining steps, got %d tools", len(tools))
	}
	if txt := child.parts[child.partID("text_11")]; txt == nil || !strings.Contains(txt.Text, "calc.py") {
		t.Fatal("the subagent's final reply must appear as text")
	}
	if child.asst.Tokens == nil || child.asst.Tokens.Input == 0 || child.asst.Tokens.Output == 0 {
		t.Fatalf("expected subagent child session tokens populated from transcript, got %+v", child.asst.Tokens)
	}
	if tr.msgInfo.Tokens == nil || tr.msgInfo.Tokens.Input != child.asst.Tokens.Input {
		t.Fatalf("expected parent turn tokens to roll up subagent transcript tokens, got parent=%+v child=%+v", tr.msgInfo.Tokens, child.asst.Tokens)
	}

	tr.FeedLine(`{"event":"result","result":{"conversation_id":"` + agyParentConv + `","status":"SUCCESS"}}`)
	if evts := tr.PollSubagentActivity(); evts != nil {
		t.Fatal("polling must stop after the turn finishes")
	}
}

func TestParseAgySystemMessage(t *testing.T) {
	t.Parallel()
	sender, body := parseAgySystemMessage("The following is a <SYSTEM_MESSAGE> not actually sent by the user.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=2026-10-19T03:53:09Z sender=abc-1 priority=MESSAGE_PRIORITY_HIGH content=Report line 1\nline 2\n</SYSTEM_MESSAGE>")
	if sender != "abc-1" || body != "Report line 1\nline 2" {
		t.Fatalf("got sender=%q body=%q", sender, body)
	}
}
