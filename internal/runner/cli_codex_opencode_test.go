package runner

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

func TestBuildCodexAndOpenCodeCLIArgs(t *testing.T) {
	gotFresh := BuildCodexArgs("build", "gpt-5.4", "high", "")
	wantFresh := []string{"exec", "--json", "--skip-git-repo-check", "--full-auto", "-m", "gpt-5.4", "-c", `model_reasoning_effort="high"`, "-"}
	if !reflect.DeepEqual(gotFresh, wantFresh) {
		t.Fatalf("BuildCodexArgs fresh = %v, want %v", gotFresh, wantFresh)
	}

	gotPlan := BuildCodexArgs("plan", "o3", "", "")
	wantPlan := []string{"exec", "--json", "--skip-git-repo-check", "-s", "read-only", "-m", "o3", "-"}
	if !reflect.DeepEqual(gotPlan, wantPlan) {
		t.Fatalf("BuildCodexArgs plan = %v, want %v", gotPlan, wantPlan)
	}

	gotResume := BuildCodexArgs("build", "gpt-5.4", "medium", "thr_123")
	wantResume := []string{"exec", "resume", "thr_123", "--json", "--skip-git-repo-check", "-m", "gpt-5.4", "-c", `model_reasoning_effort="medium"`, "-"}
	if !reflect.DeepEqual(gotResume, wantResume) {
		t.Fatalf("BuildCodexArgs resume = %v, want %v", gotResume, wantResume)
	}

	gotOC := BuildOpenCodeCLIArgs("plan", "anthropic", "claude-sonnet-4-5", "high", "ses_ext_9")
	wantOC := []string{"run", "--format", "json", "--agent", "plan", "-m", "anthropic/claude-sonnet-4-5", "--variant", "high", "-s", "ses_ext_9"}
	if !reflect.DeepEqual(gotOC, wantOC) {
		t.Fatalf("BuildOpenCodeCLIArgs = %v, want %v", gotOC, wantOC)
	}
}

