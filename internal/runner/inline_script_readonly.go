package runner

import (
	"regexp"
	"strings"
)

// Auto mode runs `python3 tool.py` and `node x.mjs` without asking, yet it
// used to ask for every `python3 -c` / `node -e`, even one that only loads a
// JSON file or queries a local SQLite table and prints. Auto mode now judges
// inline Python and Node code by what the code shows it can do. Code that
// only computes, reads files it names and prints runs like a script file.
// Code that could write, delete or move files, change a database, start a
// process, open the network, read the environment, reach the home directory,
// load code dynamically, decrypt key material or name a secret path still
// asks. Perl, Ruby and PHP keep asking. This guards a cooperative agent
// against side effects it did not mean; it is not a sandbox, since an agent
// can already write a file and run it. The destructive scan still runs over
// the whole command afterwards. Mirrors .opencode/lib/inline-script-readonly.js;
// `autoOpaque` in .opencode/lib/opaque-command.cases.json keeps them aligned.

// scriptTrust says which interpreter scripts the opacity judge lets through.
type scriptTrust uint8

const (
	scriptTrustNone     scriptTrust = iota // every interpreter script is opaque
	scriptTrustReadOnly                    // auto: inline Python/Node code that only reads and prints
	scriptTrustFull                        // full trust and unattended: inline code and on-disk shell scripts
)

const inlineCategoryWrites = "writes"

func scriptTrustFor(mode string) scriptTrust {
	switch mode {
	case ApprovalModeTrusted, ApprovalModeUnattended:
		return scriptTrustFull
	case ApprovalModeAuto:
		return scriptTrustReadOnly
	}
	return scriptTrustNone
}

