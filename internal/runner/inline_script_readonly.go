package runner

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Auto mode runs `python3 tool.py` and `node x.mjs` without asking, yet it
// used to ask for every `python3 -c` / `node -e`, even one that only loads a
// JSON file or queries a local SQLite table and prints. Auto mode now judges
// inline Python and Node code by what the code shows it can do. Code that
// only computes, reads non-secret files and prints runs like a script file.
// Code that could write, delete or move files, change a database, start a
// process, open the network, read the environment, reach the home directory,
// load code dynamically, decrypt key material or name a secret path still
// asks. Perl, Ruby and PHP keep asking. This guards a cooperative agent
// against side effects it did not mean; it is not a sandbox, since an agent
// can already write a file and run it. The destructive scan still runs over
// the whole command afterwards (masking only verified read-only inline script
// bodies). Mirrors .opencode/lib/inline-script-readonly.js; `autoOpaque` in
// .opencode/lib/opaque-command.cases.json keeps them aligned.

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
	inlineStdWriteRe      = regexp.MustCompile(`\b(?:sys|process)\s*\.\s*(?:stdout|stderr)\s*\.\s*write\b`)
	inlineSafeProcessRe   = regexp.MustCompile(`\bprocess\s*\.\s*(?:argv|cwd|version|versions|platform|arch|pid|ppid|hrtime|memoryUsage|cpuUsage|uptime|stdin)\b`)
	inlineUnsafeProcessRe = regexp.MustCompile(`\bprocess\b`)
	inlineReCompileRe     = regexp.MustCompile(`\bre\s*\.\s*compile\s*\(`)
	inlineMainGuardRe     = regexp.MustCompile(`\b__(?:name|main|dirname|filename)__\b`)
	inlineSQLExecRe       = regexp.MustCompile(`\.\s*execute\s*\(\s*`)
	inlineReadSQLRe       = regexp.MustCompile(`(?i)^\s*(?:select\b|pragma\s+(?:table_info|table_xinfo|index_list|index_info|index_xinfo|foreign_key_list|table_list|database_list)\b)`)
	inlineSQLWriteRe      = regexp.MustCompile(`(?i)\b(?:delete|drop|insert|update|alter|vacuum|attach|upsert|create|replace|reindex|pragma\s+\w+\s*=)\b`)
	inlineFieldRe         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

	// inlineMutationRe names file, database and process-state changes on masked code.
	inlineMutationRe = regexp.MustCompile(`(?i)\b(?:create)?write\w*\b|\bappendfile(?:sync)?\b|\bunlink(?:sync)?\b|\bremove(?:dirs)?\b|\brmdir(?:sync)?\b|\brmtree\b|\brename(?:s|sync)?\b|\bmkdir(?:sync|p)?\b|\bmkdtemp(?:sync)?\b|\bmkstemp\b|\bmkfifo\b|\bmknod\b|\bf?chmod(?:sync)?\b|\blchmod(?:sync)?\b|\bf?chown(?:sync)?\b|\blchown(?:sync)?\b|\bchflags\b|\bsymlink(?:sync)?\b|\bhardlink\b|\blink_to\b|\bf?truncate(?:sync)?\b|\bf?utimes?(?:sync)?\b|\blutimes(?:sync)?\b|\bcopyfile(?:sync)?\b|\bcopytree\b|\bshutil\b|\btempfile\b|\bfileinput\b|\bshelve\b|\bdbm\b|\btarfile\b|\bzipfile\b|\bgzip\b|\bbz2\b|\blzma\b|\bmmap\b|\bfcntl\b|\blogging\b|\bwebbrowser\b|\bsignal\b|\bkill\w*\b|\bsetuid\b|\bsetgid\b|\bchdir\b|\bchroot\b|\bumask\b|\bposix\b|\bnt\b|\bimportlib\b|\bO_(?:WRONLY|RDWR|CREAT|TRUNC|APPEND|EXCL)\b|\brm(?:sync|dir)?\b|\bcp(?:sync)?\b|\blink(?:sync)?\b|\btouch\b|\bsave\w*\s*\(|\bto_(?:csv|excel|parquet|pickle|json|html|sql|feather|hdf|stata|latex|markdown|xml|clipboard)\b|\btofile\b|\bdump\s*\(|\bcommit\s*\(|\bbackup\b|\bload_extension\b|\b(?:drop|insert|alter|vacuum|attach|upsert)\b|\breplace\s+into\b|\.\s*run\s*\(|\bvm\b|\brunin\w*context\b|\bconstructor\b|\bcreaterequire\b|\bglobalthis\b|\breflect\b|\bproxy\b|\bmodule\s*\.\s*_|\bfs\w*\s*\[|\brequire\s*\([^)]*\)\s*\[|\]\s*\(|[{,]\s*\[[^[\]]*\]\s*:|(?:^|[^.\w])(?:copy|move)\s*\(`)
	// inlineReachRe reaches the home directory, user database, key material or aliased os imports.
	inlineReachRe         = regexp.MustCompile(`(?i)\b(?:expanduser|expandvars|homedir|userinfo|getpass|keyring|keychain|pwd|decrypt|decipher|aesgcm|fernet|private_?key)\b|\bgetpw\w*|\bhome\s*\(|\bfrom\s+(?:os|posix|nt)\s+import\b|\bimport\s+(?:os|posix|nt)\s+as\b`)
	inlineDynamicStringRe = regexp.MustCompile(`\b(?:chr|ord|bytes|bytearray|memoryview|fromhex|b64decode|b64encode|unhexlify|hexlify|codecs|atob|btoa|fromCharCode|fromCodePoint|getattr|setattr|delattr|globals|locals|vars|__import__|__builtins__|__dict__|__subclasses__|__class__|__bases__|__mro__|unescape|decodeURI|decodeURIComponent|TextDecoder)\b|\bBuffer\s*\.\s*(?:from|alloc\w*)\b|\bString\s*\.\s*raw\s*\(|\\(?:x[0-9a-fA-F]{2}|u[0-9a-fA-F]{4}|u\{|U[0-9a-fA-F]{8}|N\{|[0-7]{1,3})`)
	inlineSQLiteMentionRe = regexp.MustCompile(`(?i)\bsqlite3?\b|\.\s*connect\s*\(|\bconnect\s*\(`)
	inlineSQLiteConnectRe = regexp.MustCompile(`(?:\.\s*|\b)connect\s*\(\s*`)
	inlineURIReadOnlyRe   = regexp.MustCompile(`^\s*,\s*uri\s*=\s*True\s*,?\s*$`)
	inlinePathlibRe       = regexp.MustCompile(`\bpathlib\b|\bPath\b`)
	inlineReplaceRe       = regexp.MustCompile(`\.\s*replace\s*\(`)
	inlineOSAttrRe        = regexp.MustCompile(`\bos\s*\.\s*([A-Za-z_]\w*)`)
	inlineKeyFileRe       = regexp.MustCompile(`(?i)(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|/)id_[a-z0-9_.-]+)$`)
	inlineLitKeyFileRe    = regexp.MustCompile(`(?i)(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|/)id_(?:rsa|dsa|ecdsa|ed25519)[a-z0-9_.-]*)$`)

	inlineOSReadAttrs = setOf("path", "sep", "linesep", "name", "getcwd", "stat", "lstat", "listdir", "scandir", "walk",
		"fspath", "fsencode", "fsdecode", "devnull", "curdir", "pardir", "extsep", "altsep", "pathsep", "cpu_count",
		"getpid", "getppid", "access", "readlink", "R_OK", "F_OK", "X_OK", "W_OK", "strerror", "get_terminal_size",
		"platform", "arch", "EOL", "cpus", "totalmem", "freemem", "hostname", "type", "release", "uptime", "endianness",
		"tmpdir", "loadavg", "version", "machine", "availableParallelism")
	inlineBlockedNodeModules = setOf("child_process", "http", "https", "http2", "net", "tls", "dgram", "dns", "cluster",
		"worker_threads", "vm", "inspector", "repl", "v8", "wasi", "module", "process", "async_hooks", "trace_events", "sqlite")
	inlineSafePyFlags   = setOf("-u", "-B", "-E", "-s", "-S", "-O", "-OO", "-q", "-I")
	inlineSafeNodeFlags = setOf("--no-warnings", "--input-type=module", "--input-type=commonjs")
	inlineCodeFlags     = setOf("-c", "-e", "--eval", "-p", "--print")
)

