//go:build darwin || linux

package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode"
)

// reclaimOwnerEnv makes the test binary act as a Hoxi instance that starts
// an OpenCode daemon through the supervisor, prints its PID and then blocks.
// Its value is "<opencode binary>|<port>|<workspace root>".
const reclaimOwnerEnv = "HOXI_TEST_RECLAIM_OWNER"

func runReclaimOwner() int {
	fields := strings.Split(os.Getenv(reclaimOwnerEnv), "|")
	if len(fields) != 3 {
		return 2
	}
	bin, rootDir := fields[0], fields[2]
	port, err := strconv.Atoi(fields[1])
	if err != nil {
		return 2
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &DesktopApp{
		cfg:          Config{RootDir: rootDir, OpenCode: bin},
		logger:       logger,
		runCtx:       context.Background(),
		openCodePort: port,
		openCodePass: fakeEnginePassword,
		ledger:       openReclaimLedger(logger),
	}
	if a.ledger == nil {
		return 3
	}
	child, err := a.startOpenCode(context.Background(), bin, port, fakeEnginePassword)
	if err != nil {
		return 4
	}
	fmt.Printf("%d\n", child.Process.Pid)
	time.Sleep(time.Hour)
	return 0
}

type ownerProcess struct {
	cmd    *exec.Cmd
	daemon int
	done   chan struct{}
}

// startOwner launches a Hoxi stand-in whose daemon serves on port.
func startOwner(t *testing.T, bin string, port int) *ownerProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s|%d|%s", reclaimOwnerEnv, bin, port, t.TempDir()))
	out, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	cmd.Stdout = w
	err = cmd.Start()
	_ = w.Close()
	if err != nil {
		t.Fatal(err)
	}
	o := &ownerProcess{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(o.done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-o.done
	})
	_ = out.SetReadDeadline(time.Now().Add(20 * time.Second))
	line, err := bufio.NewReader(out).ReadString('\n')
	if o.daemon, err = strconv.Atoi(strings.TrimSpace(line)); err != nil || o.daemon <= 1 {
		t.Fatalf("owner did not start its daemon: %q %v", line, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(o.daemon, syscall.SIGKILL) })
	return o
}

func (o *ownerProcess) crash(t *testing.T) {
	t.Helper()
	_ = o.cmd.Process.Signal(syscall.SIGKILL)
	select {
	case <-o.done:
	case <-time.After(5 * time.Second):
		t.Fatal("owner did not exit")
	}
}

// startForeignDaemon starts an `opencode serve` that no Hoxi recorded, the way
// a user's own `nohup opencode serve` would: detached and adopted by init.
func startForeignDaemon(t *testing.T, bin string, port int) int {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c", `"$0" serve --hostname 127.0.0.1 --port "$1" >/dev/null 2>&1 & echo $!`, bin, strconv.Itoa(port)).Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("foreign daemon pid %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	waitForEngineHealth(t, port, 5*time.Second)
	return pid
}

func processRunning(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func recordedDaemonPIDs(t *testing.T) map[int]bool {
	t.Helper()
	raw, err := os.ReadFile(hoxipaths.ReclaimLedgerPath())
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Entries []struct {
			Kind    string `json:"kind"`
			Process *struct {
				PID int `json:"pid"`
			} `json:"process"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	pids := map[int]bool{}
	for _, e := range f.Entries {
		if e.Kind == "opencode-daemon" && e.Process != nil {
			pids[e.Process.PID] = true
		}
	}
	return pids
}

// A Hoxi killed with SIGKILL leaves its daemon behind; the next start stops
// exactly that daemon. A concurrently running variant's daemon and an
// unrecorded `opencode serve` are left alone.
func TestSweepReclaimsDaemonOfCrashedInstanceOnly(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_ENGINE_DATA_DIR", "")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("HOXI_DB_PATH", "")
	t.Setenv("HOXI_FAKE_OPENCODE_LOG", "")
	t.Setenv("HOXI_FAKE_OPENCODE_EXIT_AFTER_MS", "")
	bin := fakeOpenCodeBinary(t)
	crashedPort, livePort, foreignPort := freeEnginePort(t), freeEnginePort(t), freeEnginePort(t)

	crashed := startOwner(t, bin, crashedPort)
	running := startOwner(t, bin, livePort)
	foreign := startForeignDaemon(t, bin, foreignPort)
	if pids := recordedDaemonPIDs(t); len(pids) != 2 || !pids[crashed.daemon] || !pids[running.daemon] || pids[foreign] {
		t.Fatalf("recorded daemons = %v", pids)
	}

	crashed.crash(t)
	waitForEngineHealth(t, crashedPort, 2*time.Second)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sweepReclaimLedger(openReclaimLedger(logger), logger)

	deadline := time.Now().Add(5 * time.Second)
	for processRunning(crashed.daemon) {
		if time.Now().After(deadline) {
			t.Fatal("daemon of the crashed instance survived the sweep")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := opencode.WaitForHealth(context.Background(), crashedPort, fakeEnginePassword, 200*time.Millisecond); err == nil {
		t.Fatal("crashed instance's daemon still serves")
	}
	waitForEngineHealth(t, livePort, 2*time.Second)
	waitForEngineHealth(t, foreignPort, 2*time.Second)
	if !processRunning(running.daemon) || !processRunning(foreign) {
		t.Fatal("a daemon not owned by an exited Hoxi was stopped")
	}
	if pids := recordedDaemonPIDs(t); len(pids) != 1 || !pids[running.daemon] {
		t.Fatalf("recorded daemons after sweep = %v", pids)
	}
}
