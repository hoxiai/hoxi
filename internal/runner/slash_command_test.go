package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

type slashCaptureNativeEngine struct {
	mu      sync.Mutex
	lastReq NativeTurnRequest
	doneCh  chan struct{}
}

func (e *slashCaptureNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	e.mu.Lock()
	e.lastReq = req
	ch := e.doneCh
	e.mu.Unlock()
	if req.Emit != nil {
		req.Emit(NativeEvent{Kind: "text_start", PartID: "p1"})
		req.Emit(NativeEvent{Kind: "text_delta", PartID: "p1", Text: "Reviewed cleanly."})
		req.Emit(NativeEvent{Kind: "text_end", PartID: "p1", Text: "Reviewed cleanly."})
	}
	if ch != nil {
		close(ch)
	}
	return nil
}

func (e *slashCaptureNativeEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "summary", nil
}

func TestSlashCommandExpansionPreservesConciseTimelineBubble(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	cmdDir := filepath.Join(wsDir, ".hoxi", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdFile := "---\ndescription: Custom audit command\nagent: plan\nmodel: anthropic/claude-sonnet-4-5\n---\nAudit target path: $1\nExtra notes: $ARGUMENTS"
	if err := os.WriteFile(filepath.Join(cmdDir, "audit-path.md"), []byte(cmdFile), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(wsDir, worktree.NewManager(wsDir), nil, filepath.Join(t.TempDir(), "sessions.json"))
	defer svc.CloseAll()

	doneCh := make(chan struct{})
	capEng := &slashCaptureNativeEngine{doneCh: doneCh}
	svc.SetNativeEngine(capEng)

	rec := SessionRecord{
		ID:           "ses_slash_1",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   wsDir,
		WorktreePath: wsDir,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	rawUserText := "/audit-path internal/runner focus on concurrency"
	status, _, err := svc.startPromptWithReceipt(context.Background(), rec.ID, wsDir, PromptRequest{
		Text:    rawUserText,
		Runtime: string(RuntimeNative),
		Agent:   "build",
		Model:   &PromptModel{ProviderID: "openai", ModelID: "gpt-5"},
	})
	if err != nil || status != 200 {
		t.Fatalf("startPromptWithReceipt status=%d err=%v", status, err)
	}
	<-doneCh

	capEng.mu.Lock()
	gotReq := capEng.lastReq
	capEng.mu.Unlock()

	if gotReq.Mode != "plan" {
		t.Fatalf("expected slash command agent override 'plan', got %q", gotReq.Mode)
	}
	if gotReq.ProviderID != "anthropic" || gotReq.ModelID != "claude-sonnet-4-5" {
		t.Fatalf("expected slash command model override anthropic/claude-sonnet-4-5, got %s/%s", gotReq.ProviderID, gotReq.ModelID)
	}
	if !strings.Contains(gotReq.UserPrompt, "Audit target path: internal/runner") ||
		!strings.Contains(gotReq.UserPrompt, "Extra notes: internal/runner focus on concurrency") {
		t.Fatalf("expected expanded template in LLM prompt, got:\n%s", gotReq.UserPrompt)
	}

	stored, ok := svc.store.Get(rec.ID)
	if !ok || len(stored.Turns) == 0 {
		t.Fatalf("expected stored turn in session %s", rec.ID)
	}
	userParts := stored.Turns[0].Parts
	if len(userParts) == 0 || userParts[0].Text != rawUserText {
		t.Fatalf("expected concise raw slash command in timeline user bubble, got %+v", userParts)
	}
}
