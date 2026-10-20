package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxVerifyOutputBytes = 32 * 1024
	maxSetupOutputBytes  = 16 * 1024
	verifyConfigFileName = "worktree-verify.json"
)

var localServerURLPattern = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d+)(/[^\s"'<>)\]]*)?`)

type WorktreeVerifyConfig struct {
	RunCommand  string `json:"runCommand"`
	TestCommand string `json:"testCommand"`
}

type WorktreeVerifyState struct {
	SessionID     string `json:"sessionID"`
	Directory     string `json:"directory"`
	SetupScript   string `json:"setupScript,omitempty"`
	SetupStatus   string `json:"setupStatus"` // "none" | "running" | "succeeded" | "failed"
	SetupOutput   string `json:"setupOutput,omitempty"`
	SetupError    string `json:"setupError,omitempty"`
	RunCommand    string `json:"runCommand,omitempty"`
	TestCommand   string `json:"testCommand,omitempty"`
	ActiveKind    string `json:"activeKind,omitempty"` // "" | "setup" | "run" | "test"
	RunStatus     string `json:"runStatus"`            // "idle" | "running" | "succeeded" | "failed" | "stopped"
	AllocatedPort int    `json:"allocatedPort,omitempty"`
	PreviewURL    string `json:"previewURL,omitempty"`
	Output        string `json:"output,omitempty"`
	ExitCode      int    `json:"exitCode,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}

type verifySessionEntry struct {
	mu            sync.Mutex
	sessionID     string
	directory     string
	setupScript   string
	setupStatus   string
	setupOutput   string
	setupError    string
	activeKind    string
	runStatus     string
	allocatedPort int
	previewURL    string
	outputBuf     bytes.Buffer
	exitCode      int
	updatedAt     string
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	done          chan struct{}
}

type verifyTracker struct {
	mu      sync.Mutex
	configM sync.Mutex
	entries map[string]*verifySessionEntry // keyed by canonical directory or sessionID
}

func newVerifyTracker() *verifyTracker {
	return &verifyTracker{
		entries: make(map[string]*verifySessionEntry),
	}
}

func (vt *verifyTracker) entryFor(sessionID, directory string) *verifySessionEntry {
	key := strings.TrimSpace(directory)
	if key == "" {
		key = strings.TrimSpace(sessionID)
	}
	vt.mu.Lock()
	defer vt.mu.Unlock()
	ent, ok := vt.entries[key]
	if !ok {
		ent = &verifySessionEntry{
			sessionID:     sessionID,
			directory:     directory,
			setupStatus:   "none",
			runStatus:     "idle",
			allocatedPort: allocateWorktreePort(sessionID),
		}
		vt.entries[key] = ent
	} else {
		if sessionID != "" {
			ent.sessionID = sessionID
		}
		if directory != "" {
			ent.directory = directory
		}
		if ent.allocatedPort == 0 {
			ent.allocatedPort = allocateWorktreePort(sessionID)
		}
	}
	return ent
}

func (vt *verifyTracker) StopDirectory(directory string) {
	key := strings.TrimSpace(directory)
	if key == "" {
		return
	}
	vt.mu.Lock()
	ent := vt.entries[key]
	vt.mu.Unlock()
	if ent == nil {
		return
	}
	ent.stopActive()
}

func (ent *verifySessionEntry) appendOutputLocked(p []byte) {
	if len(p) == 0 {
		return
	}
	ent.outputBuf.Write(p)
	if ent.outputBuf.Len() > maxVerifyOutputBytes {
		raw := ent.outputBuf.Bytes()
		keep := raw[len(raw)-maxVerifyOutputBytes:]
		ent.outputBuf.Reset()
		ent.outputBuf.Write(keep)
	}
	if detected := detectLocalPreviewURL(string(p)); detected != "" {
		ent.previewURL = detected
	}
	ent.updatedAt = time.Now().UTC().Format(time.RFC3339)
}

func (ent *verifySessionEntry) stopActive() {
	ent.mu.Lock()
	cmd := ent.cmd
	cancel := ent.cancel
	done := ent.done
	if cmd != nil {
		ent.runStatus = "stopped"
		ent.updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	ent.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		killVerifyProcessGroup(cmd)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

func (ent *verifySessionEntry) snapshot(cfg WorktreeVerifyConfig, defaultSetupScript string) WorktreeVerifyState {
	ent.mu.Lock()
	defer ent.mu.Unlock()
	setupScript := ent.setupScript
	if setupScript == "" && defaultSetupScript != "" {
		setupScript = filepath.Base(defaultSetupScript)
	}
	setupStatus := ent.setupStatus
	if setupStatus == "" {
		setupStatus = "none"
	}
	runStatus := ent.runStatus
	if runStatus == "" {
		runStatus = "idle"
	}
	return WorktreeVerifyState{
		SessionID:     ent.sessionID,
		Directory:     ent.directory,
		SetupScript:   setupScript,
		SetupStatus:   setupStatus,
		SetupOutput:   ent.setupOutput,
		SetupError:    ent.setupError,
		RunCommand:    cfg.RunCommand,
		TestCommand:   cfg.TestCommand,
		ActiveKind:    ent.activeKind,
		RunStatus:     runStatus,
		AllocatedPort: ent.allocatedPort,
		PreviewURL:    ent.previewURL,
		Output:        ent.outputBuf.String(),
		ExitCode:      ent.exitCode,
		UpdatedAt:     ent.updatedAt,
	}
}

type entryOutputWriter struct {
	ent *verifySessionEntry
}

func (w entryOutputWriter) Write(p []byte) (int, error) {
	w.ent.mu.Lock()
	w.ent.appendOutputLocked(p)
	w.ent.mu.Unlock()
	return len(p), nil
}

func detectLocalPreviewURL(chunk string) string {
	matches := localServerURLPattern.FindStringSubmatch(chunk)
	if len(matches) < 2 {
		return ""
	}
	full := matches[0]
	full = strings.Replace(full, "://0.0.0.0:", "://127.0.0.1:", 1)
	return strings.TrimRight(full, ".,;)")
}

func allocateWorktreePort(sessionID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(sessionID)))
	base := 3100 + int(h.Sum32()%800)
	for offset := 0; offset < 40; offset++ {
		cand := base + offset
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(cand))
		if err == nil {
			_ = ln.Close()
			return cand
		}
	}
	return base
}

func detectDefaultVerifyConfig(dir string) WorktreeVerifyConfig {
	var cfg WorktreeVerifyConfig
	if strings.TrimSpace(dir) == "" {
		return cfg
	}
	pkgData, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(pkgData, &pkg) == nil && len(pkg.Scripts) > 0 {
			pm := "npm"
			if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
				pm = "pnpm"
			} else if _, err := os.Stat(filepath.Join(dir, "yarn.lock")); err == nil {
				pm = "yarn"
			} else if _, err := os.Stat(filepath.Join(dir, "bun.lockb")); err == nil {
				pm = "bun"
			} else if _, err := os.Stat(filepath.Join(dir, "bun.lock")); err == nil {
				pm = "bun"
			}
			if _, ok := pkg.Scripts["dev"]; ok {
				cfg.RunCommand = pm + " run dev"
			} else if _, ok := pkg.Scripts["start"]; ok {
				cfg.RunCommand = pm + " run start"
			}
			if _, ok := pkg.Scripts["test"]; ok {
				cfg.TestCommand = pm + " test"
			}
			return cfg
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		cfg.TestCommand = "go test ./..."
		cfg.RunCommand = "go run ."
		return cfg
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		cfg.TestCommand = "cargo test"
		cfg.RunCommand = "cargo run"
		return cfg
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		cfg.TestCommand = "pytest"
		return cfg
	}
	return cfg
}

func (m *worktreeManager) verifyConfigPath() string {
	if strings.TrimSpace(m.rootDir) == "" {
		return ""
	}
	return filepath.Join(m.rootDir, ".hoxi", verifyConfigFileName)
}

func (m *worktreeManager) LoadVerifyConfig(targetDir string) WorktreeVerifyConfig {
	defaults := detectDefaultVerifyConfig(targetDir)
	if defaults.RunCommand == "" && defaults.TestCommand == "" && m.rootDir != targetDir {
		defaults = detectDefaultVerifyConfig(m.rootDir)
	}
	cfgPath := m.verifyConfigPath()
	if cfgPath == "" {
		return defaults
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return defaults
	}
	var saved WorktreeVerifyConfig
	if json.Unmarshal(data, &saved) != nil {
		return defaults
	}
	if strings.TrimSpace(saved.RunCommand) != "" {
		defaults.RunCommand = strings.TrimSpace(saved.RunCommand)
	}
	if strings.TrimSpace(saved.TestCommand) != "" {
		defaults.TestCommand = strings.TrimSpace(saved.TestCommand)
	}
	return defaults
}

func (m *worktreeManager) SaveVerifyConfig(cfg WorktreeVerifyConfig) (WorktreeVerifyConfig, error) {
	cfgPath := m.verifyConfigPath()
	if cfgPath == "" {
		return cfg, errors.New("workspace root directory is not configured")
	}
	if m.roots != nil && m.roots.verify != nil {
		m.roots.verify.configM.Lock()
		defer m.roots.verify.configM.Unlock()
	}
	hoxiDir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(hoxiDir, 0o755); err != nil {
		return cfg, err
	}
	ensureGitExclude(m.rootDir, ".hoxi/")
	normalized := WorktreeVerifyConfig{
		RunCommand:  strings.TrimSpace(cfg.RunCommand),
		TestCommand: strings.TrimSpace(cfg.TestCommand),
	}
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return cfg, err
	}
	tmp, err := os.CreateTemp(hoxiDir, ".verify-cfg-*.tmp")
	if err != nil {
		return cfg, err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return cfg, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return cfg, err
	}
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		_ = os.Remove(tmpPath)
		return cfg, err
	}
	return m.LoadVerifyConfig(m.rootDir), nil
}

func (m *worktreeManager) resolveTargetDirAndBranch(sessionID string) (string, string, error) {
	sessionID = sanitizeSessionID(sessionID)
	m.mu.RLock()
	meta, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if ok && strings.TrimSpace(meta.Directory) != "" {
		return meta.Directory, meta.Branch, nil
	}
	if sessionID != "" && sessionID != "__root__" {
		candidate := filepath.Join(m.rootDir, ".hoxi", "worktrees", sessionID)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			br, _ := runGit(candidate, "rev-parse", "--abbrev-ref", "HEAD")
			return candidate, br, nil
		}
	}
	if strings.TrimSpace(m.rootDir) == "" {
		return "", "", errors.New("workspace directory is required")
	}
	br, _ := runGit(m.rootDir, "rev-parse", "--abbrev-ref", "HEAD")
	return m.rootDir, br, nil
}

func (m *worktreeManager) VerifyStatus(sessionID string) (WorktreeVerifyState, error) {
	dir, _, err := m.resolveTargetDirAndBranch(sessionID)
	if err != nil {
		return WorktreeVerifyState{}, err
	}
	cfg := m.LoadVerifyConfig(dir)
	scriptPath := resolveWorktreeSetupScript(m.rootDir)
	if m.roots == nil || m.roots.verify == nil {
		return WorktreeVerifyState{
			SessionID:   sessionID,
			Directory:   dir,
			SetupScript: filepath.Base(scriptPath),
			SetupStatus: "none",
			RunCommand:  cfg.RunCommand,
			TestCommand: cfg.TestCommand,
			RunStatus:   "idle",
		}, nil
	}
	ent := m.roots.verify.entryFor(sessionID, dir)
	return ent.snapshot(cfg, scriptPath), nil
}

func (m *worktreeManager) RetrySetup(sessionID string) (WorktreeVerifyState, error) {
	dir, branch, err := m.resolveTargetDirAndBranch(sessionID)
	if err != nil {
		return WorktreeVerifyState{}, err
	}
	scriptPath := resolveWorktreeSetupScript(m.rootDir)
	if scriptPath == "" {
		return m.VerifyStatus(sessionID)
	}
	ent := m.roots.verify.entryFor(sessionID, dir)
	ent.mu.Lock()
	port := ent.allocatedPort
	ent.setupScript = filepath.Base(scriptPath)
	ent.setupStatus = "running"
	ent.setupError = ""
	ent.updatedAt = time.Now().UTC().Format(time.RFC3339)
	ent.mu.Unlock()

	res := executeWorktreeSetupScript(m.rootDir, dir, sessionID, branch, port)
	ent.mu.Lock()
	ent.setupScript = res.Script
	ent.setupStatus = res.Status
	ent.setupOutput = res.Output
	ent.setupError = res.Error
	ent.updatedAt = time.Now().UTC().Format(time.RFC3339)
	ent.mu.Unlock()

	m.mu.Lock()
	if meta, ok := m.sessions[sanitizeSessionID(sessionID)]; ok {
		meta.SetupScript = res.Script
		meta.SetupStatus = res.Status
		meta.SetupError = res.Error
		meta.AllocatedPort = port
		m.sessions[sanitizeSessionID(sessionID)] = meta
	}
	m.mu.Unlock()

	return m.VerifyStatus(sessionID)
}

func (m *worktreeManager) StartVerify(sessionID, kind, customCommand string) (WorktreeVerifyState, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "setup" {
		return m.RetrySetup(sessionID)
	}
	if kind != "run" && kind != "test" {
		return WorktreeVerifyState{}, errors.New("verify kind must be 'run', 'test', or 'setup'")
	}
	dir, branch, err := m.resolveTargetDirAndBranch(sessionID)
	if err != nil {
		return WorktreeVerifyState{}, err
	}
	cfg := m.LoadVerifyConfig(dir)
	cmdStr := strings.TrimSpace(customCommand)
	if cmdStr == "" {
		if kind == "run" {
			cmdStr = cfg.RunCommand
		} else {
			cmdStr = cfg.TestCommand
		}
	}
	if cmdStr == "" {
		return WorktreeVerifyState{}, fmt.Errorf("no %s command configured for this project", kind)
	}

	ent := m.roots.verify.entryFor(sessionID, dir)
	ent.stopActive()

	ent.mu.Lock()
	port := ent.allocatedPort
	if port == 0 {
		port = allocateWorktreePort(sessionID)
		ent.allocatedPort = port
	}
	ent.outputBuf.Reset()
	ent.activeKind = kind
	ent.runStatus = "running"
	ent.previewURL = ""
	ent.exitCode = 0
	ent.updatedAt = time.Now().UTC().Format(time.RFC3339)
	ent.appendOutputLocked([]byte(fmt.Sprintf("$ %s (PORT=%d)\n", cmdStr, port)))

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = dir
	setVerifyProcessGroup(cmd)
	extraEnv := []string{
		"PORT=" + strconv.Itoa(port),
		"HOXI_PORT=" + strconv.Itoa(port),
		"HOXI_WORKTREE_DIR=" + dir,
		"HOXI_REPO_ROOT=" + m.rootDir,
		"HOXI_SESSION_ID=" + sanitizeSessionID(sessionID),
		"HOXI_BRANCH=" + branch,
		"BROWSER=none",
	}
	if kind == "test" {
		extraEnv = append(extraEnv, "CI=1")
	}
	cmd.Env = nonInteractiveGitEnv(extraEnv...)
	writer := entryOutputWriter{ent: ent}
	cmd.Stdout = writer
	cmd.Stderr = writer

	if err := cmd.Start(); err != nil {
		cancel()
		ent.runStatus = "failed"
		ent.exitCode = 1
		ent.appendOutputLocked([]byte(err.Error() + "\n"))
		ent.mu.Unlock()
		return ent.snapshot(cfg, resolveWorktreeSetupScript(m.rootDir)), err
	}

	done := make(chan struct{})
	ent.cmd = cmd
	ent.cancel = cancel
	ent.done = done
	ent.mu.Unlock()

	go func(c *exec.Cmd, d chan struct{}) {
		waitErr := c.Wait()
		close(d)
		ent.mu.Lock()
		defer ent.mu.Unlock()
		if ent.cmd != c {
			return
		}
		ent.cmd = nil
		ent.cancel = nil
		if ent.runStatus == "stopped" {
			return
		}
		if waitErr != nil {
			ent.runStatus = "failed"
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				ent.exitCode = exitErr.ExitCode()
			} else {
				ent.exitCode = 1
			}
		} else {
			ent.runStatus = "succeeded"
			ent.exitCode = 0
		}
		ent.updatedAt = time.Now().UTC().Format(time.RFC3339)
	}(cmd, done)

	// Wait briefly so fast commands (or immediate startup errors / fast unit tests) settle right away
	waitDuration := 350 * time.Millisecond
	if kind == "test" {
		waitDuration = 800 * time.Millisecond
	}
	select {
	case <-done:
	case <-time.After(waitDuration):
	}

	return ent.snapshot(cfg, resolveWorktreeSetupScript(m.rootDir)), nil
}

func (m *worktreeManager) StopVerify(sessionID string) (WorktreeVerifyState, error) {
	dir, _, err := m.resolveTargetDirAndBranch(sessionID)
	if err != nil {
		return WorktreeVerifyState{}, err
	}
	if m.roots != nil && m.roots.verify != nil {
		ent := m.roots.verify.entryFor(sessionID, dir)
		ent.stopActive()
	}
	return m.VerifyStatus(sessionID)
}
