//go:build !windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/testutil/execstub"
)

// startFakeCLITurn runs script as a Qoder turn (no approval hooks involved).
// Qoder has no PreToolUse gate, so only read-only modes may start; these tests
// exercise process-group handling, not tool approvals.
func startFakeCLITurn(t *testing.T, svc *Service, sessionID, script string) *activeProcess {
	t.Helper()
	ws := t.TempDir()
	bin := execstub.Write(t, t.TempDir(), "fake-cli", script)
	rec := SessionRecord{ID: sessionID, Runtime: string(RuntimeQoder), ProjectDir: ws, WorktreePath: ws}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	user := MessageInfo{ID: "msg_u_" + sessionID, SessionID: sessionID, Role: "user"}
	if err := svc.startTurnProcess(rec, bin, "plan", "m", "", "hi", user, nil, nil, "msg_a_"+sessionID); err != nil {
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
	deadline := time.Now().Add(10 * time.Second)
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

	stalledSvc := newStallTestService(t)
	stalledSvc.stall = stallPolicy{warnAfter: 100 * time.Millisecond, abortAfter: 300 * time.Millisecond, interval: 50 * time.Millisecond}
	_, err = finishOf(t, stalledSvc, "ses_stalled", func(*activeProcess) {})
	if err == nil || err.Name != stallErrorName {
		t.Fatalf("watchdog stop ended with %+v, want %s", err, stallErrorName)
	}
}

func TestStopToolCommandKeepsTurnAliveAndContinues(t *testing.T) {
	svc := newStallTestService(t)
	sessionID := "ses_stop_tool"
	events, unsub := svc.Subscribe(sessionID)
	defer unsub()

	pidFile := filepath.Join(t.TempDir(), "tool_pid")
	t.Setenv("HOXI_TEST_PIDFILE", pidFile)

	ap := startFakeCLITurn(t, svc, sessionID, `cat >/dev/null
echo '{"type":"assistant","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"toolu_slow_test","name":"Bash","input":{"command":"sleep 300"}}]}}'
sleep 300 &
child_pid=$!
echo "$child_pid" > "$HOXI_TEST_PIDFILE"
wait "$child_pid"
rc=$?
echo "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"toolu_slow_test\",\"content\":\"exit code $rc\",\"is_error\":true}]}}"
echo '{"type":"result","subtype":"success","is_error":false,"result":"Continued after user stopped slow test"}'
`)
	childPID := readPID(t, pidFile)

	stopped, err := svc.StopToolCommand(sessionID, "toolu_slow_test", "", "sleep 300")
	if err != nil {
		t.Fatalf("StopToolCommand error: %v", err)
	}
	if stopped == 0 {
		t.Fatalf("expected StopToolCommand to terminate at least 1 child process, got 0")
	}

	assertProcessGone(t, childPID)
	waitTurnDone(t, ap, 4*orphanDrainGrace+2*time.Second)

	var finish string
	var sawStoppedMeta bool
	for {
		select {
		case evt := <-events:
			switch evt.Type {
			case "message.updated":
				if info, ok := evt.Properties["info"].(MessageInfo); ok && info.Finish != "" {
					finish = info.Finish
				}
			case "message.part.updated":
				if part, ok := evt.Properties["part"].(MessagePart); ok && part.CallID == "toolu_slow_test" && part.State != nil {
					if v, _ := part.State.Metadata["stoppedByUser"].(bool); v {
						sawStoppedMeta = true
					}
				}
			}
		default:
			if finish != "stop" {
				t.Fatalf("expected turn to continue and finish with 'stop', got %q", finish)
			}
			if !sawStoppedMeta {
				t.Fatalf("expected tool part to carry stoppedByUser=true metadata")
			}
			return
		}
	}
}

// startSleepingTurn runs a fake CLI turn whose tool child is `sleep <arg>`
// and returns the turn plus the child's pid.
func startSleepingTurn(t *testing.T, svc *Service, sessionID, arg string) (*activeProcess, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "tool_pid")
	t.Setenv("HOXI_TEST_PIDFILE", pidFile)
	ap := startFakeCLITurn(t, svc, sessionID, `cat >/dev/null
sleep `+arg+` &
echo "$!" > "$HOXI_TEST_PIDFILE"
wait
`)
	pid := readPID(t, pidFile)
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		waitTurnDone(t, ap, 4*orphanDrainGrace+2*time.Second)
	})
	return ap, pid
}

func assertProcessAlive(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process %d was killed but should have survived", pid)
	}
}

