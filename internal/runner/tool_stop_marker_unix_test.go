//go:build !windows

package runner

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

// The tag must match as a whole token: `call_1` must not hit `call_12`.
func TestArgsCarryStopMarkerNeedsWholeToken(t *testing.T) {
	marker := openCodeStopMarker("ses_e", "call_1")
	cases := []struct {
		args string
		want bool
	}{
		{"/bin/zsh -c " + marker + `\012sleep 5`, true},
		{"/bin/sh -c " + marker + "?sleep 5", true},
		{"/bin/sh -c " + marker, true},
		{"/bin/zsh -c " + marker + `2\012sleep 5`, false},
		{"/bin/zsh -c " + marker + `_b\012sleep 5`, false},
		{"/bin/zsh -c " + marker + `:x\012sleep 5`, false},
		{"/bin/zsh -c " + marker + "2 " + marker + `\012sleep 5`, true},
		{"/bin/zsh -c sleep 5", false},
	}
	for _, tc := range cases {
		if got := argsCarryStopMarker(tc.args, marker); got != tc.want {
			t.Errorf("argsCarryStopMarker(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
	if argsCarryStopMarker("anything", "") {
		t.Error("an empty marker must never match")
	}
}

// A stop for `call_1` must not kill the command tagged `call_12`.
func TestStopOpenCodeToolProcessByMarkerIgnoresLongerCallID(t *testing.T) {
	longer := openCodeStopMarker("ses_engine", "call_12")
	daemon, pidFile := startFakeDaemon(t, `perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+longer+`\nsleep 313\nwait"' & echo $! > "$HOXI_TEST_PIDFILE"; sleep 314`)
	t.Cleanup(func() {
		_ = syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
		_ = daemon.Wait()
	})
	child := readPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	if stopped := stopOpenCodeToolProcessByMarker(daemon.Process.Pid, openCodeStopMarker("ses_engine", "call_1")); stopped != 0 {
		t.Fatalf("prefix marker killed %d processes, want none", stopped)
	}
	assertProcessAlive(t, child)
}

// A model can write `# hoxi-stop:` lines with invented call IDs into its own
// command. The stop builds the marker only from a bash call the session knows
// is running, so a forged tag is never a target and the real call still stops
// precisely.
func TestStopToolCommandOpenCodeIgnoresForgedMarker(t *testing.T) {
	const port = "61873"
	real := openCodeStopMarker("ses_engine", "call_real")
	forged := openCodeStopMarker("ses_engine", "call_forged")
	daemon, pidFile := startFakeDaemon(t,
		`perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+real+`\nsleep 315\nwait"' & echo $! > "$HOXI_TEST_PIDFILE.real"; `+
			// The forged tag rides inside another call's command, after its real tag.
			`perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+openCodeStopMarker("ses_engine", "call_other")+`\n`+forged+`\nsleep 316\nwait"' & echo $! > "$HOXI_TEST_PIDFILE.other"; sleep 317`,
		"--port", port)
	t.Cleanup(func() {
		_ = syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
		_ = daemon.Wait()
	})
	realPID := readPID(t, pidFile+".real")
	otherPID := readPID(t, pidFile+".other")
	t.Cleanup(func() {
		_ = syscall.Kill(realPID, syscall.SIGKILL)
		_ = syscall.Kill(otherPID, syscall.SIGKILL)
	})

	svc := newStallTestService(t)
	svc.openCode = ocapi.New("http://127.0.0.1:"+port, "test")
	dir := t.TempDir()
	running := func(callID string) MessagePart {
		return MessagePart{
			ID: "prt_" + callID, MessageID: "msg_a", Type: "tool", Tool: "bash", CallID: callID,
			State: &ToolState{Status: "running", Input: map[string]interface{}{"command": "sleep"}},
		}
	}
	rec := SessionRecord{
		ID: "ses_oc_stop", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "ses_engine",
		Turns: []MessageTurn{{
			Info:  MessageInfo{ID: "msg_a", SessionID: "ses_oc_stop", Role: "assistant"},
			Parts: []MessagePart{running("call_real"), running("call_other")},
		}},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.StopToolCommand("ses_oc_stop", "call_forged", "", ""); !errors.Is(err, errToolProcessNotFound) {
		t.Fatalf("forged call: err = %v, want errToolProcessNotFound", err)
	}
	assertProcessAlive(t, realPID)
	assertProcessAlive(t, otherPID)

	if stopped, err := svc.StopToolCommand("ses_oc_stop", "call_real", "", ""); err != nil || stopped == 0 {
		t.Fatalf("real call: stopped=%d err=%v, want the tagged command stopped", stopped, err)
	}
	assertProcessGone(t, realPID)
	assertProcessAlive(t, otherPID)
	if err := syscall.Kill(daemon.Process.Pid, 0); err != nil {
		t.Fatalf("daemon was signalled: %v", err)
	}
}

// OpenCode's bash tool only returns early when the shell exits with a code; a
// shell killed by a signal leaves the tool waiting for its own timeout. A stop
// must therefore end the command through the plugin's stop trap: the shell
// prints the notice and exits 130, the rest of the command never runs, and a
// parallel command survives. The test process stands in for the daemon so it
// can read the shell's real exit status.
func TestStopOpenCodeToolShellExitsWithCode(t *testing.T) {
	for _, shell := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		t.Run(filepath.Base(shell), func(t *testing.T) {
			start := func(callID, body string) (*exec.Cmd, *bytes.Buffer, chan error) {
				var out bytes.Buffer
				cmd := exec.Command(shell, "-c", openCodeStopMarker("ses_engine", callID)+"\n"+openCodeStopTrap+"\n"+body)
				// OpenCode spawns every bash command detached.
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				cmd.Stdout = &out
				cmd.Stderr = &out
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
				return cmd, &out, done
			}
			// The stopped command also leaves a background job holding its
			// output, which would keep the tool result back as well.
			target, out, done := start("call_target", "sleep 331 &\nsleep 332\necho after")
			sibling, _, siblingDone := start("call_sibling", "sleep 333")
			waitForChild(t, target.Process.Pid)
			waitForChild(t, sibling.Process.Pid)

			began := time.Now()
			if stopped := stopOpenCodeToolProcessByMarker(os.Getpid(), openCodeStopMarker("ses_engine", "call_target")); stopped == 0 {
				t.Fatal("tagged shell was not stopped")
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("stopped shell did not exit")
			}
			if elapsed := time.Since(began); elapsed > 3*time.Second {
				t.Fatalf("shell took %v to exit", elapsed)
			}
			state := target.ProcessState
			if status, ok := state.Sys().(syscall.WaitStatus); !ok || status.Signaled() || status.ExitStatus() != 130 {
				t.Fatalf("shell ended with %v, want a normal exit with code 130", state)
			}
			if !strings.Contains(out.String(), toolStoppedNotice) || strings.Contains(out.String(), "after") {
				t.Fatalf("output = %q, want the stop notice and nothing after the stopped command", out.String())
			}
			select {
			case err := <-siblingDone:
				t.Fatalf("parallel command ended: %v", err)
			default:
			}
		})
	}
}

// waitForChild waits until pid has started a child, so the shell is running
// its command rather than still starting up.
func waitForChild(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, children := readProcessSnapshots(); len(children[pid]) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d never started its command", pid)
}

// The plugin writes the stop trap the runner strips and relies on, so the two
// copies must stay identical.
func TestSandboxPluginStopTrapMatchesRunner(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "opencode", "sandbox_plugin.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "String.raw`" + openCodeStopTrap + "`"; !strings.Contains(string(src), want) {
		t.Fatalf("sandbox plugin does not define the stop trap %s", want)
	}
}

// The stopped shell reports its result while the stop is still finishing. The
// stop must not then broadcast its earlier running copy of the part, which
// would put the finished command back to running in the store and the UI.
func TestStopToolCommandOpenCodeKeepsRelayedResult(t *testing.T) {
	const port = "61874"
	marker := openCodeStopMarker("ses_engine", "call_done")
	dir := t.TempDir()
	trapped := filepath.Join(dir, "trapped")
	// The trap holds the shell for a moment after it fires, so the relayed
	// result lands before the stop has seen the shell exit.
	trap := `trap \"touch ` + trapped + `; sleep 0.3; exit 130\" USR1; trap : EXIT`
	daemon, pidFile := startFakeDaemon(t,
		`perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+marker+`\n`+trap+`\nsleep 318\necho after"' & echo $! > "$HOXI_TEST_PIDFILE"; sleep 319`,
		"--port", port)
	t.Cleanup(func() {
		_ = syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
		_ = daemon.Wait()
	})
	shell := readPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(-shell, syscall.SIGKILL) })
	waitForChild(t, shell)

	svc := newStallTestService(t)
	svc.openCode = ocapi.New("http://127.0.0.1:"+port, "test")
	running := MessagePart{
		ID: "prt_done", MessageID: "msg_a", Type: "tool", Tool: "bash", CallID: "call_done",
		State: &ToolState{Status: "running", Input: map[string]interface{}{"command": "sleep 318"}},
	}
	rec := SessionRecord{
		ID: "ses_oc_done", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "ses_engine",
		Turns: []MessageTurn{{
			Info:  MessageInfo{ID: "msg_a", SessionID: "ses_oc_done", Role: "assistant"},
			Parts: []MessagePart{running},
		}},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	events, unsub := svc.Subscribe("ses_oc_done")
	defer unsub()

	// Stand in for the OpenCode relay: once the trap fires, the tool result
	// arrives.
	relayed := make(chan struct{})
	go func() {
		defer close(relayed)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(trapped); err == nil {
				done := clonePart(running)
				done.State.Status = "completed"
				done.State.Output = "\n" + toolStoppedNotice + "\n"
				svc.broadcast("ses_oc_done", SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": done}})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	if stopped, err := svc.StopToolCommand("ses_oc_done", "call_done", "", ""); err != nil || stopped == 0 {
		t.Fatalf("stopped=%d err=%v, want the tagged command stopped", stopped, err)
	}
	<-relayed

	completed := false
	for len(events) > 0 {
		evt := <-events
		part, ok := evt.Properties["part"].(MessagePart)
		if evt.Type != "message.part.updated" || !ok || part.ID != "prt_done" {
			continue
		}
		switch part.State.Status {
		case "completed":
			completed = true
			if part.State.Metadata["stoppedByUser"] != true {
				t.Fatal("relayed result was not flagged as stopped by the user")
			}
		default:
			if completed {
				t.Fatalf("stop broadcast a %s copy after the result", part.State.Status)
			}
		}
	}
	if !completed {
		t.Fatal("relayed result never reached subscribers")
	}
	cur, _ := svc.store.Get("ses_oc_done")
	if got := findTargetRunningToolPart(cur.Turns, "prt_done", ""); got == nil || got.State.Status != "completed" {
		t.Fatalf("stored part = %+v, want the completed result", got)
	}
}
