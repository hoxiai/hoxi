//go:build !windows

package runner

import (
	"errors"
	"syscall"
	"testing"

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
