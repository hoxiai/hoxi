package statusbar

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

var persistKeepAwakeMu sync.Mutex

type persistedKeepAwakeConfig struct {
	Enabled bool `json:"enabled"`
}

func keepAwakeConfigPath() string {
	if custom := strings.TrimSpace(os.Getenv("HOXI_KEEP_AWAKE_CONFIG")); custom != "" {
		return custom
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "keep-awake.json")
}

func loadPersistedKeepAwakeEnabled() bool {
	path := keepAwakeConfigPath()
	if path == "" {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var cfg persistedKeepAwakeConfig
	if json.Unmarshal(data, &cfg) != nil {
		return true
	}
	return cfg.Enabled
}

func savePersistedKeepAwakeEnabled(enabled bool) error {
	path := keepAwakeConfigPath()
	if path == "" {
		return nil
	}
	persistKeepAwakeMu.Lock()
	defer persistKeepAwakeMu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(persistedKeepAwakeConfig{Enabled: enabled}, "", "  ")
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, ".keep-awake-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(raw); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// KeepAwakeSupported reports whether the host OS supports idle sleep prevention.
func KeepAwakeSupported() bool {
	return runtime.GOOS == "darwin"
}

func (t *Tracker) KeepAwakeEnabled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.keepAwakeEnabled
}

func (t *Tracker) KeepAwakeActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.keepAwakeCmd != nil || t.keepAwakeActive
}

func (t *Tracker) SetKeepAwakeEnabled(enabled bool) {
	_ = savePersistedKeepAwakeEnabled(enabled)
	t.mu.Lock()
	t.keepAwakeEnabled = enabled
	t.syncKeepAwakeLocked()
	summary, pendingIDs, runningIDs, resolver := t.snapshotWithTargetsLocked()
	t.mu.Unlock()
	enrichSummary(&summary, pendingIDs, runningIDs, resolver)
	Update(summary)
}

// syncKeepAwakeLocked starts or stops the macOS caffeinate subprocess while holding t.mu.
func (t *Tracker) syncKeepAwakeLocked() {
	shouldWake := t.keepAwakeEnabled && len(t.busySessions) > 0
	if !shouldWake {
		t.stopKeepAwakeLocked()
		return
	}
	if t.keepAwakeActive {
		return
	}
	if t.startKeepAwakeHook != nil {
		stopFn, err := t.startKeepAwakeHook()
		if err == nil {
			t.keepAwakeStopHook = stopFn
			t.keepAwakeActive = true
		}
		return
	}
	if runtime.GOOS != "darwin" {
		return
	}
	bin, err := exec.LookPath("caffeinate")
	if err != nil || bin == "" {
		return
	}
	// -i prevents system idle sleep (display can still dim/lock);
	// -w binds caffeinate to Hoxi's PID so it exits automatically if Hoxi terminates.
	cmd := exec.Command(bin, "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return
	}
	t.keepAwakeCmd = cmd
	t.keepAwakeActive = true
	go func(c *exec.Cmd) {
		_ = c.Wait()
		t.mu.Lock()
		if t.keepAwakeCmd == c {
			t.keepAwakeCmd = nil
			t.keepAwakeActive = false
		}
		t.mu.Unlock()
	}(cmd)
}

func (t *Tracker) stopKeepAwakeLocked() {
	if t.keepAwakeStopHook != nil {
		stopFn := t.keepAwakeStopHook
		t.keepAwakeStopHook = nil
		t.keepAwakeActive = false
		stopFn()
	}
	if t.keepAwakeCmd != nil {
		cmd := t.keepAwakeCmd
		t.keepAwakeCmd = nil
		t.keepAwakeActive = false
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	t.keepAwakeActive = false
}
