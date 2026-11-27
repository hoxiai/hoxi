package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	nativeCmdMaxOutputBytes  = 128 * 1024
	nativeDiagMaxOutputBytes = 4 * 1024
	nativeDiagMaxLines       = 35
	nativeDiagTimeout        = 10 * time.Second
	nativeLiveTailBytes      = 8 * 1024
	nativeLiveEmitInterval   = 350 * time.Millisecond
)

// nativeCommandRunner executes bash tool calls for the in-process Go Agent,
// enforcing Seatbelt OS confinement (confineRuntimeCommand), credential-scrubbed
// non-interactive environments, process-group isolation, owned-process ledger
// tracking, and bounded orphan-safe output capture.
func (s *Service) nativeCommandRunner(rec SessionRecord, ap *activeProcess) func(ctx context.Context, dir, command string) (string, int, error) {
	streamFn := s.nativeStreamCommandRunner(rec, ap)
	return func(ctx context.Context, dir, command string) (string, int, error) {
		return streamFn(ctx, dir, command, nil, nil)
	}
}

func (s *Service) nativeStreamCommandRunner(rec SessionRecord, ap *activeProcess) func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(string)) (string, int, error) {
	return func(ctx context.Context, dir, command string, capture io.Writer, onOutput func(string)) (string, int, error) {
		onDemandProfile := ""
		if s.gate != nil {
			onDemandProfile = s.gate.consumeNativeOnDemand(rec.ID, command)
		}
		workDir := strings.TrimSpace(dir)
		if workDir == "" {
			workDir = rec.WorktreePath
		}
		if rec.WorktreePath != "" && !pathWithin(rec.WorktreePath, workDir) {
			err := errors.New("command working directory is outside this session's directory")
			return err.Error(), 1, err
		}
		shellBin, shellArgs := nativeShellCommand(command)
		out, code, err := s.execNativeConfinedWithProfile(ctx, rec, ap, workDir, shellBin, shellArgs, command, nativeCmdMaxOutputBytes, onDemandProfile, capture, onOutput)
		if onDemandProfile != "" {
			out = appendOnDemandSandboxNote(out)
		}
		return out, code, err
	}
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
	return s.execNativeConfinedWithProfile(ctx, rec, ap, workDir, rawBin, rawArgs, ownedLabel, maxBytes, "", nil, nil)
}

func (s *Service) execNativeConfinedWithProgress(
	ctx context.Context,
	rec SessionRecord,
	ap *activeProcess,
	workDir, rawBin string,
	rawArgs []string,
	ownedLabel string,
	maxBytes int,
	onProgress func(string),
) (string, int, error) {
	return s.execNativeConfinedWithProfile(ctx, rec, ap, workDir, rawBin, rawArgs, ownedLabel, maxBytes, "", nil, onProgress)
}

