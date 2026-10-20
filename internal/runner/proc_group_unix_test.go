//go:build !windows

package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startFakeCLITurn runs script as a Qoder turn (no approval hooks involved).
func startFakeCLITurn(t *testing.T, svc *Service, sessionID, script string) *activeProcess {
	t.Helper()
	ws := t.TempDir()
	bin := filepath.Join(t.TempDir(), "fake-cli")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := SessionRecord{ID: sessionID, Runtime: string(RuntimeQoder), ProjectDir: ws, WorktreePath: ws}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	user := MessageInfo{ID: "msg_u_" + sessionID, SessionID: sessionID, Role: "user"}
	if err := svc.startTurnProcess(rec, bin, "build", "m", "", "hi", user, nil, nil, "msg_a_"+sessionID); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	svc.mu.Lock()
	ap := svc.procs[sessionID]
	svc.mu.Unlock()
	if ap == nil {
		t.Fatal("turn not registered")
	}
	return ap
}

func waitTurnDone(t *testing.T, ap *activeProcess, within time.Duration) {
	t.Helper()
	select {
	case <-ap.done:
	case <-time.After(within):
		t.Fatalf("turn still busy after %s", within)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fake CLI never reported its background pid")
	return 0
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("process %d survived the turn", pid)
}

// A background job that inherits stdout used to keep the turn busy forever
// after the CLI itself exited.
func TestTurnEndsWhenOrphanHoldsStdout(t *testing.T) {
	svc := newStallTestService(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("HOXI_TEST_PIDFILE", pidFile)
	ap := startFakeCLITurn(t, svc, "ses_orphan", `cat >/dev/null
sleep 300 &
echo $! > "$HOXI_TEST_PIDFILE"
exit 0
`)
	pid := readPID(t, pidFile)
	waitTurnDone(t, ap, 4*orphanDrainGrace+2*time.Second)
	assertProcessGone(t, pid)
}

// Stop must also reach background jobs that ignore SIGINT and no longer hold
// the CLI's output (non-interactive sh ignores SIGINT in `&` jobs).
func TestAbortKillsWholeProcessGroup(t *testing.T) {
	svc := newStallTestService(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("HOXI_TEST_PIDFILE", pidFile)
	ap := startFakeCLITurn(t, svc, "ses_group", `cat >/dev/null
sleep 300 >/dev/null 2>&1 &
echo $! > "$HOXI_TEST_PIDFILE"
sleep 300
`)
	pid := readPID(t, pidFile)
	ap.Abort()
	waitTurnDone(t, ap, 4*orphanDrainGrace+2*time.Second)
	assertProcessGone(t, pid)
}

// A CLI that hangs without output is flagged, then stopped with a
// RunnerStalledError on its assistant message.
func TestSilentTurnIsFlaggedThenStopped(t *testing.T) {
	svc := newStallTestService(t)
	svc.stall = stallPolicy{warnAfter: 200 * time.Millisecond, abortAfter: 700 * time.Millisecond, interval: 50 * time.Millisecond}
	events, unsub := svc.Subscribe("ses_silent")
	defer unsub()
	ap := startFakeCLITurn(t, svc, "ses_silent", "cat >/dev/null\nsleep 300\n")

	var flagged bool
	var stallErr *MessageError
	deadline := time.After(10 * time.Second)
	for stallErr == nil {
		select {
		case evt := <-events:
			switch evt.Type {
			case "session.stall":
				if since, _ := evt.Properties["stalledSince"].(int64); since > 0 {
					flagged = true
				}
			case "session.error":
				stallErr, _ = evt.Properties["error"].(*MessageError)
			}
		case <-deadline:
			t.Fatal("silent turn was never stopped")
		}
	}
	if !flagged {
		t.Fatal("silent turn was stopped without a stall warning first")
	}
	if stallErr.Name != stallErrorName {
		t.Fatalf("error = %s, want %s", stallErr.Name, stallErrorName)
	}
	waitTurnDone(t, ap, 5*time.Second)
}

// A CLI that ignores SIGINT and runs its tool command in a process group of
// its own (as Claude Code does) must not leave that command behind on Stop.
func TestAbortKillsToolInOwnProcessGroup(t *testing.T) {
	svc := newStallTestService(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("HOXI_TEST_PIDFILE", pidFile)
	ap := startFakeCLITurn(t, svc, "ses_tree", `cat >/dev/null
trap '' INT
perl -e 'setpgrp(0, 0); exec "sleep", "300"' >/dev/null 2>&1 &
echo $! > "$HOXI_TEST_PIDFILE"
sleep 300
`)
	pid := readPID(t, pidFile)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if pgid, _ := syscall.Getpgid(pid); pgid == pid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tool never moved into its own process group")
		}
	}
	ap.Abort()
	waitTurnDone(t, ap, 4*orphanDrainGrace+2*time.Second)
	assertProcessGone(t, pid)
}

// Heartbeat lines the translator drops (Claude Code writes `tool_progress`
// every 30s while a tool runs) are not progress and must not hide a hang.
func TestHeartbeatsDoNotHideStall(t *testing.T) {
	svc := newStallTestService(t)
	svc.stall = stallPolicy{warnAfter: 300 * time.Millisecond, abortAfter: 0, interval: 50 * time.Millisecond}
	events, unsub := svc.Subscribe("ses_beat")
	defer unsub()
	ap := startFakeCLITurn(t, svc, "ses_beat", `cat >/dev/null
while :; do
  echo '{"type":"tool_progress","tool_use_id":"toolu_x-heartbeat-0","tool_name":"Bash","heartbeat":true}'
  sleep 0.05
done
`)
	defer func() {
		ap.Abort()
		waitTurnDone(t, ap, 2*time.Second)
	}()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.Type == "session.stall" {
				if since, _ := evt.Properties["stalledSince"].(int64); since > 0 {
					return
				}
			}
		case <-deadline:
			t.Fatal("heartbeats kept a silent turn from being flagged")
		}
	}
}

