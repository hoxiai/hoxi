package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxTrackedTSCCacheSessions = 128
	diagToolCooldownDuration   = 60 * time.Second
	maxNativeJSONDiagBytes     = 8 << 20
)

var (
	tscCacheMu       sync.Mutex
	tscCacheFiles    = make(map[string][]string)
	tscCacheOrder    []string
	diagCooldownMu   sync.Mutex
	diagToolCooldown = make(map[string]time.Time)
)

func isDiagToolCoolingDown(sessionID, extKey string) bool {
	if sessionID == "" || extKey == "" {
		return false
	}
	key := sessionID + "\x00" + extKey
	diagCooldownMu.Lock()
	defer diagCooldownMu.Unlock()
	until, ok := diagToolCooldown[key]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(diagToolCooldown, key)
		return false
	}
	return true
}

func noteDiagToolBudgetExceeded(sessionID, extKey string) {
	if sessionID == "" || extKey == "" {
		return
	}
	diagCooldownMu.Lock()
	if len(diagToolCooldown) >= 256 {
		clear(diagToolCooldown)
	}
	diagToolCooldown[sessionID+"\x00"+extKey] = time.Now().Add(diagToolCooldownDuration)
	diagCooldownMu.Unlock()
}

// RunNativeDiagnostics runs Hoxi's post-edit static diagnostic checks against
// filePath within worktreeRoot using the same checker logic as native sessions.
func (s *Service) RunNativeDiagnostics(ctx context.Context, sessionID, worktreeRoot, filePath string) string {
	rec := SessionRecord{
		ID:           strings.TrimSpace(sessionID),
		Runtime:      string(RuntimeNative),
		ProjectDir:   worktreeRoot,
		WorktreePath: worktreeRoot,
		SandboxMode:  "off",
	}
	return s.nativeDiagnostics(rec)(ctx, worktreeRoot, filePath)
}

// nativeDiagnostics runs fast post-edit/write static checks (go vet for Go
// packages, tsc --noEmit for TypeScript, node --check/oxlint for JavaScript,
// pure-Go syntax check for JSON/JSONC, vue-tsc/oxlint for Vue SFCs, cargo
// check for Rust, php -l for PHP, ruff/ast for Python, and dart analyze for
// Flutter/Dart) under the same Seatbelt confinement and sanitized environment
// as the session itself, cooling down any checker that exceeds its budget.
func (s *Service) nativeDiagnostics(rec SessionRecord) func(ctx context.Context, dir, filePath string) string {
	return func(ctx context.Context, dir, filePath string) string {
		worktreeRoot := strings.TrimSpace(dir)
		if worktreeRoot == "" {
			worktreeRoot = rec.WorktreePath
		}
		if worktreeRoot == "" || strings.TrimSpace(filePath) == "" || !pathWithin(worktreeRoot, filePath) {
			return ""
		}
		if st, err := os.Stat(filePath); err != nil || st.IsDir() {
			return ""
		}

		ext := strings.ToLower(filepath.Ext(filePath))
		if isDiagToolCoolingDown(rec.ID, ext) {
			return "Not run: checker cooling down after a timeout."
		}

		diagCtx, cancel := context.WithTimeout(ctx, nativeDiagTimeout)
		defer cancel()

		var res, scope string
		switch ext {
		case ".go":
			res, scope = s.runNativeGoVetDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".ts", ".tsx", ".mts", ".cts":
			res, scope = s.runNativeTSCDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".js", ".mjs", ".cjs":
			res = s.runNativeJSDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".jsx":
			res = s.runNativeJSXDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".json":
			res = runNativeJSONDiagnostic(filePath)
		case ".vue":
			res, scope = s.runNativeVueDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".rs":
			res = s.runNativeRustDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".php":
			res = s.runNativePHPDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".py":
			res = s.runNativePythonDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".dart":
			res = s.runNativeDartDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		default:
			return ""
		}
		if scope != "" {
			scope = " [" + scope + "]"
		}
		if diagCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			noteDiagToolBudgetExceeded(rec.ID, ext)
			return "Timed out: static check did not finish." + scope
		}
		if ctx.Err() != nil {
			return "Not completed: static check cancelled." + scope
		}
		if res == "" {
			return "Not run: checker or project configuration unavailable."
		}
		return res
	}
}

