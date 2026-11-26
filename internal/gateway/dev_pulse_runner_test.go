package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/runner"
)

func pulseTokenMessage(id, sessionID, modelID string, created int64, in, out, reasoning, cacheRead int) runner.MessageInfo {
	return runner.MessageInfo{
		ID:        id,
		SessionID: sessionID,
		Role:      "assistant",
		Time:      runner.MessageTime{Created: created, Completed: created + 1000},
		ModelID:   modelID,
		Tokens: &runner.MessageTokens{
			Input:     in,
			Output:    out,
			Reasoning: reasoning,
			Cache: struct {
				Write int `json:"write"`
				Read  int `json:"read"`
			}{Read: cacheRead},
		},
	}
}

func TestHistoricalCodexUsageBuckets(t *testing.T) {
	for _, tc := range []struct {
		name, runtime      string
		input, total, want int
	}{
		{"legacy", "codex", 1200, 1280, 800},
		{"canonical", "codex", 800, 1280, 800},
		{"unknown total", "codex", 1200, 0, 1200},
		{"other runtime", "claude-code", 1200, 1280, 1200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := pulseTokenMessage("msg_a_old", "ses_test", "model", 1, tc.input, 80, 0, 400)
			info.Tokens.Total = tc.total
			rec := runner.SessionRecord{Runtime: tc.runtime}
			u := usageSummaryToPulseUsage(runner.TurnUsageSummary{ID: info.ID, Tokens: info.Tokens}, rec)
			entry, ok := requestLogFromTurn(runner.MessageTurn{Info: info}, rec, tc.runtime, 0, false)
			if !ok || entry.InputTokens != int64(tc.want) || u.InputTokens != int64(tc.want) ||
				u.CacheReadTokens != 400 || entry.CacheReadTokens != 400 || info.Tokens.Input != tc.input {
				t.Fatalf("unexpected accounting: pulse=%+v log=%+v stored=%+v", u, entry, info.Tokens)
			}
		})
	}
}