var (
	inlineStdWriteRe  = regexp.MustCompile(`\b(?:sys|process)\s*\.\s*(?:stdout|stderr)\s*\.\s*write\b`)
	inlineReCompileRe = regexp.MustCompile(`\bre\s*\.\s*compile\s*\(`)
	inlineMainGuardRe = regexp.MustCompile(`__(?:name|main)__`)
	inlineSQLExecRe   = regexp.MustCompile(`\.\s*execute\s*\(\s*`)
	inlineReadSQLRe   = regexp.MustCompile(`(?i)^\s*(?:select\b|pragma\s+(?:table_info|table_xinfo|index_list|index_info|index_xinfo|foreign_key_list|table_list|database_list)\b)`)
	inlineFieldRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

	// inlineMutationRe names file, database and process-state changes.
	inlineMutationRe = regexp.MustCompile(`(?i)write|appendfile|unlink|\bremove(?:dirs)?\b|rmdir|rmtree|rename|\brenames\b|mkdir|mkdtemp|mkstemp|mkfifo|mknod|chmod|chown|chflags|symlink|hardlink|link_to|truncate|utime|copyfile|copytree|shutil|tempfile|fileinput|shelve|\bdbm\b|tarfile|zipfile|\bgzip\b|\bbz2\b|\blzma\b|mmap|fcntl|logging|webbrowser|\bsignal\b|\bkill|setuid|setgid|chdir|chroot|umask|\bposix\b|\bnt\b|\bimportlib\b|\bO_(?:WRONLY|RDWR|CREAT|TRUNC|APPEND|EXCL)\b|\brm(?:sync|dir)?\b|\bcp(?:sync)?\b|\blink(?:sync)?\b|\btouch\b|\bsave\w*\s*\(|\bto_(?:csv|excel|parquet|pickle|json|html|sql|feather|hdf|stata|latex|markdown|xml|clipboard)\b|tofile|\bdump\s*\(|\bcommit\s*\(|\bbackup\b|load_extension|\b(?:delete|drop|insert|update|alter|vacuum|attach|upsert)\b|\breplace\s+into\b|\.\s*run\s*\(|\bvm\b|runin\w*context|\bconstructor\b|createrequire|globalthis|\breflect\b|\bproxy\b|\bmodule\s*\.\s*_|\bfs\w*\s*\[|\brequire\s*\([^)]*\)\s*\[|(?:^|[^.\w])(?:copy|move)\s*\(`)
	// inlineWriteModeRe is an open mode that writes, appends or creates.
	inlineWriteModeRe = regexp.MustCompile(`(?:^|[^\w])(?:'[rbtUs]*[wax+][rbtwaxUs+]*'|"[rbtUs]*[wax+][rbtwaxUs+]*")`)
	// inlineReachRe reaches the home directory, the user database, key material or dynamic string construction.
	inlineReachRe         = regexp.MustCompile(`(?i)expanduser|expandvars|homedir|userinfo|\bhome\s*\(|getpass|keyring|keychain|\bpwd\b|getpw\w*|decrypt|decipher|aesgcm|fernet|private_?key|\bfrom\s+(?:os|posix|nt)\s+import\b|\bimport\s+(?:os|posix|nt)\s+as\b`)
	inlineDynamicStringRe = regexp.MustCompile(`\b(?:chr|ord|bytes|bytearray|memoryview|fromhex|b64decode|b64encode|unhexlify|hexlify|codecs|atob|btoa|fromCharCode|fromCodePoint|getattr|setattr|delattr|globals|locals|vars|__import__|__builtins__|__dict__|__subclasses__|__class__|__bases__|__mro__)\b|\bBuffer\s*\.\s*from\b|\\(?:x[0-9a-fA-F]{2}|u[0-9a-fA-F]{4}|U[0-9a-fA-F]{8}|N\{|[0-7]{1,3})`)
	inlineFileCallRe      = regexp.MustCompile(`\b(?:open|Path|PurePath|PosixPath|WindowsPath|readFileSync|readFile|realpathSync|realpath|statSync|lstatSync|readdirSync|accessSync|existsSync|createReadStream|scandir|listdir|lstat|readlink)\s*\(\s*`)
	inlineSQLiteMentionRe = regexp.MustCompile(`(?i)\bsqlite3?\b|\.\s*connect\s*\(|\bconnect\s*\(`)
	inlineSQLiteConnectRe = regexp.MustCompile(`(?:\.\s*|\b)connect\s*\(\s*`)
	inlineURIReadOnlyRe   = regexp.MustCompile(`^\s*,\s*uri\s*=\s*True\s*,?\s*$`)
	inlinePathlibRe       = regexp.MustCompile(`\bpathlib\b|\bPath\b`)
	inlineReplaceRe       = regexp.MustCompile(`\.\s*replace\s*\(`)
	inlineOSAttrRe        = regexp.MustCompile(`\bos\s*\.\s*([A-Za-z_]\w*)`)
	inlineRequireRe       = regexp.MustCompile(`\b(?:require|import)\s*\(\s*['"]([^'"]+)['"]|\bfrom\s+['"]([^'"]+)['"]`)
	inlineKeyFileRe       = regexp.MustCompile(`(?i)(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|/)id_[a-z0-9]+)$`)

	inlineOSReadAttrs = setOf("path", "sep", "linesep", "name", "getcwd", "stat", "lstat", "listdir", "scandir", "walk",
		"fspath", "fsencode", "fsdecode", "devnull", "curdir", "pardir", "extsep", "altsep", "pathsep", "cpu_count",
		"getpid", "getppid", "access", "readlink", "R_OK", "F_OK", "X_OK", "W_OK", "strerror", "get_terminal_size",
		"platform", "arch", "EOL", "cpus", "totalmem", "freemem", "hostname", "type", "release", "uptime", "endianness",
		"tmpdir", "loadavg", "version", "machine", "availableParallelism")
	inlineBlockedNodeModules = setOf("child_process", "http", "https", "http2", "net", "tls", "dgram", "dns", "cluster",
		"worker_threads", "vm", "inspector", "repl", "v8", "wasi", "module", "process", "async_hooks", "trace_events")
)

// inlineScriptsReadOnly reports whether every inline interpreter script in
// command is Python or Node code that only reads and prints. It reuses the
// unattended parser, so `-c`/`-e` code and quoted heredoc bodies are judged
// exactly as the shell passes them, and code piped in or built from an
// expansion is refused.
func inlineScriptsReadOnly(command string) bool {
	ctx := &unattendedCtx{codeJudge: readOnlyScriptCategory}
	return unattendedScriptCategory(command, 0, ctx) == ""
}