func diagnosticRelativePath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// Return the scope separately so timeouts and cancellation can identify the checker too.
func scopedDiagnostic(label, priorityFile, out string, code int, err error) (string, string) {
	if err == nil && code == 0 {
		return "Passed: " + label, label
	}
	detail := formatDiagnosticLines(out, priorityFile, nativeDiagMaxLines, nativeDiagMaxOutputBytes)
	if detail == "" {
		detail = diagnosticExitStatus(code, err)
	}
	return "[" + label + "]\n" + detail, label
}

func (s *Service) runNativeGoVetDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) (string, string) {
	modRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "go.mod")
	if modRoot == "" {
		return "", ""
	}
	goBin := resolveNativeToolBinary(rec, "go")
	if goBin == "" {
		return "", ""
	}
	relPkg, err := filepath.Rel(modRoot, filepath.Dir(filePath))
	if err != nil || strings.HasPrefix(relPkg, "..") {
		return "", ""
	}
	pkgTarget := "."
	if relPkg != "." && relPkg != "" {
		pkgTarget = "./" + filepath.ToSlash(relPkg)
	}
	out, code, err := s.execNativeConfined(ctx, rec, nil, modRoot, goBin, []string{"vet", pkgTarget}, "", nativeDiagMaxOutputBytes*2)
	label := "go vet " + pkgTarget
	if module := diagnosticRelativePath(worktreeRoot, filepath.Join(modRoot, "go.mod")); module != "go.mod" {
		label += " (module " + module + ")"
	}
	return scopedDiagnostic(label, "", out, code, err)
}

func (s *Service) runNativeTSCDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) (string, string) {
	tsRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "tsconfig.json")
	if tsRoot == "" {
		return "", ""
	}
	tscBin := resolveWorkspaceTSC(rec, tsRoot, worktreeRoot)
	if tscBin == "" {
		return "", ""
	}
	tsconfigPath := filepath.Join(tsRoot, "tsconfig.json")
	baseArgs := []string{"--project", tsconfigPath, "--noEmit", "--pretty", "false"}
	args := baseArgs
	if buildInfoPath := nativeTSCBuildInfoPath(rec.ID, tsconfigPath); buildInfoPath != "" {
		args = []string{"--project", tsconfigPath, "--noEmit", "--incremental", "--tsBuildInfoFile", buildInfoPath, "--pretty", "false"}
	}
	out, code, err := s.execNativeConfined(ctx, rec, nil, tsRoot, tscBin, args, "", nativeDiagMaxOutputBytes*4)
	if (err != nil || code != 0) && len(args) > len(baseArgs) && isUnsupportedTSCIncrementalError(out) && ctx.Err() == nil {
		out, code, err = s.execNativeConfined(ctx, rec, nil, tsRoot, tscBin, baseArgs, "", nativeDiagMaxOutputBytes*4)
	}
	return scopedDiagnostic("tsc --noEmit --project "+diagnosticRelativePath(worktreeRoot, tsconfigPath),
		diagnosticRelativePath(tsRoot, filePath), out, code, err)
}

func nativeTSCBuildInfoPath(sessionID, tsconfigPath string) string {
	tmpBase := strings.TrimSpace(os.TempDir())
	if tmpBase == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(tmpBase); err == nil && resolved != "" {
		tmpBase = resolved
	}
	cacheDir := filepath.Join(tmpBase, "hoxi-tsc-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return ""
	}
	sid := strings.TrimSpace(sessionID)
	sum := sha256.Sum256([]byte(sid + "\x00" + filepath.Clean(tsconfigPath)))
	filePath := filepath.Join(cacheDir, hex.EncodeToString(sum[:12])+".tsbuildinfo")
	recordSessionTSCCacheFile(sid, filePath)
	return filePath
}