// Native CLI runtimes (Claude Code, Antigravity, Qoder, CodeBuddy) never write
// to hoxi.db. Their usage lives only in the runner transcript store, so the
// pulse must fold it in or today's tokens silently under-report.
func TestCollectRunnerDailyUsageFoldsNativeRuntimes(t *testing.T) {
	root := t.TempDir()
	svc := runner.NewService(root, nil, nil, filepath.Join(root, "sessions"))

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	today := now.UnixMilli()
	yesterday := start - 60_000

	put := func(id, runtime string, turns ...runner.MessageInfo) {
		t.Helper()
		rec := runner.SessionRecord{
			ID:           id,
			Runtime:      runtime,
			ProjectDir:   root,
			WorktreePath: root,
			SandboxMode:  "local",
			ModelID:      "claude-opus-5-5-high",
			ProviderID:   runtime,
			Title:        id,
			CreatedAt:    today,
			UpdatedAt:    today,
		}
		for _, info := range turns {
			rec.Turns = append(rec.Turns, runner.MessageTurn{Info: info})
		}
		if err := svc.Store().Put(rec); err != nil {
			t.Fatalf("put session %s: %v", id, err)
		}
	}

	put("ses_agy_today", string(runner.RuntimeAntigravity),
		pulseTokenMessage("msg_a1", "ses_agy_today", "gemini-4-argon-high", today, 1000, 200, 50, 400),
		pulseTokenMessage("msg_a2", "ses_agy_today", "gemini-4-argon-high", today+1, 300, 100, 0, 200),
	)
	_, _, _ = svc.Store().Update("ses_agy_today", func(r *runner.SessionRecord) {
		r.Turns[0].Parts = []runner.MessagePart{
			{Type: "tool", State: &runner.ToolState{Status: "completed"}},
			{Type: "tool", State: &runner.ToolState{Status: "error"}},
		}
	})
	put("ses_qdr_today", string(runner.RuntimeQoder),
		pulseTokenMessage("msg_q1", "ses_qdr_today", "DeepSeek-Flash", today, 500, 20, 0, 0),
	)
	// Yesterday's turns must not leak into today's pulse.
	put("ses_cld_yesterday", string(runner.RuntimeClaudeCode),
		pulseTokenMessage("msg_c1", "ses_cld_yesterday", "claude-opus-5-5-high", yesterday, 9999, 9999, 0, 0),
	)
	// OpenCode usage is already counted from hoxi.db; adding it here would
	// double count.
	put("ses_hoxi_today", "opencode",
		pulseTokenMessage("msg_o1", "ses_hoxi_today", "deepseek-v4.1-flash-solo", today, 7777, 777, 0, 0),
	)

	res := gitDailyPulseResponse{DbModels: []dailyModelStat{}}
	g := &Gateway{runner: svc}
	g.collectRunnerDailyUsage(start, &res)

	const wantInput = int64(1000 + 300 + 500)
	const wantOutput = int64(200 + 100 + 20)
	const wantReasoning = int64(50)
	const wantCacheRead = int64(400 + 200)

	if res.DbInputTokens != wantInput {
		t.Fatalf("input tokens = %d, want %d", res.DbInputTokens, wantInput)
	}
	if res.DbOutputTokens != wantOutput {
		t.Fatalf("output tokens = %d, want %d", res.DbOutputTokens, wantOutput)
	}
	if res.DbReasoningTokens != wantReasoning {
		t.Fatalf("reasoning tokens = %d, want %d", res.DbReasoningTokens, wantReasoning)
	}
	if res.DbCacheReadTokens != wantCacheRead {
		t.Fatalf("cache read tokens = %d, want %d", res.DbCacheReadTokens, wantCacheRead)
	}
	if res.DbTurnsCount != 3 {
		t.Fatalf("turn count = %d, want 3", res.DbTurnsCount)
	}
	wantTotal := wantInput + wantOutput + wantReasoning + wantCacheRead
	if res.DbTotalTokens != wantTotal {
		t.Fatalf("total tokens = %d, want %d", res.DbTotalTokens, wantTotal)
	}
	if res.DbLatestTurnTs != today+1 {
		t.Fatalf("latest turn ts = %d, want %d", res.DbLatestTurnTs, today+1)
	}
	if res.DbSessionsCount != 2 {
		t.Fatalf("sessions count = %d, want 2", res.DbSessionsCount)
	}
	if res.DbToolCallsTotal != 2 || res.DbToolCallsFailed != 1 {
		t.Fatalf("tool calls = %d total / %d failed, want 2 / 1", res.DbToolCallsTotal, res.DbToolCallsFailed)
	}

	var antigravity, qoder *dailyModelStat
	for i := range res.DbModels {
		switch res.DbModels[i].ProviderID {
		case string(runner.RuntimeAntigravity):
			antigravity = &res.DbModels[i]
		case string(runner.RuntimeQoder):
			qoder = &res.DbModels[i]
		case "opencode":
			t.Fatalf("opencode usage must not be folded in: %+v", res.DbModels[i])
		}
	}
	if antigravity == nil || antigravity.ModelID != "gemini-4-argon-high" || antigravity.Turns != 2 {
		t.Fatalf("unexpected antigravity model stat: %+v", antigravity)
	}
	if qoder == nil || qoder.ModelID != "DeepSeek-Flash" {
		t.Fatalf("unexpected qoder model stat: %+v", qoder)
	}
}

// A native model already present in the SQLite aggregation must be additive,
// not replaced: appending to DbModels reallocates the slice.
func TestCollectRunnerDailyUsageAppendsToExistingModels(t *testing.T) {
	root := t.TempDir()
	svc := runner.NewService(root, nil, nil, filepath.Join(root, "sessions"))

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	today := now.UnixMilli()

	rec := runner.SessionRecord{
		ID: "ses_agy_mix", Runtime: string(runner.RuntimeAntigravity),
		ProjectDir: root, WorktreePath: root, SandboxMode: "local",
		Title: "mix", CreatedAt: today, UpdatedAt: today,
		Turns: []runner.MessageTurn{
			{Info: pulseTokenMessage("msg_m1", "ses_agy_mix", "gemini-4-argon-high", today, 100, 10, 0, 0)},
		},
	}
	if err := svc.Store().Put(rec); err != nil {
		t.Fatal(err)
	}

	res := gitDailyPulseResponse{
		DbModels: []dailyModelStat{
			{ProviderID: "antigravity", ModelID: "gemini-4-argon-high", Turns: 1, InputTokens: 900, TotalTokens: 900},
		},
	}
	g := &Gateway{runner: svc}
	g.collectRunnerDailyUsage(start, &res)

	if len(res.DbModels) != 1 {
		t.Fatalf("expected a single merged model row, got %+v", res.DbModels)
	}
	if res.DbModels[0].InputTokens != 1000 {
		t.Fatalf("merged input tokens = %d, want 1000", res.DbModels[0].InputTokens)
	}
	if res.DbInputTokens != 100 {
		t.Fatalf("summary input tokens = %d, want only native 100", res.DbInputTokens)
	}
}

