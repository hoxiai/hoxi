package reclaim

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const helperEnv = "HOXI_RECLAIM_HELPER"

// TestMain doubles as a stand-in for a headless Chrome: the test binary
// re-executes itself and blocks until signalled.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
	}
	_, _ = os.Stdout.WriteString("ready\n")
	time.Sleep(time.Hour)
	os.Exit(0)
}

type helper struct {
	cmd  *exec.Cmd
	id   Identity
	done chan struct{}
}

func spawn(t *testing.T, mode string, args ...string) *helper {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	ready, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	cmd.Stdout = w
	err = cmd.Start()
	_ = w.Close()
	if err != nil {
		t.Fatal(err)
	}
	h := &helper{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(h.done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-h.done
	})
	_ = ready.SetReadDeadline(time.Now().Add(10 * time.Second))
	if line, err := bufio.NewReader(ready).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper %d not ready: %q %v", cmd.Process.Pid, line, err)
	}
	info, err := inspectProcess(cmd.Process.Pid)
	if err != nil || !containsArgs(info.args, args) {
		t.Fatalf("helper %d not inspectable: %+v %v", cmd.Process.Pid, info, err)
	}
	h.id = Identity{PID: cmd.Process.Pid, Start: info.start}
	return h
}

func containsArgs(have, want []string) bool {
	if have == nil {
		return false
	}
	set := map[string]bool{}
	for _, a := range have {
		set[a] = true
	}
	for _, a := range want {
		if !set[a] {
			return false
		}
	}
	return true
}

func (h *helper) alive() bool {
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

func (h *helper) waitExit(t *testing.T) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("helper %d still running", h.id.PID)
	}
}

// deadOwner returns the identity of a process that has exited.
func deadOwner(t *testing.T) Identity {
	t.Helper()
	h := spawn(t, "run")
	_ = h.cmd.Process.Kill()
	h.waitExit(t)
	return h.id
}

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "reclaim.json"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	l.root = t.TempDir()
	if l.rootResolved, err = filepath.EvalSymlinks(l.root); err != nil {
		t.Fatal(err)
	}
	l.termWait, l.killWait = 2*time.Second, 2*time.Second
	return l
}

// as returns a view of l that records resources for another owner, the way
// a different Hoxi process sharing the ledger file would.
func (l *Ledger) as(owner Identity) *Ledger {
	return &Ledger{path: l.path, self: owner, root: l.root, rootResolved: l.rootResolved, logger: l.logger,
		inspect: l.inspect, signal: l.signal, termWait: l.termWait, killWait: l.killWait}
}

// browserFor records a profile directory and a fake Chrome using it for owner.
func browserFor(t *testing.T, l *Ledger, owner Identity) (string, *helper) {
	t.Helper()
	dir, id, err := l.as(owner).MkdirTemp("browser-profile", "hoxi-browser-profile-")
	if err != nil || id == "" {
		t.Fatal(dir, id, err)
	}
	arg := "--user-data-dir=" + dir
	h := spawn(t, "run", arg)
	l.as(owner).AttachProcess(id, h.id.PID, arg)
	return dir, h
}

func readEntries(t *testing.T, l *Ledger) []entry {
	t.Helper()
	raw, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var f ledgerFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f.Entries
}