// inlineScriptsReadOnly reports whether every inline interpreter script in
// command is Python or Node code that only reads and prints.
func inlineScriptsReadOnly(command string) bool {
	ctx := &unattendedCtx{codeJudge: readOnlyScriptCategory}
	return unattendedScriptCategory(command, 0, ctx) == ""
}

// readOnlyScriptCategory judges one inline script; "" means it only reads.
func readOnlyScriptCategory(family, code string) string {
	if family != "python" && family != "python3" && family != "node" {
		return unattendedCategoryDynamic
	}
	isJS := family == "node"
	rawToks, _, rawLits, ok := lexInlineScript(family, code)
	if !ok {
		return unattendedCategoryDynamic
	}
	sqlCode := maskReadOnlySQL(code)
	_, codeMask, _, ok := lexInlineScript(family, sqlCode)
	if !ok {
		return unattendedCategoryDynamic
	}
	pathVars, regexVars := collectInlinePathVars(family, code, rawToks)
	masked := maskPrintIdioms(rewriteRegexExecCalls(isJS, regexVars, codeMask))
	if cat := scanUnattendedText(masked); cat != "" {
		return cat
	}
	if unattendedScriptCapabilityRe.MatchString(masked) || (isJS && inlineUnsafeProcessRe.MatchString(masked)) {
		return unattendedCategoryDynamic
	}
	filesOK, openModesOK, requiresOK := validateInlineCalls(rawToks, pathVars)
	if !requiresOK {
		return unattendedCategoryDynamic
	}
	if !openModesOK || inlineMutationRe.MatchString(masked) {
		return inlineCategoryWrites
	}
	if inlineSQLiteMentionRe.MatchString(masked) && !hasOnlyReadOnlySQLiteConnect(code) {
		return inlineCategoryWrites
	}
	if inlineReachRe.MatchString(masked) || inlineDynamicStringRe.MatchString(masked) || !filesOK {
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
	for _, lit := range rawLits {
		if isDisallowedInlineLiteral(lit) {
			return unattendedCategorySecret
		}
	}
	return ""
}

func maskPrintIdioms(codeMask string) string {
	codeMask = inlineStdWriteRe.ReplaceAllString(codeMask, "print")
	codeMask = inlineSafeProcessRe.ReplaceAllString(codeMask, "proc_ro")
	codeMask = inlineReCompileRe.ReplaceAllString(codeMask, "re.pattern(")
	return inlineMainGuardRe.ReplaceAllString(codeMask, "MAIN")
}

// maskReadOnlySQL rewrites `.execute("SELECT …")` and read-only PRAGMAs (with
// no multi-statement `;` or SQL write keywords) to `.query("sql")` so the
// capability list does not read them as exec.
func maskReadOnlySQL(code string) string {
	var b strings.Builder
	rest := code
	for {
		loc := inlineSQLExecRe.FindStringIndex(rest)
		if loc == nil {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:loc[0]])
		after := rest[loc[1]:]
		lit, n, isF, ok := leadingPyStringLiteral(after)
		decoded, decOK := decodeInlineEscapes(false, lit, false)
		if !ok || !decOK || strings.Contains(decoded, ";") || !inlineReadSQLRe.MatchString(decoded) ||
			inlineSQLWriteRe.MatchString(decoded) || (isF && !plainFStringFields(lit)) {
			b.WriteString(rest[loc[0]:loc[1]])
			rest = after
			continue
		}
		if isF {
			b.WriteString(`.query(f"` + extractFStringRefsOnly(lit) + `"`)
		} else {
			b.WriteString(`.query("sql"`)
		}
		rest = after[n:]
	}
}

