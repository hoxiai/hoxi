package opencode

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	cachedOpenCodeVerMu   sync.Mutex
	cachedOpenCodeVerPath string
	cachedOpenCodeVerStr  string
)

func ResolveVersion(binPath string) string {
	if binPath == "" {
		return ""
	}
	cachedOpenCodeVerMu.Lock()
	if cachedOpenCodeVerPath == binPath && cachedOpenCodeVerStr != "" {
		ver := cachedOpenCodeVerStr
		cachedOpenCodeVerMu.Unlock()
		return ver
	}
	cachedOpenCodeVerMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	ver := strings.TrimSpace(lines[0])
	if ver != "" {
		cachedOpenCodeVerMu.Lock()
		cachedOpenCodeVerPath = binPath
		cachedOpenCodeVerStr = ver
		cachedOpenCodeVerMu.Unlock()
	}
	return ver
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
	child.Env = append(os.Environ(),
		"OPENCODE_SERVER_HOSTNAME=127.0.0.1",
		"OPENCODE_SERVER_PORT="+strconv.Itoa(port),
		"OPENCODE_SERVER_USERNAME=opencode",
		"OPENCODE_SERVER_PASSWORD="+password,
		"OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL=1",
		"NO_PROXY=127.0.0.1,localhost",
	)
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
	client := &http.Client{
		Timeout:   1500 * time.Millisecond,
		Transport: &http.Transport{Proxy: nil},
	}
	defer client.CloseIdleConnections()
	healthURL := "http://127.0.0.1:" + strconv.Itoa(port) + "/global/health"
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		req.SetBasicAuth("opencode", password)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
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
