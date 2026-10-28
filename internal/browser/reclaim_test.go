//go:build darwin || linux

package browser

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/hoxiai/hoxi/internal/reclaim"
)

func testProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

const ownerLedgerEnv = "HOXI_BROWSER_OWNER_LEDGER"

// TestMain lets the test binary act as a Hoxi process that owns a browser.
func TestMain(m *testing.M) {
	if path := os.Getenv(ownerLedgerEnv); path != "" {
		runBrowserOwner(path)
		return
	}
	os.Exit(m.Run())
}

// runBrowserOwner starts one recorded Chrome, reports it, and waits to be killed.
func runBrowserOwner(ledgerPath string) {
	ledger, err := reclaim.Open(ledgerPath, nil)
	if err != nil {
		fmt.Println("error", err)
		os.Exit(1)
	}
	s := New()
	s.SetLedger(ledger)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sc, err := s.acquire(ctx, scopeKey{filepath.Dir(ledgerPath), "ses_owner"}, true)
	if err == nil {
		err = sc.start(ctx)
	}
	if err != nil {
		fmt.Println("error", err)
		os.Exit(1)
	}
	fmt.Printf("ready %d %s\n", chromedp.FromContext(sc.browser).Browser.Process().Pid, sc.profile)
	time.Sleep(time.Hour)
}

type browserOwner struct {
	cmd     *exec.Cmd
	chrome  int
	profile string
}

func startBrowserOwner(t *testing.T, ledgerPath string) *browserOwner {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), ownerLedgerEnv+"="+ledgerPath)
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
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-waited })
	_ = out.SetReadDeadline(time.Now().Add(45 * time.Second))
	line, err := bufio.NewReader(out).ReadString('\n')
	o := &browserOwner{cmd: cmd}
	if _, scanErr := fmt.Sscanf(line, "ready %d %s", &o.chrome, &o.profile); err != nil || scanErr != nil {
		t.Fatalf("owner did not start Chrome: %q %v %v", line, err, scanErr)
	}
	return o
}

// crash kills the owner the way kill -9 or a crash would: Close never runs.
func (o *browserOwner) crash(t *testing.T) {
	t.Helper()
	if err := o.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(o.cmd.Process.Pid, 0) == nil; {
		if time.Now().After(deadline) {
			t.Fatal("owner survived SIGKILL")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// profileProcesses counts processes whose argv names profile: Chrome and
// every helper it spawned carry --user-data-dir. Used only for assertions.
func profileProcesses(t *testing.T, profile string) int {
	t.Helper()
	out, err := exec.Command("ps", "-ax", "-ww", "-o", "command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line+" ", " --user-data-dir="+profile+" ") {
			n++
		}
	}
	return n
}

func waitNoProfileProcesses(t *testing.T, profile string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); profileProcesses(t, profile) > 0; {
		if time.Now().After(deadline) {
			t.Fatalf("%d Chrome processes still use %s", profileProcesses(t, profile), profile)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestReclaimsChromeOfKilledOwnerOnly(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	ledgerPath := filepath.Join(t.TempDir(), "reclaim.json")
	crashed := startBrowserOwner(t, ledgerPath)
	running := startBrowserOwner(t, ledgerPath) // e.g. the other build variant
	t.Cleanup(func() {
		// Never leave a real Chrome behind if an assertion fails.
		for _, o := range []*browserOwner{crashed, running} {
			_ = o.cmd.Process.Kill()
			_ = syscall.Kill(o.chrome, syscall.SIGKILL)
			_ = os.RemoveAll(o.profile)
		}
	})
	crashed.crash(t)
	if syscall.Kill(crashed.chrome, 0) != nil || profileProcesses(t, crashed.profile) == 0 {
		t.Fatal("expected Chrome to outlive its killed owner")
	}

	ledger, err := reclaim.Open(ledgerPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if r := ledger.Sweep(); r.Processes != 1 || r.Dirs != 1 || r.Skipped != 0 || r.Pending != 0 {
		t.Fatalf("report = %+v", r)
	}
	waitNoProfileProcesses(t, crashed.profile)
	if _, err := os.Stat(crashed.profile); !os.IsNotExist(err) {
		t.Fatalf("profile survived: %v", err)
	}
	if syscall.Kill(running.chrome, 0) != nil || profileProcesses(t, running.profile) == 0 {
		t.Fatal("Chrome of a running owner was reclaimed")
	}
	if _, err := os.Stat(running.profile); err != nil {
		t.Fatalf("profile of a running owner was removed: %v", err)
	}

	running.crash(t)
	if r := ledger.Sweep(); r.Processes != 1 || r.Dirs != 1 {
		t.Fatalf("report = %+v", r)
	}
	waitNoProfileProcesses(t, running.profile)
}

func TestCloseForgetsRecordedChrome(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	ledgerPath := filepath.Join(t.TempDir(), "reclaim.json")
	ledger, err := reclaim.Open(ledgerPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	s.SetLedger(ledger)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sc, err := s.acquire(ctx, scopeKey{t.TempDir(), "ses_close"}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = sc.start(ctx)
	profile := sc.profile
	<-sc.gate
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(ledgerPath); !strings.Contains(string(raw), `"arg": "--user-data-dir=`+profile+`"`) {
		s.Close()
		t.Fatalf("Chrome not recorded:\n%s", raw)
	}
	s.Close()
	if raw, _ := os.ReadFile(ledgerPath); strings.Contains(string(raw), profile) {
		t.Fatalf("record survived Close:\n%s", raw)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("profile survived Close: %v", err)
	}
}