func extractFStringRefsOnly(body string) string {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '{' && (i+1 >= len(body) || body[i+1] != '{') {
			end := strings.IndexByte(body[i:], '}')
			if end > 0 {
				b.WriteString(body[i : i+end+1])
				i += end
			}
		} else if body[i] == '{' || body[i] == '}' {
			i++
		}
	}
	return b.String()
}

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

func isImportBindingPos(toks []inlineTok, idx int) bool {
	for k := idx - 1; k >= 0; k-- {
		if toks[k].kind == "id" && toks[k].val == "import" {
			return true
		}
		if toks[k].kind == "id" || (toks[k].kind == "op" && toks[k].val == ",") {
			continue
		}
		return false
	}
	return false
}

func validateInlineCalls(toks []inlineTok, vars map[string][]string) (filesOK, openModesOK, requiresOK bool) {
	filesOK, openModesOK, requiresOK = true, true, true
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == "tpl" {
			for _, sub := range t.exprs {
				fOK, oOK, rOK := validateInlineCalls(sub, vars)
				if !fOK {
					filesOK = false
				}
				if !oOK {
					openModesOK = false
				}
				if !rOK {
					requiresOK = false
				}
			}
			continue
		}
		if t.kind != "id" {
			continue
		}
		isProp := i > 0 && toks[i-1].kind == "op" && toks[i-1].val == "."
		if t.val == "from" && !isProp && i+1 < len(toks) && toks[i+1].kind == "str" {
			mod := strings.TrimPrefix(toks[i+1].val, "node:")
			if inlineBlockedNodeModules[strings.SplitN(mod, "/", 2)[0]] {
				requiresOK = false
			}
		}
		if t.val == "require" && !isProp {
			if i+1 >= len(toks) || toks[i+1].kind != "op" || toks[i+1].val != "(" {
				requiresOK = false
				continue
			}
			closeIdx := matchingCloseParen(toks, i+1)
			if closeIdx >= len(toks) || closeIdx != i+3 || toks[i+2].kind != "str" {
				requiresOK = false
				continue
			}
			mod := strings.TrimPrefix(toks[i+2].val, "node:")
			if inlineBlockedNodeModules[strings.SplitN(mod, "/", 2)[0]] {
				requiresOK = false
			}
		}
		if t.val == "open" || t.val == "openSync" {
			if i+1 >= len(toks) || toks[i+1].kind != "op" || toks[i+1].val != "(" {
				openModesOK = false
				continue
			}
			closeIdx := matchingCloseParen(toks, i+1)
			if closeIdx >= len(toks) {
				openModesOK = false
				continue
			}
			args, ok := splitCallArgs(toks[i+2 : closeIdx])
			if !ok || !checkOpenCallArgs(toks, i, args) {
				openModesOK = false
			}
		}
		if inlineFileCalls[t.val] {
			if isImportBindingPos(toks, i) {
				continue
			}
			if i+1 >= len(toks) || toks[i+1].kind != "op" || toks[i+1].val != "(" {
				filesOK = false
				continue
			}
			if t.val == "open" && !isPathFirstOpenCall(toks, i) {
				continue
			}
			closeIdx := matchingCloseParen(toks, i+1)
			if closeIdx >= len(toks) {
				filesOK = false
				continue
			}
			args, ok := splitCallArgs(toks[i+2 : closeIdx])
			if !ok || !checkFileCallPaths(t.val, args, vars) {
				filesOK = false
			}
		}
	}
	return filesOK, openModesOK, requiresOK
}

