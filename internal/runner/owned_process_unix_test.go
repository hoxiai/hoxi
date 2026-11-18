//go:build !windows

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var uniqueSleepSeq uint64

// startDetachedServer runs `sh -c 'nohup sleep <secs> &'` as the leader of
// its own process group, the way a session's shell starts a dev server. The
// shell exits at once, so the sleep is reparented to launchd and only its
// process group still ties it to that shell.
func startDetachedServer(t *testing.T, secs string) (shellPID, serverPID int) {
	t.Helper()
	requireProcessSnapshots(t)
	cmd := exec.Command("/bin/sh", "-c", "nohup sleep "+secs+" >/dev/null 2>&1 &")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	shellPID = cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-shellPID, syscall.SIGKILL) })
	_ = cmd.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		byPID, _ := readProcessSnapshots()
		for pid, snap := range byPID {
			if snap.PGID == shellPID && strings.Contains(snap.Args, "sleep "+secs) {
				return shellPID, pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background sleep %s did not start", secs)
	return 0, 0
}

func uniqueSleepSeconds() string {
	seq := atomic.AddUint64(&uniqueSleepSeq, 1)
	return fmt.Sprintf("%d.%03d", 300+(os.Getpid()+int(seq))%600, (os.Getpid()*31+int(seq))%1000)
}

func TestOwnedProcessVerifierFindsReparentedBackgroundJobs(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	secs := uniqueSleepSeconds()
	shellPID, serverPID := startDetachedServer(t, secs)
	svc.ownedProcs.Record("ses_a", shellPID, "")

	pattern := "sleep " + secs
	if !svc.ownedProcessVerifier("ses_a")(processControlTarget{PgrepArgs: []string{"-f", pattern}}) {
		t.Fatalf("pkill of the session's own background job should be verified")
	}
	if !svc.ownedProcessVerifier("ses_a")(processControlTarget{PIDs: []int{serverPID}}) {
		t.Fatalf("kill of the session's own background job should be verified")
	}
	if svc.ownedProcessVerifier("ses_b")(processControlTarget{PgrepArgs: []string{"-f", pattern}}) {
		t.Fatalf("another session must not be allowed to kill it")
	}
	if svc.ownedProcessVerifier("ses_b")(processControlTarget{PIDs: []int{serverPID}}) {
		t.Fatalf("another session must not be allowed to kill it by pid")
	}
}

func TestOwnedProcessVerifierRefusesProcessesOutsideTheSession(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	secs := uniqueSleepSeconds()
	ownShell, _ := startDetachedServer(t, secs)
	svc.ownedProcs.Record("ses_a", ownShell, "")
	// The same command started outside the session (by the user, say).
	foreign := exec.Command("sleep", secs)
	foreign.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foreign.Process.Kill(); _, _ = foreign.Process.Wait() })

	verify := svc.ownedProcessVerifier("ses_a")
	if verify(processControlTarget{PgrepArgs: []string{"-f", "sleep " + secs}}) {
		t.Fatalf("a pattern that also matches a process outside the session must ask")
	}
	if verify(processControlTarget{PIDs: []int{foreign.Process.Pid}}) {
		t.Fatalf("kill of a process outside the session must ask")
	}
	if verify(processControlTarget{PIDs: []int{1}}) {
		t.Fatalf("launchd/init is never owned")
	}
	if verify(processControlTarget{PgrepArgs: []string{"-f", "("}}) {
		t.Fatalf("a pattern pgrep rejects must not count as matching nothing")
	}
	if !verify(processControlTarget{PgrepArgs: []string{"-f", "hoxi-no-such-process-" + strconv.Itoa(ownShell)}}) {
		t.Fatalf("a pattern that matches nothing signals nothing")
	}
}

func TestOwnedProcessLedgerDropsEmptyAndReusedGroups(t *testing.T) {
	l := newOwnedProcessLedger()
	l.Record("ses_a", 4242, "zsh -c # hoxi-stop:ses:call_1")
	l.Record("ses_a", 5353, "zsh -c # hoxi-stop:ses:call_2")
	l.Record("ses_b", 6464, "")
	byPID := map[int]procSnapshot{
		4242: {PID: 4242, PGID: 4242, Args: "zsh -c # hoxi-stop:ses:call_1"},
		4300: {PID: 4300, PGID: 4242, Args: "./server"},
		5353: {PID: 5353, PGID: 5353, Args: "-zsh"}, // leader PID reused by another command
		6500: {PID: 6500, PGID: 6464, Args: "node dev"},
	}
	got := l.LiveGroups("ses_a", byPID)
	if !got[4242] || got[5353] || got[6464] || len(got) != 1 {
		t.Fatalf("ses_a groups = %v, want only 4242", got)
	}
	delete(byPID, 4242)
	delete(byPID, 4300)
	if got := l.LiveGroups("ses_a", byPID); len(got) != 0 {
		t.Fatalf("an emptied group must be dropped, got %v", got)
	}
	if got := l.LiveGroups("ses_b", byPID); !got[6464] {
		t.Fatalf("other sessions keep their live groups, got %v", got)
	}
}

func TestOwnedProcessJudgedItemMasksOnlyVerifiedKills(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	secs := uniqueSleepSeconds()
	shellPID, _ := startDetachedServer(t, secs)
	svc.ownedProcs.Record("ses_a", shellPID, "")
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

func TestOwnedProcessSDKRestartRefusesAMixedProcessMatch(t *testing.T) {
	if !trustedProcessControlTool("pkill") {
		t.Skip("inherited pkill is not the trusted system executable")
	}
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	secs := uniqueSleepSeconds()
	group, _ := startDetachedServer(t, secs)
	svc.ownedProcs.Record("ses_sdk_restart", group, "")
	log := filepath.ToSlash(filepath.Join(t.TempDir(), "run.log"))
	command := `export PATH="$PATH:/opt/flutter/bin"` + "\n" + `pkill -f "sleep ` + secs + `" 2>/dev/null || true` + "\n" +
		`rm -f ` + log + "\n" + `nohup bash scripts/run_local.sh -d DEVICE > ` + log + ` 2>&1 &`
	item := openCodeBashItem("ses_sdk_restart", command)
	judged, changed := svc.ownedProcessJudgedItem(item, "ses_sdk_restart")
	if !changed || !svc.policyApproves(judged, root) || svc.policyApproves(item, root) {
		t.Fatal("only a verified system-tool/owned-target restart should run directly")
	}
	// This second group was not recorded for the session. The same pattern
	// now matches both, so a fresh request must keep the pkill unmasked.
	startDetachedServer(t, secs)
	if _, changed := svc.ownedProcessJudgedItem(item, "ses_sdk_restart"); changed {
		t.Fatal("a same-name process outside the session was included in the exemption")
	}
}
