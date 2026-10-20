package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/claudeconfig"
)

func commonUserBinDirs() []string {
	var dirs []string
	if home, _ := os.UserHomeDir(); home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "sdk", "node", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".fnm", "current", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
		)
		if nvmBins, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); err == nil && len(nvmBins) > 0 {
			sort.Strings(nvmBins)
			for i := len(nvmBins) - 1; i >= 0; i-- {
				dirs = append(dirs, nvmBins[i])
			}
		}
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	return dirs
}

func firstExecutableFile(candidates ...string) string {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func geminiHomeDir() string {
	if env := strings.TrimSpace(os.Getenv("GEMINI_HOME")); env != "" {
		return filepath.Clean(env)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".gemini")
}

func ResolveAgyBinary() string {
	if gRoot := geminiHomeDir(); gRoot != "" {
		candidate := filepath.Join(gRoot, "bin", "agy")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		if os.Getenv("GEMINI_HOME") != "" || os.Getenv("ANTIGRAVITY_HOME") != "" {
			return ""
		}
	}
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	if home, _ := os.UserHomeDir(); home != "" {
		for _, rel := range []string{".local/bin/agy", ".gemini/bin/agy"} {
			candidate := filepath.Join(home, rel)
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate
			}
		}
	}
	for _, sysPath := range []string{"/opt/homebrew/bin/agy", "/usr/local/bin/agy"} {
		if st, err := os.Stat(sysPath); err == nil && !st.IsDir() {
			return sysPath
		}
	}
	return ""
}

func ResolveClaudeBinary() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	if home, _ := os.UserHomeDir(); home != "" {
		candidate := filepath.Join(home, ".local", "bin", "claude")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	for _, sysPath := range []string{"/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
		if st, err := os.Stat(sysPath); err == nil && !st.IsDir() {
			return sysPath
		}
	}
	return ""
}

var (
	agyModelsMu          sync.Mutex
	agyCachedBin         string
	agyCachedTime        time.Time
	agyCachedSlugs       []string
	agyCachedDisplayMap  map[string]string
	agyCachedReverseSlug map[string]string
)

func ParseAgyModelsOutput(raw string) (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	displayBySlug = make(map[string]string)
	slugByDisplayLower = make(map[string]string)
	seen := make(map[string]bool)
	for _, rawLine := range strings.Split(raw, "\n") {
		segments := strings.Split(rawLine, "\r")
		line := strings.TrimSpace(segments[len(segments)-1])
		if line == "" || strings.Contains(line, "Fetching available models") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		slug := strings.TrimSpace(fields[0])
		if slug == "" || strings.ContainsAny(slug, " ()") || !strings.Contains(slug, "-") {
			continue
		}
		display := strings.TrimSpace(strings.TrimPrefix(line, slug))
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
		if display != "" {
			displayBySlug[slug] = display
			slugByDisplayLower[strings.ToLower(display)] = slug
		}
	}
	return slugs, displayBySlug, slugByDisplayLower
}

func DiscoverAgyModels() ([]RunnerModelItem, map[string]string, map[string]string) {
	return DiscoverAgyModelsWithRefresh(false)
}

func DiscoverAgyModelsWithRefresh(forceRefresh bool) ([]RunnerModelItem, map[string]string, map[string]string) {
	agyBin := ResolveAgyBinary()
	if agyBin == "" {
		return nil, nil, nil
	}
	agyModelsMu.Lock()
	defer agyModelsMu.Unlock()
	if !forceRefresh && agyCachedBin == agyBin && len(agyCachedSlugs) > 0 && time.Since(agyCachedTime) < 10*time.Minute {
		return buildAgyModelItems(agyCachedSlugs, agyCachedDisplayMap), agyCachedDisplayMap, agyCachedReverseSlug
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, agyBin, "models")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return buildAgyModelItems(agyCachedSlugs, agyCachedDisplayMap), agyCachedDisplayMap, agyCachedReverseSlug
	}
	slugs, dMap, rMap := ParseAgyModelsOutput(string(out))
	if len(slugs) > 0 {
		agyCachedBin = agyBin
		agyCachedTime = time.Now()
		agyCachedSlugs = slugs
		agyCachedDisplayMap = dMap
		agyCachedReverseSlug = rMap
	}
	return buildAgyModelItems(agyCachedSlugs, agyCachedDisplayMap), agyCachedDisplayMap, agyCachedReverseSlug
}