func TestTranslateCodexAndOpenCodeCLIStreams(t *testing.T) {
	trCdx := NewTurnTranslator("ses_cdx_1", string(RuntimeCodex), "msg_a_1", "msg_u_1", "codex", "gpt-5.4", "build", "")
	trCdx.FeedLine(`{"type":"thread.started","thread_id":"thr_abc"}`)
	if trCdx.ExternalID() != "thr_abc" {
		t.Fatalf("expected externalID thr_abc, got %q", trCdx.ExternalID())
	}
	evtsCmd := trCdx.FeedLine(`{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"go test ./...","aggregated_output":"ok","exit_code":0}}`)
	if len(evtsCmd) != 1 || evtsCmd[0].Type != "message.part.updated" {
		t.Fatalf("unexpected codex cmd events: %+v", evtsCmd)
	}
	evtsMsg := trCdx.FeedLine(`{"type":"item.completed","item":{"id":"msg_1","type":"agent_message","text":"All tests passed."}}`)
	if len(evtsMsg) != 1 {
		t.Fatalf("unexpected codex msg events: %+v", evtsMsg)
	}
	evtsDone := trCdx.FeedLine(`{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":400,"output_tokens":80}}`)
	if !trCdx.IsTurnFinished() || len(evtsDone) != 3 {
		t.Fatalf("expected codex turn finished with 3 terminal events, got finished=%v evts=%d", trCdx.IsTurnFinished(), len(evtsDone))
	}
	if tok := trCdx.MsgInfo.Tokens; tok == nil || tok.Input != 800 || tok.Cache.Read != 400 || tok.Output != 80 || tok.Reasoning != 0 || tok.Total != 1280 {
		t.Fatalf("codex tokens must subtract cached_input_tokens from input_tokens: got %+v", tok)
	}

	trCdxReason := NewTurnTranslator("ses_cdx_2", string(RuntimeCodex), "msg_a_r", "msg_u_r", "codex", "o3", "build", "")
	trCdxReason.FeedLine(`{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":400,"output_tokens":80,"reasoning_output_tokens":30}}`)
	if tok := trCdxReason.MsgInfo.Tokens; tok == nil || tok.Input != 800 || tok.Cache.Read != 400 || tok.Output != 50 || tok.Reasoning != 30 || tok.Total != 1280 {
		t.Fatalf("codex tokens must subtract reasoning_output_tokens from output_tokens: got %+v", tok)
	}

	trCdxReasonOnly := NewTurnTranslator("ses_cdx_3", string(RuntimeCodex), "msg_a_ro", "msg_u_ro", "codex", "o3", "build", "")
	trCdxReasonOnly.FeedLine(`{"type":"turn.completed","usage":{"input_tokens":500,"cached_input_tokens":200,"output_tokens":0,"reasoning_output_tokens":45}}`)
	if tok := trCdxReasonOnly.MsgInfo.Tokens; tok == nil || tok.Input != 300 || tok.Cache.Read != 200 || tok.Output != 0 || tok.Reasoning != 45 || tok.Total != 545 {
		t.Fatalf("codex reasoning-only turn must retain reasoning_output_tokens when output_tokens=0: got %+v", tok)
	}

	trOC := NewTurnTranslator("ses_hoxi_1", string(RuntimeOpenCode), "msg_a_2", "msg_u_2", "opencode", "openai/gpt-5", "build", "")
	trOC.FeedLine(`{"type":"text","sessionID":"ses_oc_99","part":{"id":"prt_1","type":"text","text":"Done from OpenCode CLI"}}`)
	if trOC.ExternalID() != "ses_oc_99" {
		t.Fatalf("expected externalID ses_oc_99, got %q", trOC.ExternalID())
	}
	// Intermediate step_finish with tool-calls must NOT close the turn.
	if evts := trOC.FeedLine(`{"type":"step_finish","sessionID":"ses_oc_99","part":{"reason":"tool-calls","tokens":{"input":100,"output":20,"cache":{"read":500,"write":50}}}}`); len(evts) != 0 || trOC.IsTurnFinished() {
		t.Fatalf("tool-calls step_finish should not finish turn: %+v", evts)
	}
	evtsOCDone := trOC.FeedLine(`{"type":"step_finish","sessionID":"ses_oc_99","part":{"reason":"stop","cost":0.01,"tokens":{"input":200,"output":40,"reasoning":10,"cache":{"read":800,"write":0}}}}`)
	if !trOC.IsTurnFinished() || len(evtsOCDone) != 3 {
		t.Fatalf("expected opencode CLI turn finished, got finished=%v len=%d", trOC.IsTurnFinished(), len(evtsOCDone))
	}
	// Context/Total must reflect only the latest step's occupancy (200+40+10+800=1050),
	// while Input/Output/Reasoning/Cache retain cumulative turn billing buckets.
	tok := trOC.MsgInfo.Tokens
	if tok == nil || tok.Context != 1050 || tok.Total != 1050 || tok.Input != 300 || tok.Output != 60 || tok.Reasoning != 10 || tok.Cache.Read != 1300 || tok.Cache.Write != 50 {
		t.Fatalf("unexpected opencode CLI tokens: %+v", tok)
	}
}

