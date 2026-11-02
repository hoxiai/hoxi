package runner

import (
	"encoding/json"
	"strings"
	"testing"
)

func (f *claudeGateFixture) toolPayload(tool string, input map[string]interface{}) []byte {
	raw, _ := json.Marshal(map[string]interface{}{
		"session_id":      "cc-uuid-1",
		"cwd":             f.dir,
		"permission_mode": "acceptEdits",
		"hook_event_name": "PreToolUse",
		"tool_name":       tool,
		"tool_use_id":     "toolu_01abc",
		"tool_input":      input,
	})
	return raw
}

func TestClaudeHookAsksBeforeTheThirdIdenticalCall(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()
	edit := map[string]interface{}{"file_path": "a.go", "old_string": "x", "new_string": "y"}
	for i := 0; i < doomLoopThreshold-1; i++ {
		if d := f.decide(t, f.toolPayload("Edit", edit)); d != "" {
			t.Fatalf("call %d: non-Bash tools must stay with Claude's own policy, got %q", i+1, d)
		}
	}
	result := f.asyncDecide(t, f.toolPayload("Edit", edit))
	asked := waitForEvent(t, events, "permission.asked")
	if asked.Properties["permission"] != "doom_loop" {
		t.Fatalf("expected a doom_loop request, got %+v", asked.Properties)
	}
	id, _ := asked.Properties["id"].(string)
	f.reply(t, id, "reject", "")
	if d := <-result; !strings.HasPrefix(d, "deny|Hoxi stopped a repeated tool call: Edit") {
		t.Fatalf("rejected loop must reach the model as a deny, got %q", d)
	}
	// The answer resets the run, so the next identical call is not asked again.
	if d := f.decide(t, f.toolPayload("Edit", edit)); d != "" {
		t.Fatalf("expected no decision after the run was reset, got %q", d)
	}
}

func TestDoomLoopGuardIgnoresPollingAndKeyOrder(t *testing.T) {
	t.Parallel()
	g := newDoomLoopGuard()
	for i := 0; i < 5; i++ {
		if g.observe("s", "m", "BashOutput", json.RawMessage(`{"bash_id":"1"}`)) {
			t.Fatal("polling tools must not trip the guard")
		}
	}
	g.observe("s", "m", "Read", json.RawMessage(`{"a":1,"b":2}`))
	g.observe("s", "m", "Read", json.RawMessage(`{ "b": 2, "a": 1 }`))
	if !g.observe("s", "m", "Read", json.RawMessage(`{"b":2,"a":1}`)) {
		t.Fatal("reformatted identical input must count as a repeat")
	}
	g.settle("s", "Read", true)
	for i := 0; i < 5; i++ {
		if g.observe("s", "m", "Read", json.RawMessage(`{"a":1,"b":2}`)) {
			t.Fatal("an always-allowed tool must not be asked again")
		}
	}
	g.observe("t", "m", "Read", json.RawMessage(`{"a":1}`))
	g.observe("t", "m", "Read", json.RawMessage(`{"a":2}`))
	if g.observe("t", "m", "Read", json.RawMessage(`{"a":1}`)) {
		t.Fatal("non-consecutive repeats must not trip the guard")
	}
}

func TestDoomLoopGuardResetsBetweenTurns(t *testing.T) {
	t.Parallel()
	g := newDoomLoopGuard()
	cmd := json.RawMessage(`{"command":"go test ./..."}`)
	for _, turn := range []string{"msg_1", "msg_2", "msg_3", "msg_4"} {
		if g.observe("s", turn, "Bash", cmd) {
			t.Fatalf("the same check run once per user turn was treated as a loop (%s)", turn)
		}
	}
	g.observe("s", "msg_5", "Bash", cmd)
	g.observe("s", "msg_5", "Bash", cmd)
	if !g.observe("s", "msg_5", "Bash", cmd) {
		t.Fatal("repeats within one turn must still trip the guard")
	}
}

func TestDoomLoopGuardCatchesInterleavedNoEditLoopAndAllowsPostEditReverification(t *testing.T) {
	t.Parallel()
	g := newDoomLoopGuard()
	testCmd := json.RawMessage(`{"command":"go test ./internal/auth/..."}`)
	readA := json.RawMessage(`{"file_path":"internal/auth/handler.go"}`)
	readB := json.RawMessage(`{"file_path":"internal/auth/token.go"}`)

	// Interleaving reads with the exact same test command without editing any file
	// must trip on the 3rd execution in the same turn.
	if g.observe("s", "turn_1", "Bash", testCmd) {
		t.Fatal("1st test run should not trip")
	}
	if g.observe("s", "turn_1", "Read", readA) {
		t.Fatal("1st read should not trip")
	}
	if g.observe("s", "turn_1", "Bash", testCmd) {
		t.Fatal("2nd test run should not trip")
	}
	if g.observe("s", "turn_1", "Read", readB) {
		t.Fatal("2nd read should not trip")
	}
	if !g.observe("s", "turn_1", "Bash", testCmd) {
		t.Fatal("3rd identical test run without any intervening file edit must trip the guard")
	}

	// Conversely, running the same test command after each file edit in a turn is productive.
	for i := 1; i <= 4; i++ {
		editInput := json.RawMessage(`{"file_path":"internal/auth/handler.go","old_string":"a","new_string":"b"}`)
		if g.observe("s2", "turn_2", "Edit", editInput) {
			t.Fatalf("productive edit %d should not trip", i)
		}
		if g.observe("s2", "turn_2", "Bash", testCmd) {
			t.Fatalf("re-running test after edit %d must not trip the guard", i)
		}
	}
}