func isPathFirstOpenCall(toks []inlineTok, idx int) bool {
	if idx == 0 || toks[idx-1].kind != "op" || toks[idx-1].val != "." || toks[idx].val == "openSync" {
		return true
	}
	if idx >= 2 && toks[idx-2].kind == "id" {
		switch toks[idx-2].val {
		case "io", "fs", "promises", "fsPromises":
			return true
		}
	}
	return false
}

func isKeywordArg(arg []inlineTok) (string, []inlineTok, bool) {
	if len(arg) >= 3 && arg[0].kind == "id" && arg[1].kind == "op" && arg[1].val == "=" {
		return arg[0].val, arg[2:], true
	}
	return "", nil, false
}

func checkOpenCallArgs(toks []inlineTok, idx int, args [][]inlineTok) bool {
	pathFirst := isPathFirstOpenCall(toks, idx)
	posIdx := 0
	for _, arg := range args {
		if kw, val, ok := isKeywordArg(arg); ok {
			if kw == "mode" || kw == "flags" {
				if len(val) != 1 || val[0].kind != "str" || !inlineReadModes[val[0].val] {
					return false
				}
				continue
			}
			if !inlineOpenKwSafe[kw] {
				return false
			}
			if len(val) == 2 && kw == "buffering" && val[0].kind == "op" && val[0].val == "-" && val[1].kind == "num" {
				continue
			}
			if len(val) != 1 || (val[0].kind != "str" && val[0].kind != "num" && !(val[0].kind == "id" && val[0].val == "None")) {
				return false
			}
			continue
		}
		if pathFirst && posIdx == 0 {
			posIdx++
			continue
		}
		modePos := 0
		if pathFirst {
			modePos = 1
		}
		if posIdx == modePos {
			if len(arg) != 1 || arg[0].kind != "str" || !inlineReadModes[arg[0].val] {
				return false
			}
			posIdx++
			continue
		}
		return false
	}
	return !pathFirst || posIdx >= 1
}