func TestDaemonlessDefaultNativeAndSQLiteMigration(t *testing.T) {
	projDir := t.TempDir()
	storeDir := filepath.Join(t.TempDir(), "sessions")
	svc := NewService(projDir, nil, nil, storeDir)
	defer svc.CloseAll()

	// 1. Without an OpenCode daemon, empty runtime defaults to "native" (ses_nat_).
	infoNat, status, err := svc.CreateConversation(context.Background(), CreateSessionRequest{
		Directory: projDir,
		Title:     "Default Native Chat",
	}, projDir)
	if err != nil || status != 200 {
		t.Fatalf("CreateConversation default failed: status=%d err=%v", status, err)
	}
	if infoNat.Runtime != string(RuntimeNative) || !strings.HasPrefix(infoNat.ID, "ses_nat_") {
		t.Fatalf("expected default native runtime & ses_nat_ ID, got runtime=%q id=%q", infoNat.Runtime, infoNat.ID)
	}

	// 2. Explicit "opencode" runtime creates a standalone Hoxi-managed CLI session without daemon HTTP calls.
	infoOC, status, err := svc.CreateConversation(context.Background(), CreateSessionRequest{
		Runtime:   "opencode",
		Directory: projDir,
		Title:     "Single-Turn OpenCode CLI",
	}, projDir)
	if err != nil || status != 200 || infoOC.Runtime != "opencode" {
		t.Fatalf("CreateConversation opencode CLI failed: status=%d err=%v info=%+v", status, err, infoOC)
	}

	// 3. Explicit "codex" runtime creates a ses_cdx_ CLI session.
	infoCdx, status, err := svc.CreateConversation(context.Background(), CreateSessionRequest{
		Runtime:   "codex",
		Directory: projDir,
		Title:     "Single-Turn Codex CLI",
	}, projDir)
	if err != nil || status != 200 || infoCdx.Runtime != "codex" || !strings.HasPrefix(infoCdx.ID, "ses_cdx_") {
		t.Fatalf("CreateConversation codex CLI failed: status=%d err=%v info=%+v", status, err, infoCdx)
	}

	// 4. Legacy opencode.db SQLite sessions migrate losslessly into runner.Store.
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	initSQL := `
CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, title TEXT, time_created INTEGER, time_updated INTEGER, model TEXT, metadata TEXT);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, message_id TEXT, time_created INTEGER, data TEXT);
INSERT INTO session VALUES ('ses_legacy_42', NULL, '` + strings.ReplaceAll(projDir, "'", "''") + `', 'Legacy Architecture Thread', 1700000000000, 1700000005000, 'anthropic/claude-sonnet-4-5', '{}');
INSERT INTO message VALUES ('msg_u_legacy', 'ses_legacy_42', 1700000001000, '{"id":"msg_u_legacy","role":"user","time":{"created":1700000001000}}');
INSERT INTO part VALUES ('prt_u_1', 'ses_legacy_42', 'msg_u_legacy', 1700000001000, '{"id":"prt_u_1","type":"text","text":"Explain the gateway architecture"}');
INSERT INTO message VALUES ('msg_a_legacy', 'ses_legacy_42', 1700000003000, '{"id":"msg_a_legacy","role":"assistant","providerID":"anthropic","modelID":"claude-sonnet-4-5","time":{"created":1700000003000,"completed":1700000005000}}');
INSERT INTO part VALUES ('prt_a_1', 'ses_legacy_42', 'msg_a_legacy', 1700000004000, '{"id":"prt_a_1","type":"text","text":"The gateway routes sessions natively in Go."}');
`
	if err := sqlitecli.WithReadWrite(context.Background(), dbPath, 5*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, initSQL)
		return err
	}); err != nil {
		t.Fatalf("seed sqlite db failed: %v", err)
	}

	count := svc.MigrateOpenCodeDB(dbPath)
	if count != 1 {
		t.Fatalf("expected 1 migrated session, got %d", count)
	}
	migrated, ok := svc.Store().Get("ses_legacy_42")
	if !ok {
		t.Fatal("expected ses_legacy_42 in runner store")
	}
	if migrated.Runtime != string(RuntimeNative) || migrated.ProviderID != "anthropic" || migrated.ModelID != "claude-sonnet-4-5" || len(migrated.Turns) != 2 {
		t.Fatalf("unexpected migrated record: runtime=%q provider=%q model=%q turns=%d", migrated.Runtime, migrated.ProviderID, migrated.ModelID, len(migrated.Turns))
	}
}
