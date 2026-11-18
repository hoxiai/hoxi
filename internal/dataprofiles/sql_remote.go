package dataprofiles

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const maxRemoteSQLBytes = 128 * 1024

const (
	defaultMaxSQLRows = 200
	hardMaxSQLRows    = 1000
	defaultSQLTimeout = 20 * time.Second
)

func effectiveMaxRows(requested int) int {
	if requested <= 0 {
		return defaultMaxSQLRows
	}
	if requested > hardMaxSQLRows {
		return hardMaxSQLRows
	}
	return requested
}

func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func sanitizedDBEnv() []string {
	return []string{"LANG=en_US.UTF-8"}
}

func querySQLiteRows(ctx context.Context, target string, ro bool, limit int, stmt string) ([]string, []map[string]any, bool, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil || bin == "" {
		return nil, nil, false, errors.New("sqlite3 client is not installed in PATH on this host")
	}
	cleanTarget := strings.TrimSpace(target)
	if cleanTarget == "" || strings.HasPrefix(cleanTarget, "-") {
		return nil, nil, false, errors.New("invalid sqlite database path")
	}
	if absTarget, absErr := filepath.Abs(cleanTarget); absErr == nil {
		cleanTarget = absTarget
	}
	args := []string{"-bail", "-csv", "-header"}
	if ro {
		args = append(args, "-readonly")
	}
	args = append(args, cleanTarget)

	var script bytes.Buffer
	script.WriteString(".timeout 5000\n")
	script.WriteString(strings.TrimSpace(stmt))
	if !strings.HasSuffix(strings.TrimSpace(stmt), ";") {
		script.WriteByte(';')
	}
	script.WriteByte('\n')

	cmd := subproc.CommandContext(ctx, bin, args...)
	cmd.Env = sanitizedDBEnv()
	cmd.Stdin = &script
	stdout, stderr, runErr := runBoundedCmd(cmd, maxRemoteSQLBytes)
	if runErr != nil {
		msg := firstNonEmpty(strings.TrimSpace(stderr), strings.TrimSpace(stdout), runErr.Error())
		return nil, nil, false, errors.New(msg)
	}
	cols, rows, trunc := parseCSVTable(stdout, limit)
	return cols, rows, trunc, nil
}

func (s *Service) execPostgresDriver(ctx context.Context, prof Profile, query string, maxRows int) (map[string]any, error) {
	bin := s.psqlBin
	if bin == "" {
		found, err := exec.LookPath("psql")
		if err != nil || found == "" {
			return nil, errors.New("psql client is not installed in PATH on this host")
		}
		bin = found
	}
	host := firstNonEmpty(prof.Host, "127.0.0.1")
	port := prof.Port
	if port <= 0 {
		port = 5432
	}
	dbName := firstNonEmpty(prof.Database, "postgres")
	args := []string{
		"--no-psqlrc",
		"--no-password",
		"-v", "ON_ERROR_STOP=1",
		"--pset=pager=off",
		"--csv",
		"-h", host,
		"-p", strconv.Itoa(port),
		"-d", dbName,
	}
	if u := strings.TrimSpace(prof.Username); u != "" {
		args = append(args, "-U", u)
	}

	var script bytes.Buffer
	if prof.ReadOnly {
		script.WriteString("SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY;\n")
	}
	script.WriteString(strings.TrimSpace(query))
	if !strings.HasSuffix(strings.TrimSpace(query), ";") {
		script.WriteByte(';')
	}
	script.WriteByte('\n')

	cmd := subproc.CommandContext(ctx, bin, args...)
	env := append(sanitizedDBEnv(), "PGCONNECT_TIMEOUT=10")
	if m := strings.TrimSpace(prof.SSLMode); m != "" {
		env = append(env, "PGSSLMODE="+m)
	}
	if secret := strings.TrimSpace(prof.Password); secret != "" {
		env = append(env, "PG"+"PASSWORD="+secret)
	}
	cmd.Env = env
	cmd.Stdin = &script

	stdout, stderr, err := runBoundedCmd(cmd, maxRemoteSQLBytes)
	if err != nil {
		msg := firstNonEmpty(strings.TrimSpace(stderr), strings.TrimSpace(stdout), err.Error())
		return nil, fmt.Errorf("postgres query on %s failed: %s", prof.ID, redactProfileSecrets(msg, prof))
	}
	cols, rows, truncated := parseCSVTable(redactProfileSecrets(stdout, prof), maxRows)
	return map[string]any{
		"profile":   prof.ID,
		"kind":      KindPostgres,
		"columns":   cols,
		"rows":      rows,
		"rowCount":  len(rows),
		"truncated": truncated,
	}, nil
}

