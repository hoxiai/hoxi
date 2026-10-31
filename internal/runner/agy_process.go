package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/agentcli"
	"github.com/hoxiai/hoxi/internal/claudeconfig"
)

func geminiHomeDir() string {
	return agentcli.GeminiHomeDir()
}

func ResolveAgyBinary() string {
	return agentcli.ResolveAgyBinary()
}

func ResolveClaudeBinary() string {
	return agentcli.ResolveClaudeBinary()
}

func ParseAgyModelsOutput(raw string) (slugs []string, displayBySlug map[string]string, slugByDisplayLower map[string]string) {
	return agentcli.ParseAgyModelsOutput(raw)
}

func DiscoverAgyModels() ([]agentcli.ModelItem, map[string]string, map[string]string) {
	return DiscoverAgyModelsWithRefresh(false)
}

func DiscoverAgyModelsWithRefresh(forceRefresh bool) ([]agentcli.ModelItem, map[string]string, map[string]string) {
	slugs, dMap, rMap := agentcli.DiscoverAgyCLIModels(forceRefresh)
	if len(slugs) == 0 {
		return nil, nil, nil
	}
	return buildAgyModelItems(slugs, dMap), dMap, rMap
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
	efforts, defaultEffort = agentcli.InferVendorEffortSpec(string(RuntimeClaudeCode), lower, !strings.Contains(lower, "haiku"))
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
	case strings.HasPrefix(lower, "claude") || lower == "opus" || lower == "sonnet" || lower == "haiku" || lower == "fable":
		_, _, supportsEffort := inferClaudeModelEfforts(lower)
		if effSuffix != "" {
			supportsEffort = true
		}
		switch {
		case strings.Contains(lower, "opus") || strings.Contains(lower, "fable"):
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
	case strings.HasPrefix(lower, "gpt") || agentcli.IsOpenAIOSeriesModel(lower):
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

func buildAgyModelItems(slugs []string, displayBySlug map[string]string) []agentcli.ModelItem {
	familyEfforts := make(map[string][]string)
	for _, s := range slugs {
		if base, suf := splitAgyModelEffortSuffix(s); suf != "" {
			familyEfforts[strings.ToLower(base)] = append(familyEfforts[strings.ToLower(base)], suf)
		}
	}
	items := make([]agentcli.ModelItem, 0, len(slugs))
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
			efforts = agentcli.NormalizeEffortList(familyEfforts[strings.ToLower(base)])
			if len(efforts) == 0 {
				efforts = []string{suf}
			}
		} else if reasoning {
			efforts, defaultEffort = agentcli.InferVendorEffortSpec(string(RuntimeAntigravity), s, true)
		}
		items = append(items, agentcli.ModelItem{
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

func DiscoverClaudeModels() []agentcli.ModelItem {
	slugs := claudeconfig.Models()
	items := make([]agentcli.ModelItem, 0, len(slugs))
	for _, s := range slugs {
		name := agentcli.FormatClaudeModelName(s)
		if name == "" {
			name = s
		}
		ctxLim, outLim, inCost, outCost, image, reasoning := inferOfficialRunnerModelSpec(s)
		efforts, defEffort, supportsEffort := inferClaudeModelEfforts(s)
		if !supportsEffort {
			efforts = nil
			defEffort = ""
		}
		items = append(items, agentcli.ModelItem{
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
	cached := agentcli.CachedAgyCLISlugs()
	var found []string
	for _, s := range cached {
		if b, suf := splitAgyModelEffortSuffix(s); suf != "" && strings.EqualFold(b, lowerBase) {
			found = append(found, suf)
		}
	}
	if norm := agentcli.NormalizeEffortList(found); len(norm) > 0 {
		return norm
	}
	efforts, _ := agentcli.InferVendorEffortSpec(string(RuntimeAntigravity), lowerBase, true)
	return efforts
}

func ResolveAgyModelAndEffort(modelID, effort string) (string, string) {
	m := strings.TrimSpace(modelID)
	e := agentcli.NormalizeEffortName(effort)
	if base, suf := splitAgyModelEffortSuffix(m); suf != "" {
		if e == "" {
			return m, ""
		}
		supported := agySupportedEffortsForBase(base)
		e = agentcli.ClampEffortToSupported(e, supported, suf)
		return base + "-" + e, e
	}
	if e != "" {
		supported, def := agentcli.InferVendorEffortSpec(string(RuntimeAntigravity), m, true)
		e = agentcli.ClampEffortToSupported(e, supported, def)
	}
	return m, e
}

func BuildAgyArgs(mode, modelID, effort, externalConversationID string) []string {
	agyMode := "accept-edits"
	if isReadOnlyMode(mode) {
		agyMode = "plan"
	}
	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--sandbox",
		"--mode", agyMode,
	}
	resolvedModel, resolvedEffort := ResolveAgyModelAndEffort(modelID, effort)
	if resolvedModel != "" && !strings.EqualFold(resolvedModel, "auto") {
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
	if isReadOnlyMode(mode) {
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
	if e := agentcli.NormalizeEffortName(effort); e != "" {
		efforts, defEffort, supportsEffort := inferClaudeModelEfforts(m)
		if supportsEffort {
			if clamped := agentcli.ClampEffortToSupported(e, efforts, defEffort); clamped != "" {
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
	return EncodeStreamInputTurnWithParts(runtime, promptText, nil)
}

func EncodeStreamInputTurnWithParts(runtime, promptText string, parts []MessagePart) ([]byte, error) {
	if runtime == string(RuntimeGeminiCLI) {
		return []byte(promptText + "\n"), nil
	}
	if runtime == string(RuntimeClaudeCode) && len(parts) > 0 {
		var contentBlocks []map[string]interface{}
		for _, p := range parts {
			if p.Type != "file" {
				continue
			}
			mimeType, b64Data, _, ok := parseBase64DataURL(p.URL, p.Mime)
			if !ok {
				continue
			}
			contentBlocks = append(contentBlocks, map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type":       "base64",
					"media_type": mimeType,
					"data":       b64Data,
				},
			})
		}
		if len(contentBlocks) > 0 {
			contentBlocks = append(contentBlocks, map[string]interface{}{
				"type": "text",
				"text": promptText,
			})
			payload := map[string]interface{}{
				"type": "user",
				"message": map[string]interface{}{
					"role":    "user",
					"content": contentBlocks,
				},
			}
			b, err := json.Marshal(payload)
			return append(b, '\n'), err
		}
	}
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
	steered   bool
	idleCh    chan struct{}
	cancel    func()
	mu        sync.Mutex
	stall         stallState
	toolFails     toolFailureStreak
	reasoningLoop openCodeReasoningGuard
	pendingErr    *SSEEvent
	// openCodeChildren holds the OpenCode subagent session IDs spawned by
	// this turn, so their activity counts toward the parent's watchdog.
	openCodeChildren sync.Map
	fileURLs         sync.Map
}

var abortInterruptGrace = 2 * time.Second

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
	// Stop means the whole turn: whatever survives the interrupt (background
	// jobs that ignore SIGINT, tool commands in their own process groups) is
	// killed with the group and the process tree captured before it.
	tree := descendantPIDs(proc.Pid)
	_ = signalProcessGroup(proc, os.Interrupt)
	select {
	case <-ap.done:
	case <-time.After(abortInterruptGrace):
		tree = append(tree, descendantPIDs(proc.Pid)...)
	}
	_ = signalProcessGroup(proc, os.Kill)
	killPIDs(tree)
}

func (ap *activeProcess) wasAborted() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.aborted
}

func (ap *activeProcess) markSteered() {
	ap.mu.Lock()
	ap.steered = true
	ap.mu.Unlock()
}

func (ap *activeProcess) wasSteered() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.steered
}

func (ap *activeProcess) notifyIdle() {
	if ap.idleCh == nil {
		return
	}
	select {
	case ap.idleCh <- struct{}{}:
	default:
	}
}

func (ap *activeProcess) setPendingError(evt SSEEvent) {
	ap.mu.Lock()
	ap.pendingErr = &evt
	ap.mu.Unlock()
}

func (ap *activeProcess) hasPendingError() bool {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return ap.pendingErr != nil
}

func (ap *activeProcess) takePendingError() (SSEEvent, bool) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if ap.pendingErr == nil {
		return SSEEvent{}, false
	}
	evt := *ap.pendingErr
	ap.pendingErr = nil
	return evt, true
}

func (s *Service) releaseActiveProcess(sessionID string, ap *activeProcess) {
	s.mu.Lock()
	if s.procs[sessionID] == ap {
		delete(s.procs, sessionID)
	}
	s.mu.Unlock()
	if s.status != nil {
		s.status.SetSessionBusy(sessionID, false)
	}
}

func readStreamLines(r io.Reader, onLine func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		onLine(scanner.Text())
	}
}
