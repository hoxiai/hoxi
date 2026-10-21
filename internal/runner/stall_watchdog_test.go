package runner

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// shortenTurnGraces lowers the drain/abort grace periods once for the whole
// test binary. They are never restored: watchdog and Abort goroutines started
// by a finished test may still read them, so restoring them in t.Cleanup was a
// data race.
var shortenTurnGraces sync.Once

func newStallTestService(t *testing.T) *Service {
	t.Helper()
	shortenTurnGraces.Do(func() {
		orphanDrainGrace = 120 * time.Millisecond
		abortInterruptGrace = 120 * time.Millisecond
	})
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.stall = stallPolicy{warnAfter: 5 * time.Minute, abortAfter: 30 * time.Minute}.normalized()
	return svc
}

func registerStallTestProcess(svc *Service, sessionID string, lastActivity time.Time) *activeProcess {
	ap := &activeProcess{sessionID: sessionID, done: make(chan struct{})}
	ap.stall.touch(lastActivity)
	svc.mu.Lock()
	svc.procs[sessionID] = ap
	svc.mu.Unlock()
	return ap
}

func nextStallEvent(t *testing.T, ch chan SSEEvent) int64 {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == "session.stall" {
				since, _ := evt.Properties["stalledSince"].(int64)
				return since
			}
		case <-deadline:
			t.Fatal("no session.stall event")
		}
	}
}

func TestStallWatchdogWarnsThenClearsOnOutput(t *testing.T) {
	svc := newStallTestService(t)
	events, unsub := svc.Subscribe("ses_stall")
	defer unsub()
	now := time.Now()
	last := now.Add(-6 * time.Minute)
	ap := registerStallTestProcess(svc, "ses_stall", last)

	if !svc.checkStall(ap, now.Add(-2*time.Minute)) {
		t.Fatal("turn stopped before the warn threshold")
	}
	if ap.stall.stalledSince.Load() != 0 {
		t.Fatal("flagged as stalled after only 4 minutes of silence")
	}

	svc.checkStall(ap, now)
	if got := nextStallEvent(t, events); got != last.UnixMilli() {
		t.Fatalf("stalledSince = %d, want last output %d", got, last.UnixMilli())
	}
	// A second tick must not re-announce the same stall.
	svc.checkStall(ap, now.Add(time.Minute))

	svc.noteActivity(ap)
	if got := nextStallEvent(t, events); got != 0 {
		t.Fatalf("output did not clear the stall, stalledSince = %d", got)
	}
}

func TestStallWatchdogIgnoresApprovalWaits(t *testing.T) {
	svc := newStallTestService(t)
	now := time.Now()
	ap := registerStallTestProcess(svc, "ses_wait", now.Add(-time.Hour))
	svc.gate.mu.Lock()
	svc.gate.pending["per_rnr_test"] = &pendingApproval{sid: "ses_wait", reply: make(chan approvalReply, 1)}
	svc.gate.mu.Unlock()

	if !svc.checkStall(ap, now) || ap.stall.abortError() != nil {
		t.Fatal("a turn waiting on the user was treated as stuck")
	}
	if idle := now.Sub(time.UnixMilli(ap.stall.lastActivity.Load())); idle > time.Minute {
		t.Fatalf("approval wait did not restart the idle clock (idle %s)", idle)
	}

	svc.gate.mu.Lock()
	delete(svc.gate.pending, "per_rnr_test")
	svc.gate.mu.Unlock()
	svc.noteOpenCodeActivity("ses_wait", SSEEvent{Type: "permission.asked", Properties: map[string]interface{}{"id": "per_oc"}})
	if !svc.awaitingUser(ap) {
		t.Fatal("OpenCode permission.asked not tracked as a user wait")
	}
	svc.noteOpenCodeActivity("ses_wait", SSEEvent{Type: "permission.replied", Properties: map[string]interface{}{"requestID": "per_oc"}})
	if svc.awaitingUser(ap) {
		t.Fatal("OpenCode permission.replied did not end the user wait")
	}
}

func TestStallWatchdogStopsLongSilence(t *testing.T) {
	svc := newStallTestService(t)
	now := time.Now()
	ap := registerStallTestProcess(svc, "ses_dead", now.Add(-31*time.Minute))

	if svc.checkStall(ap, now) {
		t.Fatal("watchdog kept a turn silent past abortAfter")
	}
	if !ap.wasAborted() {
		t.Fatal("silent turn was not aborted")
	}
	if err := ap.stall.abortError(); err == nil || err.Name != stallErrorName {
		t.Fatalf("abort error = %+v, want %s", err, stallErrorName)
	}
}

func TestStallPolicyFromEnv(t *testing.T) {
	t.Setenv(envStallWarnAfter, "90s")
	t.Setenv(envStallAbortAfter, "30s")
	p := stallPolicyFromEnv()
	if p.warnAfter != 90*time.Second || p.abortAfter != 90*time.Second {
		t.Fatalf("policy = %+v, want abort clamped up to warn", p)
	}
	t.Setenv(envStallAbortAfter, "0")
	if p := stallPolicyFromEnv(); p.abortAfter != 0 {
		t.Fatalf("abortAfter = %s, want disabled", p.abortAfter)
	}
}

func TestOpenCodeSubagentActivityKeepsParentAlive(t *testing.T) {
	svc := newStallTestService(t)
	now := time.Now()
	ap := registerStallTestProcess(svc, "ses_hoxi_parent", now.Add(-time.Hour))
	rec := SessionRecord{ID: "ses_hoxi_parent", ExternalID: "ses_oc_parent"}

	created := `{"type":"session.created","properties":{"info":{"id":"ses_oc_child","parentID":"ses_oc_parent"}}}`
	svc.relayOpenCodeEvent(rec, []byte(created))
	grand := `{"type":"session.created","properties":{"info":{"id":"ses_oc_grand","parentID":"ses_oc_child"}}}`
	svc.relayOpenCodeEvent(rec, []byte(grand))
	if !ap.isOpenCodeChild("ses_oc_child") || !ap.isOpenCodeChild("ses_oc_grand") {
		t.Fatal("subagent sessions were not tracked")
	}

	ap.stall.touch(now.Add(-time.Hour))
	delta := `{"type":"message.part.updated","properties":{"part":{"id":"prt_1","sessionID":"ses_oc_grand","type":"text","text":"x"}}}`
	svc.relayOpenCodeEvent(rec, []byte(delta))
	if idle := now.Sub(time.UnixMilli(ap.stall.lastActivity.Load())); idle > time.Minute {
		t.Fatalf("subagent output did not refresh the parent turn (idle %s)", idle)
	}

	asked := `{"type":"permission.asked","properties":{"id":"per_child","sessionID":"ses_oc_child"}}`
	svc.relayOpenCodeEvent(rec, []byte(asked))
	if !svc.awaitingUser(ap) {
		t.Fatal("subagent permission request was not tracked as a user wait")
	}

	ap.stall.touch(now.Add(-time.Hour))
	other := `{"type":"message.part.updated","properties":{"part":{"id":"prt_2","sessionID":"ses_unrelated","type":"text","text":"x"}}}`
	svc.relayOpenCodeEvent(rec, []byte(other))
	if idle := now.Sub(time.UnixMilli(ap.stall.lastActivity.Load())); idle < 30*time.Minute {
		t.Fatal("unrelated session activity refreshed the turn")
	}
}
