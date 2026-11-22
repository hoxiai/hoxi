package agentcli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const maxWindowsShimBytes = 16 * 1024

var (
	windowsSafeNodeFlagRe = regexp.MustCompile(`^--?[A-Za-z0-9][A-Za-z0-9_=-]*$`)
	windowsUnsafeNodeFlag = map[string]bool{
		"-e": true, "--eval": true,
		"-p": true, "--print": true,
		"-c": true, "--check": true,
		"-r": true, "--require": true,
		"--import": true, "--loader": true, "--experimental-loader": true,
	}
	windowsShimAllowedSetVars = map[string]bool{
		"dp0":     true,
		"_prog":   true,
		"pathext": true,
	}
)

// ResolveWindowsNodeShim unwraps standard npm/pnpm/yarn/corepack `.cmd`/`.bat`
// Node shims into a direct `node.exe [flags...] <script.js>` invocation.
//
// Running `.cmd` wrappers through `cmd.exe` mangles double-quoted arguments
// such as Claude Code `--settings <json>` or Codex `-c <toml>`. Only standard
// Node shims that set no custom environment variables are unwrapped; any custom
// batch wrapper fails closed (returns ok=false) and runs as-is.
func ResolveWindowsNodeShim(binPath string, env []string) (execPath string, prefixArgs []string, ok bool) {
	return resolveWindowsNodeShimWith(binPath, env, filepath.ListSeparator, isRegularFile, exec.LookPath)
}

func resolveWindowsNodeShimWith(
	binPath string,
	env []string,
	listSep rune,
	fileExists func(string) bool,
	lookPath func(string) (string, error),
) (string, []string, bool) {
	trimmed := strings.TrimSpace(binPath)
	ext := strings.ToLower(filepath.Ext(trimmed))
	if ext != ".cmd" && ext != ".bat" {
		if resolved, found := resolveBareWindowsShimPath(trimmed, env, listSep, fileExists, lookPath); found {
			trimmed = resolved
		} else {
			return "", nil, false
		}
	}
	f, err := os.Open(trimmed)
	if err != nil {
		return "", nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxWindowsShimBytes+1))
	_ = f.Close()
	if err != nil || len(data) == 0 || len(data) > maxWindowsShimBytes {
		return "", nil, false
	}

	shimDir := filepath.Dir(trimmed)
	flags, relScript, ok := parseWindowsNodeShim(string(data))
	if !ok {
		return "", nil, false
	}
	scriptPath := joinWindowsShimRelPath(shimDir, relScript)
	if scriptPath == "" || !fileExists(scriptPath) || !isRunnableNodeShimScript(scriptPath) {
		return "", nil, false
	}
	nodeExe, ok := resolveWindowsNodeBinary(shimDir, env, listSep, fileExists, lookPath)
	if !ok {
		return "", nil, false
	}
	prefix := make([]string, 0, len(flags)+1)
	prefix = append(prefix, flags...)
	prefix = append(prefix, scriptPath)
	return nodeExe, prefix, true
}

func parseWindowsNodeShim(content string) (flags []string, relScript string, ok bool) {
	if strings.ContainsRune(content, '\x00') {
		return nil, "", false
	}
	var matchedFlags []string
	var matchedScript string
	found := false

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, "@ \t")
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "rem ") || strings.HasPrefix(lower, "::") {
			continue
		}
		// Fail closed if the wrapper mutates any custom env var (e.g. NODE_PATH,
		// NODE_OPTIONS, or app-specific config) so we never drop shim behavior.
		if strings.HasPrefix(lower, "set ") || strings.HasPrefix(lower, "set\t") {
			rest := strings.TrimSpace(trimmed[3:])
			rest = strings.TrimPrefix(rest, `"`)
			if eq := strings.IndexByte(rest, '='); eq > 0 {
				varName := strings.ToLower(strings.TrimSpace(rest[:eq]))
				if !windowsShimAllowedSetVars[varName] {
					return nil, "", false
				}
			}
			continue
		}

		candidate := extractShimInvocationCandidate(trimmed)
		if candidate == "" {
			continue
		}
		candFlags, candScript, candOK := parseShimNodeInvocation(candidate)
		if !candOK {
			return nil, "", false
		}
		if !found {
			matchedFlags = candFlags
			matchedScript = candScript
			found = true
			continue
		}
		if matchedScript != candScript || !equalStringSlices(matchedFlags, candFlags) {
			return nil, "", false
		}
	}
	if !found {
		return nil, "", false
	}
	return matchedFlags, matchedScript, true
}

