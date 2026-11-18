package dataprofiles

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

type sqlTokenKind int

const (
	sqlTokWord sqlTokenKind = iota
	sqlTokSymbol
	sqlTokString
)

type sqlToken struct {
	kind  sqlTokenKind
	upper string
}

// tokenizeSQL lexes raw SQL into keywords/identifiers, symbols, and string
// literals while stripping line comments (-- and #) and block comments (/* */),
// handling single/double/backtick quotes and PostgreSQL $tag$ dollar-quoting.
// Unterminated comments or string literals return an error so ambiguous inputs
// fail closed.
func tokenizeSQL(raw string) ([]sqlToken, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("SQL query is empty")
	}
	var tokens []sqlToken
	n := len(s)
	i := 0
	for i < n {
		ch := s[i]
		if unicode.IsSpace(rune(ch)) {
			i++
			continue
		}
		// Line comment: --
		if ch == '-' && i+1 < n && s[i+1] == '-' {
			i += 2
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		// MySQL line comment: #
		if ch == '#' {
			i++
			for i < n && s[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment: /* ... */ (Note: MySQL /*! ... */ executable comments must NOT be stripped!)
		if ch == '/' && i+1 < n && s[i+1] == '*' {
			if i+2 < n && s[i+2] == '!' {
				return nil, fmt.Errorf("executable MySQL comments (/*! ... */) are not permitted")
			}
			i += 2
			closed := false
			for i+1 < n {
				if s[i] == '*' && s[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated SQL block comment")
			}
			continue
		}
		// Quoted strings / identifiers: '...', "...", `...`
		if ch == '\'' || ch == '"' || ch == '`' {
			quote := ch
			i++
			closed := false
			bsRun := 0
			for i < n {
				if s[i] == '\\' && quote != '`' {
					bsRun++
					i++
					continue
				}
				if s[i] == quote {
					if bsRun%2 == 1 {
						return nil, fmt.Errorf("backslash-escaped quote (\\%c) is ambiguous across SQL dialects; use doubled quotes (%c%c)", quote, quote, quote)
					}
					if i+1 < n && s[i+1] == quote {
						bsRun = 0
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				bsRun = 0
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted literal in SQL")
			}
			if quote == '\'' {
				tokens = append(tokens, sqlToken{kind: sqlTokString})
			} else {
				tokens = append(tokens, sqlToken{kind: sqlTokWord, upper: "IDENT"})
			}
			continue
		}
		// PostgreSQL dollar-quoted string: $tag$...$tag$
		if ch == '$' {
			tagEnd := i + 1
			for tagEnd < n && (unicode.IsLetter(rune(s[tagEnd])) || unicode.IsDigit(rune(s[tagEnd])) || s[tagEnd] == '_') {
				tagEnd++
			}
			if tagEnd < n && s[tagEnd] == '$' {
				tag := s[i : tagEnd+1]
				if tagEnd > i+1 && unicode.IsDigit(rune(s[i+1])) {
					// Positional parameter like $1 followed by $ is invalid; treat normally below.
				} else {
					closeIdx := strings.Index(s[tagEnd+1:], tag)
					if closeIdx < 0 {
						return nil, fmt.Errorf("unterminated dollar-quoted literal in SQL")
					}
					i = tagEnd + 1 + closeIdx + len(tag)
					tokens = append(tokens, sqlToken{kind: sqlTokString})
					continue
				}
			}
		}
		// Word / keyword / identifier
		if unicode.IsLetter(rune(ch)) || ch == '_' {
			start := i
			i++
			for i < n && (unicode.IsLetter(rune(s[i])) || unicode.IsDigit(rune(s[i])) || s[i] == '_' || s[i] == '$') {
				i++
			}
			tokens = append(tokens, sqlToken{
				kind:  sqlTokWord,
				upper: strings.ToUpper(s[start:i]),
			})
			continue
		}
		// Number or parameter ($1)
		if unicode.IsDigit(rune(ch)) || (ch == '$' && i+1 < n && unicode.IsDigit(rune(s[i+1]))) {
			i++
			for i < n && (unicode.IsDigit(rune(s[i])) || s[i] == '.' || unicode.IsLetter(rune(s[i]))) {
				i++
			}
			tokens = append(tokens, sqlToken{kind: sqlTokSymbol, upper: "NUM"})
			continue
		}
		tokens = append(tokens, sqlToken{kind: sqlTokSymbol, upper: string(ch)})
		i++
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("SQL query contains no executable tokens")
	}
	return tokens, nil
}

var forbiddenReadOnlySQLKeywords = map[string]bool{
	"INSERT":     true,
	"UPDATE":     true,
	"DELETE":     true,
	"MERGE":      true,
	"REPLACE":    true,
	"UPSERT":     true,
	"DROP":       true,
	"ALTER":      true,
	"CREATE":     true,
	"TRUNCATE":   true,
	"RENAME":     true,
	"GRANT":      true,
	"REVOKE":     true,
	"CALL":       true,
	"EXEC":       true,
	"EXECUTE":    true,
	"PREPARE":    true,
	"DEALLOCATE": true,
	"BEGIN":      true,
	"START":      true,
	"COMMIT":     true,
	"ROLLBACK":   true,
	"SAVEPOINT":  true,
	"LOCK":       true,
	"UNLOCK":     true,
	"SET":        true,
	"RESET":      true,
	"PRAGMA":     true,
	"ATTACH":     true,
	"DETACH":     true,
	"VACUUM":     true,
	"REINDEX":    true,
	"COPY":       true,
	"LOAD":       true,
	"HANDLER":    true,
	"DO":         true,
	"LISTEN":     true,
	"NOTIFY":     true,
	"UNLISTEN":   true,
}

var forbiddenReadOnlySQLFunctions = map[string]bool{
	"LOAD_FILE":                 true,
	"PG_READ_FILE":              true,
	"PG_READ_BINARY_FILE":       true,
	"PG_STAT_FILE":              true,
	"PG_LS_DIR":                 true,
	"PG_LS_LOGDIR":              true,
	"PG_LS_WALDIR":              true,
	"PG_EXECUTE_SERVER_PROGRAM": true,
	"PG_TERMINATE_BACKEND":      true,
	"PG_CANCEL_BACKEND":         true,
	"PG_RELOAD_CONF":            true,
	"PG_ROTATE_LOGFILE":         true,
	"LO_IMPORT":                 true,
	"LO_EXPORT":                 true,
	"LO_UNLINK":                 true,
	"DBLINK":                    true,
	"DBLINK_EXEC":               true,
	"SQLITE_LOAD_EXTENSION":     true,
	"LOAD_EXTENSION":            true,
}

// ValidateSingleSQLStatement ensures raw contains exactly one SQL statement
// (allowing an optional trailing semicolon) with balanced comments and quotes.
func ValidateSingleSQLStatement(raw string) error {
	tokens, err := tokenizeSQL(raw)
	if err != nil {
		return err
	}
	for i, tok := range tokens {
		if tok.kind == sqlTokSymbol && tok.upper == ";" && i != len(tokens)-1 {
			return fmt.Errorf("multi-statement SQL queries are forbidden; send one statement at a time")
		}
	}
	return nil
}

// IsReadOnlySQL checks whether raw is a single, side-effect-free read query
// (SELECT, SHOW, DESCRIBE, DESC, VALUES, or read-only WITH / EXPLAIN).
// Any parse ambiguity, multi-statement chain, data-modifying CTE, file-reading
// server function, SELECT INTO, or locking FOR UPDATE/SHARE fails closed.
func IsReadOnlySQL(raw string) (bool, string) {
	tokens, err := tokenizeSQL(raw)
	if err != nil {
		return false, err.Error()
	}
	// Strip optional single trailing semicolon.
	if tokens[len(tokens)-1].kind == sqlTokSymbol && tokens[len(tokens)-1].upper == ";" {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 {
		return false, "SQL query is empty"
	}
	for _, tok := range tokens {
		if tok.kind == sqlTokSymbol && tok.upper == ";" {
			return false, "multi-statement SQL queries are not permitted"
		}
	}

	lead := tokens[0]
	if lead.kind != sqlTokWord {
		return false, "query must start with a read-only SQL keyword (SELECT, SHOW, DESCRIBE, EXPLAIN, WITH)"
	}
	switch lead.upper {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "VALUES", "WITH", "EXPLAIN":
	default:
		return false, fmt.Sprintf("statement %q is not permitted on a read-only profile", lead.upper)
	}

	if lead.upper == "EXPLAIN" {
		for i := 1; i < len(tokens); i++ {
			if tokens[i].kind == sqlTokWord && tokens[i].upper == "ANALYZE" {
				return false, "EXPLAIN ANALYZE executes the target statement and is blocked on read-only profiles"
			}
		}
	}

	hasSelectInWith := lead.upper != "WITH"
	for i, tok := range tokens {
		if tok.kind != sqlTokWord {
			continue
		}
		if lead.upper == "WITH" && (tok.upper == "SELECT" || tok.upper == "VALUES") {
			hasSelectInWith = true
		}
		if forbiddenReadOnlySQLKeywords[tok.upper] {
			return false, fmt.Sprintf("keyword %q is not permitted in read-only SQL", tok.upper)
		}
		if forbiddenReadOnlySQLFunctions[tok.upper] {
			return false, fmt.Sprintf("function %q is not permitted in read-only SQL", tok.upper)
		}
		// Block SELECT ... INTO <table/outfile/dumpfile>
		if tok.upper == "INTO" {
			return false, "SELECT INTO / INTO OUTFILE is not permitted in read-only SQL"
		}
		// Block row-locking clauses FOR UPDATE / FOR SHARE / FOR NO KEY UPDATE / FOR KEY SHARE
		if tok.upper == "FOR" && i+1 < len(tokens) && tokens[i+1].kind == sqlTokWord {
			next := tokens[i+1].upper
			if next == "UPDATE" || next == "SHARE" || next == "NO" || next == "KEY" {
				return false, "row-locking FOR UPDATE/SHARE clauses are not permitted in read-only SQL"
			}
		}
	}
	if !hasSelectInWith {
		return false, "WITH clause must conclude with a read-only SELECT"
	}
	return true, ""
}

var allowedReadOnlySSHCommands = map[string]bool{
	"ls":       true,
	"pwd":      true,
	"whoami":   true,
	"id":       true,
	"uname":    true,
	"hostname": true,
	"uptime":   true,
	"date":     true,
	"df":       true,
	"du":       true,
	"free":     true,
	"ps":       true,
	"stat":     true,
	"wc":       true,
	"head":     true,
	"tail":     true,
	"cat":      true,
	"grep":     true,
	"rg":       true,
}

// IsReadOnlySSHCommand validates that cmd is a single, shell-operator-free
// inspection command that does not execute preprocessors, overwrite files, or
// read secret/credential paths when a host SSH profile is marked ReadOnly.
func IsReadOnlySSHCommand(cmd string) (bool, string) {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return false, "SSH command is empty"
	}
	for _, bad := range []string{";", "|", "&", ">", "<", "`", "$", "{", "}", "*", "?", "[", "\\", "\n", "\r", "\x00"} {
		if strings.Contains(trimmed, bad) {
			return false, fmt.Sprintf("shell metacharacter %q is not permitted on a read-only SSH profile", bad)
		}
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false, "SSH command is empty"
	}
	bin := filepath.Base(fields[0])
	if !allowedReadOnlySSHCommands[bin] {
		// Allow two-word status subcommands: `systemctl status ...`, `docker ps ...`, `docker logs ...`, `git status/log/diff`
		if len(fields) >= 2 {
			sub := fields[1]
			switch {
			case bin == "systemctl" && (sub == "status" || sub == "is-active" || sub == "list-units"):
			case bin == "journalctl":
			case bin == "docker" && (sub == "ps" || sub == "logs" || sub == "inspect" || sub == "stats"):
				if sub == "stats" && !containsFlag(fields[2:], "--no-stream") {
					return false, "docker stats on read-only SSH requires --no-stream"
				}
			case bin == "git" && (sub == "status" || sub == "log" || sub == "diff" || sub == "branch" || sub == "rev-parse"):
			default:
				return false, fmt.Sprintf("command %q is not permitted on a read-only SSH profile", bin)
			}
		} else {
			return false, fmt.Sprintf("command %q is not permitted on a read-only SSH profile", bin)
		}
	}
	if ok, reason := checkReadOnlySSHFlags(bin, fields[1:]); !ok {
		return false, reason
	}
	for _, arg := range fields[1:] {
		clean := strings.Trim(arg, `"'`)
		candidates := []string{clean}
		if _, val, hasEq := strings.Cut(clean, "="); hasEq && val != "" {
			candidates = append(candidates, strings.Trim(val, `"'`))
		}
		for _, cand := range candidates {
			if isSensitiveSSHPath(cand) {
				return false, fmt.Sprintf("reading sensitive path %q is blocked on SSH profiles", arg)
			}
		}
	}
	return true, ""
}

func checkReadOnlySSHFlags(bin string, args []string) (bool, string) {
	nonFlagArgs := 0
	hasRecursiveGrep := false
	for _, raw := range args {
		a := strings.Trim(raw, `"'`)
		flagName, _, _ := strings.Cut(a, "=")
		if strings.HasPrefix(a, "-") && a != "-" {
			switch bin {
			case "rg":
				if flagName == "--pre" || flagName == "--pre-glob" || flagName == "-z" || flagName == "--search-zip" || flagName == "-f" || flagName == "--file" {
					return false, fmt.Sprintf("flag %q is not permitted for rg on a read-only SSH profile", flagName)
				}
			case "grep":
				if flagName == "-f" || flagName == "--file" {
					return false, fmt.Sprintf("flag %q is not permitted for grep on a read-only SSH profile", flagName)
				}
				if flagName == "-r" || flagName == "-R" || flagName == "--recursive" || (strings.HasPrefix(flagName, "-") && !strings.HasPrefix(flagName, "--") && strings.ContainsAny(flagName[1:], "rR")) {
					hasRecursiveGrep = true
				}
			case "git":
				if flagName == "-c" || flagName == "--config-env" || flagName == "-o" || flagName == "--output" ||
					flagName == "--ext-diff" || flagName == "--textconv" || flagName == "--exec" ||
					flagName == "--upload-pack" || flagName == "--git-dir" || flagName == "--work-tree" {
					return false, fmt.Sprintf("flag %q is not permitted for git on a read-only SSH profile", flagName)
				}
			case "date":
				if flagName == "-s" || flagName == "--set" {
					return false, "setting system clock via date is not permitted on a read-only SSH profile"
				}
			case "tail":
				if flagName == "-f" || flagName == "-F" || flagName == "--follow" {
					return false, "unbounded tail follow is not permitted on a read-only SSH profile"
				}
			}
			continue
		}
		nonFlagArgs++
		if (bin == "rg" || (bin == "grep" && hasRecursiveGrep)) && (a == "." || a == ".." || a == "/") {
			return false, "broad recursive secret-bearing directory scan is not permitted on a read-only SSH profile; specify a concrete log or subdirectory"
		}
	}
	if bin == "rg" && nonFlagArgs < 2 {
		return false, "rg on a read-only SSH profile requires both a pattern and an explicit target path"
	}
	return true, ""
}

func isSensitiveSSHPath(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	s := filepath.ToSlash(trimmed)
	if strings.HasPrefix(s, "~") {
		if idx := strings.IndexByte(s, '/'); idx >= 0 {
			s = s[idx+1:]
		} else {
			return false
		}
	}
	for strings.HasPrefix(s, "./") {
		s = strings.TrimPrefix(s, "./")
	}
	slash := "/" + strings.TrimLeft(s, "/")
	lowerSlash := strings.ToLower(slash)
	base := strings.ToLower(filepath.Base(slash))
	if base == ".env.example" || base == ".env.sample" || base == ".env.template" {
		return false
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == ".envrc" ||
		base == "shadow" || base == "gshadow" || base == "master.passwd" || base == "sudoers" ||
		base == "authorized_keys" || base == "credentials" ||
		base == ".netrc" || base == ".npmrc" || base == ".pypirc" || base == ".git-credentials" ||
		strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") ||
		strings.HasPrefix(base, "id_ecdsa") || strings.HasPrefix(base, "id_dsa") ||
		strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") ||
		strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".kdbx") {
		return true
	}
	for _, seg := range []string{"/.ssh/", "/.aws/", "/.kube/", "/.gnupg/", "/.docker/", "/.hoxi/", "/.config/gh/", "/.config/gcloud/"} {
		if strings.Contains(lowerSlash+"/", seg) {
			return true
		}
	}
	return false
}

func containsFlag(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