func writeEntries(t *testing.T, l *Ledger, entries ...entry) {
	t.Helper()
	if err := l.update(func(existing []entry) []entry { return append(existing, entries...) }); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestSweepReclaimsBrowserOfDeadOwner(t *testing.T) {
	l := newTestLedger(t)
	dir, chrome := browserFor(t, l, deadOwner(t))
	if entries := readEntries(t, l); len(entries) != 1 || entries[0].Process == nil || entries[0].Process.PID != chrome.id.PID {
		t.Fatalf("entries = %+v", entries)
	}
	r := l.Sweep()
	chrome.waitExit(t)
	if exists(dir) {
		t.Fatal("profile directory survived")
	}
	if r.Processes != 1 || r.Dirs != 1 || r.Skipped != 0 || r.Pending != 0 {
		t.Fatalf("report = %+v", r)
	}
	if entries := readEntries(t, l); len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

// A dev and a release instance share the ledger; each only reclaims records
// whose owner has exited, never the browser of an instance still running.
func TestSweepLeavesBrowsersOfLiveOwnersAlone(t *testing.T) {
	l := newTestLedger(t)
	otherVariant := spawn(t, "run")
	liveDir, liveChrome := browserFor(t, l, otherVariant.id)
	deadDir, deadChrome := browserFor(t, l, deadOwner(t))
	ownDir, ownID, err := l.MkdirTemp("opencode-plugin", "hoxi-opencode-plugin-")
	if err != nil {
		t.Fatal(err)
	}

	r := l.Sweep()
	deadChrome.waitExit(t)
	if exists(deadDir) || r.Processes != 1 || r.Dirs != 1 {
		t.Fatalf("dead owner not reclaimed: %+v", r)
	}
	if !liveChrome.alive() || !exists(liveDir) || !exists(ownDir) {
		t.Fatal("live owner's resources were touched")
	}
	entries := readEntries(t, l)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	for _, e := range entries {
		if e.Owner != otherVariant.id && !(e.ID == ownID && e.Owner == l.self) {
			t.Fatalf("unexpected record %+v", e)
		}
	}

	// Once the other instance exits, its browser becomes reclaimable.
	_ = otherVariant.cmd.Process.Kill()
	otherVariant.waitExit(t)
	if r := l.Sweep(); r.Processes != 1 || r.Dirs != 1 {
		t.Fatalf("report = %+v", r)
	}
	liveChrome.waitExit(t)
	if exists(liveDir) || !exists(ownDir) {
		t.Fatal("wrong directories removed")
	}
}

func TestSweepDoesNotSignalReusedPID(t *testing.T) {
	l := newTestLedger(t)
	dir, _, err := l.as(deadOwner(t)).MkdirTemp("browser-profile", "hoxi-browser-profile-")
	if err != nil {
		t.Fatal(err)
	}
	arg := "--user-data-dir=" + dir
	// The PID now belongs to a process that started later than the recorded one.
	stranger := spawn(t, "run", arg)
	stale := stranger.id
	stale.Start = "1.000000"
	if err := l.update(func(entries []entry) []entry {
		entries[0].Process = &process{Identity: stale, Args: []string{arg}}
		return entries
	}); err != nil {
		t.Fatal(err)
	}

	r := l.Sweep()
	time.Sleep(200 * time.Millisecond)
	if !stranger.alive() {
		t.Fatal("process with a reused PID was signalled")
	}
	// The recorded Chrome is gone, so only its abandoned profile is removed.
	if r.Processes != 0 || r.Dirs != 1 || exists(dir) || len(readEntries(t, l)) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestSweepDoesNotSignalProcessWithDifferentUserDataDir(t *testing.T) {
	l := newTestLedger(t)
	dir, id, err := l.as(deadOwner(t)).MkdirTemp("browser-profile", "hoxi-browser-profile-")
	if err != nil {
		t.Fatal(err)
	}
	other := spawn(t, "run", "--user-data-dir="+filepath.Join(l.root, "hoxi-browser-profile-other"))
	l.as(readEntries(t, l)[0].Owner).AttachProcess(id, other.id.PID, "--user-data-dir="+dir)

	r := l.Sweep()
	time.Sleep(200 * time.Millisecond)
	if !other.alive() {
		t.Fatal("process without the recorded --user-data-dir was signalled")
	}
	if !exists(dir) {
		t.Fatal("directory of an unverified record was removed")
	}
	if r.Processes != 0 || r.Dirs != 0 || r.Skipped != 1 || len(readEntries(t, l)) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestCorruptLedgerIsBackedUpAndNotActedOn(t *testing.T) {
	l := newTestLedger(t)
	_, chrome := browserFor(t, l, deadOwner(t))
	raw, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := raw[:len(raw)/2]
	if err := os.WriteFile(l.path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	if r := l.Sweep(); r != (Report{}) {
		t.Fatalf("report = %+v", r)
	}
	time.Sleep(200 * time.Millisecond)
	if !chrome.alive() {
		t.Fatal("process from a corrupt ledger was signalled")
	}
	backups, _ := filepath.Glob(l.path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if got, _ := os.ReadFile(backups[0]); string(got) != string(corrupt) {
		t.Fatal("backup does not hold the corrupt bytes")
	}
	if entries := readEntries(t, l); len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestCorruptLedgerThatCannotBeBackedUpIsLeftUntouched(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	l := newTestLedger(t)
	_, chrome := browserFor(t, l, deadOwner(t))
	corrupt := []byte(`{"version":1,"entries":[`)
	if err := os.WriteFile(l.path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(l.path)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	l.Sweep()
	if _, id, err := l.MkdirTemp("opencode-plugin", "hoxi-opencode-plugin-"); err != nil || id != "" {
		t.Fatalf("MkdirTemp recorded into an unreadable ledger: id=%q err=%v", id, err)
	}
	if got, _ := os.ReadFile(l.path); string(got) != string(corrupt) {
		t.Fatal("corrupt ledger was overwritten")
	}
	if !chrome.alive() {
		t.Fatal("process was signalled")
	}
}

func TestNewerLedgerVersionIsNotRewritten(t *testing.T) {
	l := newTestLedger(t)
	newer := []byte(`{"version":2,"entries":[]}`)
	if err := os.WriteFile(l.path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	l.Sweep()
	if _, id, _ := l.MkdirTemp("opencode-plugin", "hoxi-opencode-plugin-"); id != "" {
		t.Fatal("recorded into a newer ledger")
	}
	if got, _ := os.ReadFile(l.path); string(got) != string(newer) {
		t.Fatal("newer ledger was rewritten")
	}
	if backups, _ := filepath.Glob(l.path + ".corrupt-*"); len(backups) != 0 {
		t.Fatalf("newer ledger treated as corrupt: %v", backups)
	}
}

func TestSweepRemovesOnlyVerifiedDirectories(t *testing.T) {
	l := newTestLedger(t)
	owner := deadOwner(t)
	mk := func(path string) string {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	outside := mk(filepath.Join(t.TempDir(), "hoxi-browser-profile-outside"))
	unprefixed := mk(filepath.Join(l.root, "browser-profile-x"))
	nested := mk(filepath.Join(l.root, "hoxi-parent", "hoxi-child"))
	bare := mk(filepath.Join(l.root, "hoxi-"))
	target := mk(filepath.Join(t.TempDir(), "target"))
	link := filepath.Join(l.root, "hoxi-browser-profile-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(l.root, "hoxi-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dirs := []string{outside, unprefixed, nested, bare, link, file,
		"hoxi-relative", filepath.Join(l.root, "hoxi-a") + "/../hoxi-browser-profile-link"}
	var entries []entry
	for i, dir := range dirs {
		entries = append(entries, entry{ID: string(rune('a' + i)), Kind: "browser-profile", Owner: owner, Dir: dir})
	}
	writeEntries(t, l, entries...)

	r := l.Sweep()
	if r.Dirs != 0 || r.Skipped != len(dirs) {
		t.Fatalf("report = %+v", r)
	}
	for _, path := range []string{outside, unprefixed, nested, bare, link, file, filepath.Join(target, "keep")} {
		if !exists(path) {
			t.Fatalf("%s was removed", path)
		}
	}
}

func TestSweepEscalatesToSIGKILL(t *testing.T) {
	l := newTestLedger(t)
	l.termWait = 300 * time.Millisecond
	dir, id, err := l.as(deadOwner(t)).MkdirTemp("browser-profile", "hoxi-browser-profile-")
	if err != nil {
		t.Fatal(err)
	}
	arg := "--user-data-dir=" + dir
	stubborn := spawn(t, "ignore-term", arg)
	l.as(readEntries(t, l)[0].Owner).AttachProcess(id, stubborn.id.PID, arg)
	start := time.Now()
	if r := l.Sweep(); r.Processes != 1 || r.Dirs != 1 {
		t.Fatalf("report = %+v", r)
	}
	stubborn.waitExit(t)
	if time.Since(start) < l.termWait {
		t.Fatal("process exited before SIGKILL; SIGTERM was not ignored")
	}
}

// A record whose process cannot be inspected is kept, re-owned by the sweeper;
// if that sweeper dies too, the next start claims it again.
func TestUninspectableRecordIsRetriedByLaterSweeper(t *testing.T) {
	l := newTestLedger(t)
	dir, chrome := browserFor(t, l, deadOwner(t))
	failing := l.as(deadOwner(t)) // a sweeper that is about to crash
	failing.inspect = func(pid int) (procInfo, error) {
		if pid == chrome.id.PID {
			return procInfo{}, errors.New("inspection denied")
		}
		return inspectProcess(pid)
	}
	if r := failing.Sweep(); r.Pending != 1 || r.Processes != 0 {
		t.Fatalf("report = %+v", r)
	}
	if !chrome.alive() || !exists(dir) {
		t.Fatal("acted on an uninspectable process")
	}
	if entries := readEntries(t, l); len(entries) != 1 || entries[0].Owner != failing.self {
		t.Fatalf("record not claimed by the failing sweeper: %+v", entries)
	}
	if r := l.Sweep(); r.Processes != 1 || r.Dirs != 1 {
		t.Fatalf("report = %+v", r)
	}
	chrome.waitExit(t)
}

func TestSweepNeverTouchesOwnRecords(t *testing.T) {
	l := newTestLedger(t)
	dir, chrome := browserFor(t, l, l.self)
	if r := l.Sweep(); r != (Report{}) {
		t.Fatalf("report = %+v", r)
	}
	if !chrome.alive() || !exists(dir) || len(readEntries(t, l)) != 1 {
		t.Fatal("own record was reclaimed")
	}
}

func TestMalformedRecordsAreDroppedWithoutAction(t *testing.T) {
	l := newTestLedger(t)
	dir, chrome := browserFor(t, l, l.self)
	arg := "--user-data-dir=" + dir
	writeEntries(t, l,
		entry{ID: "no-owner-start", Owner: Identity{PID: 999999}, Dir: dir},
		entry{ID: "no-arg", Owner: deadOwner(t), Process: &process{Identity: chrome.id}},
		entry{ID: "pid-one", Owner: deadOwner(t), Process: &process{Identity: Identity{PID: 1, Start: "x"}, Args: []string{arg}}},
		entry{ID: "", Owner: deadOwner(t), Dir: dir},
	)
	if r := l.Sweep(); r.Skipped != 4 || r.Processes != 0 || r.Dirs != 0 {
		t.Fatalf("report = %+v", r)
	}
	if !chrome.alive() || !exists(dir) || len(readEntries(t, l)) != 1 {
		t.Fatal("malformed record was acted on")
	}
}

func TestForgetDropsOnlyOwnRecord(t *testing.T) {
	l := newTestLedger(t)
	other := spawn(t, "run")
	_, foreignID, _ := l.as(other.id).MkdirTemp("opencode-plugin", "hoxi-opencode-plugin-")
	_, ownID, _ := l.MkdirTemp("opencode-plugin", "hoxi-opencode-plugin-")
	l.Forget(foreignID)
	l.Forget(ownID)
	if entries := readEntries(t, l); len(entries) != 1 || entries[0].ID != foreignID {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestMkdirTempRejectsForeignPrefixes(t *testing.T) {
	l := newTestLedger(t)
	for _, prefix := range []string{"browser-", "hoxi-../x", "hoxi-*"} {
		if _, _, err := l.MkdirTemp("x", prefix); err == nil {
			t.Fatalf("prefix %q accepted", prefix)
		}
	}
	var nilLedger *Ledger
	dir, id, err := nilLedger.MkdirTemp("x", "hoxi-nil-")
	if err != nil || id != "" || !strings.HasPrefix(filepath.Base(dir), "hoxi-nil-") {
		t.Fatal(dir, id, err)
	}
	_ = os.Remove(dir)
	nilLedger.AttachProcess("x", 2, "--x")
	nilLedger.Forget("x")
	if r := nilLedger.Sweep(); r != (Report{}) {
		t.Fatal(r)
	}
}

// Two Hoxi processes write the same file; the file lock keeps every update.
func TestConcurrentLedgersKeepEveryRecord(t *testing.T) {
	a := newTestLedger(t)
	b, err := Open(a.path, a.logger)
	if err != nil {
		t.Fatal(err)
	}
	b.root, b.rootResolved = a.root, a.rootResolved
	var wg sync.WaitGroup
	var mu sync.Mutex
	kept := map[string]bool{}
	for _, l := range []*Ledger{a, b, a, b} {
		wg.Add(1)
		go func(l *Ledger) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, id, err := l.MkdirTemp("opencode-plugin", "hoxi-opencode-plugin-")
				if err != nil || id == "" {
					t.Error(id, err)
					return
				}
				if i%2 == 0 {
					l.Forget(id)
					continue
				}
				mu.Lock()
				kept[id] = true
				mu.Unlock()
			}
		}(l)
	}
	wg.Wait()
	entries := readEntries(t, a)
	if len(entries) != len(kept) {
		t.Fatalf("got %d records, want %d", len(entries), len(kept))
	}
	for _, e := range entries {
		if !kept[e.ID] {
			t.Fatalf("unexpected record %s", e.ID)
		}
	}
}

func TestInspectProcessIdentifiesIncarnation(t *testing.T) {
	self, err := inspectProcess(os.Getpid())
	if err != nil || self.start == "" || self.zombie {
		t.Fatal(self, err)
	}
	again, _ := inspectProcess(os.Getpid())
	if again.start != self.start {
		t.Fatal("start token is not stable")
	}
	child := spawn(t, "run", "--user-data-dir=/tmp/hoxi-x y")
	info, err := inspectProcess(child.id.PID)
	if err != nil || info.start == self.start || !containsArgs(info.args, []string{"--user-data-dir=/tmp/hoxi-x y"}) {
		t.Fatal(info, err)
	}
	gone := deadOwner(t)
	if _, err := inspectProcess(gone.PID); !errors.Is(err, errNoProcess) {
		t.Fatalf("exited process: %v", err)
	}
}
