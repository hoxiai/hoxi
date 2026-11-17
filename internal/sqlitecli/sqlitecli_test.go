package sqlitecli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveBundledSQLiteOrPathFallback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Hoxi.exe")
	if got := resolve("windows", exe); got != "sqlite3" {
		t.Fatalf("missing sidecar: %q", got)
	}
	sidecar := filepath.Join(dir, "sqlite3.exe")
	if err := os.WriteFile(sidecar, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolve("windows", exe); got != sidecar {
		t.Fatalf("bundled sidecar: %q", got)
	}
	if got := resolve("darwin", exe); got != "sqlite3" {
		t.Fatalf("non-Windows fallback: %q", got)
	}
}

func TestInProcessReadOnlyWriteRejectionAndMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	var one int
	if err := QueryRowReadOnly(context.Background(), missing, time.Second, "SELECT 1;", nil, &one); err == nil {
		t.Fatal("expected error for missing database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("read-only open unexpectedly created missing database: %v", err)
	}

	dbPath := filepath.Join(dir, "test.db")
	err := WithImmediateTx(context.Background(), dbPath, 3*time.Second, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, meta TEXT);`); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, `INSERT INTO items VALUES (?, ?);`, "k1", `{"name":"Hoxi"}`)
		return err
	})
	if err != nil {
		t.Fatalf("WithImmediateTx setup: %v", err)
	}

	var name string
	if err := QueryRowReadOnly(context.Background(), dbPath, 3*time.Second,
		`SELECT json_extract(meta, '$.name') FROM items WHERE id = ?;`, []any{"k1"}, &name); err != nil {
		t.Fatalf("QueryRowReadOnly: %v", err)
	}
	if name != "Hoxi" {
		t.Fatalf("name = %q, want Hoxi", name)
	}

	err = WithReadOnly(context.Background(), dbPath, 3*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, `DELETE FROM items;`)
		return execErr
	})
	if err == nil {
		t.Fatal("expected read-only handle to reject DELETE")
	}
}

func TestWithReadOnlySidecarlessWALImmutableFallback(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wal_no_sidecar.db")
	err := WithReadWrite(context.Background(), dbPath, 3*time.Second, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, `
			PRAGMA journal_mode = WAL;
			CREATE TABLE steps (idx INTEGER PRIMARY KEY);
			INSERT INTO steps VALUES (1), (2), (3);
			PRAGMA wal_checkpoint(TRUNCATE);
		`)
		return execErr
	})
	if err != nil {
		t.Fatalf("seed WAL database: %v", err)
	}
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	var count int
	if err := QueryRowReadOnly(context.Background(), dbPath, 2*time.Second, `SELECT count(*) FROM steps;`, nil, &count); err != nil {
		t.Fatalf("QueryRowReadOnly on sidecar-less WAL failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func seedBenchmarkDB(b *testing.B) string {
	b.Helper()
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	err := WithReadWrite(context.Background(), dbPath, 5*time.Second, func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, time_created INTEGER);
			CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
			CREATE INDEX idx_msg_session ON message(session_id);
			INSERT INTO session VALUES ('ses_1', 'Bench', 1000);
		`); err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO message VALUES (?, 'ses_1', ?, ?);`)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		defer stmt.Close()
		for i := 0; i < 300; i++ {
			if _, err := stmt.ExecContext(ctx, fmt.Sprintf("msg_%d", i), 1000+i,
				`{"role":"assistant","providerID":"openai","modelID":"gpt-5","cost":0.01,"tokens":{"input":1000,"output":200,"reasoning":50,"cache":{"read":500,"write":0}}}`); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		b.Fatal(err)
	}
	return dbPath
}

const benchmarkAggSQL = `
SELECT
  COALESCE(json_extract(m.data, '$.providerID'), 'unknown') AS provider_id,
  COALESCE(json_extract(m.data, '$.modelID'), 'unknown') AS model_id,
  COUNT(*) AS turns,
  COALESCE(SUM(CAST(COALESCE(json_extract(m.data, '$.tokens.input'), 0) AS INTEGER)), 0) AS input_tokens
FROM session s
CROSS JOIN message m ON m.session_id = s.id
WHERE m.time_created >= 1000 AND json_extract(m.data, '$.role') = 'assistant'
GROUP BY provider_id, model_id;`

func BenchmarkSQLiteSubprocessCLI(b *testing.B) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		b.Skip("sqlite3 CLI not available")
	}
	dbPath := seedBenchmarkDB(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := exec.Command("sqlite3", "-json", "-readonly", "-cmd", ".timeout 3000", dbPath, benchmarkAggSQL).Output()
		if err != nil || len(out) == 0 {
			b.Fatalf("sqlite3 CLI failed: %v", err)
		}
	}
}

func BenchmarkSQLiteInProcessReadOnly(b *testing.B) {
	dbPath := seedBenchmarkDB(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := WithReadOnly(ctx, dbPath, 3*time.Second, func(ctx context.Context, db *sql.DB) error {
			rows, err := db.QueryContext(ctx, benchmarkAggSQL)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var p, m string
				var turns, inTok int64
				if err := rows.Scan(&p, &m, &turns, &inTok); err != nil {
					return err
				}
			}
			return rows.Err()
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
