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
