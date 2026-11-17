package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	nativeCmdMaxOutputBytes  = 128 * 1024
	nativeDiagMaxOutputBytes = 4 * 1024
	nativeDiagMaxLines       = 35
	nativeDiagTimeout        = 10 * time.Second
)

// nativeCommandRunner executes bash tool calls for the in-process Go Agent,
// enforcing Seatbelt OS confinement (confineRuntimeCommand), credential-scrubbed
// non-interactive environments, process-group isolation, owned-process ledger
// tracking, and bounded orphan-safe output capture.
func (s *Service) nativeCommandRunner(rec SessionRecord, ap *activeProcess) func(ctx context.Context, dir, command string) (string, int, error) {
	return func(ctx context.Context, dir, command string) (string, int, error) {
		workDir := strings.TrimSpace(dir)
		if workDir == "" {
			workDir = rec.WorktreePath
		}
		if rec.WorktreePath != "" && !pathWithin(rec.WorktreePath, workDir) {
			err := errors.New("command working directory is outside this session's directory")
			return err.Error(), 1, err
		}
		shellBin, shellArgs := nativeShellCommand(command)
		out, code, err := s.execNativeConfined(ctx, rec, ap, workDir, shellBin, shellArgs, command, nativeCmdMaxOutputBytes)
		return out, code, err
	}
}

// nativeDiagnostics runs fast post-edit/write static checks (go vet for Go
// packages, tsc --noEmit for TypeScript, vue-tsc/oxlint for Vue SFCs, php -l
// for PHP, and dart analyze for Flutter/Dart) under the same Seatbelt
// confinement and sanitized environment as the session itself.
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

		diagCtx, cancel := context.WithTimeout(ctx, nativeDiagTimeout)
		defer cancel()

		ext := strings.ToLower(filepath.Ext(filePath))
		switch ext {
		case ".go":
			return s.runNativeGoVetDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".ts", ".tsx", ".mts", ".cts":
			return s.runNativeTSCDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".vue":
			return s.runNativeVueDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".php":
			return s.runNativePHPDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		case ".dart":
			return s.runNativeDartDiagnostic(diagCtx, rec, worktreeRoot, filePath)
		default:
			return ""
		}
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
	if err == nil && code == 0 {
		return ""
	}
	if ctx.Err() != nil {
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
	if err == nil && code == 0 {
		return ""
	}
	if ctx.Err() != nil {
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
	sum := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + filepath.Clean(tsconfigPath)))
	return filepath.Join(cacheDir, hex.EncodeToString(sum[:12])+".tsbuildinfo")
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
			if err == nil && code == 0 {
				return ""
			}
			if ctx.Err() != nil {
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

func (s *Service) execNativeConfined(
	ctx context.Context,
	rec SessionRecord,
	ap *activeProcess,
	workDir, rawBin string,
	rawArgs []string,
	ownedLabel string,
	maxBytes int,
) (string, int, error) {
	bin, args, err := s.confineRuntimeCommand(rec, rawBin, rawArgs)
	if err != nil {
		return err.Error(), 1, err
	}

	cmd := subproc.Command(bin, args...)
	cmd.Dir = workDir
	cmd.Env = nativeSubprocessEnv(rec)
	cmd.WaitDelay = orphanDrainGrace
	setProcessGroup(cmd)

	outR, outW, err := os.Pipe()
	if err != nil {
		return err.Error(), 1, err
	}
	cmd.Stdout = outW
	cmd.Stderr = outW

	if err := cmd.Start(); err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return err.Error(), 1, err
	}
	_ = outW.Close()

	if ap != nil {
		ap.mu.Lock()
		ap.cmd = cmd
		ap.mu.Unlock()
		defer func() {
			ap.mu.Lock()
			if ap.cmd == cmd {
				ap.cmd = nil
			}
			ap.mu.Unlock()
		}()
	}
	if ownedLabel != "" && s.ownedProcs != nil && runtime.GOOS != "windows" && cmd.Process != nil {
		s.ownedProcs.record(rec.ID, cmd.Process.Pid, ownedLabel)
	}

	var buf bytes.Buffer
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		_, _ = io.Copy(&nativeLimitedWriter{buf: &buf, limit: maxBytes}, outR)
	}()

	waitDone := make(chan struct{})
	go func() {
		select {
		case <-waitDone:
		case <-ctx.Done():
			terminateNativeCommandTree(cmd.Process, waitDone)
		}
	}()

	waitErr := cmd.Wait()
	close(waitDone)
	reapOrphanedOutput(cmd.Process, outR, readerDone)

	output := subproc.DecodeOutputBytes(buf.Bytes())
	if waitErr == nil {
		return output, 0, nil
	}
	code := 1
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if c := exitErr.ExitCode(); c > 0 {
			code = c
		} else if ctx.Err() != nil {
			code = 130
		} else {
			code = 137
		}
	} else if ctx.Err() != nil {
		code = 130
	}
	return output, code, waitErr
}

