//go:build windows

package subproc

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The test binary doubles as the helper processes below, picked by this
// variable: "sleeper" prints ready and sleeps; "tier" starts a tied sleeper,
// prints its PID once it is running, and sleeps until it is killed.
const helperRoleEnv = "HOXI_SUBPROC_TEST_ROLE"

func TestMain(m *testing.M) {
	switch os.Getenv(helperRoleEnv) {
	case "sleeper":
		fmt.Println("ready")
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	case "tier":
		child, out, err := startHelper("sleeper", true)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := waitReady(out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		fmt.Println(child.Process.Pid)
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func startHelper(role string, tied bool) (*exec.Cmd, io.Reader, error) {
	cmd := Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperRoleEnv+"="+role)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr
	if tied {
		var untied error
		if err := StartTied(cmd, func(e error) { untied = e }); err != nil {
			return nil, nil, err
		}
		if untied != nil {
			_ = cmd.Process.Kill()
			return nil, nil, fmt.Errorf("child not tied: %w", untied)
		}
	} else if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, out, nil
}

func readLine(r io.Reader, timeout time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		ch <- result{strings.TrimSpace(line), err}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-time.After(timeout):
		return "", fmt.Errorf("no output after %s", timeout)
	}
}

func waitReady(r io.Reader) error {
	line, err := readLine(r, 30*time.Second)
	if err != nil {
		return err
	}
	if line != "ready" {
		return fmt.Errorf("unexpected helper output %q", line)
	}
	return nil
}

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

func isInJob(t *testing.T, pid int, job windows.Handle) bool {
	t.Helper()
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	defer windows.CloseHandle(proc)
	var in int32
	if r, _, err := procIsProcessInJob.Call(uintptr(proc), uintptr(job), uintptr(unsafe.Pointer(&in))); r == 0 {
		t.Fatalf("IsProcessInJob: %v", err)
	}
	return in != 0
}

func TestStartTiedRunsChildInsideKillOnCloseJob(t *testing.T) {
	cmd, out, err := startHelper("sleeper", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	// Output proves the suspended child was resumed.
	if err := waitReady(out); err != nil {
		t.Fatal(err)
	}
	job, err := tiedJob()
	if err != nil {
		t.Fatal(err)
	}
	if !isInJob(t, cmd.Process.Pid, job) {
		t.Fatal("tied child is not in the job")
	}
	if isInJob(t, os.Getpid(), job) {
		t.Fatal("Hoxi itself must stay outside the job")
	}
}

func TestStartTiedReportsStartFailure(t *testing.T) {
	cmd := Command(`C:\hoxi-subproc-test\does-not-exist.exe`)
	called := false
	if err := StartTied(cmd, func(error) { called = true }); err == nil {
		t.Fatal("expected start error for a missing executable")
	}
	if called {
		t.Fatal("a child that never started is not an untied child")
	}
}

// A parent ended the way taskkill /F ends Hoxi must take its tied child with it.
func TestTiedChildDiesWhenParentIsKilled(t *testing.T) {
	parent, out, err := startHelper("tier", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = parent.Process.Kill()
		_ = parent.Wait()
	})
	line, err := readLine(out, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(line)
	if err != nil {
		t.Fatalf("unexpected parent output %q", line)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(childPID))
	if err != nil {
		t.Fatalf("open child %d: %v", childPID, err)
	}
	defer windows.CloseHandle(child)

	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()

	ev, err := windows.WaitForSingleObject(child, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if ev != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(child, 1)
		t.Fatal("tied child kept running after its parent was killed")
	}
}
