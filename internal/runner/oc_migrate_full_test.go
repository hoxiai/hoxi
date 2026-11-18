package runner

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/sqlitecli"
	"github.com/hoxiai/hoxi/internal/subproc"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func initMigrationGitRepo(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) string {
		cmd := subproc.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return run("rev-parse", "HEAD")
}

func TestOpenCodeFullMigrationWithoutSQLiteOrOpenCodeBinaries(t *testing.T) {
	projDir := t.TempDir()
	baseCommit := initMigrationGitRepo(t, projDir)

	wtMgr := worktree.NewManager(projDir)
	wtMeta, err := wtMgr.Create("ses_wt_77")
	if err != nil || !wtMeta.Isolated {
		t.Fatalf("create isolated worktree: meta=%+v err=%v", wtMeta, err)
	}

	// Capture two real git snapshots in projDir to verify revert/unrevert recovery.
	snapBefore, err := worktree.CreateSnapshot(projDir)
	if err != nil || snapBefore == "" {
		t.Fatalf("CreateSnapshot before: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "README.md"), []byte("reverted-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapAfter, err := worktree.CreateSnapshot(projDir)
	if err != nil || snapAfter == "" {
		t.Fatalf("CreateSnapshot after: %v", err)
	}
	// Simulate OpenCode having reverted the workspace back to snapBefore at msg_u_rev_2.
	if err := worktree.RestoreSnapshot(projDir, snapBefore); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	storeDir := filepath.Join(t.TempDir(), "sessions")
	svc := NewService(projDir, wtMgr, nil, storeDir)
	defer svc.CloseAll()

	// Pre-seed a Hoxi-managed OpenCode session (only 1 turn in Store; 2 turns in opencode.db + 1 child subagent).
	priorManaged := SessionRecord{
		ID:           "ses_hoxi_managed_1",
		Runtime:      "opencode",
		ExternalID:   "ses_oc_managed_1",
		ProjectDir:   projDir,
		WorktreePath: projDir,
		SandboxMode:  "local",
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-5",
		Title:        "Yesterday Managed Task",
		CreatedAt:    1700000010000,
		UpdatedAt:    1700000011000,
		OpenCodeIDs:  []string{"ses_oc_managed_1"},
		Turns: []MessageTurn{{
			Info:  MessageInfo{ID: "msg_m_1", SessionID: "ses_hoxi_managed_1", Role: "user", Time: MessageTime{Created: 1700000010000}},
			Parts: []MessagePart{{ID: "prt_m_1", SessionID: "ses_hoxi_managed_1", MessageID: "msg_m_1", Type: "text", Text: "Start yesterday task"}},
		}},
	}
	if err := svc.Store().Put(priorManaged); err != nil {
		t.Fatal(err)
	}

	// Build a synthetic opencode.db using pure-Go SQLite (320+ sessions, including >300 older ones,
	// Hoxi-managed parent + child subagent, attachment, archived, worktree-bound, reverted, and 1 corrupt row).
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	err = sqlitecli.WithImmediateTx(context.Background(), dbPath, 5*time.Second, func(ctx context.Context, conn *sql.Conn) error {
		schema := `
CREATE TABLE session (
	id TEXT PRIMARY KEY,
	parent_id TEXT,
	directory TEXT,
	title TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	time_archived INTEGER,
	model TEXT,
	metadata TEXT,
	revert TEXT,
	data TEXT
);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, message_id TEXT, time_created INTEGER, data TEXT);`
		if _, err := conn.ExecContext(ctx, schema); err != nil {
			return err
		}

		// 1. 305 older root sessions (verifying no LIMIT 300 truncation).
		for i := 1; i <= 305; i++ {
			sid := fmt.Sprintf("ses_old_%03d", i)
			ts := int64(1600000000000 + i*1000)
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO session VALUES (?, NULL, ?, ?, ?, ?, 0, 'openai/gpt-5', '{}', '', '');`,
				sid, projDir, fmt.Sprintf("Old Session %d", i), ts, ts+500,
			); err != nil {
				return err
			}
			msgID := fmt.Sprintf("msg_old_%03d", i)
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO message VALUES (?, ?, ?, ?);`,
				msgID, sid, ts, fmt.Sprintf(`{"id":%q,"role":"user","time":{"created":%d}}`, msgID, ts),
			); err != nil {
				return err
			}
			prtID := fmt.Sprintf("prt_old_%03d", i)
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO part VALUES (?, ?, ?, ?, ?);`,
				prtID, sid, msgID, ts, fmt.Sprintf(`{"id":%q,"type":"text","text":"Prompt %d"}`, prtID, i),
			); err != nil {
				return err
			}
		}

		// 2. Hoxi-managed session row (`hoxiConversation`) with 2 turns + task tool linking to child subagent.
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO session VALUES ('ses_oc_managed_1', NULL, ?, 'Yesterday Managed Task', 1700000010000, 1700000020000, 0, 'anthropic/claude-sonnet-4-5', '{"hoxiConversation":"ses_hoxi_managed_1"}', '', '');`,
			projDir,
		); err != nil {
			return err
		}
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO message VALUES ('msg_m_1', 'ses_oc_managed_1', 1700000010000, '{"id":"msg_m_1","role":"user","time":{"created":1700000010000}}');`, nil},
			{`INSERT INTO part VALUES ('prt_m_1', 'ses_oc_managed_1', 'msg_m_1', 1700000010000, '{"id":"prt_m_1","type":"text","text":"Start yesterday task"}');`, nil},
			{`INSERT INTO message VALUES ('msg_m_2', 'ses_oc_managed_1', 1700000015000, '{"id":"msg_m_2","parentID":"msg_m_1","role":"assistant","providerID":"anthropic","modelID":"claude-sonnet-4-5","time":{"created":1700000015000,"completed":1700000020000}}');`, nil},
			{`INSERT INTO part VALUES ('prt_m_task', 'ses_oc_managed_1', 'msg_m_2', 1700000016000, '{"id":"prt_m_task","type":"tool","tool":"task","callID":"call_sub_1","state":{"status":"completed","input":{"description":"audit"},"output":"done","metadata":{"sessionId":"ses_oc_child_1"}}}');`, nil},
			{`INSERT INTO part VALUES ('prt_m_txt', 'ses_oc_managed_1', 'msg_m_2', 1700000018000, '{"id":"prt_m_txt","type":"text","text":"Completed phase 1."}');`, nil},
		} {
			if _, err := conn.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
				return err
			}
		}

		// 3. Child subagent session linked to ses_oc_managed_1.
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO session VALUES ('ses_oc_child_1', 'ses_oc_managed_1', ?, 'Audit Subagent', 1700000016000, 1700000017500, 0, 'anthropic/claude-haiku-4-5', '{}', '', '');`,
			projDir,
		); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO message VALUES ('msg_c_1', 'ses_oc_child_1', 1700000016100, '{"id":"msg_c_1","role":"assistant","time":{"created":1700000016100}}');`,
		); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO part VALUES ('prt_c_1', 'ses_oc_child_1', 'msg_c_1', 1700000016200, '{"id":"prt_c_1","type":"text","text":"Subagent audit ok."}');`,
		); err != nil {
			return err
		}

		// 4. Archived + Attachment + Worktree-bound session.
		pngDataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO session VALUES ('ses_wt_77', NULL, ?, 'Worktree Archived Attachment Session', 1700000030000, 1700000035000, 1700000099000, 'openai/gpt-5', '{}', '', '');`,
			wtMeta.Directory,
		); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO message VALUES ('msg_wt_1', 'ses_wt_77', 1700000031000, '{"id":"msg_wt_1","role":"user","time":{"created":1700000031000}}');`,
		); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO part VALUES ('prt_wt_file', 'ses_wt_77', 'msg_wt_1', 1700000031100, ?);`,
			fmt.Sprintf(`{"id":"prt_wt_file","type":"file","mime":"image/png","filename":"ui.png","url":%q}`, pngDataURL),
		); err != nil {
			return err
		}

		// 5. Reverted session with step-start snapshot & revert state.
		revJSON := fmt.Sprintf(`{"messageID":"msg_u_rev_2","snapshot":%q}`, snapAfter)
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO session VALUES ('ses_reverted_1', NULL, ?, 'Reverted Session', 1700000040000, 1700000048000, 0, 'openai/gpt-5', '{}', ?, '');`,
			projDir, revJSON,
		); err != nil {
			return err
		}
		for _, sqlStmt := range []string{
			`INSERT INTO message VALUES ('msg_u_rev_1', 'ses_reverted_1', 1700000041000, '{"id":"msg_u_rev_1","role":"user","time":{"created":1700000041000}}');`,
			`INSERT INTO part VALUES ('prt_u_rev_1', 'ses_reverted_1', 'msg_u_rev_1', 1700000041000, '{"id":"prt_u_rev_1","type":"text","text":"First keep turn"}');`,
			fmt.Sprintf(`INSERT INTO part VALUES ('prt_step_1', 'ses_reverted_1', 'msg_u_rev_1', 1700000041100, '{"id":"prt_step_1","type":"step-start","snapshot":%q}');`, snapBefore),
			`INSERT INTO message VALUES ('msg_u_rev_2', 'ses_reverted_1', 1700000045000, '{"id":"msg_u_rev_2","role":"user","time":{"created":1700000045000}}');`,
			`INSERT INTO part VALUES ('prt_u_rev_2', 'ses_reverted_1', 'msg_u_rev_2', 1700000045000, '{"id":"prt_u_rev_2","type":"text","text":"Second reverted turn"}');`,
		} {
			if _, err := conn.ExecContext(ctx, sqlStmt); err != nil {
				return err
			}
		}

		// 6. Corrupt session row to verify per-item failure reason without aborting batch.
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO session VALUES ('ses_corrupt_1', NULL, ?, 'Corrupt Session', 1700000050000, 1700000051000, 0, 'openai/gpt-5', '{}', '', '');`,
			projDir,
		); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO message VALUES ('msg_bad', 'ses_corrupt_1', 1700000050100, '{not-valid-json');`,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed pure-go sqlite db: %v", err)
	}

	// Provide a clean bin directory containing ONLY git (neither `sqlite3` nor `opencode` exists)!
	cleanBin := t.TempDir()
	if gitBin, lookErr := exec.LookPath("git"); lookErr == nil && gitBin != "" {
		_ = os.Symlink(gitBin, filepath.Join(cleanBin, "git"))
	} else {
		for _, cand := range []string{"/usr/bin/git", "/opt/homebrew/bin/git", "/usr/local/bin/git"} {
			if _, stErr := os.Stat(cand); stErr == nil {
				_ = os.Symlink(cand, filepath.Join(cleanBin, "git"))
				break
			}
		}
	}
	t.Setenv("PATH", cleanBin)

	rep := svc.MigrateOpenCodeDBWithReport(dbPath)
	if rep.Scanned != 310 {
		t.Fatalf("expected 310 scanned sessions, got %+v", rep)
	}
	if rep.Migrated != 308 || rep.Upgraded != 1 || rep.Failed != 1 {
		t.Fatalf("unexpected migration counts: migrated=%d upgraded=%d skipped=%d failed=%d",
			rep.Migrated, rep.Upgraded, rep.Skipped, rep.Failed)
	}

	// Verify oldest session (#001 beyond 300 limit) migrated intact.
	if oldest, ok := svc.Store().Get("ses_old_001"); !ok || oldest.Runtime != string(RuntimeNative) || len(oldest.Turns) != 1 {
		t.Fatalf("oldest session beyond 300 limit missing or invalid: ok=%v rec=%+v", ok, oldest)
	}

	// Verify Hoxi-managed session upgraded to native, backfilled turn 2, and linked child subagent.
	managedAfter, ok := svc.Store().Get("ses_hoxi_managed_1")
	if !ok || managedAfter.Runtime != string(RuntimeNative) || len(managedAfter.Turns) != 2 {
		t.Fatalf("expected ses_hoxi_managed_1 upgraded with 2 turns, got ok=%v runtime=%q turns=%d",
			ok, managedAfter.Runtime, len(managedAfter.Turns))
	}
	children, _ := svc.Store().ChildSessionIDs("ses_hoxi_managed_1")
	if len(children) != 1 || children[0] != "ses_oc_child_1" {
		t.Fatalf("expected child subagent ses_oc_child_1 linked to ses_hoxi_managed_1, got %v", children)
	}

	// Verify worktree binding, archived timestamp, and extracted attachment.
	wtRec, ok := svc.Store().Get("ses_wt_77")
	if !ok || wtRec.SandboxMode != "worktree" || wtRec.ArchivedAt != 1700000099000 || wtRec.BaseCommit != baseCommit {
		t.Fatalf("unexpected worktree/archived session: %+v (want baseCommit=%s)", wtRec, baseCommit)
	}
	if len(wtRec.Turns) != 1 || len(wtRec.Turns[0].Parts) != 1 || !strings.HasPrefix(wtRec.Turns[0].Parts[0].URL, "/api/hoxi/attachments/") {
		t.Fatalf("expected data: attachment extracted into Hoxi attachment store, got %+v", wtRec.Turns)
	}

	// Verify reverted session kept RevertedTurns + Snapshots and can be unreverted via HTTP!
	revRec, ok := svc.Store().Get("ses_reverted_1")
	if !ok || revRec.Revert == nil || len(revRec.Turns) != 1 || len(revRec.RevertedTurns) != 2 || revRec.Snapshots["msg_u_rev_1"] != snapBefore {
		t.Fatalf("unexpected reverted session state: %+v", revRec)
	}
	wUnrev := httptest.NewRecorder()
	svc.handleSessionUnrevert(wUnrev, httptest.NewRequest(http.MethodPost, "/api/hoxi/runner/sessions/ses_reverted_1/unrevert", bytes.NewReader([]byte(`{}`))), "ses_reverted_1")
	if wUnrev.Code != http.StatusOK {
		t.Fatalf("unrevert migrated session failed: %d %s", wUnrev.Code, wUnrev.Body.String())
	}
	unrevRec, _ := svc.Store().Get("ses_reverted_1")
	if unrevRec.Revert != nil || len(unrevRec.Turns) != 2 {
		t.Fatalf("expected unrevert to restore 2 turns, got %+v", unrevRec)
	}

	// Verify per-item failure reason recorded for ses_corrupt_1.
	foundCorruptReason := false
	for _, it := range rep.Items {
		if it.SessionID == "ses_corrupt_1" && it.Status == OCMigrateFailed && strings.Contains(it.Reason, "corrupt message") {
			foundCorruptReason = true
		}
	}
	if !foundCorruptReason {
		t.Fatalf("expected explicit failure reason for ses_corrupt_1 in report")
	}

	// Verify repeatable idempotency: running migration a second time skips all 309 valid sessions.
	rep2 := svc.MigrateOpenCodeDBWithReport(dbPath)
	if rep2.Migrated != 0 || rep2.Upgraded != 0 || rep2.Skipped != 309 || rep2.Failed != 1 {
		t.Fatalf("expected idempotent re-run (0 migrated, 0 upgraded, 309 skipped, 1 failed), got %+v", rep2)
	}

	// Verify rollback journal restores upgraded session to its pre-migration state while keeping opencode.db intact!
	if err := svc.RestoreMigratedOpenCodeSession("ses_hoxi_managed_1"); err != nil {
		t.Fatalf("RestoreMigratedOpenCodeSession failed: %v", err)
	}
	restoredManaged, ok := svc.Store().Get("ses_hoxi_managed_1")
	if !ok || restoredManaged.Runtime != "opencode" || len(restoredManaged.Turns) != 1 {
		t.Fatalf("expected ses_hoxi_managed_1 rolled back to prior opencode 1-turn state, got %+v", restoredManaged)
	}
	if _, childStillThere := svc.Store().Get("ses_oc_child_1"); childStillThere {
		t.Fatalf("expected rollback of parent to remove newly created child subagent copy")
	}
	// Original opencode.db still has all 310 rows untouched!
	origRows, err := readAllOpenCodeSessionRows(dbPath)
	if err != nil || len(origRows) != 310 {
		t.Fatalf("expected original opencode.db preserved with 310 rows, got len=%d err=%v", len(origRows), err)
	}
	_ = time.Now()
}
