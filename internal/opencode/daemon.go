package opencode

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
	"github.com/hoxiai/hoxi/internal/reclaim"
)

const resolveVersionTimeout = 10 * time.Second

var (
	cachedOpenCodeVerMu    sync.Mutex
	cachedOpenCodeVersions = make(map[string]string)
	inflightOpenCodeVer    = make(map[string]chan struct{})
)

// EnsureHoxiEngineDataDir prepares Hoxi's private engine data and state directories
// (see hoxipaths for the override order) so Hoxi's managed opencode daemon
// keeps runtime state isolated under ~/.hoxi/.
func EnsureHoxiEngineDataDir() (dataDir, stateDir string) {
	dataDir, stateDir = hoxipaths.EngineDataDir(), hoxipaths.EngineStateDir()
	if dataDir != "" {
		ocDataDir := hoxipaths.EngineOpenCodeDir()
		_ = os.MkdirAll(ocDataDir, 0o700)
		seedOpenCodeAuthIfNeeded(ocDataDir)
	}
	if stateDir != "" {
		_ = os.MkdirAll(filepath.Join(stateDir, "opencode"), 0o700)
	}
	return dataDir, stateDir
}

// ensureHoxiDB resolves the engine database and creates its parent directory.
func ensureHoxiDB() string {
	dbPath := hoxipaths.DBPath()
	if dbPath != "" {
		_ = os.MkdirAll(filepath.Dir(dbPath), 0o700)
	}
	return dbPath
}

func seedOpenCodeAuthIfNeeded(targetOpenCodeDir string) {
	dstAuth := filepath.Join(targetOpenCodeDir, "auth.json")
	if _, err := os.Stat(dstAuth); err == nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	srcAuth := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	if filepath.Clean(srcAuth) == filepath.Clean(dstAuth) {
		return
	}
	if raw, err := os.ReadFile(srcAuth); err == nil && len(raw) > 0 {
		_ = os.WriteFile(dstAuth, raw, 0o600)
	}
}

// envWithout returns env with the named keys removed (case-sensitive on unix).
func envWithout(env []string, keys ...string) []string {
	if len(keys) == 0 {
		return env
	}
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[key] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := drop[name]; ok {
			continue
		}
		out = append(out, entry)
	}
	return out
}

