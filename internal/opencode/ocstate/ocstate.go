// Package ocstate reads the user's external OpenCode CLI state on disk and
// mirrors its Console account into Hoxi's engine database.
//
// It is a foundation package (standard library + hoxipaths only) shared by the
// daemon supervisor (seed before `opencode serve`) and the handoff importer
// (explicit "connect OpenCode" sync), so both follow one code path.
package ocstate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sqlitecli"
)

const (
	sqliteReadTimeout  = 5 * time.Second
	sqliteWriteTimeout = 6 * time.Second
)

// ExternalDBPaths lists the existing opencode.db files of the user's own
// OpenCode CLI, never Hoxi's engine databases.
func ExternalDBPaths() []string {
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_EXTERNAL_DB")); custom != "" {
		return []string{filepath.Clean(custom)}
	}
	if customDir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); customDir != "" {
		return []string{filepath.Join(filepath.Clean(customDir), "opencode.db")}
	}
	excluded := map[string]bool{}
	for _, p := range []string{hoxipaths.DBPath(), hoxipaths.EngineLegacyDBPath()} {
		if p != "" {
			excluded[filepath.Clean(p)] = true
		}
	}
	var candidates []string
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "share", "opencode", "opencode.db"))
	}
	if localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localApp != "" {
		candidates = append(candidates,
			filepath.Join(localApp, "opencode", "opencode.db"),
			filepath.Join(localApp, "opencode", "Data", "opencode.db"),
		)
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		candidates = append(candidates, filepath.Join(appData, "opencode", "opencode.db"))
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range candidates {
		clean := filepath.Clean(p)
		if excluded[clean] || seen[clean] {
			continue
		}
		if st, err := os.Stat(clean); err == nil && !st.IsDir() {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}

// ConsoleAccount is one row of OpenCode's `account` table.
type ConsoleAccount struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	URL          string `json:"url"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TokenExpiry  int64  `json:"tokenExpiry"`
	TimeCreated  int64  `json:"timeCreated"`
	TimeUpdated  int64  `json:"timeUpdated"`
}

// ConsoleState is the OpenCode Console login (`account` + `account_state`)
// that powers OpenCode Go and Personal subscriptions.
type ConsoleState struct {
	Accounts        []ConsoleAccount `json:"accounts"`
	ActiveAccountID string           `json:"activeAccountID"`
	ActiveOrgID     string           `json:"activeOrgID"`
}

const consoleStateQuery = `
SELECT json_object(
  'accounts', json(COALESCE((
    SELECT json_group_array(json_object(
      'id', COALESCE(a.id, ''),
      'email', COALESCE(a.email, ''),
      'url', COALESCE(a.url, ''),
      'accessToken', COALESCE(a.access_token, ''),
      'refreshToken', COALESCE(a.refresh_token, ''),
      'tokenExpiry', COALESCE(a.token_expiry, 0),
      'timeCreated', COALESCE(a.time_created, 0),
      'timeUpdated', COALESCE(a.time_updated, 0)
    ))
    FROM account a
  ), '[]')),
  'activeAccountID', COALESCE((SELECT active_account_id FROM account_state WHERE id = 1 LIMIT 1), ''),
  'activeOrgID', COALESCE((SELECT active_org_id FROM account_state WHERE id = 1 LIMIT 1), '')
);`

// ReadConsoleState reads the usable Console accounts from dbPath (read-only,
// with a busy timeout so a running CLI is never blocked). It returns nil when
// the database is missing or holds no account with a token.
func ReadConsoleState(dbPath string) *ConsoleState {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil
	}
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqlitecli.Executable(), "-readonly", "-cmd", ".timeout 3000", dbPath, consoleStateQuery)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	var state ConsoleState
	if json.Unmarshal(bytes.TrimSpace(out), &state) != nil {
		return nil
	}
	var valid []ConsoleAccount
	for _, a := range state.Accounts {
		if strings.TrimSpace(a.ID) != "" && (strings.TrimSpace(a.AccessToken) != "" || strings.TrimSpace(a.RefreshToken) != "") {
			valid = append(valid, a)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	state.Accounts = valid
	if strings.TrimSpace(state.ActiveAccountID) == "" {
		state.ActiveAccountID = strings.TrimSpace(valid[0].ID)
	}
	return &state
}

// ReadExternalConsoleState returns the first Console login found in the
// user's own OpenCode CLI databases.
func ReadExternalConsoleState() *ConsoleState {
	for _, dbPath := range ExternalDBPaths() {
		if state := ReadConsoleState(dbPath); state != nil {
			return state
		}
	}
	return nil
}

// IsConsoleStateSynced reports whether hoxi.db already carries ext's active
// account and organization.
func IsConsoleStateSynced(ext *ConsoleState) bool {
	if ext == nil || len(ext.Accounts) == 0 {
		return true
	}
	dst := ReadConsoleState(hoxipaths.DBPath())
	if dst == nil || len(dst.Accounts) == 0 {
		return false
	}
	if id := strings.TrimSpace(ext.ActiveAccountID); id != "" && strings.TrimSpace(dst.ActiveAccountID) != id {
		return false
	}
	if org := strings.TrimSpace(ext.ActiveOrgID); org != "" && strings.TrimSpace(dst.ActiveOrgID) != org {
		return false
	}
	return true
}

// SyncConsoleState upserts ext into dstDB. Newer tokens win per account, so a
// refresh performed by the engine is never rolled back by a stale external copy.
func SyncConsoleState(ext *ConsoleState, dstDB string) error {
	if ext == nil || len(ext.Accounts) == 0 {
		return nil
	}
	dstDB = strings.TrimSpace(dstDB)
	if dstDB == "" {
		return fmt.Errorf("cannot resolve Hoxi OpenCode database path")
	}
	if err := os.MkdirAll(filepath.Dir(dstDB), 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteWriteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqlitecli.Executable(), "-cmd", ".timeout 3000", dstDB)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(consoleUpsertSQL(ext))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sync opencode console account failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SeedConsoleAccount mirrors the external Console login into dstDB before the
// engine starts. It is best effort: a missing login or sqlite3 is not an error.
func SeedConsoleAccount(dstDB string) {
	dstDB = strings.TrimSpace(dstDB)
	if dstDB == "" {
		return
	}
	for _, src := range ExternalDBPaths() {
		if filepath.Clean(src) == filepath.Clean(dstDB) {
			continue
		}
		if state := ReadConsoleState(src); state != nil {
			_ = SyncConsoleState(state, dstDB)
			return
		}
	}
}

func consoleUpsertSQL(ext *ConsoleState) string {
	var sb strings.Builder
	sb.WriteString("BEGIN IMMEDIATE;\n")
	sb.WriteString("CREATE TABLE IF NOT EXISTS `account` (`id` text PRIMARY KEY, `email` text NOT NULL, `url` text NOT NULL, `access_token` text NOT NULL, `refresh_token` text NOT NULL, `token_expiry` integer, `time_created` integer NOT NULL, `time_updated` integer NOT NULL);\n")
	sb.WriteString("CREATE TABLE IF NOT EXISTS `account_state` (`id` integer PRIMARY KEY, `active_account_id` text, `active_org_id` text, CONSTRAINT `fk_account_state_active_account_id_account_id_fk` FOREIGN KEY (`active_account_id`) REFERENCES `account`(`id`) ON DELETE SET NULL);\n")
	for _, a := range ext.Accounts {
		fmt.Fprintf(&sb,
			"INSERT INTO `account` (`id`, `email`, `url`, `access_token`, `refresh_token`, `token_expiry`, `time_created`, `time_updated`) VALUES (%s, %s, %s, %s, %s, %d, %d, %d) ON CONFLICT(`id`) DO UPDATE SET `email`=excluded.`email`, `url`=excluded.`url`, `access_token`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR `account`.`access_token` = '' THEN excluded.`access_token` ELSE `account`.`access_token` END, `refresh_token`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR `account`.`refresh_token` = '' THEN excluded.`refresh_token` ELSE `account`.`refresh_token` END, `token_expiry`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR COALESCE(`account`.`token_expiry`, 0) = 0 THEN excluded.`token_expiry` ELSE `account`.`token_expiry` END, `time_updated`=MAX(`account`.`time_updated`, excluded.`time_updated`);\n",
			sqlQuote(strings.TrimSpace(a.ID)), sqlQuote(a.Email), sqlQuote(a.URL), sqlQuote(a.AccessToken), sqlQuote(a.RefreshToken),
			a.TokenExpiry, a.TimeCreated, a.TimeUpdated,
		)
	}
	activeID := strings.TrimSpace(ext.ActiveAccountID)
	if activeID == "" {
		activeID = strings.TrimSpace(ext.Accounts[0].ID)
	}
	fmt.Fprintf(&sb,
		"INSERT INTO `account_state` (`id`, `active_account_id`, `active_org_id`) VALUES (1, %s, %s) ON CONFLICT(`id`) DO UPDATE SET `active_account_id`=excluded.`active_account_id`, `active_org_id`=CASE WHEN excluded.`active_org_id` != '' THEN excluded.`active_org_id` ELSE `account_state`.`active_org_id` END;\n",
		sqlQuote(activeID), sqlQuote(strings.TrimSpace(ext.ActiveOrgID)),
	)
	sb.WriteString("COMMIT;\n")
	return sb.String()
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\x00", ""), "'", "''") + "'"
}

// MeasureDatabaseBytes returns the total on-disk footprint of dbPath including
// its SQLite WAL and shared-memory sidecar files.
func MeasureDatabaseBytes(dbPath string) int64 {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return 0
	}
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(dbPath + suffix); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}

// CleanDatabaseEventLog purges transient OpenCode event rows in a single batch
// and runs VACUUM only when the delete step succeeds.
func CleanDatabaseEventLog(ctx context.Context, dbPath string) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil
	}
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() {
		return nil
	}

	purgeCtx, cancelPurge := context.WithTimeout(ctx, 30*time.Second)
	defer cancelPurge()
	purgeCmd := exec.CommandContext(
		purgeCtx,
		sqlitecli.Executable(),
		"-cmd", ".timeout 10000",
		dbPath,
		"DELETE FROM event; DELETE FROM event_sequence; PRAGMA wal_checkpoint(TRUNCATE);",
	)
	purgeCmd.WaitDelay = time.Second
	if out, err := purgeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("purge sqlite event log: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	vacuumCtx, cancelVacuum := context.WithTimeout(ctx, 45*time.Second)
	defer cancelVacuum()
	vacuumCmd := exec.CommandContext(
		vacuumCtx,
		sqlitecli.Executable(),
		"-cmd", ".timeout 10000",
		dbPath,
		"VACUUM; PRAGMA wal_checkpoint(TRUNCATE);",
	)
	vacuumCmd.WaitDelay = time.Second
	if out, err := vacuumCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("vacuum sqlite database: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