func checkFileCallPaths(fnName string, args [][]inlineTok, vars map[string][]string) bool {
	var posArgs [][]inlineTok
	for _, arg := range args {
		if _, _, ok := isKeywordArg(arg); !ok {
			posArgs = append(posArgs, arg)
		}
	}
	if len(posArgs) == 0 {
		return inlineZeroArgPathCalls[fnName]
	}
	if (fnName == "readFileSync" || fnName == "readFile") && len(posArgs[0]) == 1 && posArgs[0][0].kind == "num" && posArgs[0][0].val == "0" {
		return true
	}
	multiSeg := fnName == "Path" || fnName == "PurePath" || fnName == "PosixPath" || fnName == "WindowsPath" || fnName == "joinpath"
	if !multiSeg {
		posArgs = posArgs[:1]
	}
	var joined []string
	for idx, arg := range posArgs {
		vals, ok := resolveInlinePathTokens(arg, vars, 0)
		if !ok || len(vals) == 0 {
			return false
		}
		for _, v := range vals {
			if isDisallowedFileCallTarget(v) {
				return false
			}
		}
		if idx == 0 {
			joined = append([]string(nil), vals...)
		} else {
			joined, ok = cartesianConcat(joined, vals, true)
			if !ok {
				return false
			}
		}
	}
	if multiSeg && len(posArgs) > 1 {
		for _, v := range joined {
			if isDisallowedFileCallTarget(v) {
				return false
			}
		}
	}
	return true
}

func isDisallowedFileCallTarget(raw string) bool {
	if strings.ContainsRune(raw, 0) {
		return true
	}
	p := strings.TrimSpace(raw)
	if p == "" {
		return true
	}
	for _, cand := range []string{p, cleanInlinePosixPath(p)} {
		if cand == "" {
			continue
		}
		if isSecretFilePath(cand) || isSensitiveExternalReadPath(cand) || isProtectedShellReadPath(cand) ||
			inlineKeyFileRe.MatchString(cand) || unattendedSecretRe.MatchString(cand) {
			return true
		}
	}
	return false
}

func isDisallowedInlineLiteral(raw string) bool {
	p := strings.TrimSpace(raw)
	if p == "" {
		return false
	}
	for _, cand := range []string{p, cleanInlinePosixPath(p)} {
		if cand == "" {
			continue
		}
		if isSecretFilePath(cand) || isSensitiveExternalReadPath(cand) || isProtectedShellReadPath(cand) ||
			inlineLitKeyFileRe.MatchString(cand) || unattendedSecretRe.MatchString(cand) {
			return true
		}
	}
	return false
}

