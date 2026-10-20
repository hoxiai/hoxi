package opencode

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
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
		cmd.Env = append(os.Environ(),
			"CI=1",
			"NO_COLOR=1",
			"GIT_TERMINAL_PROMPT=0",
			"OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL=1",
		)
		out, err := cmd.CombinedOutput()
		cancel()

		ver := ""
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			ver = strings.TrimSpace(lines[0])
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

func StartDaemonWithBin(
	ctx context.Context,
	binPath, rootDir, configPath string,
	port int,
	password string,
	logger *slog.Logger,
	extraEnv ...string,
) (*exec.Cmd, error) {
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
	logger.Info("started opencode daemon", "bin", binPath, "pid", child.Process.Pid, "port", port)

	if err := WaitForHealth(ctx, port, password, 15*time.Second); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		return nil, err
	}
	return child, nil
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

func ReapOrphanedDaemons(logger *slog.Logger) {
	if runtime.GOOS == "windows" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-ax", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return
	}
	reaped := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, errPid := strconv.Atoi(fields[0])
		ppid, errPpid := strconv.Atoi(fields[1])
		if errPid != nil || errPpid != nil || ppid != 1 || pid <= 1 {
			continue
		}
		cmdStr := strings.Join(fields[2:], " ")
		if strings.Contains(cmdStr, "opencode serve --hostname 127.0.0.1") {
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Kill(); err == nil {
					reaped++
				}
			}
		}
	}
	if reaped > 0 && logger != nil {
		logger.Info("reaped orphaned opencode serve daemons", "count", reaped)
	}
}
