package agent

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

type testWaitableSteer struct {
	mu     sync.Mutex
	items  []string
	notify chan struct{}
}

func (s *testWaitableSteer) Push(msg string) {
	s.mu.Lock()
	s.items = append(s.items, msg)
	if s.notify != nil {
		close(s.notify)
		s.notify = nil
	}
	s.mu.Unlock()
}

func (s *testWaitableSteer) Drain() []string {
	s.mu.Lock()
	out := s.items
	s.items = nil
	s.mu.Unlock()
	return out
}

func (s *testWaitableSteer) HasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items) > 0
}

func (s *testWaitableSteer) NotifyCh() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) > 0 {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	if s.notify == nil {
		s.notify = make(chan struct{})
	}
	return s.notify
}

func TestAnthropicStickyUserID(t *testing.T) {
	drv := newAnthropicDriver()
	baseMsgs := []Message{{
		Role:    RoleUser,
		Content: []ContentBlock{{Type: ContentText, Text: "ping"}},
	}}

	reqA1, err := drv.BuildRequest(ModelRequest{
		SessionID: "ses_sticky_001",
		Target: llm.StreamTarget{
			ProviderID: "crs-pool",
			ModelID:    "claude-sonnet-4-5",
			Protocol:   llm.ProtocolAnthropic,
		},
		Messages:  baseMsgs,
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	reqA2, err := drv.BuildRequest(ModelRequest{
		SessionID: "ses_sticky_001",
		Target: llm.StreamTarget{
			ProviderID: "crs-pool",
			ModelID:    "claude-sonnet-4-5",
			Protocol:   llm.ProtocolAnthropic,
		},
		Messages:  baseMsgs,
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("BuildRequest second call failed: %v", err)
	}
	reqB, err := drv.BuildRequest(ModelRequest{
		SessionID: "ses_sticky_002",
		Target: llm.StreamTarget{
			ProviderID: "crs-pool",
			ModelID:    "claude-sonnet-4-5",
			Protocol:   llm.ProtocolAnthropic,
		},
		Messages:  baseMsgs,
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("BuildRequest second session failed: %v", err)
	}
	reqEmpty, err := drv.BuildRequest(ModelRequest{
		SessionID: "",
		Target: llm.StreamTarget{
			ProviderID: "crs-pool",
			ModelID:    "claude-sonnet-4-5",
			Protocol:   llm.ProtocolAnthropic,
		},
		Messages:  baseMsgs,
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("BuildRequest empty session failed: %v", err)
	}

	extractUID := func(body []byte) (string, bool) {
		var payload map[string]any
		if uErr := json.Unmarshal(body, &payload); uErr != nil {
			t.Fatalf("unmarshal payload: %v", uErr)
		}
		meta, ok := payload["metadata"].(map[string]any)
		if !ok {
			return "", false
		}
		uid, _ := meta["user_id"].(string)
		return uid, uid != ""
	}

	uidA1, ok := extractUID(reqA1.Body)
	if !ok {
		t.Fatalf("expected metadata.user_id on session-bound request, got %s", string(reqA1.Body))
	}
	uidA2, _ := extractUID(reqA2.Body)
	if uidA1 != uidA2 {
		t.Fatalf("expected deterministic sticky user_id within session, got %q vs %q", uidA1, uidA2)
	}
	uidB, _ := extractUID(reqB.Body)
	if uidA1 == uidB {
		t.Fatalf("expected distinct sticky user_id across sessions, got identical %q", uidA1)
	}
	re := regexp.MustCompile(`^user_[0-9a-f]{64}_account__session_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !re.MatchString(uidA1) {
		t.Fatalf("sticky user_id %q did not match Sub2API/CRS/NewAPI format", uidA1)
	}
	if _, hasMeta := extractUID(reqEmpty.Body); hasMeta {
		t.Fatalf("expected metadata.user_id to be omitted when SessionID is empty, got %s", string(reqEmpty.Body))
	}
}

func TestCompactionStripsPrefixInvalidatedHistoricalThinkingWhileKeepingActiveTurn(t *testing.T) {
	before := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "initial task"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentThinking, Text: "early cached thought", Signature: "sig_cached_0"},
			{Type: ContentToolUse, ID: "tu_1", Name: "read", Input: json.RawMessage(`{"filePath":"a.go"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentToolResult, ToolUseID: "tu_1", Text: strings.Repeat("verbose line\n", 400)},
		}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentThinking, Text: "historical thought after mutated prefix", Signature: "sig_stale_1"},
			{Type: ContentText, Text: "I read a.go."},
		}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "now run step 2"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentThinking, Text: "active turn thought", Signature: "sig_active_2"},
			{Type: ContentToolUse, ID: "tu_2", Name: "read", Input: json.RawMessage(`{"filePath":"b.go"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentToolResult, ToolUseID: "tu_2", Text: "package main"},
		}},
	}

	after := make([]Message, len(before))
	copy(after, before)
	// Simulate quantized compaction mutating message[2] (an earlier tool output).
	after[2] = Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: ContentToolResult, ToolUseID: "tu_1", Text: "[compacted tool output]"},
		},
	}

	cleaned := stripPrefixInvalidatedHistoricalThinking(before, after)

	// Index 1 is BEFORE firstMutated (2) -> thinking block stays intact for prompt cache hit.
	if cleaned[1].Content[0].Type != ContentThinking || cleaned[1].Content[0].Signature != "sig_cached_0" {
		t.Fatalf("expected untouched prefix thinking at msg[1] to remain intact, got %+v", cleaned[1].Content)
	}
	// Index 3 is AFTER firstMutated (2) and BEFORE lastPlainUser (4) -> stale thinking must be stripped.
	for _, b := range cleaned[3].Content {
		if b.Type == ContentThinking {
			t.Fatalf("expected historical thinking after mutated prefix at msg[3] to be stripped, got %+v", cleaned[3].Content)
		}
	}
	if len(cleaned[3].Content) != 1 || cleaned[3].Content[0].Text != "I read a.go." {
		t.Fatalf("expected assistant text at msg[3] to be preserved, got %+v", cleaned[3].Content)
	}
	// Index 5 is AFTER lastPlainUser (4) -> active turn signed thinking must stay intact.
	if cleaned[5].Content[0].Type != ContentThinking || cleaned[5].Content[0].Signature != "sig_active_2" {
		t.Fatalf("expected active-turn thinking at msg[5] to remain intact, got %+v", cleaned[5].Content)
	}
}

func TestOpenAIResponsesEncryptedContentSignatureErrorRecovery(t *testing.T) {
	err := errors.New("stream HTTP 400: invalid encrypted_content signature on reasoning item rs_123")
	if !isRecoverableThinkingSignatureError(err) {
		t.Fatalf("expected encrypted_content error to be recognized as recoverable thinking signature error")
	}
}

func TestForegroundBashPromotesToBackgroundOnPendingSteerAndRespectsGraceWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell timing test")
	}
	restore := SetBashSteerPromoteGraceForTest(35 * time.Millisecond)
	defer restore()

	sessID := "ses_promote_direct_1"
	defer DeleteSessionBackgroundProcs(sessID)

	tool := &bashTool{}
	ws := t.TempDir()

	// 1. Fast command finishes well inside the 35ms grace window even when steer is already pending.
	fastSteer := &testWaitableSteer{}
	fastSteer.Push("quick steer")
	fastInput, _ := json.Marshal(map[string]any{"command": "printf 'fast-done'"})
	fastOut, err := tool.Execute(context.Background(), ToolContext{
		SessionID:   sessID,
		WorktreeDir: ws,
		Steer:       fastSteer,
	}, fastInput)
	if err != nil {
		t.Fatalf("fast bash failed: %v", err)
	}
	if fastOut.Output != "fast-done" {
		t.Fatalf("expected fast command to finish in foreground without promotion, got %q", fastOut.Output)
	}
	if prom, _ := fastOut.Metadata["promoted"].(bool); prom {
		t.Fatalf("fast command should not promote within grace window: %+v", fastOut)
	}

	// 2. Long command promotes cleanly to background after grace window without losing or duplicating output.
	slowSteer := &testWaitableSteer{}
	go func() {
		time.Sleep(25 * time.Millisecond)
		slowSteer.Push("switch task while build runs")
	}()

	slowInput, _ := json.Marshal(map[string]any{
		"command": "printf 'phase-before\\n'; sleep 0.30; printf 'phase-after\\n'",
	})
	start := time.Now()
	promOut, err := tool.Execute(context.Background(), ToolContext{
		SessionID:   sessID,
		WorktreeDir: ws,
		Steer:       slowSteer,
	}, slowInput)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("promoted bash returned error: %v", err)
	}
	if elapsed >= 260*time.Millisecond {
		t.Fatalf("expected foreground bash to promote promptly (~60ms), took %v", elapsed)
	}
	procID, _ := promOut.Metadata["procId"].(string)
	promoted, _ := promOut.Metadata["promoted"].(bool)
	if !promoted || procID == "" {
		t.Fatalf("expected promoted procId metadata, got %+v", promOut)
	}
	if !strings.Contains(promOut.Output, "[command promoted to background]") {
		t.Fatalf("expected promotion banner in tool output, got %q", promOut.Output)
	}

	// Wait for background completion and verify phase-after arrives with exitCode 0 and no duplicate phase-before if already flushed.
	var combinedAfter strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pollInput, _ := json.Marshal(map[string]any{
			"action": "output",
			"procId": procID,
			"waitMs": 250,
		})
		pollOut, pErr := tool.Execute(context.Background(), ToolContext{
			SessionID:   sessID,
			WorktreeDir: ws,
		}, pollInput)
		if pErr != nil {
			t.Fatalf("poll promoted job failed: %v", pErr)
		}
		combinedAfter.WriteString(pollOut.Output)
		running, _ := pollOut.Metadata["running"].(bool)
		if !running {
			exitCode, _ := pollOut.Metadata["exitCode"].(int)
			if exitCode != 0 {
				t.Fatalf("expected promoted command to finish with exit code 0, got %d (%s)", exitCode, pollOut.Output)
			}
			break
		}
	}
	full := promOut.Output + "\n" + combinedAfter.String()
	if !strings.Contains(full, "phase-before") || !strings.Contains(full, "phase-after") {
		t.Fatalf("expected both pre-promotion and post-promotion output across lifecycle, got:\n%s", full)
	}
}

func TestCodeIndexMapActionViaRepoMapHook(t *testing.T) {
	ci, ok := newToolRegistry().Get("hoxi_code_index")
	if !ok || ci == nil {
		t.Fatal("missing hoxi_code_index tool")
	}
	ws := t.TempDir()
	var gotDir, gotPrefix, gotQuery string
	var gotLimit int

	raw, _ := json.Marshal(map[string]any{
		"action": "map",
		"path":   "internal/agent",
		"query":  "RunTurn",
		"limit":  12,
	})
	out, err := ci.Execute(context.Background(), ToolContext{
		WorktreeDir: ws,
		RepoMap: func(_ context.Context, dir, pathPrefix, query string, limit int) (string, error) {
			gotDir, gotPrefix, gotQuery, gotLimit = dir, pathPrefix, query, limit
			return "Repository Backbone Map (12 files)\n1. internal/agent/engine.go [score=1000]", nil
		},
	}, raw)
	if err != nil || out.IsError {
		t.Fatalf("codeindex map failed: err=%v out=%+v", err, out)
	}
	if gotDir != ws || gotPrefix != "internal/agent" || gotQuery != "RunTurn" || gotLimit != 12 {
		t.Fatalf("unexpected RepoMap hook args: dir=%q prefix=%q query=%q limit=%d", gotDir, gotPrefix, gotQuery, gotLimit)
	}
	if !strings.Contains(out.Output, "internal/agent/engine.go") {
		t.Fatalf("unexpected RepoMap output: %q", out.Output)
	}
}

func TestAllDriversPropagateSessionID(t *testing.T) {
	baseMsgs := []Message{{
		Role:    RoleUser,
		Content: []ContentBlock{{Type: ContentText, Text: "hello"}},
	}}
	cases := []struct {
		name   string
		driver ModelDriver
		target llm.StreamTarget
	}{
		{"anthropic", newAnthropicDriver(), llm.StreamTarget{ProviderID: "p", ModelID: "claude-sonnet-4-5", Protocol: llm.ProtocolAnthropic}},
		{"openai-chat", newOpenAIDriver(false), llm.StreamTarget{ProviderID: "p", ModelID: "gpt-5", Protocol: llm.ProtocolOpenAI}},
		{"openai-responses", newOpenAIDriver(true), llm.StreamTarget{ProviderID: "p", ModelID: "gpt-5", Protocol: llm.ProtocolOpenAIResponses}},
		{"gemini", newGeminiDriver(), llm.StreamTarget{ProviderID: "p", ModelID: "gemini-2.5-pro", Protocol: llm.ProtocolGoogle}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpReq, err := tc.driver.BuildRequest(ModelRequest{
				SessionID: "  ses_drv_001  ",
				Target:    tc.target,
				Messages:  baseMsgs,
				MaxTokens: 256,
			})
			if err != nil {
				t.Fatalf("BuildRequest failed: %v", err)
			}
			if httpReq.SessionID != "ses_drv_001" {
				t.Fatalf("expected trimmed SessionID ses_drv_001, got %q", httpReq.SessionID)
			}
		})
	}
}
