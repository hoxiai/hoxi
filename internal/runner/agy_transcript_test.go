package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAgyActiveToDonePreservesEditAndWritePayloadsAcrossResumedTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "33333333-4444-5555-6666-777777777777"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}

	writeJSONLines := func(path string, rows []map[string]interface{}) {
		var buf bytes.Buffer
		for _, r := range rows {
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Compact transcript.jsonl has truncated double-quoted strings and an earlier turn's edit.
	writeJSONLines(filepath.Join(logDir, "transcript.jsonl"), []map[string]interface{}{
		{
			"step_index": 1,
			"type":       "PLANNER_RESPONSE",
			"tool_calls": []map[string]interface{}{
				{"name": "edit_file", "args": map[string]string{
					"Path":        `"/repo/old_turn.php"`,
					"Target":      `"old turn line 1\n<truncated 7724 bytes>"`,
					"Replacement": `"old turn replacement\n<truncated 7724 bytes>"`,
				}},
			},
		},
		{
			"step_index": 3,
			"type":       "USER_INPUT",
			"content":    "new turn request",
		},
		{
			"step_index": 4,
			"type":       "PLANNER_RESPONSE",
			"tool_calls": []map[string]interface{}{
				{"name": "write_file", "args": map[string]string{
					"Path":    `"/repo/package.json"`,
					"Content": `"{\n  \"name\": \"demo\"\n}\n<truncated 400 bytes>"`,
				}},
				{"name": "edit_file", "args": map[string]string{
					"Path":        `"/repo/Feed.vue"`,
					"Target":      `"const a = 1;\n<truncated 500 bytes>"`,
					"Replacement": `"const a = 2;\nconst b = 3;\n<truncated 500 bytes>"`,
				}},
			},
		},
		{
			"step_index": 7,
			"type":       "PLANNER_RESPONSE",
			"tool_calls": []map[string]interface{}{
				{"name": "edit_file", "args": map[string]string{
					"Path":        `"/repo/Spots.vue"`,
					"Target":      `"export default {\n<truncated 900 bytes>"`,
					"Replacement": `"export default {\n  name: 'Spots'\n<truncated 900 bytes>"`,
				}},
			},
		},
	})

	// Full transcript_full.jsonl has plain untruncated strings (including valid JSON for package.json).
	writeJSONLines(filepath.Join(logDir, "transcript_full.jsonl"), []map[string]interface{}{
		{
			"step_index": 1,
			"type":       "PLANNER_RESPONSE",
			"tool_calls": []map[string]interface{}{
				{"name": "edit_file", "args": map[string]string{
					"Path":        "/repo/old_turn.php",
					"Target":      "old turn line 1\nold turn line 2\n",
					"Replacement": "old turn replacement 1\nold turn replacement 2\n",
				}},
			},
		},
		{
			"step_index": 3,
			"type":       "USER_INPUT",
			"content":    "new turn request",
		},
		{
			"step_index": 4,
			"type":       "PLANNER_RESPONSE",
			"tool_calls": []map[string]interface{}{
				{"name": "write_file", "args": map[string]string{
					"Path":      "/repo/package.json",
					"Content":   "{\n  \"name\": \"demo\",\n  \"private\": true\n}\n",
					"Overwrite": "true",
				}},
				{"name": "edit_file", "args": map[string]string{
					"Path":        "/repo/Feed.vue",
					"Target":      "const a = 1;\nconst old = true;\n",
					"Replacement": "const a = 2;\nconst b = 3;\nconst updated = true;\n",
				}},
			},
		},
		{
			"step_index": 7,
			"type":       "PLANNER_RESPONSE",
			"tool_calls": []map[string]interface{}{
				{"name": "edit_file", "args": map[string]string{
					"Path":        "/repo/Spots.vue",
					"Target":      "export default {\n  name: 'OldSpots'\n}\n",
					"Replacement": "export default {\n  name: 'Spots',\n  props: ['items']\n}\n",
				}},
			},
		},
	})

	tr := NewTurnTranslator("ses_agy_resumed", string(RuntimeAntigravity), "msg_a_2", "msg_u_2", "antigravity", "gemini", "build", conv)
	stepLine := func(idx int, state, path string) string {
		return fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"%s","step_index":%d,"step_type":"tool","tool_name":"edit_file","tool_info":{"name":"edit_file","parameters":{"Path":"%s"}}}}`, conv, conv, state, idx, path)
	}

	// Feed ACTIVE then DONE for each step (5, 6, 8), matching real agy stdout behavior.
	for _, item := range []struct {
		idx  int
		path string
	}{
		{5, "/repo/package.json"},
		{6, "/repo/Feed.vue"},
		{8, "/repo/Spots.vue"},
	} {
		tr.FeedLine(stepLine(item.idx, "ACTIVE", item.path))
		tr.FeedLine(stepLine(item.idx, "DONE", item.path))
	}

	p5 := tr.partsByID["prt_msg_a_2_tool_5"]
	if p5 == nil || p5.State == nil || p5.State.Status != "completed" {
		t.Fatalf("expected step 5 completed, got %+v", p5)
	}
	if p5.Tool != "write" {
		t.Fatalf("expected step 5 to stay tool=write after DONE, got %q", p5.Tool)
	}
	if got, ok := p5.State.Input["Content"].(string); !ok || got != "{\n  \"name\": \"demo\",\n  \"private\": true\n}\n" {
		t.Fatalf("expected JSON file Content to remain untruncated string, got %T %v", p5.State.Input["Content"], p5.State.Input["Content"])
	}

	p6 := tr.partsByID["prt_msg_a_2_tool_6"]
	if p6 == nil || p6.State == nil || p6.State.Status != "completed" {
		t.Fatalf("expected step 6 completed, got %+v", p6)
	}
	if p6.State.Input["Target"] != "const a = 1;\nconst old = true;\n" ||
		p6.State.Input["Replacement"] != "const a = 2;\nconst b = 3;\nconst updated = true;\n" {
		t.Fatalf("step 6 lost or mismatched payload on DONE: %+v", p6.State.Input)
	}

	p8 := tr.partsByID["prt_msg_a_2_tool_8"]
	if p8 == nil || p8.State == nil || p8.State.Status != "completed" {
		t.Fatalf("expected step 8 completed, got %+v", p8)
	}
	if p8.State.Input["Target"] != "export default {\n  name: 'OldSpots'\n}\n" ||
		p8.State.Input["Replacement"] != "export default {\n  name: 'Spots',\n  props: ['items']\n}\n" {
		t.Fatalf("step 8 lost or mismatched payload on DONE: %+v", p8.State.Input)
	}
}

func TestStoreHydratesTruncatedAndMissingAgyEditPayloadsOnLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "88888888-9999-aaaa-bbbb-cccccccccccc"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}

	fullRow, err := json.Marshal(map[string]interface{}{
		"step_index": 9,
		"type":       "PLANNER_RESPONSE",
		"tool_calls": []map[string]interface{}{
			{"name": "write_file", "args": map[string]string{
				"Path":    "/repo/index.html",
				"Content": "<!DOCTYPE html>\n<html><body>Hello</body></html>\n",
			}},
			{"name": "edit_file", "args": map[string]string{
				"Path":        "/repo/Feed.vue",
				"Target":      "const feed = 1;\n",
				"Replacement": "const feed = 2;\nconst extra = 3;\n",
			}},
			{"name": "edit_file", "args": map[string]string{
				"Path":        "/repo/Spots.vue",
				"Target":      "const spots = 1;\n",
				"Replacement": "const spots = 2;\n",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), append(fullRow, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	storeDir := t.TempDir()
	s1 := NewStore(storeDir)
	rec := SessionRecord{
		ID:         "ses_hoxi_hydrate_test",
		Runtime:    string(RuntimeAntigravity),
		ExternalID: conv,
		ProjectDir: "/repo",
		Turns: []MessageTurn{
			{
				Info: MessageInfo{ID: "msg_a_1", SessionID: "ses_hoxi_hydrate_test", Role: "assistant"},
				Parts: []MessagePart{
					{
						ID:        "prt_msg_a_1_tool_10",
						SessionID: "ses_hoxi_hydrate_test",
						MessageID: "msg_a_1",
						Type:      "tool",
						Tool:      "edit",
						State: &ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"Path": "/repo/index.html", "filePath": "/repo/index.html"},
						},
					},
					{
						ID:        "prt_msg_a_1_tool_11",
						SessionID: "ses_hoxi_hydrate_test",
						MessageID: "msg_a_1",
						Type:      "tool",
						Tool:      "edit",
						State: &ToolState{
							Status: "completed",
							Input: map[string]interface{}{
								"Path":        "/repo/Feed.vue",
								"filePath":    "/repo/Feed.vue",
								"Target":      "\"wrong target\\n\n<truncated 7724 bytes>",
								"Replacement": "\"wrong replacement\\n\n<truncated 7724 bytes>",
							},
						},
					},
					{
						ID:        "prt_msg_a_1_tool_12",
						SessionID: "ses_hoxi_hydrate_test",
						MessageID: "msg_a_1",
						Type:      "tool",
						Tool:      "edit",
						State: &ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"Path": "/repo/Spots.vue", "filePath": "/repo/Spots.vue"},
						},
					},
				},
			},
		},
	}
	if err := s1.Put(rec); err != nil {
		t.Fatal(err)
	}

	// Re-open store from disk and verify hydration repairs all 3 tool parts.
	s2 := NewStore(storeDir)
	loaded, ok := s2.Get("ses_hoxi_hydrate_test")
	if !ok || len(loaded.Turns) != 1 || len(loaded.Turns[0].Parts) != 3 {
		t.Fatalf("unexpected loaded record: %+v", loaded)
	}
	parts := loaded.Turns[0].Parts
	if parts[0].Tool != "write" || parts[0].State.Input["Content"] != "<!DOCTYPE html>\n<html><body>Hello</body></html>\n" {
		t.Fatalf("part 0 not hydrated as write: tool=%s input=%+v", parts[0].Tool, parts[0].State.Input)
	}
	if parts[1].State.Input["Target"] != "const feed = 1;\n" || parts[1].State.Input["Replacement"] != "const feed = 2;\nconst extra = 3;\n" {
		t.Fatalf("part 1 truncated payload not repaired: %+v", parts[1].State.Input)
	}
	if parts[2].State.Input["Target"] != "const spots = 1;\n" || parts[2].State.Input["Replacement"] != "const spots = 2;\n" {
		t.Fatalf("part 2 missing payload not repaired: %+v", parts[2].State.Input)
	}
}

func TestAgyDoneThinkingOrdersBeforeSameStepTextAndPersistsOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "e058382d-b78a-4ea5-bbc6-914bd834b9c5"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}

	thinking := "I'm thinking through how to approach this.\n\nThe task involves a security audit of a specific repository."
	row, err := json.Marshal(map[string]interface{}{
		"step_index": 147,
		"type":       "PLANNER_RESPONSE",
		"thinking":   thinking,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), append(row, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := NewTurnTranslator("ses_agy_order", string(RuntimeAntigravity), "msg_a_1", "msg_u_1", "antigravity", "gemini", "build", conv)
	activeLine := fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"ACTIVE","step_index":147,"step_type":"agent_response","text_delta":"## Audit Summary\n\nAll checks complete."}}`, conv, conv)
	doneLine := fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"DONE","step_index":147,"step_type":"agent_response","usage":{"thinking_tokens":1969}}}`, conv, conv)

	evts := tr.FeedLine(activeLine)
	txtAfterActive := tr.partsByID["prt_msg_a_1_text_147"]
	if txtAfterActive == nil || txtAfterActive.Time == nil || txtAfterActive.Time.Start <= 0 {
		t.Fatalf("expected active text part with start time, got %+v", txtAfterActive)
	}
	activeTextStart := txtAfterActive.Time.Start

	// Simulate pre-thinking step start earlier than the first text_delta.
	tr.stepStartMs[147] = activeTextStart - 5000
	evts = append(evts, tr.FeedLine(doneLine)...)

	if len(tr.partOrder) != 2 || tr.partOrder[0] != "prt_msg_a_1_reason_147" || tr.partOrder[1] != "prt_msg_a_1_text_147" {
		t.Fatalf("expected reasoning part ordered before same-step text part, got %v", tr.partOrder)
	}
	rPart := tr.partsByID["prt_msg_a_1_reason_147"]
	txtPart := tr.partsByID["prt_msg_a_1_text_147"]
	if rPart == nil || rPart.Text != thinking || rPart.Time == nil {
		t.Fatalf("unexpected reasoning part: %+v", rPart)
	}
	if txtPart == nil || txtPart.Time == nil {
		t.Fatalf("unexpected text part: %+v", txtPart)
	}
	if txtPart.Time.Start != activeTextStart {
		t.Fatalf("expected text part start %d to be preserved, got %d", activeTextStart, txtPart.Time.Start)
	}
	if rPart.Time.End != txtPart.Time.Start {
		t.Fatalf("expected reasoning end %d to cap at text start %d", rPart.Time.End, txtPart.Time.Start)
	}

	// Verify Store orders prt_<msg>_reason_<step> before prt_<msg>_text_<step> both live and on reload from turns.jsonl.
	storeDir := t.TempDir()
	s1 := NewStore(storeDir)
	if err := s1.Put(SessionRecord{
		ID:         "ses_agy_order",
		Runtime:    string(RuntimeAntigravity),
		ExternalID: conv,
		ProjectDir: "/repo",
		Turns: []MessageTurn{
			{Info: MessageInfo{ID: "msg_a_1", SessionID: "ses_agy_order", Role: "assistant"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, evt := range evts {
		if evt.Type != "message.part.updated" {
			continue
		}
		if part, ok := evt.Properties["part"].(MessagePart); ok {
			s1.UpsertTurnPart("ses_agy_order", part)
		}
	}

	assertReasonBeforeText := func(label string, s *Store) {
		t.Helper()
		rec, ok := s.Get("ses_agy_order")
		if !ok || len(rec.Turns) != 1 || len(rec.Turns[0].Parts) != 2 {
			t.Fatalf("%s: unexpected stored turns: %+v", label, rec.Turns)
		}
		got := []string{rec.Turns[0].Parts[0].ID, rec.Turns[0].Parts[1].ID}
		if got[0] != "prt_msg_a_1_reason_147" || got[1] != "prt_msg_a_1_text_147" {
			t.Fatalf("%s: expected reason before text, got %v", label, got)
		}
	}
	assertReasonBeforeText("live store", s1)
	assertReasonBeforeText("reloaded store", NewStore(storeDir))
}

func TestStoreHydratesLegacyAgyMultiStepTurnContextTokensOnLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "dc62d52e-cf46-417b-bc2b-f6416457f971"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}

	rows := []map[string]interface{}{
		{"step_index": 0, "type": "USER_INPUT", "content": "investigate issue"},
		{"step_index": 1, "type": "PLANNER_RESPONSE", "input_tokens": 37466, "output_tokens": 399, "thinking_tokens": 236, "cache_read_tokens": 0},
		{"step_index": 234, "type": "PLANNER_RESPONSE", "input_tokens": 2256, "output_tokens": 1591, "thinking_tokens": 800, "cache_read_tokens": 200150},
	}
	var buf bytes.Buffer
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	storeDir := t.TempDir()
	s1 := NewStore(storeDir)
	legacyTokens := &MessageTokens{
		Total:     986765, // legacy sum of Input + Output across 83 steps
		Input:     882081,
		Output:    104684,
		Reasoning: 78645,
	}
	legacyTokens.Cache.Read = 11258494
	rec := SessionRecord{
		ID:         "ses_agy_legacy_tokens",
		Runtime:    string(RuntimeAntigravity),
		ExternalID: conv,
		ProjectDir: "/repo",
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u_1", SessionID: "ses_agy_legacy_tokens", Role: "user"},
				Parts: []MessagePart{{ID: "prt_u_1", SessionID: "ses_agy_legacy_tokens", MessageID: "msg_u_1", Type: "text", Text: "investigate"}},
			},
			{
				Info: MessageInfo{
					ID:        "msg_a_1",
					SessionID: "ses_agy_legacy_tokens",
					Role:      "assistant",
					Tokens:    legacyTokens,
				},
				Parts: []MessagePart{
					{ID: "prt_msg_a_1_reason_1", SessionID: "ses_agy_legacy_tokens", MessageID: "msg_a_1", Type: "reasoning", Text: "step 1"},
					{ID: "prt_msg_a_1_text_234", SessionID: "ses_agy_legacy_tokens", MessageID: "msg_a_1", Type: "text", Text: "final answer"},
				},
			},
		},
	}
	if err := s1.Put(rec); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore(storeDir)
	var summaryTotal int
	if !s2.VisitTurnsSummary("ses_agy_legacy_tokens", func(r SessionRecord) {
		if len(r.Turns) == 2 && r.Turns[1].Info.Tokens != nil {
			summaryTotal = r.Turns[1].Info.Tokens.Total
		}
	}) {
		t.Fatal("VisitTurnsSummary failed")
	}
	loaded, ok := s2.Get("ses_agy_legacy_tokens")
	if !ok || len(loaded.Turns) != 2 || loaded.Turns[1].Info.Tokens == nil {
		t.Fatalf("unexpected loaded record: %+v", loaded)
	}
	tok := loaded.Turns[1].Info.Tokens
	wantContext := 2256 + 200150 + 1591 // 203,997
	if summaryTotal != wantContext {
		t.Fatalf("VisitTurnsSummary Tokens.Total = %d, want %d", summaryTotal, wantContext)
	}
	if tok.Total != wantContext {
		t.Fatalf("hydrated Tokens.Total = %d, want %d", tok.Total, wantContext)
	}
	if tok.Input != 882081 || tok.Output != 104684 || tok.Reasoning != 78645 || tok.Cache.Read != 11258494 {
		t.Fatalf("hydration must preserve cumulative billing/telemetry tokens, got %+v", tok)
	}
}

func TestStoreHydrationClearsStaleAgyResultErrorOnCompletedTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "74f0a507-fbff-464d-b33a-6dd193ff5f77"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]interface{}{
		{"step_index": 0, "type": "USER_INPUT", "status": "DONE", "content": "fix stats"},
		{"step_index": 197, "type": "PLANNER_RESPONSE", "status": "DONE", "content": "已修复并通过全部验证。"},
	}
	var buf bytes.Buffer
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	storeDir := t.TempDir()
	s1 := NewStore(storeDir)
	rec := SessionRecord{
		ID:         "ses_agy_stale_err_hydrate",
		Runtime:    string(RuntimeAntigravity),
		ExternalID: conv,
		ProjectDir: "/repo",
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u_1", SessionID: "ses_agy_stale_err_hydrate", Role: "user"},
				Parts: []MessagePart{{ID: "prt_u_1", SessionID: "ses_agy_stale_err_hydrate", MessageID: "msg_u_1", Type: "text", Text: "fix stats"}},
			},
			{
				Info: MessageInfo{
					ID:        "msg_a_1",
					SessionID: "ses_agy_stale_err_hydrate",
					Role:      "assistant",
					Finish:    "error",
					Error:     newClassifiedMessageError("RunnerError", `API error (attempt 1): request failed: Post "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse": EOF`, 0),
				},
				Parts: []MessagePart{
					{
						ID:        "prt_msg_a_1_tool_89",
						SessionID: "ses_agy_stale_err_hydrate",
						MessageID: "msg_a_1",
						Type:      "tool",
						Tool:      "manage_task",
						State:     &ToolState{Status: "error", Error: "task is not running", Time: &PartTime{Start: 100, End: 110}},
					},
					{
						ID:        "prt_msg_a_1_text_197",
						SessionID: "ses_agy_stale_err_hydrate",
						MessageID: "msg_a_1",
						Type:      "text",
						Text:      "已修复并通过全部验证。",
						Time:      &PartTime{Start: 200, End: 300},
					},
				},
			},
		},
	}
	if err := s1.Put(rec); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore(storeDir)
	loaded, ok := s2.Get("ses_agy_stale_err_hydrate")
	if !ok || len(loaded.Turns) != 2 {
		t.Fatalf("unexpected loaded record: %+v", loaded)
	}
	if loaded.Turns[1].Info.Error != nil || loaded.Turns[1].Info.Finish != "stop" {
		t.Fatalf("expected stale RunnerError cleared on completed turn, got finish=%q err=%+v", loaded.Turns[1].Info.Finish, loaded.Turns[1].Info.Error)
	}
}

func TestStoreHydrationPreservesAgyErrorAfterTransitionalTextStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GEMINI_HOME", home)
	const conv = "11111111-2222-3333-4444-555555555555"
	logDir := agyTranscriptLogDir(filepath.Join(home, "antigravity-cli", "brain"), conv)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Step 1 finished in the transcript, then step 2 started on stdout and failed
	// with a network error before producing any text or completing.
	rows := []map[string]interface{}{
		{"step_index": 0, "type": "USER_INPUT", "status": "DONE", "content": "inspect issue"},
		{"step_index": 1, "type": "PLANNER_RESPONSE", "status": "DONE", "content": "Let me inspect the repository first."},
	}
	var buf bytes.Buffer
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := NewTurnTranslator("ses_agy_transitional_err", string(RuntimeAntigravity), "msg_a_1", "msg_u_1", "antigravity", "gemini", "build", conv)
	lines := []string{
		fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"ACTIVE","step_index":1,"step_type":"agent_response","text_delta":"Let me inspect the repository first."}}`, conv, conv),
		fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"DONE","step_index":1,"step_type":"agent_response"}}`, conv, conv),
		fmt.Sprintf(`{"conversation_id":"%s","event":"step_update","step_update":{"conversation_id":"%s","state":"ACTIVE","step_index":2,"step_type":"agent_response"}}`, conv, conv),
		fmt.Sprintf(`{"conversation_id":"%s","event":"result","result":{"conversation_id":"%s","status":"ERROR","error":"API error (attempt 1): request failed: Post \"https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse\": EOF"}}`, conv, conv),
	}

	storeDir := t.TempDir()
	s1 := NewStore(storeDir)
	if err := s1.Put(SessionRecord{
		ID:         "ses_agy_transitional_err",
		Runtime:    string(RuntimeAntigravity),
		ExternalID: conv,
		ProjectDir: "/repo",
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u_1", SessionID: "ses_agy_transitional_err", Role: "user"},
				Parts: []MessagePart{{ID: "prt_u_1", SessionID: "ses_agy_transitional_err", MessageID: "msg_u_1", Type: "text", Text: "inspect issue"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	s1.UpsertTurnInfo("ses_agy_transitional_err", tr.msgInfo)
	for _, line := range lines {
		for _, evt := range tr.FeedLine(line) {
			switch evt.Type {
			case "message.updated":
				if info, ok := evt.Properties["info"].(MessageInfo); ok {
					s1.UpsertTurnInfo("ses_agy_transitional_err", info)
				}
			case "message.part.updated":
				if part, ok := evt.Properties["part"].(MessagePart); ok {
					s1.UpsertTurnPart("ses_agy_transitional_err", part)
				}
			case "message.part.delta":
				msgID, _ := evt.Properties["messageID"].(string)
				partID, _ := evt.Properties["partID"].(string)
				delta, _ := evt.Properties["delta"].(string)
				s1.AppendPartDelta("ses_agy_transitional_err", msgID, partID, delta)
			}
		}
	}
	s1.FlushSession("ses_agy_transitional_err")

	if tr.msgInfo.Error == nil || tr.msgInfo.Finish != "error" {
		t.Fatalf("live translator must record error when step 2 fails, got finish=%q err=%+v", tr.msgInfo.Finish, tr.msgInfo.Error)
	}

	s2 := NewStore(storeDir)
	loaded, ok := s2.Get("ses_agy_transitional_err")
	if !ok || len(loaded.Turns) != 2 {
		t.Fatalf("unexpected loaded record: %+v", loaded)
	}
	if loaded.Turns[1].Info.Error == nil || loaded.Turns[1].Info.Finish != "error" {
		t.Fatalf("reloaded turn must preserve real AGY error after transitional step 1 text, got finish=%q err=%+v", loaded.Turns[1].Info.Finish, loaded.Turns[1].Info.Error)
	}

	// Also verify a legacy stored turn (without agyTransitional metadata) is not
	// cleared when the transcript shows step 1 had tool_calls or step 2 started.
	rowsWithStep2 := append(rows, map[string]interface{}{
		"step_index": 2, "type": "PLANNER_RESPONSE", "status": "ERROR", "content": "",
	})
	buf.Reset()
	for _, r := range rowsWithStep2 {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	s3 := NewStore(t.TempDir())
	if err := s3.Put(SessionRecord{
		ID:         "ses_agy_legacy_step2_err",
		Runtime:    string(RuntimeAntigravity),
		ExternalID: conv,
		ProjectDir: "/repo",
		Turns: []MessageTurn{
			{
				Info: MessageInfo{
					ID:        "msg_a_legacy",
					SessionID: "ses_agy_legacy_step2_err",
					Role:      "assistant",
					Finish:    "error",
					Error:     newClassifiedMessageError("RunnerError", "API error (attempt 1): EOF", 0),
				},
				Parts: []MessagePart{
					{
						ID:        "prt_msg_a_legacy_text_1",
						SessionID: "ses_agy_legacy_step2_err",
						MessageID: "msg_a_legacy",
						Type:      "text",
						Text:      "Let me inspect the repository first.",
						Time:      &PartTime{Start: 100, End: 200},
					},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	s4 := NewStore(s3.dir)
	legacyLoaded, ok := s4.Get("ses_agy_legacy_step2_err")
	if !ok || len(legacyLoaded.Turns) != 1 || legacyLoaded.Turns[0].Info.Error == nil || legacyLoaded.Turns[0].Info.Finish != "error" {
		t.Fatalf("legacy turn with subsequent step 2 error in transcript must preserve error, got %+v", legacyLoaded.Turns)
	}
}
