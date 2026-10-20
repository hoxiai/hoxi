package opencode

import (
	"bytes"
	"context"
	"encoding/json"
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
)

const resolveVersionTimeout = 10 * time.Second

var (
	cachedOpenCodeVerMu    sync.Mutex
	cachedOpenCodeVersions = make(map[string]string)
	inflightOpenCodeVer    = make(map[string]chan struct{})
)

// ResolveHoxiDBPath returns the path to Hoxi's dedicated SQLite database (~/.hoxi/hoxi.db).
func ResolveHoxiDBPath() string {
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_DB")); custom != "" {
		return custom
	}
	if custom := strings.TrimSpace(os.Getenv("HOXI_DB_PATH")); custom != "" {
		p := filepath.Clean(custom)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	hoxiDir := filepath.Join(home, ".hoxi")
	_ = os.MkdirAll(hoxiDir, 0o700)
	return filepath.Join(hoxiDir, "hoxi.db")
}

// EnsureHoxiEngineDataDir prepares Hoxi's private engine data and state directories
// (~/.hoxi/engine-data and ~/.hoxi/engine-state) so Hoxi's managed opencode daemon
// keeps runtime state isolated under ~/.hoxi/.
func EnsureHoxiEngineDataDir() (dataDir, stateDir string) {
	if custom := strings.TrimSpace(os.Getenv("HOXI_ENGINE_DATA_DIR")); custom != "" {
		dataDir = filepath.Clean(custom)
		stateDir = filepath.Join(dataDir, "state")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		dataDir = filepath.Join(home, ".hoxi", "engine-data")
		stateDir = filepath.Join(home, ".hoxi", "engine-state")
	}
	if dataDir != "" {
		ocDataDir := filepath.Join(dataDir, "opencode")
		_ = os.MkdirAll(ocDataDir, 0o700)
		seedOpenCodeAuthIfNeeded(ocDataDir)
	}
	if stateDir != "" {
		_ = os.MkdirAll(filepath.Join(stateDir, "opencode"), 0o700)
	}
	return dataDir, stateDir
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

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\x00", ""), "'", "''") + "'"
}

