package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestSeedFromPriorTurnWithPruneRestoresWarmPrunedPrefix(t *testing.T) {
	now := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)
	target := llm.StreamTarget{
		ProviderID:   "openai",
		ModelID:      "gpt-5",
		Protocol:     llm.ProtocolOpenAIResponses,
		HasEndpoint:  true,
		ContextLimit: 12_000,
		OutputLimit:  2_000,
	}
	thresholds := resolveTurnContextThresholds(target)
	specs := []ToolSpec{{Name: "read", Description: "read file"}}
	const fullSystem = "stable system prompt"

	makeStep := func(id, body string) []Message {
		in, _ := json.Marshal(map[string]any{"filePath": id + ".go"})
		return []Message{
			{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: id, Name: "read", Input: in}}},
			{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: id, Text: body}}},
		}
	}

	var unprunedTurn1 []Message
	unprunedTurn1 = append(unprunedTurn1, Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "investigate"}}})
	verbose := strings.Repeat("verbose file line for pruning continuity test\n", 90) // ~4140 bytes (~1100 est tokens)
	for i := 0; i < 6; i++ {
		unprunedTurn1 = append(unprunedTurn1, makeStep(fmt.Sprintf("c%d", i), verbose)...)
	}
	signedMessage := func(signature string) Message {
		return Message{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentThinking, Text: "reasoning", Signature: signature},
			{Type: ContentText, Text: "summary"},
		}}
	}
	historical := len(unprunedTurn1)
	unprunedTurn1 = append(unprunedTurn1, signedMessage("historical"),
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "continue"}}})
	unprunedTurn1 = append(unprunedTurn1, signedMessage("active"))
	for i := 6; i < 10; i++ {
		unprunedTurn1 = append(unprunedTurn1, makeStep(fmt.Sprintf("c%d", i), "short ok")...)
	}

	// Simulate Turn 1: pre-prune total exceeds triggerTokens (9000), pruning Chunk 0.
	t1Tracker := newContextTokenTracker()
	t1Tracker.nowFn = func() time.Time { return now }
	t1Tracker.ObserveStep(unprunedTurn1[:len(unprunedTurn1)-2], fullSystem, specs, TokenUsage{Input: 1000, CacheRead: 8500})
	preTotal := t1Tracker.EstimateTotalTokens(unprunedTurn1, fullSystem, specs)
	if preTotal <= thresholds.triggerTokens {
		t.Fatalf("expected preTotal (%d) > trigger (%d)", preTotal, thresholds.triggerTokens)
	}
	engine := &Engine{defaultCompact: true}
	prunedTurn1, changed := engine.applyCompactWithBudget(unprunedTurn1, fullSystem, "", specs, 12000, thresholds, t1Tracker)
	if !changed {
		t.Fatal("expected Turn 1 pruning to mutate Chunk 0")
	}
	if prunedTurn1[historical].Content[0].Type != ContentText || prunedTurn1[historical+2].Content[0].Signature != "active" {
		t.Fatal("live pruning must invalidate only historical reasoning after the first changed tool result")
	}
	t1Tracker.RebaseAfterPrune(unprunedTurn1, prunedTurn1, fullSystem, specs)
	t1Tracker.ObserveStep(prunedTurn1, fullSystem, specs, TokenUsage{Input: 500, CacheRead: 5500, ReportedCacheInput: 6000})
	t1Tracker.ObservePrefixCacheStability("ses_prune_cont", target, "ses_prune_cont", fullSystem, specs, prunedTurn1, TokenUsage{Input: 500, CacheRead: 5500, ReportedCacheInput: 6000})
	snap, ok := t1Tracker.Snapshot()
	if !ok || !snap.prunedInTurn || snap.prePrunePrompt <= thresholds.triggerTokens {
		t.Fatalf("expected snapshot to record prunedInTurn and prePrunePrompt > trigger, got %+v", snap)
	}

	// Turn 2 replays UNPRUNED durable history from disk + new Turn 2 user message.
	turn2Unpruned := append(append([]Message(nil), unprunedTurn1...),
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "done"}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "follow up"}}},
	)
	t2Tracker := newContextTokenTracker()
	t2Tracker.nowFn = func() time.Time { return now.Add(30 * time.Second) }
	adopted := t2Tracker.SeedFromPriorTurnWithPrune(snap, now.Add(30*time.Second), "ses_prune_cont", fullSystem, "", specs, thresholds, turn2Unpruned)

	if !t2Tracker.anchored || !t2Tracker.IsPrefixCacheWarm() {
		t.Fatalf("expected Turn 2 to adopt matching pruned prefix and stay warm, anchored=%v warm=%v", t2Tracker.anchored, t2Tracker.IsPrefixCacheWarm())
	}
	if !messagesSliceMutated(turn2Unpruned, adopted) {
		t.Fatal("expected Turn 2 to adopt the pruned prefix matching Turn 1")
	}
	// Prefix of adopted must be byte-identical to Turn 1's prunedTurn1!
	for i := range prunedTurn1 {
		if !messageBlocksIdentical(prunedTurn1[i], adopted[i]) {
			t.Fatalf("adopted Turn 2 message[%d] diverged from Turn 1 pruned prefix", i)
		}
	}

	// A provider may report total input while omitting the cache-read breakdown.
	unreported := TokenUsage{Input: 6000}
	t1Tracker.ObserveStep(prunedTurn1, fullSystem, specs, unreported)
	t1Tracker.ObservePrefixCacheStability("ses_prune_cont", target, "ses_prune_cont", fullSystem, specs, prunedTurn1, unreported)
	unreportedSnap, ok := t1Tracker.Snapshot()
	next := newContextTokenTracker()
	replayed := next.SeedFromPriorTurnWithPrune(unreportedSnap, now.Add(30*time.Second), "ses_prune_cont", fullSystem, "", specs, thresholds, turn2Unpruned)
	if !ok || !next.anchored || !messagesSliceMutated(turn2Unpruned, replayed) {
		t.Fatal("missing cache telemetry prevented replay of a verified pruned prefix")
	}

	// Rejection: if Turn 2 history actually mutated earlier text, SeedFromPriorTurnWithPrune MUST NOT prune or mark warm.
	turn2Mutated := append([]Message(nil), turn2Unpruned...)
	turn2Mutated[0] = Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "different prompt"}}}
	t2Rej := newContextTokenTracker()
	t2Rej.nowFn = func() time.Time { return now.Add(30 * time.Second) }
	rejected := t2Rej.SeedFromPriorTurnWithPrune(snap, now.Add(30*time.Second), "ses_prune_cont", fullSystem, "", specs, thresholds, turn2Mutated)
	if t2Rej.anchored || t2Rej.IsPrefixCacheWarm() {
		t.Fatalf("mutated prefix must not anchor or mark warm: anchored=%v warm=%v", t2Rej.anchored, t2Rej.IsPrefixCacheWarm())
	}
	if messagesSliceMutated(turn2Mutated, rejected) {
		t.Fatal("mutated Turn 2 history must remain untouched when SHA-256 prefix does not match")
	}
}

func TestEncryptedContentSignatureEstimationStaysBounded(t *testing.T) {
	// 50 steps of ~10 KB OpenAI Responses encrypted_content (~500 KB ciphertext) must not
	// be inflated as 65K+ phantom text tokens.
	bigSig := strings.Repeat("A", 10_000)
	var msgs []Message
	for i := 0; i < 50; i++ {
		msgs = append(msgs, Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{{Type: ContentThinking, Signature: bigSig}},
		})
	}
	est := estimateTokens(msgs, "")
	if est > 22_000 {
		t.Fatalf("expected 50x10KB encrypted_content signatures to stay realistically bounded (<= 22K est tokens), got %d", est)
	}
}
