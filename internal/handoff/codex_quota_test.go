package handoff

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func buildTestJWT(claims map[string]interface{}) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	bodyBytes, _ := json.Marshal(claims)
	body := base64.RawURLEncoding.EncodeToString(bodyBytes)
	return hdr + "." + body + ".sig"
}

func TestCollectCodexQuotaSnapshotAndWindowRollover(t *testing.T) {
	cDir := t.TempDir()
	t.Setenv("CODEX_HOME", cDir)

	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	jwt := buildTestJWT(map[string]interface{}{
		"exp": now.Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_plan_type": "Pro",
		},
	})
	authDoc := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"access_token":%q,"refresh_token":"ref-1","account_id":"acct-1"}}`, jwt)
	if err := os.WriteFile(filepath.Join(cDir, "auth.json"), []byte(authDoc), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	rolloutDir := filepath.Join(cDir, "sessions", "2026", "11", "10")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-1.jsonl")
	primaryResetSec := now.Add(3 * time.Hour).Unix()
	secondaryResetSec := now.Add(4 * 24 * time.Hour).Unix()
	eventTs := now.Add(-15 * time.Minute).Format(time.RFC3339)

	lines := fmt.Sprintf(
		`{"type":"response_item","timestamp":%q,"payload":{"type":"message","role":"assistant","content":"Done."}}`+"\n"+
			`{"type":"token_usage_record","timestamp":%q,"payload":{"turn_id":"t1","usage":{"input_tokens":1200,"cached_input_tokens":300,"output_tokens":200,"reasoning_output_tokens":50}}}`+"\n"+
			`{"type":"event_msg","timestamp":%q,"payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1200,"cached_input_tokens":300,"output_tokens":200,"reasoning_output_tokens":50}},"rate_limits":{"primary":{"used_percent":22.5,"window_minutes":300,"resets_at":%d},"secondary":{"used_percent":64.0,"window_minutes":10080,"resets_at":%d}}}}`+"\n",
		eventTs, eventTs, eventTs, primaryResetSec, secondaryResetSec,
	)
	if err := os.WriteFile(rolloutPath, []byte(lines), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	snap := collectCodexQuotaSnapshot(cDir, now)
	if snap == nil {
		t.Fatal("expected non-nil Codex quota snapshot")
	}
	if snap.PlanType != "pro" {
		t.Fatalf("expected planType=pro, got %q", snap.PlanType)
	}
	if snap.Primary == nil || snap.Primary.UsedPercent == nil || snap.Primary.RemainingPercent == nil {
		t.Fatalf("expected primary quota window, got %+v", snap.Primary)
	}
	if *snap.Primary.UsedPercent != 22.5 || *snap.Primary.RemainingPercent != 77.5 || snap.Primary.WindowMinutes != 300 {
		t.Fatalf("unexpected primary window: used=%v rem=%v min=%d", *snap.Primary.UsedPercent, *snap.Primary.RemainingPercent, snap.Primary.WindowMinutes)
	}
	if snap.Secondary == nil || snap.Secondary.UsedPercent == nil || snap.Secondary.RemainingPercent == nil {
		t.Fatalf("expected secondary (weekly) quota window, got %+v", snap.Secondary)
	}
	if *snap.Secondary.UsedPercent != 64.0 || *snap.Secondary.RemainingPercent != 36.0 || snap.Secondary.WindowMinutes != 10080 {
		t.Fatalf("unexpected secondary window: used=%v rem=%v min=%d", *snap.Secondary.UsedPercent, *snap.Secondary.RemainingPercent, snap.Secondary.WindowMinutes)
	}
	if snap.WeeklyTokens != 1400 || snap.WeeklyTurns != 1 {
		t.Fatalf("expected weeklyTokens=1400 weeklyTurns=1, got tokens=%d turns=%d", snap.WeeklyTokens, snap.WeeklyTurns)
	}

	// Verify expired primary window resets to 0% used / 100% remaining while weekly window stays active.
	afterPrimaryReset := now.Add(4 * time.Hour)
	snapAfter := collectCodexQuotaSnapshot(cDir, afterPrimaryReset)
	if snapAfter == nil || snapAfter.Primary == nil {
		t.Fatalf("expected snapshot after primary reset, got %+v", snapAfter)
	}
	if *snapAfter.Primary.UsedPercent != 0 || *snapAfter.Primary.RemainingPercent != 100 || snapAfter.Primary.ResetsAt != 0 {
		t.Fatalf("expected expired primary window to roll over to 100%% remaining, got used=%v rem=%v resetsAt=%d",
			*snapAfter.Primary.UsedPercent, *snapAfter.Primary.RemainingPercent, snapAfter.Primary.ResetsAt)
	}
	if *snapAfter.Secondary.RemainingPercent != 36.0 {
		t.Fatalf("expected weekly window to remain at 36%% remaining, got %v", *snapAfter.Secondary.RemainingPercent)
	}
}

