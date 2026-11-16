package reclaim

import (
	"errors"
	"testing"
	"time"
)

// daemonArgs is the argv run Hoxi records for an `opencode serve` child.
func daemonArgs(port string) []string {
	return []string{"serve", "--hostname", "127.0.0.1", "--port", port}
}

// daemonFor starts a fake daemon carrying argv and records it for owner with
// the run recorded.
func daemonFor(t *testing.T, l *Ledger, owner Identity, argv, recorded []string) (string, *helper) {
	t.Helper()
	h := spawn(t, "run", argv...)
	id := l.as(owner).RecordProcess("opencode-daemon", h.id.PID, recorded...)
	if id == "" {
		t.Fatal("daemon was not recorded")
	}
	return id, h
}

func TestSweepReclaimsDaemonOfDeadOwner(t *testing.T) {
	l := newTestLedger(t)
	args := daemonArgs("54321")
	_, daemon := daemonFor(t, l, deadOwner(t), args, args)
	entries := readEntries(t, l)
	if len(entries) != 1 || entries[0].Dir != "" || entries[0].Process == nil || len(entries[0].Process.Args) != len(args) {
		t.Fatalf("entries = %+v", entries)
	}
	r := l.Sweep()
	daemon.waitExit(t)
	if r.Processes != 1 || r.Dirs != 0 || r.Skipped != 0 || r.Pending != 0 {
		t.Fatalf("report = %+v", r)
	}
	if entries := readEntries(t, l); len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

// A dev and a release instance run at the same time: neither reclaims the
// other's daemon, and nobody reclaims its own.
func TestSweepLeavesDaemonsOfLiveOwnersAlone(t *testing.T) {
	l := newTestLedger(t)
	otherVariant := spawn(t, "run")
	theirs := daemonArgs("54001")
	_, theirDaemon := daemonFor(t, l, otherVariant.id, theirs, theirs)
	ours := daemonArgs("54002")
	_, ownDaemon := daemonFor(t, l, l.self, ours, ours)

	if r := l.Sweep(); r != (Report{}) {
		t.Fatalf("report = %+v", r)
	}
	time.Sleep(200 * time.Millisecond)
	if !theirDaemon.alive() || !ownDaemon.alive() || len(readEntries(t, l)) != 2 {
		t.Fatal("a daemon of a running Hoxi process was reclaimed")
	}
}

func TestSweepDoesNotSignalDaemonWithReusedPID(t *testing.T) {
	l := newTestLedger(t)
	args := daemonArgs("54321")
	_, stranger := daemonFor(t, l, deadOwner(t), args, args)
	if err := l.update(func(entries []entry) []entry {
		entries[0].Process.Start = "1.000000"
		return entries
	}); err != nil {
		t.Fatal(err)
	}
	r := l.Sweep()
	time.Sleep(200 * time.Millisecond)
	if !stranger.alive() {
		t.Fatal("process with a reused PID was signalled")
	}
	if r.Processes != 0 || len(readEntries(t, l)) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

// The recorded identity matches, but the live argv does not carry the
// recorded run: the process is left alone and the record is dropped.
func TestSweepDoesNotSignalDaemonWithDifferentArgs(t *testing.T) {
	recorded := daemonArgs("54321")
	for name, argv := range map[string][]string{
		"other port":      daemonArgs("54322"),
		"port elsewhere":  {"serve", "--hostname", "127.0.0.1", "--port", "54322", "--x", "54321"},
		"split run":       {"serve", "--hostname", "127.0.0.1", "--port", "--print-logs", "54321"},
		"reordered":       {"serve", "--port", "54321", "--hostname", "127.0.0.1"},
		"prefix only":     {"serve", "--hostname", "127.0.0.1", "--port"},
		"joined elements": {"serve --hostname 127.0.0.1 --port 54321"},
		"other command":   {"run", "--hostname", "127.0.0.1", "--port", "54321"},
	} {
		t.Run(name, func(t *testing.T) {
			l := newTestLedger(t)
			_, other := daemonFor(t, l, deadOwner(t), argv, recorded)
			r := l.Sweep()
			time.Sleep(200 * time.Millisecond)
			if !other.alive() {
				t.Fatalf("process with argv %q was signalled", argv)
			}
			if r.Processes != 0 || r.Skipped != 1 || len(readEntries(t, l)) != 0 {
				t.Fatalf("report = %+v", r)
			}
		})
	}
}

func TestProcessStateMatchesArgsAsOneRun(t *testing.T) {
	id := Identity{PID: 42, Start: "7.000001"}
	live := []string{"/bin/opencode", "serve", "--hostname", "127.0.0.1", "--port", "54321"}
	inspect := func(argv []string, err error) func(int) (procInfo, error) {
		return func(int) (procInfo, error) { return procInfo{start: id.Start, args: argv}, err }
	}
	cases := []struct {
		name string
		argv []string
		err  error
		args []string
		want procState
	}{
		{"whole run", live, nil, daemonArgs("54321"), stateMatch},
		{"single element", live, nil, []string{"--port"}, stateMatch},
		{"tail pair", live, nil, []string{"--port", "54321"}, stateMatch},
		{"identity only", live, nil, nil, stateMatch},
		{"other port", live, nil, []string{"--port", "5432"}, stateMismatch},
		{"gap", live, nil, []string{"serve", "--port"}, stateMismatch},
		{"longer than argv", live[:2], nil, daemonArgs("54321"), stateMismatch},
		{"unreadable argv", nil, nil, []string{"--port"}, stateUnknown},
		{"inspection error", live, errors.New("denied"), []string{"--port"}, stateUnknown},
		{"no process", nil, errNoProcess, []string{"--port"}, stateGone},
	}
	for _, c := range cases {
		if got := processState(inspect(c.argv, c.err), id, c.args); got != c.want {
			t.Errorf("%s: state = %d, want %d", c.name, got, c.want)
		}
	}
	restarted := func(int) (procInfo, error) { return procInfo{start: "8.000000", args: live}, nil }
	if got := processState(restarted, id, daemonArgs("54321")); got != stateGone {
		t.Errorf("reused PID: state = %d, want gone", got)
	}
}

func TestRecordProcessRejectsUnverifiableInput(t *testing.T) {
	l := newTestLedger(t)
	h := spawn(t, "run", "--port", "1")
	gone := deadOwner(t)
	for name, record := range map[string]func() string{
		"no args":       func() string { return l.RecordProcess("d", h.id.PID) },
		"empty arg":     func() string { return l.RecordProcess("d", h.id.PID, "") },
		"empty in run":  func() string { return l.RecordProcess("d", h.id.PID, "--port", "") },
		"pid one":       func() string { return l.RecordProcess("d", 1, "--port") },
		"exited child":  func() string { return l.RecordProcess("d", gone.PID, "--port") },
		"nil ledger":    func() string { var nl *Ledger; return nl.RecordProcess("d", h.id.PID, "--port") },
		"attach no arg": func() string { _, id, _ := l.MkdirTemp("d", "hoxi-d-"); l.AttachProcess(id, h.id.PID); return "" },
	} {
		if id := record(); id != "" {
			t.Errorf("%s: recorded %q", name, id)
		}
	}
	for _, e := range readEntries(t, l) {
		if e.Process != nil {
			t.Fatalf("%s: unverifiable process recorded: %+v", e.Kind, e.Process)
		}
	}
}

func TestMalformedProcessArgsAreDroppedWithoutAction(t *testing.T) {
	l := newTestLedger(t)
	h := spawn(t, "run", "--port", "54321")
	owner := deadOwner(t)
	writeEntries(t, l,
		entry{ID: "empty-arg", Owner: owner, Process: &process{Identity: h.id, Args: []string{""}}},
		entry{ID: "empty-in-run", Owner: owner, Process: &process{Identity: h.id, Args: []string{"--port", ""}}},
		entry{ID: "no-args", Owner: owner, Process: &process{Identity: h.id, Args: []string{}}},
	)
	if r := l.Sweep(); r.Skipped != 3 || r.Processes != 0 {
		t.Fatalf("report = %+v", r)
	}
	time.Sleep(200 * time.Millisecond)
	if !h.alive() || len(readEntries(t, l)) != 0 {
		t.Fatal("malformed record was acted on")
	}
}

func TestForgetDropsRecordedDaemon(t *testing.T) {
	l := newTestLedger(t)
	args := daemonArgs("54321")
	id, daemon := daemonFor(t, l, l.self, args, args)
	l.Forget(id)
	if entries := readEntries(t, l); len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
	if !daemon.alive() {
		t.Fatal("Forget must not signal the process")
	}
}