func splitAgyModelEffortSuffix(slug string) (base, suffixEffort string) {
	trimmed := strings.TrimSpace(slug)
	lower := strings.ToLower(trimmed)
	for _, eff := range []string{"xhigh", "medium", "high", "low", "max"} {
		suf := "-" + eff
		if strings.HasSuffix(lower, suf) && len(trimmed) > len(suf) {
			return trimmed[:len(trimmed)-len(suf)], eff
		}
	}
	return trimmed, ""
}

func inferClaudeModelEfforts(slug string) (efforts []string, defaultEffort string, supportsEffort bool) {
	lower := strings.ToLower(strings.TrimSpace(slug))
	efforts, defaultEffort = inferVendorEffortSpec(string(RuntimeClaudeCode), lower, !strings.Contains(lower, "haiku"))
	return efforts, defaultEffort, len(efforts) > 0
}

func inferOfficialRunnerModelSpec(slug string) (ctxLimit, outLimit int, inCost, outCost float64, image, reasoning bool) {
	lower := strings.ToLower(strings.TrimSpace(slug))
	_, effSuffix := splitAgyModelEffortSuffix(lower)
	image = true
	reasoning = !strings.Contains(lower, "fast") && !strings.Contains(lower, "flash") && !strings.Contains(lower, "lite") && !strings.Contains(lower, "haiku")
	if effSuffix != "" || strings.Contains(lower, "thinking") || strings.Contains(lower, "pro") {
		reasoning = true
	}

	switch {
	case strings.HasPrefix(lower, "claude"):
		_, _, supportsEffort := inferClaudeModelEfforts(lower)
		if effSuffix != "" {
			supportsEffort = true
		}
		switch {
		case strings.Contains(lower, "opus"):
			return 200000, 64000, 5.00, 25.00, true, true
		case strings.Contains(lower, "haiku"):
			return 200000, 64000, 0.80, 4.00, true, supportsEffort
		default:
			return 200000, 64000, 3.00, 15.00, true, true
		}
	case strings.HasPrefix(lower, "gemini"):
		switch {
		case strings.Contains(lower, "lite"):
			return 1000000, 65536, 0.10, 0.40, true, reasoning
		case strings.Contains(lower, "flash"):
			return 1000000, 65536, 0.35, 1.40, true, reasoning
		default:
			return 1000000, 65536, 1.25, 10.00, true, true
		}
	case strings.HasPrefix(lower, "gpt") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4"):
		switch {
		case strings.Contains(lower, "nano"):
			return 400000, 128000, 0.10, 0.40, true, reasoning
		case strings.Contains(lower, "mini"):
			return 400000, 128000, 0.25, 1.00, true, reasoning
		default:
			return 400000, 128000, 2.00, 8.00, true, true
		}
	}
	return 200000, 64000, 0, 0, image, reasoning
}

