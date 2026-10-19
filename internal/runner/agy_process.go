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
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/claudeconfig"
)

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

func buildAgyModelItems(slugs []string, displayBySlug map[string]string) []RunnerModelItem {
	items := make([]RunnerModelItem, 0, len(slugs))
	for _, s := range slugs {
		name := displayBySlug[s]
		if name == "" {
			name = s
		}
		lower := strings.ToLower(s)
		reasoning := strings.Contains(lower, "thinking") ||
			strings.Contains(lower, "argon") ||
			strings.Contains(lower, "opus") ||
			strings.Contains(lower, "sonnet") ||
			strings.Contains(lower, "oss")
		items = append(items, RunnerModelItem{
			ID:         s,
			Name:       name,
			ProviderID: string(RuntimeAntigravity),
			Runtime:    string(RuntimeAntigravity),
			Reasoning:  reasoning,
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
		items = append(items, RunnerModelItem{
			ID:         s,
			Name:       name,
			ProviderID: string(RuntimeClaudeCode),
			Runtime:    string(RuntimeClaudeCode),
			Reasoning:  true,
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
	if m := strings.TrimSpace(modelID); m != "" {
		args = append(args, "--model", m)
	}
	if e := strings.TrimSpace(effort); e != "" {
		args = append(args, "--effort", e)
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
	if m := strings.TrimSpace(modelID); m != "" {
		args = append(args, "--model", m)
	}
	if e := strings.TrimSpace(effort); e != "" {
		args = append(args, "--effort", e)
	}
	if sid := strings.TrimSpace(externalSessionID); sid != "" {
		args = append(args, "--resume", sid)
	}
	return args
}

func EncodeStreamInputTurn(runtime, promptText string) ([]byte, error) {
	if runtime == string(RuntimeClaudeCode) {
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

func lookupAgyTranscriptThinking(conversationID string, stepIndex int) string {
	home, _ := os.UserHomeDir()
	if home == "" || conversationID == "" {
		return ""
	}
	roots := []string{
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", conversationID, ".system_generated", "logs"),
		filepath.Join(home, ".gemini", "antigravity", "brain", conversationID, ".system_generated", "logs"),
	}
	for _, dir := range roots {
		for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
			f, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				var row struct {
					StepIndex int    `json:"step_index"`
					Thinking  string `json:"thinking"`
				}
				if json.Unmarshal(bytes.TrimSpace(scanner.Bytes()), &row) == nil && row.StepIndex == stepIndex && strings.TrimSpace(row.Thinking) != "" {
					_ = f.Close()
					return strings.TrimSpace(row.Thinking)
				}
			}
			_ = f.Close()
		}
	}
	return ""
}
