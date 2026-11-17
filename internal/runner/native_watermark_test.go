package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type multiStepUsageNativeEngine struct {
	done chan struct{}
}

func (m *multiStepUsageNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	defer close(m.done)
	if req.Emit == nil {
		return nil
	}
	// Parent step 1: 24,500 context tokens
	req.Emit(NativeEvent{
		Kind:  NativeEventTurnUsage,
		Usage: &NativeTokenUsage{Input: 4000, CacheRead: 20000, Output: 500},
	})
	// Child subagent step: 18,800 tokens (must contribute to billing, NOT parent context watermark)
	req.Emit(NativeEvent{
		Kind:         NativeEventTurnUsage,
		ParentCallID: "call_sub_usage",
		Usage:        &NativeTokenUsage{Input: 3000, CacheRead: 15000, Output: 800},
	})
	// Parent step 2 (final step): 30,500 context tokens
	req.Emit(NativeEvent{
		Kind:  NativeEventTurnUsage,
		Usage: &NativeTokenUsage{Input: 5000, CacheRead: 24500, Output: 600, Reasoning: 400},
	})
	req.Emit(NativeEvent{Kind: NativeEventTextStart, PartID: "prt_fin"})
	req.Emit(NativeEvent{Kind: NativeEventTextEnd, PartID: "prt_fin", Text: "Done."})
	return nil
}

func (m *multiStepUsageNativeEngine) Summarize(_ context.Context, _, _, transcript string) (string, error) {
	return transcript, nil
}

func TestNativeTurnSeparatesSingleStepContextWatermarkFromMultiStepBilling(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(workspace, nil, nil, filepath.Join(root, "sessions.json"))
	eng := &multiStepUsageNativeEngine{done: make(chan struct{})}
	svc.SetNativeEngine(eng)

	rec := SessionRecord{
		ID:           "ses_watermark",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   workspace,
		WorktreePath: workspace,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	userMsg := MessageInfo{ID: "msg_u_wm", SessionID: rec.ID, Role: "user"}
	storedParts := []MessagePart{{ID: "prt_u_wm", SessionID: rec.ID, MessageID: userMsg.ID, Type: "text", Text: "go"}}
	if err := svc.startNativeTurn(rec, "code", "gpt-5", "", "", "go", userMsg, storedParts, "msg_a_wm", storedParts...); err != nil {
		t.Fatalf("startNativeTurn failed: %v", err)
	}
	select {
	case <-eng.done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for multiStepUsageNativeEngine")
	}
	svc.CloseAll()

	saved, ok := svc.store.Get(rec.ID)
	if !ok || len(saved.Turns) < 2 || saved.Turns[1].Info.Tokens == nil {
		t.Fatalf("expected persisted assistant turn with tokens, got: %+v", saved.Turns)
	}
	tok := saved.Turns[1].Info.Tokens
	// Latest parent single-step window occupancy = 5000 + 24500 + 600 + 400 = 30,500
	if tok.Context != 30500 || tok.Total != 30500 {
		t.Fatalf("expected single-step context watermark 30500, got Context=%d Total=%d", tok.Context, tok.Total)
	}
	// Cumulative billing across both parent steps + child subagent
	if tok.Input != 12000 || tok.Cache.Read != 59500 || tok.Output != 1900 || tok.Reasoning != 400 {
		t.Fatalf("expected cumulative billing buckets {Input:12000 CacheRead:59500 Output:1900 Reasoning:400}, got: %+v", tok)
	}
}