func recordSessionTSCCacheFile(sessionID, filePath string) {
	if sessionID == "" || filePath == "" {
		return
	}
	tscCacheMu.Lock()
	var evictedPaths []string
	if _, exists := tscCacheFiles[sessionID]; !exists {
		for len(tscCacheOrder) >= maxTrackedTSCCacheSessions {
			oldest := tscCacheOrder[0]
			tscCacheOrder = tscCacheOrder[1:]
			evictedPaths = append(evictedPaths, tscCacheFiles[oldest]...)
			delete(tscCacheFiles, oldest)
		}
		tscCacheOrder = append(tscCacheOrder, sessionID)
	}
	list := tscCacheFiles[sessionID]
	already := false
	for _, p := range list {
		if p == filePath {
			already = true
			break
		}
	}
	if !already && len(list) < 16 {
		tscCacheFiles[sessionID] = append(list, filePath)
	}
	tscCacheMu.Unlock()
	for _, p := range evictedPaths {
		_ = os.Remove(p)
	}
}

func cleanupSessionTSCCache(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	tscCacheMu.Lock()
	paths := tscCacheFiles[sid]
	delete(tscCacheFiles, sid)
	for i, k := range tscCacheOrder {
		if k == sid {
			tscCacheOrder = append(tscCacheOrder[:i], tscCacheOrder[i+1:]...)
			break
		}
	}
	tscCacheMu.Unlock()
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

func isUnsupportedTSCIncrementalError(out string) bool {
	return strings.Contains(out, "TS5052") || strings.Contains(out, "TS5053") || strings.Contains(out, "TS5069") || strings.Contains(out, "TS5074")
}

func (s *Service) runNativeJSDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	pkgRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "package.json")
	if pkgRoot == "" {
		pkgRoot = worktreeRoot
	}
	if nodeBin := resolveNativeToolBinary(rec, "node"); nodeBin != "" {
		out, code, err := s.execNativeConfined(ctx, rec, nil, pkgRoot, nodeBin, []string{"--check", filePath}, "", nativeDiagMaxOutputBytes*2)
		if ctx.Err() != nil {
			return ""
		}
		if err == nil && code == 0 {
			return "Passed: node --check."
		}
		if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
			return "[node --check]\n" + formatted
		}
		return diagnosticExitStatus(code, err)
	}
	return s.runNativeJSXDiagnostic(ctx, rec, worktreeRoot, filePath)
}

func (s *Service) runNativeJSXDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	pkgRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "package.json")
	if pkgRoot == "" {
		pkgRoot = worktreeRoot
	}
	if oxlintBin := resolveWorkspaceNodeBin(rec, pkgRoot, worktreeRoot, "oxlint"); oxlintBin != "" {
		out, code, err := s.execNativeConfined(ctx, rec, nil, pkgRoot, oxlintBin, []string{"--quiet", filePath}, "", nativeDiagMaxOutputBytes*2)
		if ctx.Err() != nil {
			return ""
		}
		if err == nil && code == 0 {
			return "Passed: oxlint."
		}
		if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
			return "[oxlint]\n" + formatted
		}
		return diagnosticExitStatus(code, err)
	}
	return ""
}

func isJSONCConfigFile(filePath string) bool {
	base := strings.ToLower(filepath.Base(filePath))
	parent := strings.ToLower(filepath.Base(filepath.Dir(filePath)))
	if base == "hoxi.json" || base == "opencode.json" || parent == ".vscode" {
		return true
	}
	if strings.HasSuffix(base, ".json") && (strings.HasPrefix(base, "tsconfig") || strings.HasPrefix(base, "jsconfig")) {
		return true
	}
	return false
}