func (s *Service) execMySQLDriver(ctx context.Context, prof Profile, query string, maxRows int) (map[string]any, error) {
	bin := s.mysqlBin
	if bin == "" {
		found, err := exec.LookPath("mysql")
		if err != nil || found == "" {
			return nil, errors.New("mysql client is not installed in PATH on this host")
		}
		bin = found
	}
	host := firstNonEmpty(prof.Host, "127.0.0.1")
	port := prof.Port
	if port <= 0 {
		port = 3306
	}
	args := []string{
		"--no-defaults",
		"--batch",
		"--raw",
		"--connect-timeout=10",
		"--default-character-set=utf8mb4",
		"-h", host,
		"-P", strconv.Itoa(port),
	}
	if u := strings.TrimSpace(prof.Username); u != "" {
		args = append(args, "-u", u)
	}
	if dbName := strings.TrimSpace(prof.Database); dbName != "" {
		args = append(args, dbName)
	}

	var script bytes.Buffer
	if prof.ReadOnly {
		script.WriteString("SET SESSION TRANSACTION READ ONLY;\n")
	}
	script.WriteString(strings.TrimSpace(query))
	if !strings.HasSuffix(strings.TrimSpace(query), ";") {
		script.WriteByte(';')
	}
	script.WriteByte('\n')

	cmd := subproc.CommandContext(ctx, bin, args...)
	env := sanitizedDBEnv()
	if secret := strings.TrimSpace(prof.Password); secret != "" {
		env = append(env, "MYSQL_"+"PWD="+secret)
	}
	cmd.Env = env
	cmd.Stdin = &script

	stdout, stderr, err := runBoundedCmd(cmd, maxRemoteSQLBytes)
	if err != nil {
		msg := firstNonEmpty(strings.TrimSpace(stderr), strings.TrimSpace(stdout), err.Error())
		return nil, fmt.Errorf("mysql query on %s failed: %s", prof.ID, redactProfileSecrets(msg, prof))
	}
	cols, rows, truncated := parseTSVTable(redactProfileSecrets(stdout, prof), maxRows)
	return map[string]any{
		"profile":   prof.ID,
		"kind":      KindMySQL,
		"columns":   cols,
		"rows":      rows,
		"rowCount":  len(rows),
		"truncated": truncated,
	}, nil
}

func parseCSVTable(raw string, maxRows int) ([]string, []map[string]any, bool) {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n")), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "SET" {
		lines = lines[1:]
	}
	cleaned := strings.Join(lines, "\n")
	if strings.TrimSpace(cleaned) == "" {
		return []string{}, []map[string]any{}, false
	}
	r := csv.NewReader(strings.NewReader(cleaned))
	r.FieldsPerRecord = -1
	cols, err := r.Read()
	if err != nil || len(cols) == 0 {
		return []string{}, []map[string]any{}, false
	}
	var rows []map[string]any
	truncated := false
	for {
		rec, rErr := r.Read()
		if errors.Is(rErr, io.EOF) {
			break
		}
		if rErr != nil {
			break
		}
		if len(rows) >= maxRows {
			truncated = true
			break
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if i < len(rec) {
				row[c] = rec[i]
			} else {
				row[c] = ""
			}
		}
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return cols, rows, truncated
}

func parseTSVTable(raw string, maxRows int) ([]string, []map[string]any, bool) {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(raw, "\r\n", "\n"), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return []string{}, []map[string]any{}, false
	}
	cols := strings.Split(lines[0], "\t")
	rows := make([]map[string]any, 0, min(len(lines)-1, maxRows))
	truncated := false
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		if len(rows) >= maxRows {
			truncated = true
			break
		}
		vals := strings.Split(line, "\t")
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if i < len(vals) && vals[i] != "NULL" {
				row[c] = vals[i]
			} else {
				row[c] = nil
			}
		}
		rows = append(rows, row)
	}
	return cols, rows, truncated
}
