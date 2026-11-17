// Package sqlitecli provides pure-Go in-process SQLite execution alongside
// path resolution for the standalone sqlite3 command-line tool.
package sqlitecli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

const driverName = "sqlite"

var uriPathEscaper = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

// Executable prefers the Windows release's bundled sidecar, falling back to
// PATH for development builds and platforms that do not bundle SQLite.
func Executable() string {
	exe, _ := os.Executable()
	return resolve(runtime.GOOS, exe)
}

func resolve(goos, exe string) string {
	if goos == "windows" && exe != "" {
		path := filepath.Join(filepath.Dir(exe), "sqlite3.exe")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return "sqlite3"
}

// FileURI formats a filesystem path as a SQLite file: URI with optional query parameters.
func FileURI(dbPath, params string) string {
	trimmed := strings.TrimSpace(dbPath)
	if strings.HasPrefix(trimmed, "file:") {
		if params == "" || strings.Contains(trimmed, "immutable=1") {
			return trimmed
		}
		if strings.Contains(trimmed, "?") {
			return trimmed + "&" + params
		}
		return trimmed + "?" + params
	}
	slash := filepath.ToSlash(trimmed)
	escaped := uriPathEscaper.Replace(slash)
	uri := "file:" + escaped
	if len(escaped) >= 2 && escaped[1] == ':' {
		uri = "file:///" + escaped
	}
	if params != "" {
		uri += "?" + params
	}
	return uri
}

// ImmutableURI formats dbPath as an immutable=1 SQLite URI for sidecar-less WAL inspection.
func ImmutableURI(dbPath string) string {
	return FileURI(dbPath, "immutable=1")
}

func readOnlyURI(dbPath string, busyTimeout time.Duration) string {
	ms := busyTimeout.Milliseconds()
	if ms <= 0 {
		ms = 3000
	}
	params := fmt.Sprintf("mode=ro&_pragma=busy_timeout(%d)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-4000)", ms)
	return FileURI(dbPath, params)
}

func readWriteURI(dbPath string, busyTimeout time.Duration) string {
	ms := busyTimeout.Milliseconds()
	if ms <= 0 {
		ms = 5000
	}
	params := fmt.Sprintf("_pragma=busy_timeout(%d)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-4000)", ms)
	return FileURI(dbPath, params)
}

func isCantOpenError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unable to open database file") || strings.Contains(msg, "sqlite_cantopen") || strings.Contains(msg, "(14)")
}

func openSingleConnDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// WithReadOnly executes fn against a pure-Go in-process read-only SQLite handle.
// If the database header is in WAL mode (02 02) and an external CLI unlinked its
// -wal/-shm sidecars on clean exit, WithReadOnly safely retries with immutable=1
// after verifying no uncheckpointed -wal file exists on disk.
func WithReadOnly(ctx context.Context, dbPath string, busyTimeout time.Duration, fn func(ctx context.Context, db *sql.DB) error) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return os.ErrNotExist
	}
	if !strings.HasPrefix(dbPath, "file:") {
		st, err := os.Stat(dbPath)
		if err != nil {
			return err
		}
		if st.IsDir() {
			return fmt.Errorf("sqlite path is a directory: %s", dbPath)
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runWithDSN := func(dsn string) error {
		db, err := openSingleConnDB(dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		return fn(ctx, db)
	}

	err := runWithDSN(readOnlyURI(dbPath, busyTimeout))
	if err != nil && !strings.HasPrefix(dbPath, "file:") && isCantOpenError(err) {
		if _, walErr := os.Lstat(dbPath + "-wal"); os.IsNotExist(walErr) {
			return runWithDSN(ImmutableURI(dbPath))
		}
	}
	return err
}

// QueryRowReadOnly executes a single-row read-only query in-process and scans into dest.
func QueryRowReadOnly(ctx context.Context, dbPath string, busyTimeout time.Duration, query string, args []any, dest ...any) error {
	return WithReadOnly(ctx, dbPath, busyTimeout, func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, query, args...).Scan(dest...)
	})
}

// WithReadWrite executes fn against a pure-Go in-process read-write SQLite handle.
func WithReadWrite(ctx context.Context, dbPath string, busyTimeout time.Duration, fn func(ctx context.Context, db *sql.DB) error) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return fmt.Errorf("empty sqlite database path")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := openSingleConnDB(readWriteURI(dbPath, busyTimeout))
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(ctx, db)
}

// WithImmediateTx executes fn inside a BEGIN IMMEDIATE transaction on a single
// physical SQLite connection, ensuring busy_timeout is honored under concurrent writers.
func WithImmediateTx(ctx context.Context, dbPath string, busyTimeout time.Duration, fn func(ctx context.Context, conn *sql.Conn) error) error {
	return WithReadWrite(ctx, dbPath, busyTimeout, func(ctx context.Context, db *sql.DB) error {
		conn, err := db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()

		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		if err := fn(ctx, conn); err != nil {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
			return err
		}
		_, err = conn.ExecContext(ctx, "COMMIT")
		return err
	})
}
