package runner

import (
	"strings"
	"testing"
)

func TestClaudeStreamsBeforeResultAndIsolatesAssistantMessages(t *testing.T) {
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
	args := strings.Join(BuildClaudeArgs("build", "sonnet", "", ""), " ")
	for _, flag := range []string{"--output-format stream-json", "--include-partial-messages", "--verbose"} {
		if !strings.Contains(args, flag) {
			t.Fatalf("missing streaming flag %q: %s", flag, args)
		}
	}
}

func TestClaudeReasoningSnapshotCompletesStreamedPart(t *testing.T) {
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