func extractShimInvocationCandidate(line string) string {
	trimmed := strings.TrimSpace(strings.TrimLeft(line, "@ \t"))
	if !strings.HasSuffix(trimmed, "%*") {
		return ""
	}
	if idx := strings.LastIndexByte(trimmed, '&'); idx >= 0 {
		prefixLower := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
		// Standard npm cmd-shim tail:
		// `endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%" ... %*`
		if !strings.Contains(prefixLower, "endlocal") {
			return ""
		}
		trimmed = strings.TrimSpace(trimmed[idx+1:])
	}
	return trimmed
}

func parseShimNodeInvocation(cmdLine string) (flags []string, relScript string, ok bool) {
	tokens, ok := splitBatchCommandTokens(cmdLine)
	if !ok || len(tokens) < 3 || tokens[len(tokens)-1] != "%*" {
		return nil, "", false
	}
	tokens = tokens[:len(tokens)-1]
	if !isShimNodeProgToken(tokens[0]) {
		return nil, "", false
	}

	scriptToken := tokens[len(tokens)-1]
	rel, ok := extractShimRelativeScript(scriptToken)
	if !ok {
		return nil, "", false
	}

	rawFlags := tokens[1 : len(tokens)-1]
	flags = make([]string, 0, len(rawFlags))
	for _, flag := range rawFlags {
		if !isSafeShimNodeFlag(flag) {
			return nil, "", false
		}
		flags = append(flags, flag)
	}
	return flags, rel, true
}

func splitBatchCommandTokens(line string) ([]string, bool) {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	hasToken := false

	for i := 0; i < len(line); i++ {
		c := line[i]
		if c < 0x20 && c != '\t' {
			return nil, false
		}
		if inQuote {
			if c == '"' {
				inQuote = false
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '"':
			inQuote = true
			hasToken = true
		case ' ', '\t':
			if hasToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				hasToken = false
			}
		case '|', '<', '>', '&', '^', '!', '(', ')':
			return nil, false
		default:
			cur.WriteByte(c)
			hasToken = true
		}
	}
	if inQuote {
		return nil, false
	}
	if hasToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, true
}

func isShimNodeProgToken(tok string) bool {
	lower := strings.ToLower(strings.TrimSpace(tok))
	switch lower {
	case "%_prog%", "node", "node.exe",
		`%dp0%\node.exe`, `%dp0%/node.exe`, `%dp0%node.exe`,
		`%~dp0\node.exe`, `%~dp0/node.exe`, `%~dp0node.exe`:
		return true
	default:
		return false
	}
}

func isSafeShimNodeFlag(flag string) bool {
	if !windowsSafeNodeFlagRe.MatchString(flag) {
		return false
	}
	name, _, _ := strings.Cut(strings.ToLower(flag), "=")
	return !windowsUnsafeNodeFlag[name]
}