// seedOpenCodeConsoleAccountIfNeeded copies the active OpenCode Console account
// (`account` and `account_state` tables, which power OpenCode Go and Personal
// subscriptions) from ~/.local/share/opencode/opencode.db into Hoxi's isolated
// hoxi.db before starting `opencode serve`.
func seedOpenCodeConsoleAccountIfNeeded(hoxiDBPath string) {
	hoxiDBPath = strings.TrimSpace(hoxiDBPath)
	if hoxiDBPath == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	srcDB := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if custom := strings.TrimSpace(os.Getenv("OPENCODE_EXTERNAL_DB")); custom != "" {
		srcDB = filepath.Clean(custom)
	}
	if filepath.Clean(srcDB) == filepath.Clean(hoxiDBPath) {
		return
	}
	if st, err := os.Stat(srcDB); err != nil || st.IsDir() {
		return
	}
	querySQL := `
SELECT json_object(
  'accounts', COALESCE((
    SELECT json_group_array(json_object(
      'id', COALESCE(a.id, ''),
      'email', COALESCE(a.email, ''),
      'url', COALESCE(a.url, ''),
      'accessToken', COALESCE(a.access_token, ''),
      'refreshToken', COALESCE(a.refresh_token, ''),
      'tokenExpiry', COALESCE(a.token_expiry, 0),
      'timeCreated', COALESCE(a.time_created, 0),
      'timeUpdated', COALESCE(a.time_updated, 0)
    ))
    FROM account a
  ), json('[]')),
  'activeAccountID', COALESCE((SELECT active_account_id FROM account_state WHERE id = 1 LIMIT 1), ''),
  'activeOrgID', COALESCE((SELECT active_org_id FROM account_state WHERE id = 1 LIMIT 1), '')
);`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-cmd", ".timeout 3000", srcDB, querySQL)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return
	}
	var payload struct {
		Accounts []struct {
			ID           string `json:"id"`
			Email        string `json:"email"`
			URL          string `json:"url"`
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			TokenExpiry  int64  `json:"tokenExpiry"`
			TimeCreated  int64  `json:"timeCreated"`
			TimeUpdated  int64  `json:"timeUpdated"`
		} `json:"accounts"`
		ActiveAccountID string `json:"activeAccountID"`
		ActiveOrgID     string `json:"activeOrgID"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &payload) != nil || len(payload.Accounts) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString("BEGIN IMMEDIATE;\n")
	sb.WriteString("CREATE TABLE IF NOT EXISTS `account` (`id` text PRIMARY KEY, `email` text NOT NULL, `url` text NOT NULL, `access_token` text NOT NULL, `refresh_token` text NOT NULL, `token_expiry` integer, `time_created` integer NOT NULL, `time_updated` integer NOT NULL);\n")
	sb.WriteString("CREATE TABLE IF NOT EXISTS `account_state` (`id` integer PRIMARY KEY, `active_account_id` text, `active_org_id` text, CONSTRAINT `fk_account_state_active_account_id_account_id_fk` FOREIGN KEY (`active_account_id`) REFERENCES `account`(`id`) ON DELETE SET NULL);\n")
	validCount := 0
	for _, a := range payload.Accounts {
		id := strings.TrimSpace(a.ID)
		if id == "" || (strings.TrimSpace(a.AccessToken) == "" && strings.TrimSpace(a.RefreshToken) == "") {
			continue
		}
		validCount++
		fmt.Fprintf(&sb,
			"INSERT INTO `account` (`id`, `email`, `url`, `access_token`, `refresh_token`, `token_expiry`, `time_created`, `time_updated`) VALUES (%s, %s, %s, %s, %s, %d, %d, %d) ON CONFLICT(`id`) DO UPDATE SET `email`=excluded.`email`, `url`=excluded.`url`, `access_token`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR `account`.`access_token` = '' THEN excluded.`access_token` ELSE `account`.`access_token` END, `refresh_token`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR `account`.`refresh_token` = '' THEN excluded.`refresh_token` ELSE `account`.`refresh_token` END, `token_expiry`=CASE WHEN excluded.`time_updated` >= `account`.`time_updated` OR COALESCE(`account`.`token_expiry`, 0) = 0 THEN excluded.`token_expiry` ELSE `account`.`token_expiry` END, `time_updated`=MAX(`account`.`time_updated`, excluded.`time_updated`);\n",
			sqlQuote(id), sqlQuote(a.Email), sqlQuote(a.URL), sqlQuote(a.AccessToken), sqlQuote(a.RefreshToken), a.TokenExpiry, a.TimeCreated, a.TimeUpdated,
		)
	}
	if validCount == 0 {
		return
	}
	activeID := strings.TrimSpace(payload.ActiveAccountID)
	if activeID == "" {
		activeID = strings.TrimSpace(payload.Accounts[0].ID)
	}
	activeOrg := strings.TrimSpace(payload.ActiveOrgID)
	fmt.Fprintf(&sb,
		"INSERT INTO `account_state` (`id`, `active_account_id`, `active_org_id`) VALUES (1, %s, %s) ON CONFLICT(`id`) DO UPDATE SET `active_account_id`=excluded.`active_account_id`, `active_org_id`=CASE WHEN excluded.`active_org_id` != '' THEN excluded.`active_org_id` ELSE `account_state`.`active_org_id` END;\n",
		sqlQuote(activeID), sqlQuote(activeOrg),
	)
	sb.WriteString("COMMIT;\n")

	_ = os.MkdirAll(filepath.Dir(hoxiDBPath), 0o700)
	wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer wCancel()
	wCmd := exec.CommandContext(wCtx, "sqlite3", "-cmd", ".timeout 3000", hoxiDBPath)
	wCmd.WaitDelay = time.Second
	wCmd.Stdin = strings.NewReader(sb.String())
	_ = wCmd.Run()
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
	hoxiDBPath := ResolveHoxiDBPath()
	seedOpenCodeConsoleAccountIfNeeded(hoxiDBPath)
	dataDir, stateDir := EnsureHoxiEngineDataDir()
	child.Env = append(os.Environ(),
		"OPENCODE_SERVER_HOSTNAME=127.0.0.1",
		"OPENCODE_SERVER_PORT="+strconv.Itoa(port),
		"OPENCODE_SERVER_USERNAME=opencode",
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