func TestCollectRunnerDailyUsageHandlesMixedRuntimeSwitchSessions(t *testing.T) {
	root := t.TempDir()
	svc := runner.NewService(root, nil, nil, filepath.Join(root, "sessions"))
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	today := now.UnixMilli()

	// Session 1 started on Antigravity (msg_a_*) then switched to OpenCode:
	// the Antigravity turn must still be counted, while the OpenCode turn (msg_oc1) must be skipped.
	agyTurn := pulseTokenMessage("msg_a_1", "ses_switched_to_oc", "gemini-4-argon-high", today, 400, 50, 10, 100)
	agyTurn.ProviderID = string(runner.RuntimeAntigravity)
	ocTurn := pulseTokenMessage("msg_oc1", "ses_switched_to_oc", "gpt-5", today+1, 9000, 900, 0, 0)
	ocTurn.ProviderID = "openai"
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_switched_to_oc", Runtime: "opencode", OpenCodeIDs: []string{"ses_oc_inner"},
		ProjectDir: root, WorktreePath: root, SandboxMode: "local",
		Title: "switched to opencode", CreatedAt: today, UpdatedAt: today + 1,
		Turns: []runner.MessageTurn{{Info: agyTurn}, {Info: ocTurn}},
	}); err != nil {
		t.Fatal(err)
	}

	// Session 2 started on OpenCode then switched to Antigravity:
	// the earlier OpenCode turn (msg_oc2) must not be double-counted under Antigravity.
	ocTurn2 := pulseTokenMessage("msg_oc2", "ses_switched_to_agy", "gpt-5", today, 8000, 800, 0, 0)
	ocTurn2.ProviderID = "openai"
	agyTurn2 := pulseTokenMessage("msg_a_2", "ses_switched_to_agy", "gemini-4-argon-high", today+2, 600, 70, 20, 200)
	agyTurn2.ProviderID = string(runner.RuntimeAntigravity)
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_switched_to_agy", Runtime: string(runner.RuntimeAntigravity), OpenCodeIDs: []string{"ses_oc_inner_2"},
		ProjectDir: root, WorktreePath: root, SandboxMode: "local",
		Title: "switched to agy", CreatedAt: today, UpdatedAt: today + 2,
		Turns: []runner.MessageTurn{{Info: ocTurn2}, {Info: agyTurn2}},
	}); err != nil {
		t.Fatal(err)
	}

	res := gitDailyPulseResponse{DbModels: []dailyModelStat{}}
	g := &Gateway{runner: svc}
	g.collectRunnerDailyUsage(start, &res)

	if res.DbTurnsCount != 2 || res.DbInputTokens != 1000 || res.DbOutputTokens != 120 || res.DbReasoningTokens != 30 || res.DbCacheReadTokens != 300 {
		t.Fatalf("unexpected mixed-session rollup: turns=%d in=%d out=%d reasoning=%d cacheRead=%d",
			res.DbTurnsCount, res.DbInputTokens, res.DbOutputTokens, res.DbReasoningTokens, res.DbCacheReadTokens)
	}
	if len(res.DbModels) != 1 || res.DbModels[0].ProviderID != string(runner.RuntimeAntigravity) {
		t.Fatalf("expected only antigravity model usage, got %+v", res.DbModels)
	}
}

