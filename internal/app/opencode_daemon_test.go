package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode"
)

const fakeEnginePassword = "fake-engine-password"

// The test binary doubles as a tiny `opencode serve` stand-in. Copying or
// symlinking it gives EnsureEngine a real process to start without touching
// ~/.hoxi or the network.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(runFakeOpenCodeDaemon(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func runFakeOpenCodeDaemon(args []string) int {
	port := 0
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--port" {
			parsed, err := strconv.Atoi(args[i+1])
			if err != nil {
				return 2
			}
			port = parsed
		}
	}
	if port == 0 {
		return 2
	}
	if logPath := os.Getenv("HOXI_FAKE_OPENCODE_LOG"); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"healthy":true}`)
	})
	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
	}
	if ms, err := strconv.Atoi(os.Getenv("HOXI_FAKE_OPENCODE_EXIT_AFTER_MS")); err == nil && ms > 0 {
		time.AfterFunc(time.Duration(ms)*time.Millisecond, func() { _ = srv.Close() })
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return 1
	}
	return 0
}

func fakeOpenCodeBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "opencode"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		raw, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0o755); err != nil {
			t.Fatal(err)
		}
		return target
	}
	if err := os.Symlink(exe, target); err != nil {
		t.Fatal(err)
	}
	return target
}

func newEngineTestApp(t *testing.T, binPath string, port int) *DesktopApp {
	t.Helper()
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_ENGINE_DATA_DIR", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", "")
	return &DesktopApp{
		cfg:          Config{RootDir: t.TempDir(), OpenCode: binPath},
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		runCtx:       context.Background(),
		openCodePort: port,
		openCodePass: fakeEnginePassword,
	}
}

func freeEnginePort(t *testing.T) int {
	t.Helper()
	port, err := chooseFreePort()
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func launchCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}

func waitForLaunches(t *testing.T, path string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if launchCount(path) >= want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("daemon launched %d times, want at least %d", launchCount(path), want)
}

func waitForEngineHealth(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	if err := opencode.WaitForHealth(context.Background(), port, fakeEnginePassword, timeout); err != nil {
		t.Fatalf("engine is not healthy: %v", err)
	}
}

// waitForRegisteredChild blocks until the supervisor owns the daemon, so a test
// cannot exit between a successful health probe and setChild and orphan it.
func waitForRegisteredChild(t *testing.T, a *DesktopApp, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.childMu.Lock()
		registered := a.child != nil
		a.childMu.Unlock()
		if registered {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon was never registered with the supervisor")
}

// EnsureEngine is the settings-page retry: a healthy daemon must be reused,
// never downloaded or started a second time.
func TestEnsureEngineIsIdempotent(t *testing.T) {
	bin := fakeOpenCodeBinary(t)
	logPath := filepath.Join(t.TempDir(), "launches.log")
	t.Setenv("HOXI_FAKE_OPENCODE_LOG", logPath)
	port := freeEnginePort(t)
	a := newEngineTestApp(t, bin, port)
	defer a.killChild()

	for attempt := 1; attempt <= 2; attempt++ {
		got, err := a.EnsureEngine(context.Background())
		if err != nil {
			t.Fatalf("EnsureEngine attempt %d: %v", attempt, err)
		}
		if got != bin {
			t.Fatalf("EnsureEngine attempt %d resolved %q, want %q", attempt, got, bin)
		}
	}
	if got := launchCount(logPath); got != 1 {
		t.Fatalf("daemon launched %d times, want 1", got)
	}
	waitForEngineHealth(t, port, 2*time.Second)
}

// A daemon that exits on its own is restarted by the supervisor with the same
// binary and endpoint.
func TestUnexpectedDaemonExitIsRespawned(t *testing.T) {
	bin := fakeOpenCodeBinary(t)
	logPath := filepath.Join(t.TempDir(), "launches.log")
	t.Setenv("HOXI_FAKE_OPENCODE_LOG", logPath)
	t.Setenv("HOXI_FAKE_OPENCODE_EXIT_AFTER_MS", "200")
	port := freeEnginePort(t)
	a := newEngineTestApp(t, bin, port)
	defer a.killChild()

	if _, err := a.EnsureEngine(context.Background()); err != nil {
		t.Fatalf("EnsureEngine: %v", err)
	}
	a.childMu.Lock()
	done := a.childDone
	a.childMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit as configured")
	}
	// The restart inherits the current environment; let it stay up.
	if err := os.Unsetenv("HOXI_FAKE_OPENCODE_EXIT_AFTER_MS"); err != nil {
		t.Fatal(err)
	}
	waitForLaunches(t, logPath, 2, respawnDelay+5*time.Second)
	waitForEngineHealth(t, port, 3*time.Second)
	waitForRegisteredChild(t, a, 3*time.Second)
}

// A pending automatic respawn must be abandoned when EnsureEngine restarts the
// daemon first; otherwise both would bind the same port and one would lose.
func TestEnsureEngineRestartInvalidatesPendingRespawn(t *testing.T) {
	bin := fakeOpenCodeBinary(t)
	logPath := filepath.Join(t.TempDir(), "launches.log")
	t.Setenv("HOXI_FAKE_OPENCODE_LOG", logPath)
	t.Setenv("HOXI_FAKE_OPENCODE_EXIT_AFTER_MS", "200")
	port := freeEnginePort(t)
	a := newEngineTestApp(t, bin, port)
	defer a.killChild()

	if _, err := a.EnsureEngine(context.Background()); err != nil {
		t.Fatalf("EnsureEngine: %v", err)
	}
	a.childMu.Lock()
	done := a.childDone
	a.childMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit as configured")
	}
	if err := os.Unsetenv("HOXI_FAKE_OPENCODE_EXIT_AFTER_MS"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureEngine(context.Background()); err != nil {
		t.Fatalf("EnsureEngine retry: %v", err)
	}
	// Wait past the old respawn delay; it must notice the new generation and
	// leave the manually started daemon alone.
	time.Sleep(respawnDelay + 750*time.Millisecond)
	if got := launchCount(logPath); got != 2 {
		t.Fatalf("daemon launched %d times, want 2 (initial + manual retry)", got)
	}
	waitForEngineHealth(t, port, 2*time.Second)
}

// A freshly installed binary that cannot run is rolled back to the previous
// managed one instead of leaving the user without an engine.
func TestStartResolvedEngineRollsBackBrokenBinary(t *testing.T) {
	bin := fakeOpenCodeBinary(t)
	backupPath := opencode.BinaryBackupPath(bin)
	if err := os.Rename(bin, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("not an executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freeEnginePort(t)
	a := newEngineTestApp(t, bin, port)
	defer a.killChild()

	child, err := a.startResolvedEngine(context.Background(), bin, port, fakeEnginePassword)
	if err != nil {
		t.Fatalf("startResolvedEngine: %v", err)
	}
	if child == nil {
		t.Fatal("expected the rolled-back daemon to start")
	}
	a.setChild(child)
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup must be consumed by the rollback, stat err=%v", err)
	}
	waitForEngineHealth(t, port, 3*time.Second)
}

// Without a backup the start failure is returned unchanged; nothing is invented.
func TestStartResolvedEngineReportsFailureWithoutBackup(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(bin, []byte("not an executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newEngineTestApp(t, bin, freeEnginePort(t))
	if _, err := a.startResolvedEngine(context.Background(), bin, a.openCodePort, fakeEnginePassword); err == nil {
		t.Fatal("expected the start failure to surface")
	}
}