func TestLocalCLIStatusExposesCodexAntigravityAndClaudeQuota(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := setupSyntheticCLIHome(t, workspaceDir)

	now := time.Now().UTC()
	recentISO := now.Add(-1 * time.Hour).Format(time.RFC3339)
	resetPrimarySec := now.Add(2 * time.Hour).Unix()
	resetWeeklySec := now.Add(5 * 24 * time.Hour).Unix()

	// 1. Enrich Codex rollout with rate_limits and recent timestamp
	rolloutPath := filepath.Join(homeDir, ".codex", "sessions", "2026", "10", "17", "rollout-codex-1.jsonl")
	codexLine := fmt.Sprintf(
		`{"type":"response_item","timestamp":%q,"payload":{"type":"message","role":"assistant","content":"ok"}}`+"\n"+
			`{"type":"event_msg","timestamp":%q,"payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2000,"output_tokens":500}},"rate_limits":{"plan_type":"plus","primary":{"used_percent":15,"window_minutes":300,"resets_at":%d},"secondary":{"used_percent":40,"window_minutes":10080,"resets_at":%d}}}}`+"\n",
		recentISO, recentISO, resetPrimarySec, resetWeeklySec,
	)
	if err := os.WriteFile(rolloutPath, []byte(codexLine), 0o644); err != nil {
		t.Fatalf("write codex rollout: %v", err)
	}

	// 2. Enrich Antigravity summary & transcript with recent timestamps + token counts
	agyDB := filepath.Join(homeDir, ".gemini", "antigravity", "conversation_summaries.db")
	runSQLite(t, agyDB, fmt.Sprintf("UPDATE conversation_summaries SET last_modified_time = %s, last_user_input_time = %s WHERE conversation_id = 'gemini-sess-1';",
		sqlEscapeLiteral(recentISO), sqlEscapeLiteral(recentISO)))
	agyTranscript := filepath.Join(homeDir, ".gemini", "antigravity", "brain", "gemini-sess-1", ".system_generated", "logs", "transcript.jsonl")
	agyLine := fmt.Sprintf(`{"type":"PLANNER_RESPONSE","created_at":%q,"content":"done","input_tokens":3000,"output_tokens":400,"thinking_tokens":100,"cache_read_tokens":500}`+"\n", recentISO)
	if err := os.WriteFile(agyTranscript, []byte(agyLine), 0o644); err != nil {
		t.Fatalf("write agy transcript: %v", err)
	}

	// 3. Enrich Claude Code with subscriptionType in .credentials.json and recent assistant turn
	if err := os.WriteFile(filepath.Join(homeDir, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-test","subscriptionType":"max"}}`), 0o600); err != nil {
		t.Fatalf("write claude credentials: %v", err)
	}
	claudeFile := filepath.Join(homeDir, ".claude", "projects", encodeClaudeProjectHint(workspaceDir), "claude-sess-1.jsonl")
	claudeLine := fmt.Sprintf(
		`{"type":"user","timestamp":%q,"cwd":%q,"message":{"content":[{"type":"text","text":"Run check"}]}}`+"\n"+
			`{"type":"assistant","timestamp":%q,"message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":1200,"output_tokens":80,"cache_read_input_tokens":400,"cache_creation_input_tokens":50},"content":[{"type":"text","text":"Done"}]}}`+"\n",
		recentISO, workspaceDir, recentISO,
	)
	if err := os.WriteFile(claudeFile, []byte(claudeLine), 0o644); err != nil {
		t.Fatalf("write claude session: %v", err)
	}

	svc := NewService(0, "test", workspaceDir, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/hoxi/local-cli/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp LocalCLIStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}

	byID := map[ExternalToolID]LocalCLIStatusItem{}
	for _, item := range resp.Tools {
		byID[item.ID] = item
	}

	codex := byID[ExternalToolCodex]
	if codex.Quota == nil || codex.Quota.PlanType != "plus" || codex.Quota.Primary == nil || codex.Quota.Secondary == nil {
		t.Fatalf("expected codex quota with primary & secondary windows, got %+v", codex.Quota)
	}
	if *codex.Quota.Primary.RemainingPercent != 85 || *codex.Quota.Secondary.RemainingPercent != 60 {
		t.Fatalf("unexpected codex remaining percents: primary=%v secondary=%v",
			*codex.Quota.Primary.RemainingPercent, *codex.Quota.Secondary.RemainingPercent)
	}

	gemini := byID[ExternalToolGemini]
	if gemini.Quota == nil || gemini.Quota.WeeklyTokens != 4000 || gemini.Quota.WeeklyTurns != 1 {
		t.Fatalf("expected gemini weekly quota (4000 tokens, 1 turn), got %+v", gemini.Quota)
	}

	claude := byID[ExternalToolClaude]
	if claude.Quota == nil || claude.Quota.PlanType != "max" || claude.Quota.WeeklyTokens == 0 {
		t.Fatalf("expected claude quota with planType=max and weekly tokens > 0, got %+v", claude.Quota)
	}
}
