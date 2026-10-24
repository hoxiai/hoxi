package runner

import (
	"strings"
	"testing"
	"time"
)

func TestClaudeStreamsBeforeResultAndIsolatesAssistantMessages(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_cld_test", string(RuntimeClaudeCode), "msg_turn", "msg_user", "claude-code", "sonnet", "build", "")
	var partIDs []string
	for _, message := range []struct{ id, text string }{{"msg_first", "Checking files."}, {"msg_second", "Finished checking."}} {
		tr.FeedLine(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"` + message.id + `"}}}`)
		events := tr.FeedLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + message.text + `"}}}`)
		if len(events) != 2 || events[1].Type != "message.part.delta" || events[1].Properties["delta"] != message.text {
			t.Fatalf("expected immediate text delta before result, got %+v", events)
		}
		if tr.IsTurnFinished() {
			t.Fatal("text must be streamed while the turn is still running")
		}
		partID := events[1].Properties["partID"].(string)
		partIDs = append(partIDs, partID)
		tr.FeedLine(`{"type":"assistant","message":{"id":"` + message.id + `","content":[{"type":"text","text":"` + message.text + `"}]}}`)
		if tr.partsByID[partID].Text != message.text {
			t.Fatalf("snapshot duplicated or replaced the wrong text: %+v", tr.partsByID[partID])
		}
	}
	if partIDs[0] == partIDs[1] || tr.partsByID[partIDs[0]].Text != "Checking files." {
		t.Fatal("later assistant message overwrote earlier streamed text")
	}
	tr.FeedLine(`{"type":"result","result":"Finished checking.","is_error":false}`)
	if !tr.IsTurnFinished() {
		t.Fatal("result must finish the turn")
	}
}

func TestClaudeReasoningBlockIndexesAreScopedToAssistantMessage(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_cld_test", string(RuntimeClaudeCode), "msg_turn", "msg_user", "claude-code", "sonnet", "build", "")
	for _, id := range []string{"msg_first", "msg_second"} {
		tr.FeedLine(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"` + id + `"}}}`)
		tr.FeedLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Thinking"}}}`)
	}
	if len(tr.partsByID) != 2 {
		t.Fatalf("expected independent reasoning parts, got %+v", tr.partsByID)
	}
}

func TestClaudeArgsEnablePartialMessages(t *testing.T) {
	t.Parallel()
	args := strings.Join(BuildClaudeArgs("build", "sonnet", "", ""), " ")
	for _, flag := range []string{"--output-format stream-json", "--include-partial-messages", "--verbose"} {
		if !strings.Contains(args, flag) {
			t.Fatalf("missing streaming flag %q: %s", flag, args)
		}
	}
}

