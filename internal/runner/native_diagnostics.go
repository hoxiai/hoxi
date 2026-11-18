package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// nativeDiagnostics runs fast post-edit/write static checks (go vet for Go
// packages, tsc --noEmit for TypeScript, vue-tsc/oxlint for Vue SFCs, cargo
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
			return ""
		}

		diagCtx, cancel := context.WithTimeout(ctx, nativeDiagTimeout)
		defer cancel()

		var res string
		switch ext {
		case ".go":
			res = s.runNativeGoVetDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".ts", ".tsx", ".mts", ".cts":
			res = s.runNativeTSCDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".vue":
			res = s.runNativeVueDiagnostic(diagCtx, rec, worktreeRoot, filePath)
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
		if diagCtx.Err() == context.DeadlineExceeded && (ctx == nil || ctx.Err() == nil) {
			noteDiagToolBudgetExceeded(rec.ID, ext)
		}
		return res
	}
}

func (s *Service) runNativeGoVetDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	modRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "go.mod")
	if modRoot == "" {
		return ""
	}
	goBin := resolveNativeToolBinary(rec, "go")
	if goBin == "" {
		return ""
	}
	relPkg, err := filepath.Rel(modRoot, filepath.Dir(filePath))
	if err != nil || strings.HasPrefix(relPkg, "..") {
		return ""
	}
	pkgTarget := "."
	if relPkg != "." && relPkg != "" {
		pkgTarget = "./" + filepath.ToSlash(relPkg)
	}
	out, code, err := s.execNativeConfined(ctx, rec, nil, modRoot, goBin, []string{"vet", pkgTarget}, "", nativeDiagMaxOutputBytes*2)
	if err == nil && code == 0 || ctx.Err() != nil {
		return ""
	}
	formatted := formatDiagnosticLines(out, "", nativeDiagMaxLines, nativeDiagMaxOutputBytes)
	if formatted == "" {
		return ""
	}
	return fmt.Sprintf("[go vet %s]\n%s", pkgTarget, formatted)
}

func (s *Service) runNativeTSCDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	tsRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "tsconfig.json")
	if tsRoot == "" {
		return ""
	}
	tscBin := resolveWorkspaceTSC(rec, tsRoot, worktreeRoot)
	if tscBin == "" {
		return ""
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
	if err == nil && code == 0 || ctx.Err() != nil {
		return ""
	}
	relFile := filepath.Base(filePath)
	if rel, relErr := filepath.Rel(tsRoot, filePath); relErr == nil && !strings.HasPrefix(rel, "..") {
		relFile = filepath.ToSlash(rel)
	}
	formatted := formatDiagnosticLines(out, relFile, nativeDiagMaxLines, nativeDiagMaxOutputBytes)
	if formatted == "" {
		return ""
	}
	return "[tsc --noEmit]\n" + formatted
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

func (s *Service) runNativeVueDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	tsRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "tsconfig.json")
	if tsRoot != "" {
		if vueTscBin := resolveWorkspaceNodeBin(rec, tsRoot, worktreeRoot, "vue-tsc"); vueTscBin != "" {
			tsconfigPath := filepath.Join(tsRoot, "tsconfig.json")
			args := []string{"--project", tsconfigPath, "--noEmit", "--pretty", "false"}
			out, code, err := s.execNativeConfined(ctx, rec, nil, tsRoot, vueTscBin, args, "", nativeDiagMaxOutputBytes*4)
			if err == nil && code == 0 || ctx.Err() != nil {
				return ""
			}
			relFile := filepath.Base(filePath)
			if rel, relErr := filepath.Rel(tsRoot, filePath); relErr == nil && !strings.HasPrefix(rel, "..") {
				relFile = filepath.ToSlash(rel)
			}
			if formatted := formatDiagnosticLines(out, relFile, nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
				return "[vue-tsc --noEmit]\n" + formatted
			}
			return ""
		}
	}
	pkgRoot := findAncestorWithFile(filepath.Dir(filePath), worktreeRoot, "package.json")
	if pkgRoot == "" {
		pkgRoot = worktreeRoot
	}
	if oxlintBin := resolveWorkspaceNodeBin(rec, pkgRoot, worktreeRoot, "oxlint"); oxlintBin != "" {
		out, code, err := s.execNativeConfined(ctx, rec, nil, pkgRoot, oxlintBin, []string{"--quiet", filePath}, "", nativeDiagMaxOutputBytes*2)
		if err == nil && code == 0 || ctx.Err() != nil {
			return ""
		}
		if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
			return "[oxlint]\n" + formatted
		}
	}
	return ""
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
	if err == nil && code == 0 || ctx.Err() != nil {
		return ""
	}
	relFile := filepath.Base(filePath)
	if rel, relErr := filepath.Rel(cargoRoot, filePath); relErr == nil && !strings.HasPrefix(rel, "..") {
		relFile = filepath.ToSlash(rel)
	}
	if strings.Contains(out, "--offline") && !strings.Contains(out, relFile) {
		return ""
	}
	if formatted := formatDiagnosticLines(out, relFile, nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[cargo check]\n" + formatted
	}
	return ""
}

func (s *Service) runNativePHPDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	phpBin := resolveNativeToolBinary(rec, "php")
	if phpBin == "" {
		return ""
	}
	out, code, err := s.execNativeConfined(ctx, rec, nil, worktreeRoot, phpBin, []string{"-l", filePath}, "", nativeDiagMaxOutputBytes*2)
	if err == nil && code == 0 || ctx.Err() != nil {
		return ""
	}
	if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[php -l]\n" + formatted
	}
	return ""
}

func (s *Service) runNativePythonDiagnostic(ctx context.Context, rec SessionRecord, worktreeRoot, filePath string) string {
	if ruffBin := resolveWorkspacePythonBin(rec, filepath.Dir(filePath), worktreeRoot, "ruff"); ruffBin != "" {
		out, code, err := s.execNativeConfined(ctx, rec, nil, worktreeRoot, ruffBin, []string{"check", "--output-format=concise", "--no-cache", filePath}, "", nativeDiagMaxOutputBytes*2)
		if err == nil && code == 0 || ctx.Err() != nil {
			return ""
		}
		if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
			return "[ruff check]\n" + formatted
		}
		return ""
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
	if err == nil && code == 0 || ctx.Err() != nil {
		return ""
	}
	if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[python ast.parse]\n" + formatted
	}
	return ""
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
	if err == nil && code == 0 || ctx.Err() != nil {
		return ""
	}
	if formatted := formatDiagnosticLines(out, filepath.Base(filePath), nativeDiagMaxLines, nativeDiagMaxOutputBytes); formatted != "" {
		return "[dart analyze]\n" + formatted
	}
	return ""
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
