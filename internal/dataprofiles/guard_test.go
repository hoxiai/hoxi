package dataprofiles

import (
	"testing"
)

func TestSQLAndSSHReadOnlyGuards(t *testing.T) {
	for _, valid := range []string{
		"SELECT id, email FROM accounts WHERE active = 1",
		"SELECT 'UPDATE accounts' AS note;",
		"SELECT 'O''Reilly' AS author, 'C:\\temp\\logs' AS win_path, 'abc\\\\' AS trailing_slashes;",
		"WITH top_orders AS (SELECT id FROM orders LIMIT 5) SELECT * FROM top_orders",
		"EXPLAIN QUERY PLAN SELECT * FROM accounts",
		"SHOW TABLES",
		"DESCRIBE orders",
	} {
		if ok, reason := IsReadOnlySQL(valid); !ok {
			t.Fatalf("expected %q allowed, got %s", valid, reason)
		}
	}
	for _, blocked := range []string{
		"SELECT 1; UPDATE accounts SET active = 0",
		"SELECT 'abc\\'; DROP TABLE accounts; --'",
		"SELECT 'abc\\''",
		`SELECT 1 AS "col\"; DROP TABLE accounts; --"`,
		"WITH changed AS (DELETE FROM accounts RETURNING id) SELECT * FROM changed",
		"EXPLAIN ANALYZE UPDATE accounts SET active = 1",
		"SELECT * INTO archive_accounts FROM accounts",
		"SELECT * FROM accounts FOR UPDATE",
		"PRAGMA writable_schema = 1",
	} {
		if ok, _ := IsReadOnlySQL(blocked); ok {
			t.Fatalf("expected %q blocked by IsReadOnlySQL", blocked)
		}
	}
	for _, blockedMulti := range []string{
		"SELECT 'abc\\'; DROP TABLE accounts; SELECT 'x'",
		`SELECT 1 AS "col\"; DROP TABLE accounts; SELECT "x"`,
	} {
		if err := ValidateSingleSQLStatement(blockedMulti); err == nil {
			t.Fatalf("expected ValidateSingleSQLStatement to reject backslash-quote smuggling: %q", blockedMulti)
		}
	}
	for _, allowedSSH := range []string{
		"uptime",
		"docker ps",
		"rg ERROR /var/log/app.log",
		"cat /srv/app/.env.example",
		"git diff HEAD~1",
	} {
		if ok, reason := IsReadOnlySSHCommand(allowedSSH); !ok {
			t.Fatalf("expected SSH command %q allowed, got: %s", allowedSSH, reason)
		}
	}
	for _, blockedSSH := range []string{
		"uptime && ls",
		"rg --pre=sh root /etc/hostname",
		"git diff --output=/tmp/overwrite HEAD",
		"git -c core.pager=sh log",
		"date -s 2026-01-01",
		"tail -f /var/log/syslog",
		"cat .ssh/config",
		"cat .ssh/id_ed25519_deploy",
		"cat .aws/credentials",
		"cat .kube/config",
		"cat ${HOME}/.env",
		"cat {.e,}nv",
		"cat .en*",
		"rg password .",
		"rg password",
	} {
		if ok, _ := IsReadOnlySSHCommand(blockedSSH); ok {
			t.Fatalf("expected SSH command %q blocked on read-only SSH", blockedSSH)
		}
	}
}
