package runner

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestSplitTurnDirectiveKeepsSystemPrefixStable(t *testing.T) {
	stableBlock := "<hoxi_work_store_profile>store</hoxi_work_store_profile>\n\nAlways answer in Chinese."
	withDirective := stableBlock + "\n\n<hoxi_turn_directive>\n[Modality Router: Image]\n- Invoke hoxi_image.\n</hoxi_turn_directive>"
	stable, directive := splitTurnDirective(withDirective)
	plain, none := splitTurnDirective(stableBlock)
	if stable != plain || none != "" {
		t.Fatalf("system prefix must be identical with and without a turn directive:\n%q\n%q", stable, plain)
	}
	if directive != "[Modality Router: Image]\n- Invoke hoxi_image." {
		t.Fatalf("unexpected directive %q", directive)
	}
	got := withTurnDirective(directive, "draw a cat")
	if !strings.HasPrefix(got, "<hoxi_turn_directive>\n[Modality Router: Image]") || !strings.HasSuffix(got, "\n\ndraw a cat") {
		t.Fatalf("directive must travel with the user prompt: %q", got)
	}
	if withTurnDirective("", "draw a cat") != "draw a cat" {
		t.Fatal("prompts without a directive must be unchanged")
	}
}

func TestSplitTurnDirectiveIgnoresUnclosedOrEmptySections(t *testing.T) {
	unclosed := "base rule\n\n<hoxi_turn_directive>\nnever closed"
	if stable, directive := splitTurnDirective(unclosed); stable != unclosed || directive != "" {
		t.Fatalf("an unclosed tag must be left untouched, got %q / %q", stable, directive)
	}
	if stable, directive := splitTurnDirective("base\n\n<hoxi_turn_directive>  </hoxi_turn_directive>"); stable != "base" || directive != "" {
		t.Fatalf("empty directive must be dropped without leaking: %q / %q", stable, directive)
	}
	if got := joinPromptSections("sys", "dir"); got != "sys\n\ndir" || joinPromptSections("", "dir") != "dir" || joinPromptSections("sys", " ") != "sys" {
		t.Fatalf("joinPromptSections = %q", got)
	}
}

type multiTurnCaptureEngine struct {
	mu   sync.Mutex
	reqs []NativeTurnRequest
}

func (e *multiTurnCaptureEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	e.mu.Lock()
	e.reqs = append(e.reqs, req)
	e.mu.Unlock()
	if req.Emit != nil {
		req.Emit(NativeEvent{Kind: NativeEventTextStart, Step: 1, PartID: "p_txt"})
		req.Emit(NativeEvent{Kind: NativeEventTextDelta, Step: 1, PartID: "p_txt", Text: "ack"})
		req.Emit(NativeEvent{Kind: NativeEventTextEnd, Step: 1, PartID: "p_txt", Text: "ack"})
		req.Emit(NativeEvent{Kind: NativeEventTurnFinish, Step: 1, Finish: "stop"})
	}
	return nil
}

func (e *multiTurnCaptureEngine) Summarize(_ context.Context, _, _, _ string) (string, error) {
	return "summary", nil
}