// normalizeJSONC strips // and /* */ comments and trailing commas outside
// string literals while preserving newlines and exact 1:1 byte offsets.
func normalizeJSONC(src []byte) ([]byte, int, bool) {
	out := make([]byte, len(src))
	inString := false
	escaped := false
	for i := 0; i < len(src); i++ {
		b := src[i]
		if inString {
			out[i] = b
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			out[i] = b
			continue
		}
		if b == '/' && i+1 < len(src) && src[i+1] == '/' {
			out[i] = ' '
			out[i+1] = ' '
			i += 2
			for i < len(src) && src[i] != '\n' {
				if src[i] == '\r' {
					out[i] = '\r'
				} else {
					out[i] = ' '
				}
				i++
			}
			if i < len(src) {
				out[i] = '\n'
			}
			continue
		}
		if b == '/' && i+1 < len(src) && src[i+1] == '*' {
			start := i
			out[i] = ' '
			out[i+1] = ' '
			i += 2
			closed := false
			for i < len(src) {
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					out[i] = ' '
					out[i+1] = ' '
					i++
					closed = true
					break
				}
				if src[i] == '\n' || src[i] == '\r' {
					out[i] = src[i]
				} else {
					out[i] = ' '
				}
				i++
			}
			if !closed {
				return nil, start, false
			}
			continue
		}
		out[i] = b
	}

	// Second pass: clear trailing commas before } or ] outside strings.
	inString = false
	escaped = false
	for i := 0; i < len(out); i++ {
		b := out[i]
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		if b == '"' {
			inString = true
			continue
		}
		if b == ',' {
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\t' || out[j] == '\r' || out[j] == '\n') {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				out[i] = ' '
			}
		}
	}
	return out, 0, true
}

func offsetToLineCol(src []byte, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	line := 1
	col := 1
	for i := 0; i < offset; {
		r, size := utf8.DecodeRune(src[i:offset])
		if r == '\n' {
			line++
			col = 1
		} else if r != '\r' {
			col++
		}
		i += size
	}
	return line, col
}

func runNativeJSONDiagnostic(filePath string) string {
	st, err := os.Stat(filePath)
	if err != nil || st.IsDir() {
		return ""
	}
	if st.Size() > maxNativeJSONDiagBytes {
		return "Not run: JSON file exceeds diagnostic size limit."
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Sprintf("Check failed: %v", err)
	}
	data := bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	isJSONC := isJSONCConfigFile(filePath)
	checkBuf := data
	if isJSONC {
		norm, badPos, ok := normalizeJSONC(data)
		if !ok {
			line, col := offsetToLineCol(data, badPos)
			return fmt.Sprintf("[jsonc]\n%s:%d:%d: unterminated block comment", filepath.Base(filePath), line, col)
		}
		checkBuf = norm
	}
	if json.Valid(checkBuf) {
		if isJSONC {
			return "Passed: jsonc syntax."
		}
		return "Passed: json syntax."
	}
	var parsed any
	uErr := json.Unmarshal(checkBuf, &parsed)
	tag := "json"
	if isJSONC {
		tag = "jsonc"
	}
	var synErr *json.SyntaxError
	if errors.As(uErr, &synErr) {
		pos := int(synErr.Offset)
		if pos > 0 {
			pos--
		}
		line, col := offsetToLineCol(data, pos)
		return fmt.Sprintf("[%s]\n%s:%d:%d: %s", tag, filepath.Base(filePath), line, col, synErr.Error())
	}
	if uErr != nil {
		return fmt.Sprintf("[%s]\n%s: %v", tag, filepath.Base(filePath), uErr)
	}
	return fmt.Sprintf("[%s]\n%s: invalid JSON syntax", tag, filepath.Base(filePath))
}