// A stop request whose command matches nothing must kill nothing (it used to
// fall back to every child of the CLI).
func TestStopToolCommandNoMatchKillsNothing(t *testing.T) {
	svc := newStallTestService(t)
	_, childPID := startSleepingTurn(t, svc, "ses_stop_nomatch", "301")

	stopped, err := svc.StopToolCommand("ses_stop_nomatch", "", "", "npm run dev")
	if !errors.Is(err, errToolProcessNotFound) || stopped != 0 {
		t.Fatalf("stopped=%d err=%v, want 0 and errToolProcessNotFound", stopped, err)
	}
	stopped, err = svc.StopToolCommand("ses_stop_nomatch", "", "", "")
	if !errors.Is(err, errToolProcessNotFound) || stopped != 0 {
		t.Fatalf("empty command: stopped=%d err=%v, want not found", stopped, err)
	}
	time.Sleep(100 * time.Millisecond)
	assertProcessAlive(t, childPID)
	if _, ok := svc.stoppedTools.Load("ses_stop_nomatch\x00call:"); ok {
		t.Fatal("failed stop left a stopped-tool mark behind")
	}
}

// Stopping a command in one session must never reach another session's
// processes, even when their command lines are identical.
func TestStopToolCommandSparesSiblingSession(t *testing.T) {
	svc := newStallTestService(t)
	_, siblingPID := startSleepingTurn(t, svc, "ses_stop_sibling", "302")
	_, targetPID := startSleepingTurn(t, svc, "ses_stop_target", "302")

	stopped, err := svc.StopToolCommand("ses_stop_target", "", "", "sleep 302")
	if err != nil || stopped == 0 {
		t.Fatalf("stopped=%d err=%v, want the target's child stopped", stopped, err)
	}
	assertProcessGone(t, targetPID)
	assertProcessAlive(t, siblingPID)
}

// Sessions without a runner-owned CLI process and without an OpenCode backing
// session are refused instead of scanning unrelated process trees.
func TestStopToolCommandRefusesSessionWithoutRunnerProcess(t *testing.T) {
	svc := newStallTestService(t)
	if _, err := svc.StopToolCommand("ses_not_running", "", "", "sleep 1"); !errors.Is(err, errSessionNotRunning) {
		t.Fatalf("err = %v, want errSessionNotRunning", err)
	}
	// An OpenCode turn has no CLI process of its own: the stop is attempted
	// inside the shared daemon tree and reports "not found" when nothing
	// matches, rather than refusing the session outright.
	ap := registerStallTestProcess(svc, "ses_opencode_turn", time.Now())
	ap.runtime = "opencode"
	if _, err := svc.StopToolCommand("ses_opencode_turn", "", "", "sleep 1"); !errors.Is(err, errToolProcessNotFound) {
		t.Fatalf("OpenCode turn: err = %v, want errToolProcessNotFound", err)
	}
}