func buildAgyModelItems(slugs []string, displayBySlug map[string]string) []RunnerModelItem {
	familyEfforts := make(map[string][]string)
	for _, s := range slugs {
		if base, suf := splitAgyModelEffortSuffix(s); suf != "" {
			familyEfforts[strings.ToLower(base)] = append(familyEfforts[strings.ToLower(base)], suf)
		}
	}
	items := make([]RunnerModelItem, 0, len(slugs))
	for _, s := range slugs {
		name := displayBySlug[s]
		if name == "" {
			name = s
		}
		ctxLim, outLim, inCost, outCost, image, reasoning := inferOfficialRunnerModelSpec(s)
		var efforts []string
		var defaultEffort string
		if base, suf := splitAgyModelEffortSuffix(s); suf != "" {
			reasoning = true
			defaultEffort = suf
			efforts = normalizeEffortList(familyEfforts[strings.ToLower(base)])
			if len(efforts) == 0 {
				efforts = []string{suf}
			}
		} else if reasoning {
			efforts, defaultEffort = inferVendorEffortSpec(string(RuntimeAntigravity), s, true)
		}
		items = append(items, RunnerModelItem{
			ID:            s,
			Name:          name,
			ProviderID:    string(RuntimeAntigravity),
			Runtime:       string(RuntimeAntigravity),
			Reasoning:     reasoning,
			Efforts:       efforts,
			DefaultEffort: defaultEffort,
			Image:         image,
			ContextLimit:  ctxLim,
			OutputLimit:   outLim,
			InputCost:     inCost,
			OutputCost:    outCost,
		})
	}
	return items
}

func DiscoverClaudeModels() []RunnerModelItem {
	slugs := claudeconfig.Models()
	friendly := map[string]string{
		"claude-opus-5-5":           "Claude Opus 5.5",
		"claude-sonnet-4-6":         "Claude Sonnet 4.6",
		"claude-opus-4-6":           "Claude Opus 4.6",
		"claude-haiku-4-5-20251001": "Claude Haiku 4.5",
	}
	items := make([]RunnerModelItem, 0, len(slugs))
	for _, s := range slugs {
		name := friendly[s]
		if name == "" {
			name = s
		}
		ctxLim, outLim, inCost, outCost, image, reasoning := inferOfficialRunnerModelSpec(s)
		efforts, defEffort, supportsEffort := inferClaudeModelEfforts(s)
		if !supportsEffort {
			efforts = nil
			defEffort = ""
		}
		items = append(items, RunnerModelItem{
			ID:            s,
			Name:          name,
			ProviderID:    string(RuntimeClaudeCode),
			Runtime:       string(RuntimeClaudeCode),
			Reasoning:     reasoning,
			Efforts:       efforts,
			DefaultEffort: defEffort,
			Image:         image,
			ContextLimit:  ctxLim,
			OutputLimit:   outLim,
			InputCost:     inCost,
			OutputCost:    outCost,
		})
	}
	return items
}

func ValidateWorktreeDir(worktreePath, projectRoot string) error {
	wt := strings.TrimSpace(worktreePath)
	if wt == "" {
		return errors.New("runner worktree path is required")
	}
	absWT, err := filepath.Abs(wt)
	if err != nil {
		return fmt.Errorf("resolve worktree path: %w", err)
	}
	st, err := os.Stat(absWT)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("worktree directory does not exist: %s", absWT)
	}
	slashWT := filepath.ToSlash(filepath.Clean(absWT))
	if !strings.Contains(slashWT, "/.hoxi/worktrees/") {
		return fmt.Errorf("refusing to run CLI outside isolated worktree (.hoxi/worktrees/<session-id>): %s", absWT)
	}
	if home, _ := os.UserHomeDir(); home != "" {
		if normalizeComparePath(absWT) == normalizeComparePath(home) {
			return errors.New("refusing to run CLI in user home directory")
		}
	}
	if strings.TrimSpace(projectRoot) != "" {
		if normalizeComparePath(absWT) == normalizeComparePath(projectRoot) {
			return errors.New("refusing to run CLI directly in main repository root")
		}
	}
	return nil
}