func TestCollectRunnerDailyUsageSkipsImportedNativeTurnsAndTracksActiveTurns(t *testing.T) {
	root := t.TempDir()
	svc := runner.NewService(root, nil, nil, filepath.Join(root, "sessions"))
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	today := now.UnixMilli()

	// 1. Newly materialized native handoff session: imported historical turns must
	// be skipped in both Daily Pulse and Request Log, while post-import Hoxi turns count.
	matInfo, err := svc.MaterializeNativeHandoff(runner.NativeHandoffRequest{
		TargetDirectory: root,
		Title:           "[Codex] Imported Today",
		ProviderID:      "openai",
		ModelID:         "gpt-5.4",
		CreatedAt:       today - 10_000,
		UpdatedAt:       today - 5_000,
		Turns: []runner.NativeHandoffTurn{
			{
				ID:          "msg_019imported1",
				Role:        "assistant",
				CreatedMs:   today - 8_000,
				CompletedMs: today - 7_000,
				ProviderID:  "openai",
				ModelID:     "gpt-5.4",
				Tokens:      &runner.NativeHandoffTokens{Total: 9500, Input: 9000, Output: 500},
			},
		},
	})
	if err != nil {
		t.Fatalf("MaterializeNativeHandoff: %v", err)
	}
	matID, _ := matInfo["id"].(string)
	postTurn := pulseTokenMessage("msg_a_post_1", matID, "gpt-5.4", today-2_000, 300, 40, 10, 50)
	postTurn.ProviderID = "openai"
	if _, _, err := svc.Store().Update(matID, func(rec *runner.SessionRecord) {
		rec.Turns = append(rec.Turns, runner.MessageTurn{Info: postTurn})
		rec.UpdatedAt = today - 1_000
	}); err != nil {
		t.Fatal(err)
	}

	// 2. Legacy pre-fix native imported session (no Imported flag, handoff title prefix,
	// non-msg_a_ ID): imported turn is excluded, post-import in-flight turn is counted
	// and exposed in DbActiveTurns.
	legacyImp := pulseTokenMessage("msg_019legacyimp", "ses_nat_legacy_imp", "claude-opus-5", today-6_000, 8000, 400, 0, 0)
	legacyImp.ProviderID = "anthropic"
	inFlight := pulseTokenMessage("msg_a_live_1", "ses_nat_legacy_imp", "claude-opus-5", today-500, 200, 25, 5, 100)
	inFlight.ProviderID = "anthropic"
	inFlight.Time.Completed = 0
	if err := svc.Store().Put(runner.SessionRecord{
		ID: "ses_nat_legacy_imp", Runtime: string(runner.RuntimeNative),
		ProjectDir: root, WorktreePath: root, SandboxMode: "local",
		Title: "[Claude Code] Earlier Task", CreatedAt: today - 9_000, UpdatedAt: today,
		Turns: []runner.MessageTurn{{Info: legacyImp}, {Info: inFlight}},
	}); err != nil {
		t.Fatal(err)
	}

	res := gitDailyPulseResponse{DbModels: []dailyModelStat{}}
	g := &Gateway{runner: svc}
	g.collectRunnerDailyUsage(start, &res)

	if res.DbTurnsCount != 2 || res.DbInputTokens != 500 || res.DbOutputTokens != 65 || res.DbReasoningTokens != 15 || res.DbCacheReadTokens != 150 {
		t.Fatalf("unexpected imported-filtered rollup: turns=%d in=%d out=%d reasoning=%d cacheRead=%d",
			res.DbTurnsCount, res.DbInputTokens, res.DbOutputTokens, res.DbReasoningTokens, res.DbCacheReadTokens)
	}
	active, ok := res.DbActiveTurns["msg_a_live_1"]
	if !ok || active.Input != 200 || active.Output != 25 || active.Reasoning != 5 || active.CacheRead != 100 {
		t.Fatalf("expected in-flight turn in DbActiveTurns, got %+v", res.DbActiveTurns)
	}
	if _, exists := res.DbActiveTurns["msg_a_post_1"]; exists {
		t.Fatalf("completed turn must not appear in DbActiveTurns: %+v", res.DbActiveTurns)
	}

	reqEntries := g.collectRequestLog()
	for _, entry := range reqEntries {
		if entry.ID == "msg_019imported1" || entry.ID == "msg_019legacyimp" {
			t.Fatalf("imported turn %s must be excluded from request log", entry.ID)
		}
	}
}