// readOnlyScriptCategory judges one inline script; "" means it only reads.
func readOnlyScriptCategory(family, code string) string {
	if family != "python" && family != "python3" && family != "node" {
		return unattendedCategoryDynamic
	}
	if cat := scanUnattendedText(code); cat != "" {
		return cat
	}
	idioms := maskPrintIdioms(code)
	masked, ok := maskReadOnlySQL(idioms)
	if !ok || unattendedScriptCapabilityRe.MatchString(masked) {
		return unattendedCategoryDynamic
	}
	// Query text is masked only for the capability list; a write keyword
	// inside it still counts.
	if inlineMutationRe.MatchString(idioms) || inlineWriteModeRe.MatchString(idioms) {
		return inlineCategoryWrites
	}
	if inlineSQLiteMentionRe.MatchString(idioms) && !hasOnlyReadOnlySQLiteConnect(code) {
		return inlineCategoryWrites
	}
	if inlineReachRe.MatchString(masked) || inlineDynamicStringRe.MatchString(code) || !hasOnlyLiteralFileCalls(code) {
		return unattendedCategorySecret
	}
	if inlinePathlibRe.MatchString(masked) && inlineReplaceRe.MatchString(masked) {
		return inlineCategoryWrites // Path.replace moves a file
	}
	for _, m := range inlineOSAttrRe.FindAllStringSubmatch(masked, -1) {
		if !inlineOSReadAttrs[m[1]] {
			return inlineCategoryWrites
		}
	}
	for _, m := range inlineRequireRe.FindAllStringSubmatch(masked, -1) {
		mod := strings.TrimPrefix(m[1]+m[2], "node:")
		if inlineBlockedNodeModules[strings.SplitN(mod, "/", 2)[0]] {
			return unattendedCategoryDynamic
		}
	}
	for _, lit := range inlineStringLiterals(code) {
		lit = strings.TrimSpace(lit)
		if isSecretFilePath(lit) || isSensitiveExternalReadPath(lit) || isProtectedShellReadPath(lit) || inlineKeyFileRe.MatchString(lit) {
			return unattendedCategorySecret
		}
	}
	return ""
}

// maskPrintIdioms rewrites idioms the capability and write lists would flag
// although they only print or match: stdout/stderr writes, re.compile, the
// main guard, and JS template literals (their `${…}` code stays visible).
func maskPrintIdioms(code string) string {
	code = inlineStdWriteRe.ReplaceAllString(code, "print")
	code = inlineReCompileRe.ReplaceAllString(code, "re.pattern(")
	code = inlineMainGuardRe.ReplaceAllString(code, "MAIN")
	return strings.ReplaceAll(code, "`", `"`)
}

// maskReadOnlySQL rewrites `.execute("SELECT …")` and read-only PRAGMAs so
// the capability list does not read them as exec. An f-string query passes
// only when its fields are plain names. A literal that cannot be parsed
// keeps `execute` visible.
func maskReadOnlySQL(code string) (string, bool) {
	var b strings.Builder
	rest := code
	for {
		loc := inlineSQLExecRe.FindStringIndex(rest)
		if loc == nil {
			b.WriteString(rest)
			return b.String(), true
		}
		b.WriteString(rest[:loc[0]])
		after := rest[loc[1]:]
		lit, n, isF, ok := leadingPyStringLiteral(after)
		if !ok || !inlineReadSQLRe.MatchString(lit) || (isF && !plainFStringFields(lit)) {
			b.WriteString(rest[loc[0]:loc[1]]) // the capability list still sees execute
			rest = after
			continue
		}
		b.WriteString(`.query(""`)
		rest = after[n:]
	}
}

// leadingPyStringLiteral parses a Python string literal at the start of s and
// returns its body, its length, and whether it is an f-string.
func leadingPyStringLiteral(s string) (body string, n int, isF bool, ok bool) {
	i := 0
	for i < len(s) && i < 2 && strings.IndexByte("rRbBuUfF", s[i]) >= 0 {
		if s[i] == 'f' || s[i] == 'F' {
			isF = true
		}
		i++
	}
	if i >= len(s) || (s[i] != '"' && s[i] != '\'') {
		return "", 0, false, false
	}
	q := s[i : i+1]
	if strings.HasPrefix(s[i:], q+q+q) {
		q = q + q + q
	}
	start := i + len(q)
	for j := start; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if strings.HasPrefix(s[j:], q) {
			return s[start:j], j + len(q), isF, true
		}
	}
	return "", 0, false, false
}