func TestNativeTurnDirectiveReplaysByteIdenticalOnNextTurn(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	wsDir := t.TempDir()

	svc := NewService(wsDir, worktree.NewManager(wsDir), nil, filepath.Join(t.TempDir(), "sessions.json"))
	defer svc.CloseAll()

	capEng := &multiTurnCaptureEngine{}
	svc.SetNativeEngine(capEng)

	rec := SessionRecord{
		ID:           "ses_nat_directive_cache",
		Runtime:      string(RuntimeNative),
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		ProjectDir:   wsDir,
		WorktreePath: wsDir,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	// Plain prompt must not attach redundant nativeModelText metadata.
	plainParts := []MessagePart{{ID: "p1", Type: "text", Text: "plain question"}}
	attachNativeModelText(plainParts, "plain question")
	if plainParts[0].Metadata != nil {
		t.Fatalf("expected no nativeModelText metadata for plain prompt, got %+v", plainParts[0].Metadata)
	}

	// Turn 1 includes a one-off <hoxi_turn_directive> in System.
	ch1, unsub1 := svc.Subscribe(rec.ID)
	status, _, err := svc.startPromptWithReceipt(context.Background(), rec.ID, wsDir, PromptRequest{
		Text:    "帮我生成一张商品白底图",
		Runtime: string(RuntimeNative),
		Agent:   "build",
		Model:   &PromptModel{ProviderID: "openai", ModelID: "gpt-5"},
		System:  "Base stable system.\n\n<hoxi_turn_directive>\n[Modality Router: Image]\n- Invoke hoxi_image.\n</hoxi_turn_directive>",
	})
	if err != nil || status != 200 {
		unsub1()
		t.Fatalf("turn 1 failed: status=%d err=%v", status, err)
	}
	waitForRunnerSessionIdle(t, ch1)
	unsub1()

	// Stored timeline bubble must stay clean and user-visible without <hoxi_turn_directive>.
	stored, ok := svc.store.Get(rec.ID)
	if !ok || len(stored.Turns) < 2 {
		t.Fatalf("expected 2 stored turns after turn 1, got %d", len(stored.Turns))
	}
	if got := stored.Turns[0].Parts[0].Text; got != "帮我生成一张商品白底图" {
		t.Fatalf("expected clean stored user bubble text, got %q", got)
	}

	// Turn 2 has no <hoxi_turn_directive>; History[0] must remain byte-identical to Turn 1's live UserBlocks.
	ch2, unsub2 := svc.Subscribe(rec.ID)
	status, _, err = svc.startPromptWithReceipt(context.Background(), rec.ID, wsDir, PromptRequest{
		Text:    "把背景调亮一点",
		Runtime: string(RuntimeNative),
		Agent:   "build",
		Model:   &PromptModel{ProviderID: "openai", ModelID: "gpt-5"},
		System:  "Base stable system.",
	})
	if err != nil || status != 200 {
		unsub2()
		t.Fatalf("turn 2 failed: status=%d err=%v", status, err)
	}
	waitForRunnerSessionIdle(t, ch2)
	unsub2()

	capEng.mu.Lock()
	reqs := append([]NativeTurnRequest(nil), capEng.reqs...)
	capEng.mu.Unlock()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 captured turns, got %d", len(reqs))
	}
	if reqs[0].System != reqs[1].System {
		t.Fatalf("system prefix mutated across turns:\nturn1=%q\nturn2=%q", reqs[0].System, reqs[1].System)
	}
	if len(reqs[1].History) < 2 {
		t.Fatalf("expected turn 2 history to contain turn 1 user+assistant messages, got %+v", reqs[1].History)
	}
	if !reflect.DeepEqual(reqs[0].UserBlocks, reqs[1].History[0].Content) {
		t.Fatalf("turn 1 live user blocks diverged from turn 2 replayed History[0] (prompt cache bust):\nlive=%+v\nreplay=%+v", reqs[0].UserBlocks, reqs[1].History[0].Content)
	}

	// Exact whitespace preservation: leading/trailing newlines in nativeModelText must never be trimmed on replay.
	rawModelText := "\n<inline_skill>\nbody\n</inline_skill>\n\nuser text\n"
	parts := []MessagePart{{ID: "p_ws", Type: "text", Text: "user text"}}
	attachNativeModelText(parts, rawModelText)
	projected := svc.projectCanonicalNativeHistory(svc.rootDir, []MessageTurn{{
		Info:  MessageInfo{ID: "msg_ws", SessionID: rec.ID, Role: "user"},
		Parts: parts,
	}}, "openai", "gpt-5")
	if len(projected) != 1 || len(projected[0].Content) != 1 || projected[0].Content[0].Text != rawModelText {
		t.Fatalf("expected exact untrimmed nativeModelText replay, got %+v", projected)
	}
}
