//go:build !windows

package runner

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startDetachedServer runs `sh -c 'nohup sleep <secs> &'` as the leader of
// its own process group, the way a session's shell starts a dev server. The
// shell exits at once, so the sleep is reparented to launchd and only its
// process group still ties it to that shell.
func startDetachedServer(t *testing.T, secs string) (shellPID, serverPID int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "nohup sleep "+secs+" >/dev/null 2>&1 &")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	shellPID = cmd.Process.Pid
	_ = cmd.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		byPID, _ := readProcessSnapshots()
		for pid, snap := range byPID {
			if snap.pgid == shellPID && strings.Contains(snap.args, "sleep "+secs) {
				t.Cleanup(func() { _ = syscall.Kill(-shellPID, syscall.SIGKILL) })
				return shellPID, pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background sleep %s did not start", secs)
	return 0, 0
}

func uniqueSleepSeconds() string {
	return fmt.Sprintf("%d.%03d", 300+time.Now().UnixNano()%600, time.Now().UnixNano()%1000)
}

func TestOwnedProcessVerifierFindsReparentedBackgroundJobs(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	secs := uniqueSleepSeconds()
	shellPID, serverPID := startDetachedServer(t, secs)
	svc.ownedProcs.record("ses_a", shellPID, "")

	pattern := "sleep " + secs
	if !svc.ownedProcessVerifier("ses_a")(processControlTarget{pgrepArgs: []string{"-f", pattern}}) {
		t.Fatalf("pkill of the session's own background job should be verified")
	}
	if !svc.ownedProcessVerifier("ses_a")(processControlTarget{pids: []int{serverPID}}) {
		t.Fatalf("kill of the session's own background job should be verified")
	}
	if svc.ownedProcessVerifier("ses_b")(processControlTarget{pgrepArgs: []string{"-f", pattern}}) {
		t.Fatalf("another session must not be allowed to kill it")
	}
	if svc.ownedProcessVerifier("ses_b")(processControlTarget{pids: []int{serverPID}}) {
		t.Fatalf("another session must not be allowed to kill it by pid")
	}
}

func TestOwnedProcessVerifierRefusesProcessesOutsideTheSession(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	secs := uniqueSleepSeconds()
	ownShell, _ := startDetachedServer(t, secs)
	svc.ownedProcs.record("ses_a", ownShell, "")
	// The same command started outside the session (by the user, say).
	foreign := exec.Command("sleep", secs)
	foreign.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foreign.Process.Kill(); _, _ = foreign.Process.Wait() })

	verify := svc.ownedProcessVerifier("ses_a")
	if verify(processControlTarget{pgrepArgs: []string{"-f", "sleep " + secs}}) {
		t.Fatalf("a pattern that also matches a process outside the session must ask")
	}
	if verify(processControlTarget{pids: []int{foreign.Process.Pid}}) {
		t.Fatalf("kill of a process outside the session must ask")
	}
	if verify(processControlTarget{pids: []int{1}}) {
		t.Fatalf("launchd/init is never owned")
	}
	if verify(processControlTarget{pgrepArgs: []string{"-f", "("}}) {
		t.Fatalf("a pattern pgrep rejects must not count as matching nothing")
	}
	if !verify(processControlTarget{pgrepArgs: []string{"-f", "hoxi-no-such-process-" + strconv.Itoa(ownShell)}}) {
		t.Fatalf("a pattern that matches nothing signals nothing")
	}
}

func TestOwnedProcessLedgerDropsEmptyAndReusedGroups(t *testing.T) {
	l := newOwnedProcessLedger()
	l.record("ses_a", 4242, "zsh -c # hoxi-stop:ses:call_1")
	l.record("ses_a", 5353, "zsh -c # hoxi-stop:ses:call_2")
	l.record("ses_b", 6464, "")
	byPID := map[int]procSnapshot{
		4242: {pid: 4242, pgid: 4242, args: "zsh -c # hoxi-stop:ses:call_1"},
		4300: {pid: 4300, pgid: 4242, args: "./server"},
		5353: {pid: 5353, pgid: 5353, args: "-zsh"}, // leader PID reused by another command
		6500: {pid: 6500, pgid: 6464, args: "node dev"},
	}
	got := l.liveGroups("ses_a", byPID)
	if !got[4242] || got[5353] || got[6464] || len(got) != 1 {
		t.Fatalf("ses_a groups = %v, want only 4242", got)
	}
	delete(byPID, 4242)
	delete(byPID, 4300)
	if got := l.liveGroups("ses_a", byPID); len(got) != 0 {
		t.Fatalf("an emptied group must be dropped, got %v", got)
	}
	if got := l.liveGroups("ses_b", byPID); !got[6464] {
		t.Fatalf("other sessions keep their live groups, got %v", got)
	}
}

func TestOwnedProcessJudgedItemMasksOnlyVerifiedKills(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	secs := uniqueSleepSeconds()
	shellPID, _ := startDetachedServer(t, secs)
	svc.ownedProcs.record("ses_a", shellPID, "")
	restart := `pkill -f "sleep ` + secs + `"; sleep 1; nohup ./server > /tmp/run.out 2>&1 & sleep 5; curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:5800/`
	item := map[string]interface{}{
		"id": "per_x", "permission": "bash",
		"patterns": []interface{}{`pkill -f "sleep ` + secs + `"`, "sleep 1"},
		"metadata": map[string]interface{}{"command": restart},
	}

	judged, masked := svc.ownedProcessJudgedItem(item, "ses_a")
	if !masked {
		t.Fatalf("the verified kill should be masked")
	}
	if got := judged["patterns"].([]interface{})[0]; got != "true" {
		t.Fatalf("pattern = %v, want true", got)
	}
	if cmd := itemMetadata(judged)["command"].(string); strings.Contains(cmd, "pkill") || !strings.Contains(cmd, "nohup ./server") {
		t.Fatalf("judged command = %q", cmd)
	}
	if itemMetadata(item)["command"] != restart || item["patterns"].([]interface{})[0] == "true" {
		t.Fatalf("the original request shown to the user must stay untouched")
	}
	if !svc.policyApproves(judged, "/repo") || svc.policyApproves(item, "/repo") {
		t.Fatalf("only the judged copy should pass the policy")
	}
	if _, masked := svc.ownedProcessJudgedItem(item, "ses_b"); masked {
		t.Fatalf("another session's kill must not be masked")
	}

	manual := defaultApprovalPolicy()
	manual.Mode = ApprovalModeManual
	if _, err := svc.policy.set(manual); err != nil {
		t.Fatal(err)
	}
	if _, masked := svc.ownedProcessJudgedItem(item, "ses_a"); masked {
		t.Fatalf("manual mode asks for everything")
	}
}