func TestClaudeReasoningSnapshotCompletesStreamedPart(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_cld_test", string(RuntimeClaudeCode), "msg_turn", "msg_user", "claude-code", "sonnet", "build", "")
	tr.stepStartMs[0] = 1
	tr.FeedLine(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_first"}}}`)
	tr.FeedLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Partial"}}}`)
	events := tr.FeedLine(`{"type":"assistant","message":{"id":"msg_first","content":[{"type":"thinking","thinking":"Complete reasoning"}]}}`)
	if len(events) != 1 || events[0].Type != "message.part.updated" {
		t.Fatalf("expected a completed reasoning snapshot, got %+v", events)
	}
	part := events[0].Properties["part"].(MessagePart)
	if part.Text != "Complete reasoning" || part.Time == nil || part.Time.End == 0 || part.Time.Start <= 1 {
		t.Fatalf("reasoning snapshot has stale text or timing: %+v", part)
	}
}

func TestClaudeThinkingPlusTextDoesNotDuplicateReplyOnSplitAssistantSnapshots(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_cld_test", string(RuntimeCodeBuddy), "msg_turn", "msg_user", "codebuddy", "hy3", "build", "")
	tr.FeedLine(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"01a1533e"}}}`)
	tr.FeedLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Thinking first"}}}`)

	textEvents := tr.FeedLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"你好！我在当前 Hoxi 工作树里。"}}}`)
	reasonID := tr.currentClaudeReasonPartID()
	if tr.partsByID[reasonID] == nil || tr.partsByID[reasonID].Time == nil || tr.partsByID[reasonID].Time.End == 0 {
		t.Fatalf("expected reasoning part to be closed immediately when text_delta starts, got %+v (events=%+v)", tr.partsByID[reasonID], textEvents)
	}

	tr.FeedLine(`{"type":"assistant","message":{"id":"01a1533e","content":[{"type":"thinking","thinking":"Thinking first"}]}}`)
	tr.FeedLine(`{"type":"assistant","message":{"id":"01a1533e","content":[{"type":"text","text":"你好！我在当前 Hoxi 工作树里。"}]}}`)
	tr.FeedLine(`{"type":"result","result":"你好！我在当前 Hoxi 工作树里。","is_error":false}`)

	var textParts, reasonParts int
	for _, id := range tr.partOrder {
		switch tr.partsByID[id].Type {
		case "text":
			textParts++
		case "reasoning":
			reasonParts++
		}
	}
	if reasonParts != 1 || textParts != 1 {
		t.Fatalf("expected 1 reasoning part and 1 text part, got reason=%d text=%d parts=%+v", reasonParts, textParts, tr.partsByID)
	}
}

func TestClaudeAPIRetryBecomesRetryStatusUntilOutputResumes(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_cld_test", string(RuntimeClaudeCode), "msg_turn", "msg_user", "claude-code", "sonnet", "build", "")
	before := time.Now().UnixMilli()
	events := tr.FeedLine(`{"type":"system","subtype":"api_retry","attempt":2,"max_retries":10,"retry_delay_ms":4000,"error_status":529,"error":"overloaded","session_id":"abc"}`)
	if len(events) != 1 || events[0].Type != "session.status" {
		t.Fatalf("expected one retry status, got %+v", events)
	}
	st := events[0].Properties["status"].(SessionStatus)
	if st.Type != "retry" || st.Attempt != 2 || st.Message != "Provider is overloaded (HTTP 529)" || st.Next < before+4000 {
		t.Fatalf("unexpected retry status: %+v", st)
	}

	events = tr.FeedLine(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_a"}}}`)
	if len(events) == 0 || events[0].Properties["status"] != (SessionStatus{Type: "busy"}) {
		t.Fatalf("output after a retry must restore busy, got %+v", events)
	}
	events = tr.FeedLine(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}}`)
	for _, evt := range events {
		if evt.Type == "session.status" {
			t.Fatalf("busy must be restored only once, got %+v", events)
		}
	}
}

func TestClaudeRetryMessageFallsBackForUnknownCategories(t *testing.T) {
	t.Parallel()
	if got := claudeRetryMessage("unknown", nil); got != "API request failed" {
		t.Fatalf("got %q", got)
	}
	status := 429
	if got := claudeRetryMessage("rate_limit", &status); got != "Rate limited (HTTP 429)" {
		t.Fatalf("got %q", got)
	}
}

func TestClaudeResultUsageAndSubagentRollup(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_cld_usage", string(RuntimeClaudeCode), "msg_turn", "msg_user", "claude-code", "", "build", "")
	tr.FeedLine(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":12,"cache_creation_input_tokens":500,"cache_read_input_tokens":1200,"output_tokens":4}}}}`)
	tr.FeedLine(`{"type":"stream_event","event":{"type":"message_delta","usage":{"input_tokens":12,"cache_creation_input_tokens":500,"cache_read_input_tokens":1200,"output_tokens":85,"output_tokens_details":{"thinking_tokens":30}}}}`)

	// Before result arrives, streamed usage is already reflected on msgInfo.Tokens.
	// Anthropic output_tokens (85) includes thinking_tokens (30), so normalized Output is 55 and Total is 12 + 55 + 30 + 1200 + 500 = 1797.
	if tr.msgInfo.Tokens == nil || tr.msgInfo.Tokens.Input != 12 || tr.msgInfo.Tokens.Output != 55 || tr.msgInfo.Tokens.Reasoning != 30 || tr.msgInfo.Tokens.Total != 1797 || tr.msgInfo.Tokens.Cache.Read != 1200 || tr.msgInfo.Tokens.Cache.Write != 500 {
		t.Fatalf("unexpected streamed tokens: %+v", tr.msgInfo.Tokens)
	}
	if tr.msgInfo.ModelID != "claude-sonnet-4-5" {
		t.Fatalf("expected modelID populated from message_start, got %q", tr.msgInfo.ModelID)
	}

	// Final result with modelUsage (including subagent rollup) and total_cost_usd.
	// modelUsage outputTokens (210) includes thinkingTokens (45), so normalized Output is 165 and Total is 20 + 165 + 45 + 3400 + 900 = 4530.
	evts := tr.FeedLine(`{"type":"result","subtype":"success","is_error":false,"result":"Done","total_cost_usd":0.00817,"usage":{"input_tokens":12,"cache_creation_input_tokens":500,"cache_read_input_tokens":1200,"output_tokens":85,"output_tokens_details":{"thinking_tokens":30}},"modelUsage":{"claude-sonnet-4-5":{"inputTokens":20,"outputTokens":210,"cacheReadInputTokens":3400,"cacheCreationInputTokens":900,"thinkingTokens":45,"costUSD":0.00817}}}`)
	var updated *MessageInfo
	for _, evt := range evts {
		if evt.Type == "message.updated" {
			if info, ok := evt.Properties["info"].(MessageInfo); ok {
				updated = &info
			}
		}
	}
	if updated == nil || updated.Tokens == nil {
		t.Fatalf("expected message.updated with tokens, got %+v", evts)
	}
	if updated.Tokens.Input != 20 || updated.Tokens.Output != 165 || updated.Tokens.Reasoning != 45 || updated.Tokens.Cache.Read != 3400 || updated.Tokens.Cache.Write != 900 {
		t.Fatalf("unexpected final tokens: %+v", updated.Tokens)
	}
	wantTotal := 20 + 210 + 3400 + 900
	if updated.Tokens.Total != wantTotal {
		t.Fatalf("total tokens = %d, want %d", updated.Tokens.Total, wantTotal)
	}
	if updated.Cost != 0.00817 {
		t.Fatalf("cost = %v, want 0.00817", updated.Cost)
	}

	// Verify exact reviewer case: input=10, output=100, thinking=30 -> Total=110 (Output=70, Reasoning=30).
	got110 := sumClaudeUsageMap(map[string]claudeUsage{
		"m1": {
			InputTokens:  10,
			OutputTokens: 100,
			OutputTokensDetails: &struct {
				ThinkingTokens int `json:"thinking_tokens"`
			}{ThinkingTokens: 30},
		},
	})
	if got110 == nil || got110.Total != 110 || got110.Input != 10 || got110.Output != 70 || got110.Reasoning != 30 {
		t.Fatalf("expected normalized total=110 (in=10, out=70, reasoning=30), got %+v", got110)
	}
}

