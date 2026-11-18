package procgroup

import (
	"reflect"
	"testing"
	"time"
)

func TestOwnedProcessForgetCancelsPendingScans(t *testing.T) {
	l := NewOwnedLedger()
	watch := l.StartWatch("ses_a", "call_1")
	other := l.StartWatch("ses_b", "call_1")
	if watch == nil || other == nil {
		t.Fatal("expected watches for both sessions")
	}
	if l.StartWatch("ses_a", "call_1") != nil {
		t.Fatal("duplicate scans must be suppressed")
	}
	l.RecordWatched("ses_a", "call_1", watch, 4242, "server a")
	l.RecordWatched("ses_b", "call_1", other, 5353, "server b")
	done := make(chan bool, 1)
	go func() { done <- watch.Wait(time.Hour) }()
	l.Forget("ses_a")
	select {
	case elapsed := <-done:
		if elapsed {
			t.Fatal("deleted session's scan delay must be canceled")
		}
	case <-time.After(time.Second):
		t.Fatal("deleted session's scanner is still waiting")
	}
	if watch.Wait(0) {
		t.Fatal("deleted session's zero-delay scan must not proceed")
	}
	l.Forget("ses_a")
	select {
	case <-other.Done():
		t.Fatal("another session's scanner must survive")
	default:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.groups["ses_a"]) != 0 || len(l.groups["ses_b"]) != 1 {
		t.Fatalf("unexpected process ownership after deletion: %v", l.groups)
	}
}

func TestOwnedProcessForgottenScanCannotRestoreOwnership(t *testing.T) {
	l := NewOwnedLedger()
	stale := l.StartWatch("ses_a", "call_1")
	l.Forget("ses_a")
	l.RecordWatched("ses_a", "call_1", stale, 4242, "stale server")
	if len(l.groups) != 0 {
		t.Fatal("an in-flight scan restored deleted ownership")
	}

	current := l.StartWatch("ses_a", "call_1")
	if current == nil {
		t.Fatal("expected a new watch after cleanup")
	}
	l.RecordWatched("ses_a", "call_1", stale, 4242, "stale server")
	l.EndWatch("ses_a", "call_1", stale)
	if l.StartWatch("ses_a", "call_1") != nil {
		t.Fatal("stale scanner cleanup removed the replacement watch")
	}
	l.RecordWatched("ses_a", "call_1", current, 5353, "current server")
	l.EndWatch("ses_a", "call_1", current)
	l.RecordWatched("ses_a", "call_1", current, 6464, "finished scan")
	groups := l.groups["ses_a"]
	if len(groups) != 1 || groups[0].pgid != 5353 {
		t.Fatalf("only the current scanner may record ownership: %v", groups)
	}
}

func TestOwnedProcessWindowsPIDLedgerVerifiesCreationTimeAndRejectsReuseAndOrphans(t *testing.T) {
	l := NewOwnedLedger()
	watchA := l.StartWatch("ses_win_a", "call_1")
	watchB := l.StartWatch("ses_win_b", "call_1")
	if watchA == nil || watchB == nil {
		t.Fatal("expected watches")
	}
	defer l.EndWatch("ses_win_a", "call_1", watchA)
	defer l.EndWatch("ses_win_b", "call_1", watchB)

	l.RecordWatchedPID("ses_win_a", "call_1", watchA, 5000, 300, `bash.exe -c "node server.js &"`)
	l.RecordWatchedPID("ses_win_b", "call_1", watchB, 6000, 310, `bash.exe -c "python -m http.server"`)

	byPID1 := map[int]Snapshot{
		5000: {PID: 5000, PPID: 1000, CreateTime: 300, Args: `bash.exe -c "node server.js &"`},
		5001: {PID: 5001, PPID: 5000, CreateTime: 305, Args: `node.exe server.js`},
		4999: {PID: 4999, PPID: 5000, CreateTime: 100, Args: `unrelated-orphan.exe`},
		5002: {PID: 5002, PPID: 5000, CreateTime: 306, Args: `C:\Users\dev\.local\bin\claude.exe`},
		6000: {PID: 6000, PPID: 1000, CreateTime: 310, Args: `bash.exe -c "python -m http.server"`},
	}
	children1 := map[int][]int{
		5000: {5001, 4999, 5002},
	}

	ownedA1 := l.LivePIDs("ses_win_a", byPID1, children1)
	if !ownedA1[5000] || !ownedA1[5001] {
		t.Fatalf("expected ses_win_a to own parent 5000 and child 5001, got %v", ownedA1)
	}
	if ownedA1[4999] {
		t.Fatalf("pre-existing orphan PID 4999 (createTime < parent) must never be adopted: %v", ownedA1)
	}
	if ownedA1[5002] {
		t.Fatalf("agent runtime binary PID 5002 must never be adopted: %v", ownedA1)
	}
	if ownedA1[6000] {
		t.Fatalf("other session's PID 6000 must not be owned by ses_win_a: %v", ownedA1)
	}

	byPID2 := map[int]Snapshot{
		5000: {PID: 5000, PPID: 800, CreateTime: 900, Args: `bash.exe -c "node server.js &"`},
		5001: {PID: 5001, PPID: 5000, CreateTime: 305, Args: `node.exe server.js`},
		5099: {PID: 5099, PPID: 5000, CreateTime: 905, Args: `victim.exe`},
		6000: {PID: 6000, PPID: 1000, CreateTime: 310, Args: `bash.exe -c "python -m http.server"`},
	}
	children2 := map[int][]int{
		5000: {5099},
	}

	ownedA2 := l.LivePIDs("ses_win_a", byPID2, children2)
	if ownedA2[5000] || ownedA2[5099] {
		t.Fatalf("recycled PID 5000 (createTime mismatch) and its child 5099 must be rejected: %v", ownedA2)
	}
	if !ownedA2[5001] {
		t.Fatalf("surviving background child 5001 must remain owned after parent shell exited: %v", ownedA2)
	}

	l.Forget("ses_win_a")
	if after := l.LivePIDs("ses_win_a", byPID2, children2); len(after) != 0 {
		t.Fatalf("expected ses_win_a PIDs wiped after forget, got %v", after)
	}
	if other := l.LivePIDs("ses_win_b", byPID2, children2); !other[6000] {
		t.Fatalf("expected ses_win_b PID 6000 to survive ses_win_a deletion, got %v", other)
	}
}

