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
	g := newDoomLoopGuard()
	for i := 0; i < 5; i++ {
		if g.observe("s", "BashOutput", json.RawMessage(`{"bash_id":"1"}`)) {
			t.Fatal("polling tools must not trip the guard")
		}
	}
	g.observe("s", "Read", json.RawMessage(`{"a":1,"b":2}`))
	g.observe("s", "Read", json.RawMessage(`{ "b": 2, "a": 1 }`))
	if !g.observe("s", "Read", json.RawMessage(`{"b":2,"a":1}`)) {
		t.Fatal("reformatted identical input must count as a repeat")
	}
	g.settle("s", "Read", true)
	for i := 0; i < 5; i++ {
		if g.observe("s", "Read", json.RawMessage(`{"a":1,"b":2}`)) {
			t.Fatal("an always-allowed tool must not be asked again")
		}
	}
	g.observe("t", "Read", json.RawMessage(`{"a":1}`))
	g.observe("t", "Read", json.RawMessage(`{"a":2}`))
	if g.observe("t", "Read", json.RawMessage(`{"a":1}`)) {
		t.Fatal("non-consecutive repeats must not trip the guard")
	}
}
