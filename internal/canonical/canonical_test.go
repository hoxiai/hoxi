package canonical

import "testing"

func TestAppendMessageAndMutatingClassification(t *testing.T) {
	var msgs []Message
	msgs = AppendMessage(msgs, "user", []ContentBlock{{Type: "text", Text: "first"}})
	msgs = AppendMessage(msgs, "user", []ContentBlock{{Type: "text", Text: "second"}})
	msgs = AppendMessage(msgs, "assistant", []ContentBlock{{Type: "text", Text: "reply"}})

	if len(msgs) != 2 || len(msgs[0].Content) != 2 {
		t.Fatalf("expected consecutive same-role blocks to merge into 2 messages, got %+v", msgs)
	}

	if IsMutatingToolCall("plan_consent", nil) {
		t.Fatalf("expected plan_consent to be non-mutating")
	}
	for _, act := range []string{"context", "search", "inspect", "callers", "callees", "impact", "refresh"} {
		if IsMutatingToolCall("hoxi_code_index", map[string]any{"action": act}) {
			t.Fatalf("expected hoxi_code_index (%s) to be non-mutating", act)
		}
	}
	for _, roAct := range []string{"output", "list"} {
		if IsMutatingToolCall("bash", map[string]any{"action": roAct}) {
			t.Fatalf("expected bash (%s) to be non-mutating", roAct)
		}
	}
	if !IsMutatingToolCall("bash", map[string]any{"command": "echo hi"}) {
		t.Fatalf("expected bash command execution to be mutating")
	}
	if IsMutatingToolCall("hoxi_data", map[string]any{"action": "sql_query", "query": "SELECT id FROM users LIMIT 5"}) {
		t.Fatalf("expected read-only SELECT via hoxi_data to be non-mutating")
	}
	if !IsMutatingToolCall("hoxi_data", map[string]any{"action": "sql_query", "query": "UPDATE users SET active = false"}) {
		t.Fatalf("expected UPDATE via hoxi_data to be mutating")
	}
	if IsMutatingToolCall("hoxi_data", map[string]any{"action": "ssh_exec", "command": "uptime"}) {
		t.Fatalf("expected read-only uptime via hoxi_data ssh_exec to be non-mutating")
	}
	if !IsMutatingToolCall("hoxi_data", map[string]any{"action": "ssh_exec", "command": "systemctl restart nginx"}) {
		t.Fatalf("expected service restart via hoxi_data ssh_exec to be mutating")
	}
	if !IsMutatingToolCall("edit", map[string]any{"filePath": "main.go"}) {
		t.Fatalf("expected edit to be mutating")
	}
}

func TestReasoningAffinityInvariants(t *testing.T) {
	meta := map[string]any{
		"signature":  "sig_abc123",
		"providerID": "anthropic",
		"modelID":    "claude-sonnet-4-5",
	}
	sig, keep := KeepReasoningAffinity("anthropic", "claude-sonnet-4-5", "thinking...", meta, "anthropic", "claude-sonnet-4-5")
	if !keep || sig != "sig_abc123" {
		t.Fatalf("expected same provider/model reasoning block to retain signature, got keep=%v sig=%q", keep, sig)
	}

	if _, keepSwitch := KeepReasoningAffinity("anthropic", "claude-sonnet-4-5", "thinking...", meta, "openai", "gpt-5"); keepSwitch {
		t.Fatalf("expected model switch to strip private reasoning block")
	}
}