func TestSelectOpenCodeDaemonPIDPrefersLeaf(t *testing.T) {
	byPID := map[int]Snapshot{
		100: {PID: 100, PPID: 1, PGID: 100, Args: "/bin/sh -c opencode serve --hostname 127.0.0.1 --port 5432"},
		200: {PID: 200, PPID: 100, PGID: 100, Args: "/usr/local/bin/opencode serve --hostname 127.0.0.1 --port 5432"},
	}
	children := map[int][]int{
		1:   {100},
		100: {200},
	}
	if got := SelectOpenCodeDaemonPID(byPID, children, "5432"); got != 200 {
		t.Fatalf("SelectOpenCodeDaemonPID = %d, want leaf daemon PID 200", got)
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
		if got := IsAgentRuntimeProcessArgs(tc.args); got != tc.want {
			t.Errorf("IsAgentRuntimeProcessArgs(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestFindOpenCodeShellGroupAndRuntimeShellGroups(t *testing.T) {
	marker := StopMarkerPrefix + "ses_eng:call_1"
	byPID := map[int]Snapshot{
		100: {PID: 100, PGID: 100, Args: "opencode serve --hostname 127.0.0.1 --port 4096"},
		200: {PID: 200, PGID: 200, Args: "/bin/zsh -c # hoxi-stop:ses_eng:call_9\\012sleep 30"},
		300: {PID: 300, PGID: 200, Args: "bash -c # hoxi-stop:ses_eng:call_1\\012kill 1"},
		400: {PID: 400, PGID: 100, Args: "/bin/zsh -c " + marker + "\\012sleep 5"},
	}
	children := map[int][]int{100: {200, 400}, 200: {300}}
	if got := FindOpenCodeShellGroup(byPID, children, "4096", marker); got != 0 {
		t.Fatalf("got %d; a forged tag or a shell in the daemon's group must not be recorded", got)
	}
	byPID[500] = Snapshot{PID: 500, PGID: 500, Args: "/bin/zsh -c " + marker + "\\012nohup ./server &"}
	children[100] = append(children[100], 500)
	if got := FindOpenCodeShellGroup(byPID, children, "4096", marker); got != 500 {
		t.Fatalf("got %d, want the tagged shell 500", got)
	}

	rtByPID := map[int]Snapshot{
		10: {PID: 10, PGID: 10, Args: "claude -p"},
		11: {PID: 11, PGID: 11, Args: "/bin/zsh -c npm run dev"},
		12: {PID: 12, PGID: 11, Args: "node vite"},
		13: {PID: 13, PGID: 50, Args: "joined a foreign group"},
		50: {PID: 50, PGID: 50, Args: "user terminal"},
		60: {PID: 60, PGID: 60, Args: "hoxi"},
	}
	rtChildren := map[int][]int{10: {11, 13}, 11: {12}}
	if got := RuntimeShellGroups(rtByPID, rtChildren, 10, 60); !reflect.DeepEqual(got, []int{10, 11}) {
		t.Fatalf("groups = %v, want [10 11]", got)
	}
}