func extractShimRelativeScript(tok string) (string, bool) {
	lower := strings.ToLower(tok)
	var rest string
	switch {
	case strings.HasPrefix(lower, `%dp0%\`), strings.HasPrefix(lower, `%dp0%/`):
		rest = tok[len(`%dp0%\`):]
	case strings.HasPrefix(lower, `%dp0%`):
		rest = tok[len(`%dp0%`):]
	case strings.HasPrefix(lower, `%~dp0\`), strings.HasPrefix(lower, `%~dp0/`):
		rest = tok[len(`%~dp0\`):]
	case strings.HasPrefix(lower, `%~dp0`):
		rest = tok[len(`%~dp0`):]
	default:
		return "", false
	}
	if rest == "" || strings.ContainsAny(rest, "%\x00\r\n") {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(strings.ReplaceAll(rest, `\`, `/`)))
	if ext != "" && ext != ".js" && ext != ".mjs" && ext != ".cjs" {
		return "", false
	}
	return rest, true
}

func resolveBareWindowsShimPath(
	raw string,
	env []string,
	listSep rune,
	fileExists func(string) bool,
	lookPath func(string) (string, error),
) (string, bool) {
	if raw == "" || strings.ContainsAny(raw, `/\`) || filepath.Ext(raw) != "" {
		return "", false
	}
	if pathVal, ok := lookupEnvCaseInsensitive(env, "PATH"); ok {
		for _, dir := range strings.Split(pathVal, string(listSep)) {
			dir = strings.Trim(strings.TrimSpace(dir), `"`)
			if dir == "" {
				continue
			}
			for _, suffix := range []string{".cmd", ".bat"} {
				cand := filepath.Join(dir, raw+suffix)
				if fileExists(cand) {
					return cand, true
				}
			}
		}
	}
	if lookPath != nil {
		for _, suffix := range []string{".cmd", ".bat"} {
			if found, err := lookPath(raw + suffix); err == nil && found != "" {
				return found, true
			}
		}
	}
	return "", false
}

// isRunnableNodeShimScript accepts explicit JS extensions (.js/.mjs/.cjs) and
// extensionless npm bin scripts whose shebang explicitly invokes node.
func isRunnableNodeShimScript(scriptPath string) bool {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	if ext == ".js" || ext == ".mjs" || ext == ".cjs" {
		return true
	}
	if ext != "" {
		return false
	}
	f, err := os.Open(scriptPath)
	if err != nil {
		return false
	}
	buf := make([]byte, 128)
	n, _ := f.Read(buf)
	_ = f.Close()
	if n < 2 || buf[0] != '#' || buf[1] != '!' {
		return false
	}
	firstLine, _, _ := strings.Cut(string(buf[:n]), "\n")
	lower := strings.ToLower(strings.TrimSpace(firstLine))
	return strings.Contains(lower, "node")
}

func joinWindowsShimRelPath(shimDir, relScript string) string {
	slash := strings.ReplaceAll(relScript, `\`, `/`)
	slash = strings.TrimLeft(slash, "/")
	if slash == "" {
		return ""
	}
	return filepath.Join(shimDir, filepath.FromSlash(slash))
}

func resolveWindowsNodeBinary(
	shimDir string,
	env []string,
	listSep rune,
	fileExists func(string) bool,
	lookPath func(string) (string, error),
) (string, bool) {
	localNode := filepath.Join(shimDir, "node.exe")
	if fileExists(localNode) {
		return localNode, true
	}
	if pathVal, ok := lookupEnvCaseInsensitive(env, "PATH"); ok {
		for _, dir := range strings.Split(pathVal, string(listSep)) {
			dir = strings.Trim(strings.TrimSpace(dir), `"`)
			if dir == "" {
				continue
			}
			cand := filepath.Join(dir, "node.exe")
			if fileExists(cand) {
				return cand, true
			}
		}
	}
	for _, name := range []string{"node.exe", "node"} {
		if found, err := lookPath(name); err == nil && found != "" {
			ext := strings.ToLower(filepath.Ext(found))
			if ext != ".cmd" && ext != ".bat" {
				return found, true
			}
		}
	}
	return "", false
}

func lookupEnvCaseInsensitive(env []string, target string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && strings.EqualFold(k, target) {
			return v, true
		}
	}
	return "", false
}

func isRegularFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