var (
	ansiEscapePattern  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-_]`)
	versionLinePattern = regexp.MustCompile(`(?i)^(?:opencode\s+)?v?\d+\.\d+\.\d+`)
)

// parseVersionOutput extracts the version line from CLI output that may carry
// ANSI escapes, leading warning lines or stack frames.
func parseVersionOutput(out []byte) string {
	cleaned := ansiEscapePattern.ReplaceAllString(string(out), "")
	var fallback string
	for _, line := range strings.Split(cleaned, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if versionLinePattern.MatchString(line) {
			return line
		}
		if fallback == "" && !isNoiseLine(line) {
			fallback = line
		}
	}
	return fallback
}

func isNoiseLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasPrefix(lower, "warning") ||
		strings.HasPrefix(lower, "at ") ||
		strings.HasPrefix(lower, "node:")
}

func ResolveVersion(binPath string) string {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return ""
	}
	key := filepath.Clean(binPath)
	for {
		cachedOpenCodeVerMu.Lock()
		if ver, ok := cachedOpenCodeVersions[key]; ok && ver != "" {
			cachedOpenCodeVerMu.Unlock()
			return ver
		}
		if waitCh, inflight := inflightOpenCodeVer[key]; inflight {
			cachedOpenCodeVerMu.Unlock()
			<-waitCh
			cachedOpenCodeVerMu.Lock()
			ver := cachedOpenCodeVersions[key]
			cachedOpenCodeVerMu.Unlock()
			return ver
		}
		done := make(chan struct{})
		inflightOpenCodeVer[key] = done
		cachedOpenCodeVerMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), resolveVersionTimeout)
		cmd := exec.CommandContext(ctx, binPath, "--version")
		// Drop inherited color-forcing vars: with FORCE_COLOR set the CLI ignores
		// NO_COLOR and prints an ANSI-colored warning on top of the version line.
		cmd.Env = append(envWithout(os.Environ(), "FORCE_COLOR", "CLICOLOR_FORCE", "CLICOLOR"),
			"CI=1",
			"NO_COLOR=1",
			"GIT_TERMINAL_PROMPT=0",
			"OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL=1",
		)
		out, err := cmd.CombinedOutput()
		cancel()

		ver := ""
		if err == nil {
			ver = parseVersionOutput(out)
		}

		cachedOpenCodeVerMu.Lock()
		if ver != "" {
			cachedOpenCodeVersions[key] = ver
		}
		delete(inflightOpenCodeVer, key)
		close(done)
		cachedOpenCodeVerMu.Unlock()
		return ver
	}
}

// Daemon is a started `opencode serve` child. It is recorded in the reclaim
// ledger while it runs, so a later Hoxi start can stop it if this process dies
// without stopping it first.
type Daemon struct {
	*exec.Cmd
	ledger   *reclaim.Ledger
	ledgerID string
}

// Wait reaps the daemon and then drops its ledger record. The record outlives
// the process, never the other way round.
func (d *Daemon) Wait() error {
	err := d.Cmd.Wait()
	d.ledger.Forget(d.ledgerID)
	return err
}

// StartDaemonWithBin starts `opencode serve` and waits until it is healthy. A
// non-nil ledger records the child, identified by its argv, until Wait reaps it.
func StartDaemonWithBin(
	ctx context.Context,
	binPath, rootDir, configPath string,
	port int,
	password string,
	ledger *reclaim.Ledger,
	logger *slog.Logger,
	extraEnv ...string,
) (*Daemon, error) {
	args := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)}
	child := exec.CommandContext(ctx, binPath, args...)
	daemonDir := strings.TrimSpace(rootDir)
	if daemonDir == "" || daemonDir == "/" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			daemonDir = home
		} else {
			daemonDir = os.TempDir()
		}
	}
	child.Dir = daemonDir
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	hoxiDBPath := ensureHoxiDB()
	// Mirror the user's OpenCode Console login (OpenCode Go / Personal
	// subscriptions) into the isolated engine database before it boots.
	ocstate.SeedConsoleAccount(hoxiDBPath)
	dataDir, stateDir := EnsureHoxiEngineDataDir()
	child.Env = append(os.Environ(),
		"OPENCODE_SERVER_HOSTNAME=127.0.0.1",
		"OPENCODE_SERVER_PORT="+strconv.Itoa(port),
		"OPENCODE_SERVER_USERNAME="+ocapi.Username,
		"OPENCODE_SERVER_PASSWORD="+password,
		"OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL=1",
		"NO_PROXY=127.0.0.1,localhost",
	)
	if hoxiDBPath != "" && strings.TrimSpace(os.Getenv("OPENCODE_DB")) == "" {
		child.Env = append(child.Env, "OPENCODE_DB="+hoxiDBPath)
	}
	if dataDir != "" && strings.TrimSpace(os.Getenv("XDG_DATA_HOME")) == "" {
		child.Env = append(child.Env, "XDG_DATA_HOME="+dataDir)
	}
	if stateDir != "" && strings.TrimSpace(os.Getenv("XDG_STATE_HOME")) == "" {
		child.Env = append(child.Env, "XDG_STATE_HOME="+stateDir)
	}
	if configPath != "" {
		child.Env = append(child.Env, "OPENCODE_CONFIG="+configPath)
	}
	child.Env = append(child.Env, extraEnv...)
	if err := child.Start(); err != nil {
		return nil, fmt.Errorf("start opencode serve (%s): %w", binPath, err)
	}
	daemon := &Daemon{Cmd: child, ledger: ledger}
	daemon.ledgerID = ledger.RecordProcess("opencode-daemon", child.Process.Pid, args...)
	logger.Info("started opencode daemon", "bin", binPath, "pid", child.Process.Pid, "port", port)

	if err := WaitForHealth(ctx, port, password, 15*time.Second); err != nil {
		_ = child.Process.Kill()
		_ = daemon.Wait()
		return nil, err
	}
	return daemon, nil
}

func WaitForHealth(ctx context.Context, port int, password string, timeout time.Duration) error {
	client := ocapi.NewForPort(port, password)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		status, _, err := client.Raw(probeCtx, http.MethodGet, "/global/health", nil, nil)
		cancel()
		if err == nil && status == http.StatusOK {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("opencode health check timed out on port %d after %s", port, timeout)
}