// A tool command inside the shared OpenCode daemon is stopped by its unique
// command match; the daemon itself and unrelated children survive.
func TestStopOpenCodeToolProcessStopsUniqueMatch(t *testing.T) {
	// `opencode serve` spawns every tool command detached, in its own process
	// group; the fake daemon reproduces that with setpgrp.
	marker := openCodeStopMarker("ses_engine", "call_stop_1")
	daemon, pidFile := startFakeDaemon(t, `perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+marker+`\nsleep 305\nwait"' & echo $! > "$HOXI_TEST_PIDFILE"; sleep 306`)
	t.Cleanup(func() {
		_ = syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
		_ = daemon.Wait()
	})
	daemonPID := readPID(t, pidFile)

	if stopped := stopOpenCodeToolProcessByMarker(daemon.Process.Pid, marker); stopped == 0 {
		t.Fatal("tagged tool command was not stopped")
	}
	assertProcessGone(t, daemonPID)
	if err := syscall.Kill(daemon.Process.Pid, 0); err != nil {
		t.Fatalf("daemon was signalled: %v", err)
	}
}

// A marker that no subtree carries must kill nothing.
func TestStopOpenCodeToolProcessByMarkerIgnoresUnknownMarker(t *testing.T) {
	marker := openCodeStopMarker("ses_engine", "call_present")
	daemon, pidFile := startFakeDaemon(t, `perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+marker+`\nsleep 309\nwait"' & echo $! > "$HOXI_TEST_PIDFILE"; sleep 310`)
	t.Cleanup(func() {
		_ = syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
		_ = daemon.Wait()
	})
	child := readPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	other := openCodeStopMarker("ses_engine", "call_absent")
	if stopped := stopOpenCodeToolProcessByMarker(daemon.Process.Pid, other); stopped != 0 {
		t.Fatalf("unknown marker killed %d processes, want none", stopped)
	}
	assertProcessAlive(t, child)
}

// Two subtrees carrying the same marker are ambiguous: the stop refuses.
func TestStopOpenCodeToolProcessByMarkerRefusesAmbiguousMatch(t *testing.T) {
	marker := openCodeStopMarker("ses_engine", "call_dup")
	daemon, pidFile := startFakeDaemon(t, `perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+marker+`\nsleep 311\nwait"' & echo $! > "$HOXI_TEST_PIDFILE.1"; perl -e 'setpgrp(0, 0); exec "sh", "-c", "`+marker+`\nsleep 311\nwait"' & echo $! > "$HOXI_TEST_PIDFILE.2"; sleep 312`)
	t.Cleanup(func() {
		_ = syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
		_ = daemon.Wait()
	})
	first := readPID(t, pidFile+".1")
	second := readPID(t, pidFile+".2")
	t.Cleanup(func() {
		_ = syscall.Kill(first, syscall.SIGKILL)
		_ = syscall.Kill(second, syscall.SIGKILL)
	})

	if stopped := stopOpenCodeToolProcessByMarker(daemon.Process.Pid, marker); stopped != 0 {
		t.Fatalf("ambiguous marker killed %d processes, want none", stopped)
	}
	assertProcessAlive(t, first)
	assertProcessAlive(t, second)
}

// The plugin tag is stripped from tool parts so the UI shows the real command.
func TestStripOpenCodeStopMarker(t *testing.T) {
	part := MessagePart{
		Type: "tool",
		Tool: "bash",
		State: &ToolState{
			Status: "running",
			Input:  map[string]interface{}{"command": openCodeStopMarker("ses_e", "call_1") + "\n" + openCodeStopTrap + "\necho hi"},
			Title:  openCodeStopMarker("ses_e", "call_1") + "\n" + openCodeStopTrap + "\necho hi",
		},
	}
	stripOpenCodeStopMarker(&part)
	if got := part.State.Input["command"]; got != "echo hi" {
		t.Fatalf("command = %v, want the untagged command", got)
	}
	if got := part.State.Title; got != "echo hi" {
		t.Fatalf("title = %q, want the tag and stop trap stripped", got)
	}

	// Tags written before the stop trap existed have no trap line.
	legacy := MessagePart{
		Type:  "tool",
		Tool:  "bash",
		State: &ToolState{Status: "completed", Input: map[string]interface{}{"command": openCodeStopMarker("ses_e", "call_0") + "\necho hi"}},
	}
	stripOpenCodeStopMarker(&legacy)
	if got := legacy.State.Input["command"]; got != "echo hi" {
		t.Fatalf("legacy command = %v, want the untagged command", got)
	}

	wrappedPart := MessagePart{
		Type: "tool",
		Tool: "bash",
		State: &ToolState{
			Status: "running",
			Input:  map[string]interface{}{"command": openCodeStopMarker("ses_e", "call_1") + "\ntrap : EXIT\n'/usr/bin/sandbox-exec' -f '/tmp/p.sb' -- /bin/sh -c 'git status'"},
		},
	}
	stripOpenCodeStopMarker(&wrappedPart)
	if got := unwrapSandboxCommand(wrappedPart.State.Input["command"].(string)); got != "git status" {
		t.Fatalf("unwrapSandboxCommand = %q, want 'git status'", got)
	}

	untouched := MessagePart{
		Type:  "tool",
		Tool:  "bash",
		State: &ToolState{Status: "running", Input: map[string]interface{}{"command": "echo hi"}},
	}
	stripOpenCodeStopMarker(&untouched)
	if got := untouched.State.Input["command"]; got != "echo hi" {
		t.Fatalf("untagged command changed to %v", got)
	}

	// History written before the plugin dropped echoed tags holds tags the
	// model copied, with invented call IDs, after the real one.
	forged := MessagePart{
		Type: "tool",
		Tool: "bash",
		State: &ToolState{
			Status: "completed",
			Input: map[string]interface{}{"command": openCodeStopMarker("ses_e", "call_2") + "\n" +
				"# hoxi-stop:ses_e:call_01_r8s0t2u4v6w8x0y2z4a6b8c0d\n  # hoxi-stop:ses_e:call_fake\ngo test ./..."},
			Title: openCodeStopMarker("ses_e", "call_2") + "\ngo test ./...",
		},
	}
	stripOpenCodeStopMarker(&forged)
	if got := forged.State.Input["command"]; got != "go test ./..." {
		t.Fatalf("command = %q, want every leading tag stripped", got)
	}
	if got := forged.State.Title; got != "go test ./..." {
		t.Fatalf("title = %q, want the tag stripped", got)
	}

	// Only leading lines are tags; a later line may be heredoc data.
	heredoc := "cat > notes.md <<'EOF'\n# hoxi-stop:ses_e:call_3\nEOF"
	inner := MessagePart{
		Type:  "tool",
		Tool:  "bash",
		State: &ToolState{Status: "completed", Input: map[string]interface{}{"command": heredoc}},
	}
	stripOpenCodeStopMarker(&inner)
	if got := inner.State.Input["command"]; got != heredoc {
		t.Fatalf("heredoc command changed to %q", got)
	}
}

func TestSelectOpenCodeDaemonPIDPrefersLeaf(t *testing.T) {
	byPID := map[int]procSnapshot{
		100: {pid: 100, ppid: 1, pgid: 100, args: "/bin/sh -c opencode serve --hostname 127.0.0.1 --port 5432"},
		200: {pid: 200, ppid: 100, pgid: 100, args: "/usr/local/bin/opencode serve --hostname 127.0.0.1 --port 5432"},
	}
	children := map[int][]int{
		1:   {100},
		100: {200},
	}
	if got := selectOpenCodeDaemonPID(byPID, children, "5432"); got != 200 {
		t.Fatalf("selectOpenCodeDaemonPID = %d, want leaf daemon PID 200", got)
	}
}

func TestIsAgentRuntimeProcessArgsHandlesWindowsPathsAndExtensions(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{`"C:\Users\dev\.local\bin\claude.exe" --output-format stream-json`, true},
		{`C:\Users\dev\AppData\Roaming\npm\gemini.cmd -p ""`, true},
		{`C:\Users\dev\.hoxi\bin\opencode.exe serve --hostname 127.0.0.1 --port 5411`, true},
		{`"C:\Users\dev\AppData\Local\Programs\Antigravity\bin\agy.cmd" -p ""`, true},
		{`"C:\Program Files\Git\bin\bash.exe" -c "npm test"`, false},
		{`/bin/sh -c "sleep 300"`, false},
	}
	for _, tc := range cases {
		if got := isAgentRuntimeProcessArgs(tc.args); got != tc.want {
			t.Errorf("isAgentRuntimeProcessArgs(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestStopOpenCodeToolProcessSubagentCallIDFallbackAndTrapExit(t *testing.T) {
	subagentMarker := openCodeStopMarker("ses_subagent", "call_sub_1") + `\ntrap : EXIT`
	// Notice the single command `sleep 307` without `\nwait`: `trap : EXIT` prevents `/bin/sh -c` from tail-call execve'ing into `sleep 307` and losing the marker from argv.
	daemon, pidFile := startFakeDaemon(t, `perl -e 'setpgrp(0, 0); exec "/bin/sh", "-c", "`+subagentMarker+`\nsleep 307"' & echo $! > "$HOXI_TEST_PIDFILE"; sleep 308`)
	t.Cleanup(func() {
		_ = syscall.Kill(daemon.Process.Pid, syscall.SIGKILL)
		_ = daemon.Wait()
	})
	toolPID := readPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(toolPID, syscall.SIGKILL) })

	// Stop using parent session ID `ses_parent` which does not match `ses_subagent`, so it falls back to unique callID `call_sub_1`.
	parentMarker := openCodeStopMarker("ses_parent", "call_sub_1")
	if stopped := stopOpenCodeToolProcessByMarker(daemon.Process.Pid, parentMarker); stopped == 0 {
		t.Fatal("expected subagent tool process to be stopped via unique callID fallback")
	}
	assertProcessGone(t, toolPID)
}

// startFakeDaemon runs script as a stand-in for `opencode serve`: the script is
// the root process and everything it backgrounds becomes its children.
func startFakeDaemon(t *testing.T, script string, args ...string) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	bin := execstub.Write(t, dir, "fake-opencode-serve", script+"\n")
	pidFile := filepath.Join(dir, "pid")
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "HOXI_TEST_PIDFILE="+pidFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, pidFile
}

// Stopped-tool marks are dropped once the turn ends.
func TestStopToolCommandClearsMarksWhenTurnEnds(t *testing.T) {
	svc := newStallTestService(t)
	ap, _ := startSleepingTurn(t, svc, "ses_stop_marks", "303")
	if _, err := svc.StopToolCommand("ses_stop_marks", "toolu_marks", "", "sleep 303"); err != nil {
		t.Fatal(err)
	}
	waitTurnDone(t, ap, 4*orphanDrainGrace+2*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for svc.isToolMarkedStopped("ses_stop_marks", "", "toolu_marks") {
		if time.Now().After(deadline) {
			t.Fatal("stopped-tool mark leaked past the end of the turn")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