func hasOnlyReadOnlySQLiteConnect(code string) bool {
	locs := inlineSQLiteConnectRe.FindAllStringIndex(code, -1)
	if len(locs) == 0 {
		return false
	}
	for _, loc := range locs {
		after := code[loc[1]:]
		rawLit, n, isF, ok := leadingPyStringLiteral(after)
		lit, decOK := decodeInlineEscapes(false, rawLit, false)
		if !ok || !decOK || isF {
			return false
		}
		rest := after[n:]
		closeIdx := strings.IndexByte(rest, ')')
		if closeIdx < 0 {
			return false
		}
		extraArgs := rest[:closeIdx]
		if strings.ContainsAny(extraArgs, "(\"'") {
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
		if isDisallowedFileCallTarget(dbPath) {
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

// maskReadOnlyInlineScripts replaces the literal `-c`/`-e` code argument of a
// top-level Python or Node segment with Q when readOnlyScriptCategory proves
// the script only reads and prints, keeping other segments untouched.
func maskReadOnlyInlineScripts(s string) string {
	return mapTopLevelSegments(s, rewriteReadOnlyInlineSegment)
}

func rewriteReadOnlyInlineSegment(seg string) string {
	spans := scanShellWordSpans(seg)
	if len(spans) < 3 {
		return seg
	}
	idx := 0
	for idx < len(spans) && shellKeywords[spans[idx].word] {
		idx++
	}
	for idx < len(spans) && envVarAssignmentWordRe.MatchString(spans[idx].word) && !strings.ContainsAny(seg[spans[idx].start:spans[idx].end], "$`<>") {
		idx++
	}
	if idx < len(spans) {
		head := filepath.Base(normalizeAbsoluteTool(spans[idx].word))
		if head == "nohup" || head == "time" || head == "command" {
			idx++
			for idx < len(spans) && strings.HasPrefix(spans[idx].word, "-") {
				if head == "command" && (spans[idx].word == "-v" || spans[idx].word == "-V") {
					return seg
				}
				if spans[idx].word == "--" {
					idx++
					break
				}
				idx++
			}
		}
	}
	if idx >= len(spans) {
		return seg
	}
	family, ok := inlineScriptTool(filepath.Base(normalizeAbsoluteTool(spans[idx].word)))
	if !ok || (family != "python" && family != "python3" && family != "node") {
		return seg
	}
	idx++
	for idx < len(spans) {
		w := spans[idx].word
		if inlineCodeFlags[w] {
			break
		}
		if (family == "node" && inlineSafeNodeFlags[w]) || (family != "node" && inlineSafePyFlags[w]) {
			idx++
			continue
		}
		return seg
	}
	if idx+1 >= len(spans) {
		return seg
	}
	codeSpan := spans[idx+1]
	for k := idx + 2; k < len(spans); k++ {
		w := spans[k].word
		if devNullWordRe.MatchString(w) || dupStreamWordRe.MatchString(w) {
			continue
		}
		if redirectWordRe.MatchString(w) && k+1 < len(spans) && spans[k+1].word == "/dev/null" {
			k++
			continue
		}
		return seg
	}
	code, ok := unquoteStrictInlineCodeArg(seg[codeSpan.start:codeSpan.end])
	if !ok || readOnlyScriptCategory(family, code) != "" {
		return seg
	}
	return seg[:codeSpan.start] + maskedDataArgument + seg[codeSpan.end:]
}

func unquoteStrictInlineCodeArg(raw string) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	if raw[0] == '"' && raw[len(raw)-1] == '"' {
		inner := raw[1 : len(raw)-1]
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				i++
				continue
			}
			if inner[i] == '"' || inner[i] == '`' {
				return "", false
			}
			if inner[i] == '$' && i+1 < len(inner) && isDoubleQuoteDollarExpansion(inner[i+1]) {
				return "", false
			}
		}
	}
	return unquoteLiteralShellScriptArg(raw)
}
