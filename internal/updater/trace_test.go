package updater

import (
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

func captureTrace(t *testing.T) *[]diagnostics.Entry {
	t.Helper()
	var got []diagnostics.Entry
	old := traceRecord
	traceRecord = func(e diagnostics.Entry) { got = append(got, e) }
	tracedPhase.Lock()
	oldPhase := tracedPhase.phase
	tracedPhase.phase = ""
	tracedPhase.Unlock()
	t.Cleanup(func() {
		traceRecord = old
		tracedPhase.Lock()
		tracedPhase.phase = oldPhase
		tracedPhase.Unlock()
	})
	return &got
}

func TestTraceUpdateRecordsOnlyKnownEvents(t *testing.T) {
	got := captureTrace(t)
	traceUpdate("updater install requested", 0, 0)
	traceUpdate("updater admission checked", 409, 1500*time.Millisecond)
	// Free-form text (paths, error messages) must never reach the log.
	traceUpdate("/Users/someone/secret failed: token=abc", 0, 0)
	traceUpdate("updater phase bogus", 0, 0)

	if len(*got) != 2 {
		t.Fatalf("recorded %d entries, want 2: %+v", len(*got), *got)
	}
	first, second := (*got)[0], (*got)[1]
	if first.Message != "updater install requested" || first.Source != "updater" || first.Kind != "system" {
		t.Fatalf("unexpected first entry: %+v", first)
	}
	if second.Status != 409 || second.DurationMs != 1500 {
		t.Fatalf("admission entry lost status or duration: %+v", second)
	}
}

func TestTracePhaseRecordsChangesOnly(t *testing.T) {
	got := captureTrace(t)
	for _, phase := range []string{"preparing", "downloading", "downloading", "downloading", "staging", "", ""} {
		tracePhase(phase)
	}
	want := []string{"updater phase preparing", "updater phase downloading", "updater phase staging", "updater phase cleared"}
	if len(*got) != len(want) {
		t.Fatalf("recorded %d phases, want %d: %+v", len(*got), len(want), *got)
	}
	for i, msg := range want {
		if (*got)[i].Message != msg {
			t.Fatalf("phase %d = %q, want %q", i, (*got)[i].Message, msg)
		}
	}
}