func TestDoomLoopGuardExemptsReadOnlyTaskPollingAndTripsMutatingTaskActions(t *testing.T) {
	t.Parallel()
	g := newDoomLoopGuard()
	pollingCalls := []struct {
		tool  string
		input json.RawMessage
	}{
		{"manage_task", json.RawMessage(`{"Action":"status","TaskId":"conv/task-1"}`)},
		{"manage_task", json.RawMessage(`{"Action":"\"status\"","TaskId":"\"conv/task-1\""}`)},
		{"manage_task", json.RawMessage(`{"Action":"list"}`)},
		{"manage_subagents", json.RawMessage(`{"Action":"list"}`)},
		{"manage_subagents", json.RawMessage(`{"Action":"\"list\""}`)},
		{"wait", json.RawMessage(`{"duration_ms":5000}`)},
		{"wait_5_seconds", json.RawMessage(`{}`)},
	}
	for _, tc := range pollingCalls {
		for i := 0; i < 6; i++ {
			if g.observe("s_poll", "m1", tc.tool, tc.input) {
				t.Fatalf("read-only polling call %s %s must not trip doom_loop on iteration %d", tc.tool, tc.input, i+1)
			}
		}
	}

	mutatingCalls := []struct {
		session string
		tool    string
		input   json.RawMessage
	}{
		{"s_kill", "manage_task", json.RawMessage(`{"Action":"kill","TaskId":"conv/task-1"}`)},
		{"s_kill_q", "manage_task", json.RawMessage(`{"Action":"\"send_input\"","TaskId":"\"conv/task-1\"","Input":"\"y\""}`)},
		{"s_sub_kill", "manage_subagents", json.RawMessage(`{"Action":"kill","ConversationIds":["sub-1"]}`)},
	}
	for _, tc := range mutatingCalls {
		if g.observe(tc.session, "m1", tc.tool, tc.input) {
			t.Fatalf("1st mutating call %s %s must not trip", tc.tool, tc.input)
		}
		if g.observe(tc.session, "m1", tc.tool, tc.input) {
			t.Fatalf("2nd mutating call %s %s must not trip", tc.tool, tc.input)
		}
		if !g.observe(tc.session, "m1", tc.tool, tc.input) {
			t.Fatalf("3rd identical mutating call %s %s must trip doom_loop", tc.tool, tc.input)
		}
	}
}

func TestAgyHookAllowsReadOnlyTaskPollingAndGatesMutatingTaskControl(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	defer f.revoke()
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeManual})
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()

	toolBody := func(name string, args map[string]interface{}) []byte {
		raw, _ := json.Marshal(map[string]interface{}{
			"conversationId": "conv-1",
			"stepIdx":        9,
			"workspacePaths": []string{f.dir},
			"toolCall": map[string]interface{}{
				"name": name,
				"args": args,
			},
		})
		return raw
	}

	for i := 0; i < 5; i++ {
		if d := f.callHook(t, toolBody("manage_task", map[string]interface{}{
			"Action": `"status"`,
			"TaskId": `"conv-1/task-180"`,
		})); d.Decision != "auto_approve" {
			t.Fatalf("poll %d: expected auto_approve for manage_task(status), got %+v", i+1, d)
		}
		if d := f.callHook(t, toolBody("manage_subagents", map[string]interface{}{
			"Action": `"list"`,
		})); d.Decision != "auto_approve" {
			t.Fatalf("poll %d: expected auto_approve for manage_subagents(list), got %+v", i+1, d)
		}
	}

	// Mutating manage_task(kill) must not be treated as a read-only poll and must gate in manual mode.
	ch := f.asyncHook(t, toolBody("manage_task", map[string]interface{}{
		"Action": `"kill"`,
		"TaskId": `"conv-1/task-180"`,
	}))
	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	f.reply(t, id, "reject", "")
	if d := waitDecision(t, ch, "manage_task(kill)"); d.Decision != "deny" {
		t.Fatalf("expected deny for rejected manage_task(kill), got %+v", d)
	}
	if asked.Properties["permission"] != "tool" {
		t.Fatalf("expected tool permission request for kill action, got %+v", asked.Properties)
	}
}