func agySupportedEffortsForBase(base string) []string {
	lowerBase := strings.ToLower(strings.TrimSpace(base))
	agyModelsMu.Lock()
	cached := append([]string(nil), agyCachedSlugs...)
	agyModelsMu.Unlock()
	var found []string
	for _, s := range cached {
		if b, suf := splitAgyModelEffortSuffix(s); suf != "" && strings.EqualFold(b, lowerBase) {
			found = append(found, suf)
		}
	}
	if norm := normalizeEffortList(found); len(norm) > 0 {
		return norm
	}
	efforts, _ := inferVendorEffortSpec(string(RuntimeAntigravity), lowerBase, true)
	return efforts
}

func ResolveAgyModelAndEffort(modelID, effort string) (string, string) {
	m := strings.TrimSpace(modelID)
	e := normalizeEffortName(effort)
	if base, suf := splitAgyModelEffortSuffix(m); suf != "" {
		if e == "" {
			return m, ""
		}
		supported := agySupportedEffortsForBase(base)
		e = clampEffortToSupported(e, supported, suf)
		return base + "-" + e, e
	}
	if e != "" {
		supported, def := inferVendorEffortSpec(string(RuntimeAntigravity), m, true)
		e = clampEffortToSupported(e, supported, def)
	}
	return m, e
}

func BuildAgyArgs(mode, modelID, effort, externalConversationID string) []string {
	agyMode := "accept-edits"
	if strings.EqualFold(strings.TrimSpace(mode), "plan") {
		agyMode = "plan"
	}
	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--sandbox",
		"--mode", agyMode,
	}
	resolvedModel, resolvedEffort := ResolveAgyModelAndEffort(modelID, effort)
	if resolvedModel != "" {
		args = append(args, "--model", resolvedModel)
	}
	if resolvedEffort != "" {
		args = append(args, "--effort", resolvedEffort)
	}
	if cid := strings.TrimSpace(externalConversationID); cid != "" {
		args = append(args, "--conversation", cid)
	}
	args = append(args, "-p=")
	return args
}

func BuildClaudeArgs(mode, modelID, effort, externalSessionID string) []string {
	permMode := "acceptEdits"
	if strings.EqualFold(strings.TrimSpace(mode), "plan") {
		permMode = "plan"
	}
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-mode", permMode,
		"--permission-prompts", "none",
	}
	m := strings.TrimSpace(modelID)
	if m != "" {
		args = append(args, "--model", m)
	}
	if e := normalizeEffortName(effort); e != "" {
		efforts, defEffort, supportsEffort := inferClaudeModelEfforts(m)
		if supportsEffort {
			if clamped := clampEffortToSupported(e, efforts, defEffort); clamped != "" {
				args = append(args, "--effort", clamped)
			}
		}
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "--resume", sid)
	}
	return args
}

func EncodeStreamInputTurn(runtime, promptText string) ([]byte, error) {
	if IsClaudeProtocolRuntime(runtime) {
		payload := map[string]interface{}{
			"type": "user",
			"message": map[string]interface{}{
				"role":    "user",
				"content": promptText,
			},
		}
		b, err := json.Marshal(payload)
		return append(b, '\n'), err
	}
	payload := map[string]interface{}{
		"event": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": promptText,
		},
	}
	b, err := json.Marshal(payload)
	return append(b, '\n'), err
}

type activeProcess struct {
	sessionID string
	runtime   string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	done      chan struct{}
	aborted   bool
	cancel    func()
	mu        sync.Mutex
}

func (ap *activeProcess) Abort() {
	ap.mu.Lock()
	ap.aborted = true
	var proc *os.Process
	if ap.cmd != nil {
		proc = ap.cmd.Process
	}
	stdin := ap.stdin
	cancel := ap.cancel
	ap.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	if stdin != nil {
		_ = stdin.Close()
	}
	if proc == nil {
		return
	}
	_ = proc.Signal(os.Interrupt)
	select {
	case <-ap.done:
		return
	case <-time.After(2 * time.Second):
		_ = proc.Kill()
	}
}

func (ap *activeProcess) wasAborted() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.aborted
}

func readStreamLines(r io.Reader, onLine func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		onLine(scanner.Text())
	}
}
