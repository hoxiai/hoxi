// Package ocstate reads the user's external OpenCode CLI state on disk and
// mirrors its Console account into Hoxi's engine database.
//
// It is a foundation package (standard library + hoxipaths + sqlitecli) shared
// by the daemon supervisor (seed before `opencode serve`) and the handoff
// importer (explicit "connect OpenCode" sync), so both follow one code path.
package ocstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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

// ReadConsoleState reads the usable Console accounts from dbPath (read-only,
// with a busy timeout so a running CLI is never blocked). It returns nil when
// the database is missing or holds no account with a token.
func ReadConsoleState(dbPath string) *ConsoleState {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteReadTimeout)
	defer cancel()

	var state ConsoleState
	err := sqlitecli.WithReadOnly(ctx, dbPath, 3*time.Second, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `SELECT COALESCE(id, ''), COALESCE(email, ''), COALESCE(url, ''), COALESCE(access_token, ''), COALESCE(refresh_token, ''), COALESCE(token_expiry, 0), COALESCE(time_created, 0), COALESCE(time_updated, 0) FROM account`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var a ConsoleAccount
			if err := rows.Scan(&a.ID, &a.Email, &a.URL, &a.AccessToken, &a.RefreshToken, &a.TokenExpiry, &a.TimeCreated, &a.TimeUpdated); err != nil {
				_ = rows.Close()
				return err
			}
			if strings.TrimSpace(a.ID) != "" && (strings.TrimSpace(a.AccessToken) != "" || strings.TrimSpace(a.RefreshToken) != "") {
				state.Accounts = append(state.Accounts, a)
			}
		}
		rErr := rows.Err()
		_ = rows.Close()
		if rErr != nil {
			return rErr
		}
		if len(state.Accounts) == 0 {
			return nil
		}
		err = db.QueryRowContext(ctx,
			`SELECT COALESCE(active_account_id, ''), COALESCE(active_org_id, '') FROM account_state WHERE id = 1 LIMIT 1`,
		).Scan(&state.ActiveAccountID, &state.ActiveOrgID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil && strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	})
	if err != nil || len(state.Accounts) == 0 {
		return nil
	}
	if strings.TrimSpace(state.ActiveAccountID) == "" {
		state.ActiveAccountID = strings.TrimSpace(state.Accounts[0].ID)
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
	return isConsoleStateSyncedTo(ext, hoxipaths.DBPath())
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

	err := sqlitecli.WithImmediateTx(ctx, dstDB, 3*time.Second, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx,
			"CREATE TABLE IF NOT EXISTS `account` (`id` text PRIMARY KEY, `email` text NOT NULL, `url` text NOT NULL, `access_token` text NOT NULL, `refresh_token` text NOT NULL, `token_expiry` integer, `time_created` integer NOT NULL, `time_updated` integer NOT NULL);"); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			"CREATE TABLE IF NOT EXISTS `account_state` (`id` integer PRIMARY KEY, `active_account_id` text, `active_org_id` text, CONSTRAINT `fk_account_state_active_account_id_account_id_fk` FOREIGN KEY (`active_account_id`) REFERENCES `account`(`id`) ON DELETE SET NULL);"); err != nil {
			return err
		}
		stmt, err := conn.PrepareContext(ctx, `INSERT INTO account (id, email, url, access_token, refresh_token, token_expiry, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET email = excluded.email, url = excluded.url, access_token = CASE WHEN excluded.time_updated >= account.time_updated OR account.access_token = '' THEN excluded.access_token ELSE account.access_token END, refresh_token = CASE WHEN excluded.time_updated >= account.time_updated OR account.refresh_token = '' THEN excluded.refresh_token ELSE account.refresh_token END, token_expiry = CASE WHEN excluded.time_updated >= account.time_updated OR COALESCE(account.token_expiry, 0) = 0 THEN excluded.token_expiry ELSE account.token_expiry END, time_updated = MAX(account.time_updated, excluded.time_updated)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, a := range ext.Accounts {
			if _, err := stmt.ExecContext(ctx,
				strings.TrimSpace(a.ID), a.Email, a.URL, a.AccessToken, a.RefreshToken,
				a.TokenExpiry, a.TimeCreated, a.TimeUpdated,
			); err != nil {
				return err
			}
		}

		activeID := strings.TrimSpace(ext.ActiveAccountID)
		if activeID == "" {
			activeID = strings.TrimSpace(ext.Accounts[0].ID)
		}
		_, err = conn.ExecContext(ctx, `INSERT INTO account_state (id, active_account_id, active_org_id) VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET active_account_id = excluded.active_account_id, active_org_id = CASE WHEN excluded.active_org_id != '' THEN excluded.active_org_id ELSE account_state.active_org_id END`, activeID, strings.TrimSpace(ext.ActiveOrgID))
		return err
	})
	if err != nil {
		return fmt.Errorf("sync opencode console account failed: %w", err)
	}
	return nil
}

// HasEngineSchema reports whether dbPath already holds an initialized OpenCode
// schema (`session` table). Creating `account` tables in an empty SQLite file
// before `opencode serve` runs causes OpenCode 1.19+ to abort startup with
// "Database is not empty and has no session table".
func HasEngineSchema(dbPath string) bool {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return false
	}
	st, err := os.Stat(dbPath)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteReadTimeout)
	defer cancel()
	var one int
	err = sqlitecli.QueryRowReadOnly(
		ctx,
		dbPath,
		3*time.Second,
		"SELECT 1 FROM sqlite_master WHERE type='table' AND name='session' LIMIT 1;",
		nil,
		&one,
	)
	return err == nil && one == 1
}

func isConsoleStateSyncedTo(ext *ConsoleState, dstDB string) bool {
	if ext == nil || len(ext.Accounts) == 0 {
		return true
	}
	dst := ReadConsoleState(dstDB)
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

// SeedConsoleAccount mirrors the external Console login into dstDB once dstDB
// has an initialized OpenCode schema. On a fresh install where dstDB does not
// exist yet, pre-boot seeding is skipped so OpenCode can run its initial
// migrations cleanly, and StartDaemonWithBin calls SeedConsoleAccount again
// immediately after health check succeeds.
func SeedConsoleAccount(dstDB string) {
	dstDB = strings.TrimSpace(dstDB)
	if dstDB == "" || !HasEngineSchema(dstDB) {
		return
	}
	for _, src := range ExternalDBPaths() {
		if filepath.Clean(src) == filepath.Clean(dstDB) {
			continue
		}
		if state := ReadConsoleState(src); state != nil {
			if !isConsoleStateSyncedTo(state, dstDB) {
				_ = SyncConsoleState(state, dstDB)
			}
			return
		}
	}
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

// PruneOversizedMessageDiffs strips bloated $.summary.diffs arrays from
// OpenCode message rows exceeding 64 KiB while keeping $.summary.diffs as a
// valid empty JSON array ([]), preventing multi-megabyte message payloads from
// breaking engine history reads.
func PruneOversizedMessageDiffs(ctx context.Context, dbPath string) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil
	}
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() {
		return nil
	}
	pruneCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := sqlitecli.WithReadWrite(pruneCtx, dbPath, 10*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx,
			"UPDATE message SET data = json_set(data, '$.summary.diffs', json('[]')) WHERE length(data) > 65536 AND json_type(data, '$.summary.diffs') = 'array' AND json_array_length(data, '$.summary.diffs') > 0;")
		return execErr
	})
	if err != nil {
		if strings.Contains(err.Error(), "no such table: message") {
			return nil
		}
		return fmt.Errorf("prune oversized message diffs: %w", err)
	}
	return nil
}

const (
	// vacuumMinFreeBytes skips VACUUM when too little space would be reclaimed.
	vacuumMinFreeBytes = 8 << 20
	// vacuumAlwaysFreeBytes reclaims a large free list even in a big database.
	vacuumAlwaysFreeBytes = 256 << 20
)

// VacuumWorthwhile reports whether rewriting the whole database file pays off.
// VACUUM rewrites every page and blocks the engine's writers, so a large
// database that is mostly live data must not be rewritten just for its size.
func VacuumWorthwhile(pageSize, pageCount, freePages int64) bool {
	if pageSize <= 0 || pageCount <= 0 || freePages <= 0 {
		return false
	}
	freeBytes := pageSize * freePages
	if freeBytes < vacuumMinFreeBytes {
		return false
	}
	return freePages*5 >= pageCount || freeBytes >= vacuumAlwaysFreeBytes
}

// readFreeListStats returns page_size, page_count and freelist_count.
func readFreeListStats(ctx context.Context, dbPath string) (pageSize, pageCount, freePages int64, err error) {
	statCtx, cancel := context.WithTimeout(ctx, sqliteReadTimeout)
	defer cancel()
	err = sqlitecli.QueryRowReadOnly(
		statCtx,
		dbPath,
		3*time.Second,
		"SELECT (SELECT page_size FROM pragma_page_size()), (SELECT page_count FROM pragma_page_count()), (SELECT freelist_count FROM pragma_freelist_count());",
		nil,
		&pageSize, &pageCount, &freePages,
	)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read sqlite free list: %w", err)
	}
	return pageSize, pageCount, freePages, nil
}

// CleanDatabaseEventLog purges transient OpenCode event rows and oversized
// message summary diffs, then runs VACUUM only when the delete step succeeded
// and the free list is large enough to be worth a full rewrite.
func CleanDatabaseEventLog(ctx context.Context, dbPath string) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil
	}
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() {
		return nil
	}

	_ = PruneOversizedMessageDiffs(ctx, dbPath)

	purgeCtx, cancelPurge := context.WithTimeout(ctx, 30*time.Second)
	defer cancelPurge()
	err := sqlitecli.WithReadWrite(purgeCtx, dbPath, 10*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, "DELETE FROM event; DELETE FROM event_sequence; PRAGMA wal_checkpoint(TRUNCATE);")
		return execErr
	})
	if err != nil {
		return fmt.Errorf("purge sqlite event log: %w", err)
	}

	pageSize, pageCount, freePages, err := readFreeListStats(ctx, dbPath)
	if err != nil {
		return err
	}
	if !VacuumWorthwhile(pageSize, pageCount, freePages) {
		return nil
	}

	vacuumCtx, cancelVacuum := context.WithTimeout(ctx, 45*time.Second)
	defer cancelVacuum()
	err = sqlitecli.WithReadWrite(vacuumCtx, dbPath, 10*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, "VACUUM; PRAGMA wal_checkpoint(TRUNCATE);")
		return execErr
	})
	if err != nil {
		return fmt.Errorf("vacuum sqlite database: %w", err)
	}
	return nil
}

// SessionContentBytes returns the message and part payload bytes stored per
// engine session, read-only. A missing database yields an empty map.
func SessionContentBytes(ctx context.Context, dbPath string) (map[string]int64, error) {
	out := map[string]int64{}
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return out, nil
	}
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() {
		return out, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := sqlitecli.WithReadOnly(readCtx, dbPath, 5*time.Second, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			"SELECT COALESCE(session_id, ''), COALESCE(SUM(b), 0) FROM (SELECT session_id, length(data) AS b FROM part UNION ALL SELECT session_id, length(data) FROM message) GROUP BY session_id;")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var size int64
			if err := rows.Scan(&id, &size); err != nil {
				return err
			}
			if id != "" {
				out[id] = size
			}
		}
		return rows.Err()
	})
	if err != nil {
		return out, fmt.Errorf("read session sizes: %w", err)
	}
	return out, nil
}

// DeleteEngineSessions removes session, message, and part rows for sessionIDs
// (and any direct child sessions) from Hoxi's local engine SQLite database
// when `opencode serve` is not running, preventing orphaned payload bloat.
func DeleteEngineSessions(ctx context.Context, dbPath string, sessionIDs []string) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" || len(sessionIDs) == 0 {
		return nil
	}
	if st, err := os.Stat(dbPath); err != nil || st.IsDir() || st.Size() == 0 {
		return nil
	}
	seen := make(map[string]bool, len(sessionIDs))
	ids := make([]string, 0, len(sessionIDs))
	for _, raw := range sessionIDs {
		if id := strings.TrimSpace(raw); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	delCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := sqlitecli.WithImmediateTx(delCtx, dbPath, 5*time.Second, func(ctx context.Context, conn *sql.Conn) error {
		for i := 0; i < len(ids); i++ {
			rows, qErr := conn.QueryContext(ctx, "SELECT COALESCE(id, '') FROM session WHERE parent_id = ?;", ids[i])
			if qErr != nil {
				if strings.Contains(qErr.Error(), "no such table") {
					return nil
				}
				continue
			}
			for rows.Next() {
				var childID string
				if rows.Scan(&childID) == nil {
					if childID = strings.TrimSpace(childID); childID != "" && !seen[childID] {
						seen[childID] = true
						ids = append(ids, childID)
					}
				}
			}
			_ = rows.Close()
		}
		for _, id := range ids {
			for _, stmt := range []string{
				"DELETE FROM part WHERE session_id = ?;",
				"DELETE FROM message WHERE session_id = ?;",
				"DELETE FROM session WHERE id = ?;",
			} {
				if _, execErr := conn.ExecContext(ctx, stmt, id); execErr != nil && !strings.Contains(execErr.Error(), "no such table") {
					return execErr
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete sqlite engine sessions: %w", err)
	}
	return nil
}