// Claude Code answers SIGINT with an is_error result; a stopped turn must end
// as "aborted" (or with the stall explanation), not as a CLI failure.
func TestStoppedTurnIgnoresInterruptedResult(t *testing.T) {
	const script = `cat >/dev/null
trap 'echo "{\"type\":\"result\",\"subtype\":\"error_during_execution\",\"is_error\":true}"; exit 1' INT
sleep 300 &
echo $! > "$HOXI_TEST_PIDFILE"
wait
`
	finishOf := func(t *testing.T, svc *Service, sessionID string, stop func(*activeProcess)) (string, *MessageError) {
		t.Helper()
		events, unsub := svc.Subscribe(sessionID)
		defer unsub()
		pidFile := filepath.Join(t.TempDir(), "pid")
		t.Setenv("HOXI_TEST_PIDFILE", pidFile)
		ap := startFakeCLITurn(t, svc, sessionID, script)
		readPID(t, pidFile) // the INT trap is installed
		stop(ap)
		waitTurnDone(t, ap, 4*orphanDrainGrace+2*time.Second)
		var finish string
		var sessionErr *MessageError
		for {
			select {
			case evt := <-events:
				switch evt.Type {
				case "message.updated":
					if info, ok := evt.Properties["info"].(MessageInfo); ok && info.Finish != "" {
						finish = info.Finish
					}
				case "session.error":
					sessionErr, _ = evt.Properties["error"].(*MessageError)
				}
			default:
				return finish, sessionErr
			}
		}
	}

	svc := newStallTestService(t)
	finish, err := finishOf(t, svc, "ses_stop", func(ap *activeProcess) { ap.Abort() })
	if finish != "aborted" || err != nil {
		t.Fatalf("user stop ended as finish=%q error=%+v, want aborted without error", finish, err)
	}

	svc.stall = stallPolicy{warnAfter: 100 * time.Millisecond, abortAfter: 300 * time.Millisecond, interval: 50 * time.Millisecond}
	_, err = finishOf(t, svc, "ses_stalled", func(*activeProcess) {})
	if err == nil || err.Name != stallErrorName {
		t.Fatalf("watchdog stop ended with %+v, want %s", err, stallErrorName)
	}
}
