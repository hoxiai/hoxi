package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestHardenMessagesForRequestPreservesWellFormedPrefixAndRepairsBrokenTurns(t *testing.T) {
	wellFormed := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "read main.go"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "call_1", Name: "read", Input: json.RawMessage(`{"filePath":"main.go"}`)}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "call_1", Text: "package main"}}},
	}
	gotWell := hardenMessagesForRequest(wellFormed, true)
	if &gotWell[0] != &wellFormed[0] {
		t.Fatal("well-formed history should pass through untouched without reallocation")
	}

	// Broken history: unpaired tool_use followed immediately by another user prompt + orphaned tool_result + adjacent user turns.
	broken := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "step 1"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "steered note"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentToolUse, ID: "call_missing", Name: "bash", Input: json.RawMessage(`{"command":"sleep 10"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentToolResult, ToolUseID: "call_orphan", Text: "stale output"},
			{Type: ContentText, Text: "continue"},
		}},
	}
	repaired := hardenMessagesForRequest(broken, true)
	if len(repaired) != 3 {
		t.Fatalf("expected 3 alternating turns (user -> assistant -> user), got %d: %+v", len(repaired), repaired)
	}
	if repaired[0].Role != RoleUser || len(repaired[0].Content) != 2 {
		t.Fatalf("expected coalesced leading user turn, got %+v", repaired[0])
	}
	if repaired[2].Role != RoleUser || len(repaired[2].Content) != 3 {
		t.Fatalf("expected synthetic tool_result + converted orphan text + user text, got %+v", repaired[2])
	}
	if repaired[2].Content[0].Type != ContentToolResult || repaired[2].Content[0].ToolUseID != "call_missing" || !repaired[2].Content[0].IsError {
		t.Fatalf("expected synthetic error tool_result for call_missing, got %+v", repaired[2].Content[0])
	}
	if repaired[2].Content[1].Type != ContentText || !strings.Contains(repaired[2].Content[1].Text, "stale output") {
		t.Fatalf("expected orphaned tool_result to be preserved as text, got %+v", repaired[2].Content[1])
	}
}

func TestGeminiDriverInjectsSyntheticThoughtSignatureAndCoalescesDroppedThinkingTurns(t *testing.T) {
	drv := newGeminiDriver()
	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "inspect"}}},
		// Unsigned thinking-only assistant turn (e.g. switched from another model) gets dropped by Gemini wire encoder.
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentThinking, Text: "unsigned thought"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "follow up"}}},
		// Cross-model tool calls without Gemini signatures.
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentToolUse, ID: "call_a", Name: "glob", Input: json.RawMessage(`{"pattern":"*.go"}`)},
			{Type: ContentToolUse, ID: "call_b", Name: "read", Input: json.RawMessage(`{"filePath":"main.go"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentToolResult, ToolUseID: "call_a", Text: "main.go"},
			{Type: ContentToolResult, ToolUseID: "call_b", Text: "package main"},
		}},
	}

	req, err := drv.BuildRequest(ModelRequest{
		SessionID: "ses_gem_cross",
		Target:    llm.StreamTarget{ProviderID: "google", ModelID: "gemini-3-pro-preview", Protocol: llm.ProtocolGoogle},
		Messages:  msgs,
	})
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	var payload struct {
		Contents []struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	// Adjacent user turns after dropped unsigned-thinking turn must be coalesced!
	if len(payload.Contents) != 3 {
		t.Fatalf("expected 3 strictly alternating contents (user, model, user), got %d: %s", len(payload.Contents), req.Body)
	}
	if payload.Contents[0].Role != "user" || len(payload.Contents[0].Parts) != 2 {
		t.Fatalf("expected coalesced user parts, got %+v", payload.Contents[0])
	}
	modelParts := payload.Contents[1].Parts
	if len(modelParts) != 2 {
		t.Fatalf("expected 2 functionCall parts, got %+v", modelParts)
	}
	if got := modelParts[0]["thoughtSignature"]; got != geminiSyntheticThoughtSignature {
		t.Fatalf("first unsigned functionCall missing synthetic thoughtSignature: got %v", got)
	}
	if _, hasSecond := modelParts[1]["thoughtSignature"]; hasSecond {
		t.Fatalf("only the first functionCall in a model turn should receive synthetic signature: %+v", modelParts[1])
	}

	// Legacy Gemini 2.0 without thinkingConfig must NOT get thoughtSignature injected.
	legacyReq, err := drv.BuildRequest(ModelRequest{
		SessionID: "ses_gem_legacy",
		Target:    llm.StreamTarget{ProviderID: "google", ModelID: "gemini-2.0-flash", Protocol: llm.ProtocolGoogle},
		Messages:  msgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyReq.Body), geminiSyntheticThoughtSignature) {
		t.Fatalf("legacy gemini-2.0 should not inject synthetic thoughtSignature: %s", legacyReq.Body)
	}
}

func TestAnthropicDriverCoalescesAdjacentUserTurnsWhenUnsignedThinkingDropped(t *testing.T) {
	drv := newAnthropicDriver()
	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "first"}}},
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentThinking, Text: "no signature"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "second"}}},
	}
	req, err := drv.BuildRequest(ModelRequest{
		SessionID: "ses_anth_coal",
		Target:    llm.StreamTarget{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6", Protocol: llm.ProtocolAnthropic},
		Messages:  msgs,
	})
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	var payload struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || len(payload.Messages[0].Content) != 2 {
		t.Fatalf("expected adjacent user turns to coalesce into 1 user message with 2 blocks, got: %s", req.Body)
	}
}