// plainFStringFields reports whether every `{…}` field of an f-string body is
// a plain name or attribute path, so no code hides in the query text.
func plainFStringFields(body string) bool {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			if i+1 < len(body) && body[i+1] == '{' {
				i++
				continue
			}
			end := strings.IndexByte(body[i:], '}')
			if end < 0 || !inlineFieldRe.MatchString(body[i+1:i+end]) {
				return false
			}
			i += end
		case '}':
			if i+1 < len(body) && body[i+1] == '}' {
				i++
				continue
			}
			return false
		}
	}
	return true
}

// inlineStringLiterals returns the bodies of the quoted strings in code, for
// the secret path check. An unterminated quote returns what was read so far.
func inlineStringLiterals(code string) []string {
	var out []string
	for i := 0; i < len(code); i++ {
		c := code[i]
		if c != '"' && c != '\'' && c != '`' {
			continue
		}
		var b strings.Builder
		j := i + 1
		for ; j < len(code) && code[j] != c; j++ {
			if code[j] == '\\' && j+1 < len(code) {
				j++
			}
			b.WriteByte(code[j])
		}
		out = append(out, b.String())
		i = j
	}
	return out
}

// hasOnlyLiteralFileCalls requires every file/path call (open, Path,
// readFileSync, realpathSync, etc.) to take a single plain string literal as
// its first argument, refusing runtime-constructed paths like open(chr(46)+"env").
func hasOnlyLiteralFileCalls(code string) bool {
	locs := inlineFileCallRe.FindAllStringIndex(code, -1)
	for _, loc := range locs {
		after := code[loc[1]:]
		_, n, isF, ok := leadingPyStringLiteral(after)
		if !ok || isF {
			return false
		}
		tail := strings.TrimLeft(after[n:], " \t\r\n")
		if len(tail) == 0 || (tail[0] != ')' && tail[0] != ',') {
			return false
		}
	}
	return true
}

// hasOnlyReadOnlySQLiteConnect requires every sqlite3 connect(...) call to use
// ":memory:" or a "file:<path>?mode=ro" URI with uri=True so connecting never
// creates a new database file on disk.
func hasOnlyReadOnlySQLiteConnect(code string) bool {
	locs := inlineSQLiteConnectRe.FindAllStringIndex(code, -1)
	if len(locs) == 0 {
		return false
	}
	for _, loc := range locs {
		after := code[loc[1]:]
		lit, n, isF, ok := leadingPyStringLiteral(after)
		if !ok || isF {
			return false
		}
		rest := after[n:]
		closeIdx := strings.IndexByte(rest, ')')
		if closeIdx < 0 {
			return false
		}
		extraArgs := rest[:closeIdx]
		if strings.ContainsAny(extraArgs, "(\"") {
			return false
		}
		if lit == ":memory:" {
			if strings.TrimSpace(extraArgs) != "" {
				return false
			}
			continue
		}
		if !strings.HasPrefix(lit, "file:") || !inlineURIReadOnlyRe.MatchString(extraArgs) {
			return false
		}
		parts := strings.SplitN(strings.TrimPrefix(lit, "file:"), "?", 2)
		if len(parts) != 2 {
			return false
		}
		dbPath := strings.TrimSpace(parts[0])
		if dbPath == "" || isSecretFilePath(dbPath) || isSensitiveExternalReadPath(dbPath) || isProtectedShellReadPath(dbPath) || inlineKeyFileRe.MatchString(dbPath) {
			return false
		}
		hasModeRO := false
		for _, param := range strings.Split(parts[1], "&") {
			if param == "mode=ro" {
				hasModeRO = true
			} else if strings.HasPrefix(strings.ToLower(param), "mode=") {
				return false
			}
		}
		if !hasModeRO {
			return false
		}
	}
	return true
}