func (s *Service) runNativeVueDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) (string, string) {
	tsRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "tsconfig.json")
	if tsRoot != "" {
		if vueTscBin := resolveWorkspaceNodeBin(rec, tsRoot, worktreeRoot, "vue-tsc"); vueTscBin != "" {
			tsconfigPath := filepath.Join(tsRoot, "tsconfig.json")
			args := []string{"--project", tsconfigPath, "--noEmit", "--pretty", "false"}
			out, code, err := s.execNativeConfined(ctx, rec, nil, tsRoot, vueTscBin, args, "", nativeDiagMaxOutputBytes*4)
			return scopedDiagnostic("vue-tsc --noEmit --project "+diagnosticRelativePath(worktreeRoot, tsconfigPath),
				diagnosticRelativePath(tsRoot, filePath), out, code, err)
		}
	}
	pkgRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "package.json")
	if pkgRoot == "" {
		pkgRoot = worktreeRoot
	}
	if oxlintBin := resolveWorkspaceNodeBin(rec, pkgRoot, worktreeRoot, "oxlint"); oxlintBin != "" {
		out, code, err := s.execNativeConfined(ctx, rec, nil, pkgRoot, oxlintBin, []string{"--quiet", filePath}, "", nativeDiagMaxOutputBytes*2)
		if ctx.Err() != nil {
			return "", ""
		}
		if err == nil && code == 0 {
			return "Passed: oxlint.", ""
		}
		if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
			return "[oxlint]\n" + formatted, ""
		}
		return diagnosticExitStatus(code, err), ""
	}
	return "", ""
}

func (s *Service) runNativeRustDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	cargoRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "Cargo.toml")
	if cargoRoot == "" {
		return ""
	}
	cargoBin := resolveNativeToolBinary(rec, "cargo")
	if cargoBin == "" {
		return ""
	}
	out, code, err := s.execNativeConfined(
		ctx,
		rec,
		nil,
		cargoRoot,
		cargoBin,
		[]string{"check", "--offline", "--quiet", "--message-format=short"},
		"",
		nativeDiagMaxOutputBytes*4,
	)
	if ctx.Err() != nil {
		return ""
	}
	if err == nil && code == 0 {
		return "Passed: cargo check --offline."
	}
	relFile := filepath.Base(filePath)
	if rel, relErr := filepath.Rel(cargoRoot, filePath); relErr == nil && !strings.HasPrefix(rel, "..") {
		relFile = filepath.ToSlash(rel)
	}
	if strings.Contains(out, "--offline") && !strings.Contains(out, relFile) {
		return "Not completed: cargo dependencies unavailable offline."
	}
	if formatted := formatDiagnosticLines(out, relFile, nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[cargo check]\n" + formatted
	}
	return diagnosticExitStatus(code, err)
}

func (s *Service) runNativePHPDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	phpBin := resolveNativeToolBinary(rec, "php")
	if phpBin == "" {
		return ""
	}
	out, code, err := s.execNativeConfined(ctx, rec, nil, worktreeRoot, phpBin, []string{"-l", filePath}, "", nativeDiagMaxOutputBytes*2)
	if ctx.Err() != nil {
		return ""
	}
	if err == nil && code == 0 {
		return "Passed: php -l."
	}
	if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[php -l]\n" + formatted
	}
	return diagnosticExitStatus(code, err)
}

func (s *Service) runNativePythonDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	if ruffBin := resolveWorkspacePythonBin(rec, filepath.Dir(filePath), worktreeRoot, "ruff"); ruffBin != "" {
		out, code, err := s.execNativeConfined(ctx, rec, nil, worktreeRoot, ruffBin, []string{"check", "--output-format=concise", "--no-cache", filePath}, "", nativeDiagMaxOutputBytes*2)
		if ctx.Err() != nil {
			return ""
		}
		if err == nil && code == 0 {
			return "Passed: ruff check."
		}
		if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
			return "[ruff check]\n" + formatted
		}
		return diagnosticExitStatus(code, err)
	}
	pyBin := resolveWorkspacePythonBin(rec, filepath.Dir(filePath), worktreeRoot, "python3")
	if pyBin == "" {
		pyBin = resolveWorkspacePythonBin(rec, filepath.Dir(filePath), worktreeRoot, "python")
	}
	if pyBin == "" {
		return ""
	}
	const astScript = "import ast, tokenize, sys; p = sys.argv[1]; f = tokenize.open(p); src = f.read(); f.close(); ast.parse(src, filename=p)"
	out, code, err := s.execNativeConfined(ctx, rec, nil, worktreeRoot, pyBin, []string{"-I", "-B", "-c", astScript, filePath}, "", nativeDiagMaxOutputBytes*2)
	if ctx.Err() != nil {
		return ""
	}
	if err == nil && code == 0 {
		return "Passed: Python syntax."
	}
	if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[python ast.parse]\n" + formatted
	}
	return diagnosticExitStatus(code, err)
}

