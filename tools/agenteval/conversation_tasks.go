package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

const keyContract = "Trim only outer whitespace and preserve case and internal slashes."
const keyFollowUp = "Now update the lookup function in lookup.mjs to honor our earlier contract on incoming keys. Keep missing entries undefined."
const reportFollowUp = "Expose the count of selected IDs in report.mjs as count(rows). Reuse the selector and retain the original audit semantics."
const reportContract = "The audit contract counts only committed rows and preserves duplicate IDs."
const completedStage = "First change verified."

func taskKeyMigration() Task {
	const verify = `import assert from "node:assert/strict";
import {key} from "./key.mjs";
assert.equal(key(" A/b "), "A/b");
assert.equal(key("a/B"), "a/B");
`
	return Task{
		ID: "js_key_migration", Language: "javascript", Category: "multi-turn", RequireTool: "node",
		Description:    "Migrate a key contract, then fix cross-file lookup without losing the first turn's constraint.",
		Prompt:         "Fix the exported key function in key.mjs. " + keyContract,
		FollowUps:      []string{keyFollowUp},
		Files:          map[string]string{"key.mjs": "export const key = value => value.toLowerCase();\n", "lookup.mjs": "export const lookup = (table, value) => table[value];\n"},
		FirstTurnCheck: []string{"node", "--input-type=module", "-e", verify},
		VerifyCmd: []string{"node", "--input-type=module", "-e", verify + `import {lookup} from "./lookup.mjs";
const rows = {"A/b": 1, "a/B": 2};
assert.equal(lookup(rows, " A/b "), 1);
assert.equal(lookup(rows, " a/B "), 2);
assert.equal(lookup(rows, " x "), undefined);`},
	}
}

func taskReportReplay() Task {
	// A resumed investigation with 24 completed batch checks. Old logs are expendable;
	// the original user contract is the only place that defines eligible rows.
	history := []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: reportContract}}}}
	for i := 0; i < 24; i++ {
		id := fmt.Sprintf("audit_%02d", i)
		args, _ := json.Marshal(map[string]string{"command": "node --test batch_" + id + ".test.mjs"})
		log := strings.Repeat("ok - checked audit fixture row and retained source order\n", 72)
		history = append(history, agent.Message{Role: agent.RoleAssistant, Content: []agent.ContentBlock{{Type: agent.ContentToolUse, ID: id, Name: "bash", Input: args}}},
			agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{{Type: agent.ContentToolResult, ToolUseID: id, Text: log + "batch complete: " + id}}})
	}
	const verify = `import assert from "node:assert/strict";
import {select} from "./select.mjs";
const rows = [
  {id: "x", state: "committed"}, {id: "y", state: "retry"},
  {id: "x", state: "committed"}, {id: "z", state: "new"}, {id: "a", state: "committed"},
];
assert.deepEqual(select(rows), ["x", "x", "a"]);
`
	return Task{
		ID: "js_report_replay", Language: "javascript", Category: "long-context", RequireTool: "node",
		Description:    "Resume a long audit, then expose its result through a public reporting API.",
		InitialHistory: history,
		Prompt:         "Fix select(rows) in select.mjs to return the IDs in source order using our original audit contract.",
		FollowUps:      []string{reportFollowUp},
		Files:          map[string]string{"select.mjs": "export const select = rows => rows.map(row => row.id);\n", "report.mjs": "export const count = rows => rows.length;\n"},
		FirstTurnCheck: []string{"node", "--input-type=module", "-e", verify},
		VerifyCmd: []string{"node", "--input-type=module", "-e", verify + `import {count} from "./report.mjs";
assert.equal(count(rows), 3);
assert.equal(count([]), 0);`},
	}
}

// The smoke provider checks the serialized history at both turn boundaries.
// Edits, diagnostics, per-turn checks, pruning and final verification run normally.
type conversationSmokeClient struct {
	taskID string
	step   int
}

func (*conversationSmokeClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	return llm.StreamTarget{ProviderID: p, ModelID: m, Protocol: llm.ProtocolAnthropic, HasEndpoint: true, ContextLimit: 24576, OutputLimit: 2048}
}

func (s *conversationSmokeClient) OpenStream(_ context.Context, req llm.StreamHTTPRequest) (*http.Response, error) {
	s.step++
	wire := string(req.Body)
	path, contract, followUp := "key.mjs", keyContract, keyFollowUp
	before, after := "value.toLowerCase()", "value.trim()"
	second, oldSecond, newSecond := "lookup.mjs", "export const lookup = (table, value) => table[value];", "import {key} from './key.mjs';\nexport const lookup = (table, value) => table[key(value)];"
	if s.taskID == "js_report_replay" {
		path, contract, followUp = "select.mjs", reportContract, reportFollowUp
		before, after = "rows.map(row => row.id)", "rows.filter(row => row.state === 'committed').map(row => row.id)"
		second, oldSecond, newSecond = "report.mjs", "export const count = rows => rows.length;", "import {select} from './select.mjs';\nexport const count = rows => select(rows).length;"
		if !strings.Contains(wire, "[tool output pruned:") {
			return nil, fmt.Errorf("expected a pruned audit log in step %d", s.step)
		}
	}
	if !strings.Contains(wire, contract) {
		return nil, fmt.Errorf("missing original contract at step %d", s.step)
	}
	if s.step >= 4 && (!strings.Contains(wire, followUp) || !strings.Contains(wire, completedStage) || !strings.Contains(wire, "fixture_edit_2")) {
		return nil, fmt.Errorf("follow-up request lost the new instruction, first turn's edits or answer")
	}
	var answer string
	switch s.step {
	case 3:
		answer = buildAnthropicTextSSE(900, 20, 300, 0, completedStage)
	case 6:
		answer = buildAnthropicTextSSE(900, 20, 300, 0, "Follow-up complete.")
	default:
		if s.step > 3 {
			path, before, after = second, oldSecond, newSecond
		}
		call := smokeToolCall{Name: "read", ID: fmt.Sprintf("fixture_read_%d", s.step), Input: map[string]any{"filePath": path}}
		if s.step == 2 || s.step == 5 {
			call = smokeToolCall{Name: "edit", ID: fmt.Sprintf("fixture_edit_%d", s.step), Input: map[string]any{"filePath": path, "oldString": before, "newString": after}}
		}
		answer = buildAnthropicToolSSE(900, 20, 300, 0, []smokeToolCall{call})
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(answer))}, nil
}
