package ocstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runSQLite(t *testing.T, db, sql string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
}

func TestSeedConsoleAccountMirrorsExternalLogin(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	for k, v := range map[string]string{
		"HOME": home, "USERPROFILE": home, "OPENCODE_DB": "", "HOXI_DB_PATH": "", "OPENCODE_EXTERNAL_DB": "",
		"OPENCODE_DATA_DIR": "", "HOXI_ENGINE_DATA_DIR": "", "LOCALAPPDATA": "", "APPDATA": "",
	} {
		t.Setenv(k, v)
	}
	ext := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	runSQLite(t, ext, `
CREATE TABLE account (id TEXT PRIMARY KEY, email TEXT NOT NULL, url TEXT NOT NULL, access_token TEXT NOT NULL, refresh_token TEXT NOT NULL, token_expiry INTEGER, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
CREATE TABLE account_state (id INTEGER PRIMARY KEY NOT NULL, active_account_id TEXT, active_org_id TEXT);
INSERT INTO account VALUES ('user_1', 'dev@hoxi.ai', 'https://opencode.ai', 'st_tok''en', 'rt', 1, 1, 2);
INSERT INTO account VALUES ('user_empty', 'x@hoxi.ai', 'https://opencode.ai', '', '', 0, 1, 1);
INSERT INTO account_state VALUES (1, 'user_1', 'org_1');
`)
	if got := ExternalDBPaths(); len(got) != 1 || got[0] != ext {
		t.Fatalf("ExternalDBPaths() = %v, want [%s]", got, ext)
	}
	state := ReadExternalConsoleState()
	if state == nil || len(state.Accounts) != 1 || state.Accounts[0].AccessToken != "st_tok'en" {
		t.Fatalf("unexpected external state: %+v", state)
	}
	if IsConsoleStateSynced(state) {
		t.Fatal("hoxi.db must not report synced before seeding")
	}

	SeedConsoleAccount(filepath.Join(home, ".hoxi", "hoxi.db"))
	if !IsConsoleStateSynced(state) {
		t.Fatal("seeded hoxi.db must carry the active account and org")
	}
	dst := ReadConsoleState(filepath.Join(home, ".hoxi", "hoxi.db"))
	if dst == nil || dst.ActiveOrgID != "org_1" || len(dst.Accounts) != 1 {
		t.Fatalf("unexpected seeded state: %+v", dst)
	}
}