func (s *Service) execNativeConfinedWithProfile(
	ctx context.Context,
	rec SessionRecord,
	ap *activeProcess,
	workDir, rawBin string,
	rawArgs []string,
	ownedLabel string,
	maxBytes int,
	onDemandProfile string,
	capture io.Writer,
	onProgress func(string),
) (string, int, error) {
	bin, args, err := s.confineNativeCommandWithOnDemand(rec, onDemandProfile, rawBin, rawArgs)
	if err != nil {
		return err.Error(), 1, err
	}

	cmd := subproc.Command(bin, args...)
	cmd.Dir = workDir
	cmd.Env = s.applySessionBundleEnv(rec, nativeSubprocessEnv(rec))
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
		s.ownedProcs.Record(rec.ID, cmd.Process.Pid, ownedLabel)
	}

	writer := &nativeLimitedWriter{limit: maxBytes, capture: capture, onProgress: onProgress}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		_, _ = io.Copy(writer, outR)
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

	output := writer.String()
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
		upper := strings.ToUpper(name)
		if upper == "GOWORK" {
			if gw := strings.TrimSpace(val); gw != "" && gw != "off" && rec.WorktreePath != "" && !pathWithin(rec.WorktreePath, gw) {
				continue
			}
		}
		switch upper {
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
		if bash := subproc.ResolveWindowsBash(false); bash != "" {
			return bash, []string{"-c", command}
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

func resolveNativeToolBinaryFromEnv(env []string, toolName string) string {
	for _, kv := range env {
		if len(kv) >= 5 && strings.EqualFold(kv[:5], "PATH=") {
			pathVal := kv[5:]
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

func resolveNativeToolBinary(rec SessionRecord, toolName string) string {
	return resolveNativeToolBinaryFromEnv(nativeSubprocessEnv(rec), toolName)
}

type nativeLimitedWriter struct {
	capture    io.Writer
	limit      int
	onProgress func(string)
	lastEmit   time.Time

	totalBytes int64
	head       bytes.Buffer
	tail       []byte
	liveTail   []byte
}

func appendRollingWindow(dst, src []byte, maxWindow int) []byte {
	if maxWindow <= 0 || len(src) == 0 {
		return dst
	}
	if len(src) >= maxWindow {
		return append(dst[:0], src[len(src)-maxWindow:]...)
	}
	if len(dst)+len(src) <= maxWindow {
		return append(dst, src...)
	}
	keep := maxWindow - len(src)
	copy(dst, dst[len(dst)-keep:])
	return append(dst[:keep], src...)
}

func alignUTF8WindowStart(raw []byte) []byte {
	for skipped := 0; skipped < 3 && len(raw) > 0 && !utf8.RuneStart(raw[0]); skipped++ {
		raw = raw[1:]
	}
	return raw
}

func trimTrailingIncompleteUTF8(raw []byte) []byte {
	if len(raw) == 0 || utf8.Valid(raw) {
		return raw
	}
	start := max(0, len(raw)-3)
	for i := len(raw) - 1; i >= start; i-- {
		if utf8.RuneStart(raw[i]) {
			if r, width := utf8.DecodeRune(raw[i:]); r == utf8.RuneError && width == 1 && !utf8.FullRune(raw[i:]) {
				return raw[:i]
			}
			break
		}
	}
	return raw
}

func (w *nativeLimitedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.capture != nil {
		_, _ = w.capture.Write(p)
	}
	if w.limit <= 0 {
		_, _ = w.head.Write(p)
		w.totalBytes += int64(len(p))
	} else if w.totalBytes+int64(len(p)) <= int64(w.limit) {
		_, _ = w.head.Write(p)
		w.totalBytes += int64(len(p))
	} else {
		headCap := w.limit / 2
		tailCap := w.limit - headCap
		if w.totalBytes <= int64(w.limit) {
			if w.head.Len() > headCap {
				w.tail = appendRollingWindow(w.tail, w.head.Bytes()[headCap:], tailCap)
				w.head.Truncate(headCap)
				w.tail = appendRollingWindow(w.tail, p, tailCap)
			} else {
				needHead := headCap - w.head.Len()
				_, _ = w.head.Write(p[:needHead])
				w.tail = appendRollingWindow(w.tail, p[needHead:], tailCap)
			}
		} else {
			w.tail = appendRollingWindow(w.tail, p, tailCap)
		}
		w.totalBytes += int64(len(p))
	}

	if w.onProgress != nil {
		w.liveTail = appendRollingWindow(w.liveTail, p, nativeLiveTailBytes)
		if len(w.liveTail) > 0 && (w.lastEmit.IsZero() || time.Since(w.lastEmit) >= nativeLiveEmitInterval) {
			w.lastEmit = time.Now()
			raw := w.liveTail
			if w.totalBytes > int64(len(raw)) {
				raw = alignUTF8WindowStart(raw)
			}
			w.onProgress(subproc.DecodeOutputBytes(raw))
		}
	}
	return len(p), nil
}

func (w *nativeLimitedWriter) String() string {
	if w == nil {
		return ""
	}
	if w.limit <= 0 || w.totalBytes <= int64(w.limit) {
		return subproc.DecodeOutputBytes(w.head.Bytes())
	}
	headBytes := trimTrailingIncompleteUTF8(w.head.Bytes())
	tailBytes := alignUTF8WindowStart(w.tail)
	omitted := max(int64(1), w.totalBytes-int64(len(headBytes))-int64(len(tailBytes)))
	return subproc.DecodeOutputBytes(headBytes) +
		fmt.Sprintf("\n... [%d bytes omitted] ...\n", omitted) +
		subproc.DecodeOutputBytes(tailBytes)
}

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

func (s *Service) saveNativeToolMediaAttachments(rec SessionRecord, media []NativeContentBlock) []map[string]interface{} {
	if s == nil || s.attachments == nil || len(media) == 0 {
		return nil
	}
	var refs []map[string]interface{}
	for _, mb := range media {
		mime := strings.TrimSpace(mb.MimeType)
		data := strings.TrimSpace(mb.Data)
		if mime == "" || data == "" {
			continue
		}
		fname := strings.TrimSpace(mb.Filename)
		if fname == "" {
			fname = "tool_media"
		}
		saved, err := s.attachments.Save(attachment.SaveRequest{
			SessionID: rec.ID,
			Directory: rec.WorktreePath,
			Filename:  fname,
			Mime:      mime,
			DataURL:   "data:" + mime + ";base64," + data,
		})
		if err != nil || saved.ID == "" {
			continue
		}
		ref := map[string]interface{}{
			"id":       saved.ID,
			"url":      saved.URL,
			"mime":     saved.Mime,
			"filename": saved.Filename,
		}
		if mb.ScreenshotTrigger != 0 {
			ref["screenshotTrigger"] = mb.ScreenshotTrigger
		}
		refs = append(refs, ref)
	}
	return refs
}

func buildNativeUserBlocks(promptText string, deliveryParts []MessagePart) []NativeContentBlock {
	var blocks []NativeContentBlock
	if strings.TrimSpace(promptText) != "" {
		blocks = append(blocks, NativeContentBlock{Type: "text", Text: promptText})
	}
	for _, p := range deliveryParts {
		if p.Type != "file" || strings.TrimSpace(p.URL) == "" {
			continue
		}
		if mimeType, b64Data, _, ok := parseBase64DataURL(p.URL, p.Mime); ok {
			blocks = append(blocks, NativeContentBlock{
				Type:     "media",
				MimeType: mimeType,
				Data:     b64Data,
				Filename: p.Filename,
			})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, NativeContentBlock{Type: "text", Text: promptText})
	}
	return blocks
}

func (s *Service) resolveNativeHistoryFileBlock(p MessagePart) (NativeContentBlock, bool) {
	rawURL := strings.TrimSpace(p.URL)
	if !strings.HasPrefix(rawURL, "data:") && s != nil && s.attachments != nil && rawURL != "" {
		for _, prefix := range []string{"/api/hoxi/attachments/", "hoxi-attachment://"} {
			if idx := strings.Index(rawURL, prefix); idx >= 0 {
				refID := strings.TrimSpace(rawURL[idx+len(prefix):])
				if slash := strings.IndexByte(refID, '/'); slash >= 0 {
					refID = refID[:slash]
				}
				if q := strings.IndexByte(refID, '?'); q >= 0 {
					refID = refID[:q]
				}
				if refID != "" {
					if _, dataURL, err := s.attachments.ReadAsDataURL(refID); err == nil && dataURL != "" {
						rawURL = dataURL
					}
				}
				break
			}
		}
	}
	if rawURL == "" {
		return NativeContentBlock{}, false
	}
	mimeType, b64Data, _, ok := parseBase64DataURL(rawURL, p.Mime)
	if !ok {
		return NativeContentBlock{}, false
	}
	return NativeContentBlock{
		Type:     "media",
		MimeType: mimeType,
		Data:     b64Data,
		Filename: p.Filename,
	}, true
}