func terminateNativeCommandTree(proc *os.Process, waitDone <-chan struct{}) {
	if proc == nil {
		return
	}
	tree := descendantPIDs(proc.Pid)
	_ = signalProcessGroup(proc, os.Interrupt)
	select {
	case <-waitDone:
		return
	case <-time.After(200 * time.Millisecond):
		tree = append(tree, descendantPIDs(proc.Pid)...)
	}
	_ = signalProcessGroup(proc, os.Kill)
	killPIDs(tree)
}

func nativeSubprocessEnv(rec SessionRecord) []string {
	base := runtimeProcessEnv(rec, os.Environ(), nil)
	filtered := make([]string, 0, len(base)+4)
	for _, kv := range base {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name == "GOWORK" {
			if gw := strings.TrimSpace(val); gw != "" && gw != "off" && rec.WorktreePath != "" && !pathWithin(rec.WorktreePath, gw) {
				continue
			}
		}
		switch name {
		case "GIT_TERMINAL_PROMPT",
			"GCM_INTERACTIVE",
			"GIT_ASKPASS",
			"SSH_ASKPASS",
			"SUDO_ASKPASS",
			"GIT_EDITOR",
			"GIT_SEQUENCE_EDITOR",
			"EDITOR",
			"VISUAL",
			"PAGER",
			"GIT_PAGER",
			"NO_COLOR",
			"FORCE_COLOR":
			continue
		}
		filtered = append(filtered, kv)
	}
	return append(filtered,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
		"NO_COLOR=1",
		"FORCE_COLOR=0",
	)
}

func nativeShellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		for _, k := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			root := strings.TrimSpace(os.Getenv(k))
			if root == "" {
				continue
			}
			for _, rel := range []string{
				filepath.Join(root, "Git", "bin", "bash.exe"),
				filepath.Join(root, "Git", "usr", "bin", "bash.exe"),
				filepath.Join(root, "Programs", "Git", "bin", "bash.exe"),
			} {
				if st, err := os.Stat(rel); err == nil && !st.IsDir() {
					return rel, []string{"-c", command}
				}
			}
		}
		if p, err := exec.LookPath("bash"); err == nil && p != "" {
			return p, []string{"-c", command}
		}
		return "cmd", []string{"/C", "chcp 65001 >nul 2>&1 & " + command}
	}
	return "/bin/sh", []string{"-c", command}
}

func findAncestorWithFile(startDir, boundaryDir, markerFile string) string {
	cur, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	boundary, err := filepath.Abs(boundaryDir)
	if err != nil {
		return ""
	}
	for {
		if !pathWithin(boundary, cur) {
			return ""
		}
		if st, err := os.Stat(filepath.Join(cur, markerFile)); err == nil && !st.IsDir() {
			return cur
		}
		if normalizeComparePath(cur) == normalizeComparePath(boundary) {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func resolveWorkspaceTSC(rec SessionRecord, tsRoot, worktreeRoot string) string {
	return resolveWorkspaceNodeBin(rec, tsRoot, worktreeRoot, "tsc")
}

func resolveWorkspaceNodeBin(rec SessionRecord, startDir, worktreeRoot, toolName string) string {
	binName := toolName
	if runtime.GOOS == "windows" {
		binName = toolName + ".cmd"
	}
	if strings.TrimSpace(startDir) != "" {
		cur, err := filepath.Abs(startDir)
		if err == nil {
			boundary, bErr := filepath.Abs(worktreeRoot)
			if bErr == nil {
				for pathWithin(boundary, cur) {
					cand := filepath.Join(cur, "node_modules", ".bin", binName)
					if st, sErr := os.Stat(cand); sErr == nil && !st.IsDir() {
						return cand
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
	}
	return resolveNativeToolBinary(rec, toolName)
}

func resolveNativeToolBinary(rec SessionRecord, toolName string) string {
	for _, kv := range nativeSubprocessEnv(rec) {
		if pathVal, ok := strings.CutPrefix(kv, "PATH="); ok {
			for _, dir := range filepath.SplitList(pathVal) {
				dir = strings.TrimSpace(dir)
				if dir == "" {
					continue
				}
				cand := filepath.Join(dir, toolName)
				if runtime.GOOS == "windows" {
					for _, ext := range []string{".exe", ".cmd", ".bat"} {
						if st, err := os.Stat(cand + ext); err == nil && !st.IsDir() {
							return cand + ext
						}
					}
				} else if st, err := os.Stat(cand); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
					return cand
				}
			}
		}
	}
	if p, err := exec.LookPath(toolName); err == nil {
		return p
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
		joined = joined[:maxBytes] + "\n... [diagnostics truncated]"
	} else if truncatedLines > 0 {
		joined += fmt.Sprintf("\n... (%d more diagnostic lines)", truncatedLines)
	}
	return joined
}

type nativeLimitedWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *nativeLimitedWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 || w.buf.Len() < w.limit {
		avail := len(p)
		if w.limit > 0 && w.buf.Len()+avail > bLimit(w.limit) {
			avail = w.limit - w.buf.Len()
		}
		_, _ = w.buf.Write(p[:avail])
	}
	return len(p), nil
}

func bLimit(v int) int { return v }

func (ap *activeProcess) activeProcess() *os.Process {
	if ap == nil {
		return nil
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.cmd == nil {
		return nil
	}
	return ap.cmd.Process
}