func resolveWorkspacePythonBin(rec SessionRecord, startDir, worktreeRoot, toolName string) string {
	binSubdir := "bin"
	ext := ""
	if runtime.GOOS == "windows" {
		binSubdir = "Scripts"
		ext = ".exe"
	}
	if cur, err := filepath.Abs(startDir); err == nil {
		if boundary, bErr := filepath.Abs(worktreeRoot); bErr == nil {
			for pathWithin(boundary, cur) {
				for _, venvDir := range []string{".venv", "venv"} {
					cand := filepath.Join(cur, venvDir, binSubdir, toolName+ext)
					if st, sErr := os.Stat(cand); sErr == nil && !st.IsDir() {
						return cand
					}
				}
				if normalizeComparePath(cur) == normalizeComparePath(boundary) {
					break
				}
				parent := filepath.Dir(cur)
				if parent == cur {
					break
				}
				cur = parent
			}
		}
	}
	return resolveNativeToolBinary(rec, toolName)
}

func (s *Service) runNativeDartDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	dartBin := resolveNativeToolBinary(rec, "dart")
	if dartBin == "" {
		if flutterBin := resolveNativeToolBinary(rec, "flutter"); flutterBin != "" {
			cand := filepath.Join(filepath.Dir(flutterBin), "dart")
			if runtime.GOOS == "windows" {
				cand += ".bat"
			}
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				dartBin = cand
			}
		}
	}
	if dartBin == "" {
		return ""
	}
	pubRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "pubspec.yaml")
	if pubRoot == "" {
		pubRoot = worktreeRoot
	}
	out, code, err := s.execNativeConfined(ctx, rec, nil, pubRoot, dartBin, []string{"analyze", "--format=machine", filePath}, "", nativeDiagMaxOutputBytes*2)
	if ctx.Err() != nil {
		return ""
	}
	if err == nil && code == 0 {
		return "Passed: dart analyze."
	}
	if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[dart analyze]\n" + formatted
	}
	return diagnosticExitStatus(code, err)
}

func formatDiagnosticLines(raw, priorityNeedle string, maxLines, maxBytes int) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	var prioritized, others []string
	baseNeedle := ""
	if priorityNeedle != "" {
		baseNeedle = filepath.Base(priorityNeedle)
	}
	for _, line := range lines {
		l := strings.TrimRight(line, " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if priorityNeedle != "" && (strings.Contains(l, priorityNeedle) || (baseNeedle != "" && strings.Contains(l, baseNeedle))) {
			prioritized = append(prioritized, l)
		} else {
			others = append(others, l)
		}
	}
	ordered := append(prioritized, others...)
	if len(ordered) == 0 {
		return ""
	}
	truncatedLines := 0
	if maxLines > 0 && len(ordered) > maxLines {
		truncatedLines = len(ordered) - maxLines
		ordered = ordered[:maxLines]
	}
	joined := strings.Join(ordered, "\n")
	if maxBytes > 0 && len(joined) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(joined[cut]) {
			cut--
		}
		joined = joined[:cut] + "\n... [diagnostics truncated]"
	} else if truncatedLines > 0 {
		joined += fmt.Sprintf("\n... (%d more diagnostic lines)", truncatedLines)
	}
	return joined
}

func diagnosticExitStatus(code int, err error) string {
	if err != nil {
		return fmt.Sprintf("Check failed: %v", err)
	}
	return fmt.Sprintf("Check exited with code %d and no diagnostic output.", code)
}