func TestQoderContextUsageRatioAndIncrementalMessageUpdated(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_qdr_usage", string(RuntimeQoder), "msg_a_1", "msg_u_1", "qoder", "DeepSeek-Flash", "plan", "")
	// Qoder masks input_tokens/output_tokens as 0 and reports context_usage_ratio = tokens / 180000.
	evts := tr.FeedLine(`{"type":"assistant","message":{"id":"q_msg_1","model":"dfmodel","usage":{"input_tokens":0,"output_tokens":0,"context_usage_ratio":0.13671111111111112},"content":[{"type":"text","text":"Checking repo"}]}}`)
	var sawMsgUpdated bool
	for _, evt := range evts {
		if evt.Type == "message.updated" {
			if info, ok := evt.Properties["info"].(MessageInfo); ok && info.Tokens != nil && info.Tokens.Input == 24608 {
				sawMsgUpdated = true
			}
		}
	}
	if !sawMsgUpdated || tr.msgInfo.Tokens == nil || tr.msgInfo.Tokens.Input != 24608 {
		t.Fatalf("expected immediate message.updated with input=24608 from context_usage_ratio, got tokens=%+v evts=%+v", tr.msgInfo.Tokens, evts)
	}
}

func TestAgyStepUsageStreamsImmediatelyAndIgnoresCumulativeOrZeroResultOverwrite(t *testing.T) {
	t.Parallel()
	tr := NewTurnTranslator("ses_agy_usage", string(RuntimeAntigravity), "msg_a_1", "msg_u_1", "antigravity", "gemini-4-argon-high", "build", "conv-1")
	evts := tr.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":10,"state":"DONE","step_type":"agent_response","text_delta":"Step 10","usage":{"input_tokens":1200,"output_tokens":80,"thinking_tokens":20,"cache_read_tokens":500,"total_tokens":1800}}}`)
	var stepUpdated *MessageInfo
	for _, evt := range evts {
		if evt.Type == "message.updated" {
			if info, ok := evt.Properties["info"].(MessageInfo); ok {
				stepUpdated = &info
			}
		}
	}
	if stepUpdated == nil || stepUpdated.Tokens == nil || stepUpdated.Tokens.Total != 1800 {
		t.Fatalf("expected step DONE to emit message.updated with tokens, got %+v", evts)
	}

	// Resumed conversation result.usage carries cumulative history (999,999); must not overwrite current turn's step sum.
	resEvts := tr.FeedLine(`{"event":"result","result":{"conversation_id":"conv-1","status":"SUCCESS","usage":{"input_tokens":500000,"output_tokens":50000,"thinking_tokens":10000,"cache_read_tokens":439999,"total_tokens":999999}}}`)
	for _, evt := range resEvts {
		if evt.Type == "message.updated" {
			if info, ok := evt.Properties["info"].(MessageInfo); ok {
				stepUpdated = &info
			}
		}
	}
	if stepUpdated == nil || stepUpdated.Tokens == nil || stepUpdated.Tokens.Total != 1800 || stepUpdated.Tokens.Input != 1200 {
		t.Fatalf("cumulative result.usage overwrote current turn step tokens: %+v", stepUpdated.Tokens)
	}

	// Interrupted turn with all-zero result.usage must preserve accumulated step tokens.
	trAbort := NewTurnTranslator("ses_agy_abort", string(RuntimeAntigravity), "msg_a_2", "msg_u_2", "antigravity", "gemini-4-argon-high", "build", "conv-2")
	trAbort.FeedLine(`{"event":"step_update","step_update":{"conversation_id":"conv-2","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"Partial","usage":{"input_tokens":400,"output_tokens":30,"thinking_tokens":10,"cache_read_tokens":100,"total_tokens":540}}}`)
	trAbort.FeedLine(`{"event":"result","result":{"conversation_id":"conv-2","status":"ERROR","error":"interrupted","usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0}}}`)
	if trAbort.msgInfo.Tokens == nil || trAbort.msgInfo.Tokens.Total != 540 {
		t.Fatalf("zero result.usage on abort wiped out step tokens: %+v", trAbort.msgInfo.Tokens)
	}
}
