package updater

import (
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

// Update steps are written to the system diagnostics log so a stalled install
// click can be traced afterwards. Only these fixed event names are recorded:
// no URLs, paths, versions or error text.
var traceEvents = map[string]bool{
	"updater install requested":       true,
	"updater install returned":        true,
	"updater install rejected":        true,
	"updater sparkle check started":   true,
	"updater sparkle alert reused":    true,
	"updater sparkle check finished":  true,
	"updater sparkle aborted":         true,
	"updater sparkle update found":    true,
	"updater sparkle up to date":      true,
	"updater sparkle error":           true,
	"updater admission checked":       true,
	"updater busy confirm shown":      true,
	"updater busy confirm answered":   true,
	"updater phase preparing":         true,
	"updater phase downloading":       true,
	"updater phase verifying":         true,
	"updater phase staging":           true,
	"updater phase restarting":        true,
	"updater phase failed":            true,
	"updater phase cleared":           true,
	"updater dmg self-update started": true,
}

// traceRecord is replaceable in tests.
var traceRecord = diagnostics.Record

func traceUpdate(event string, status int, elapsed time.Duration) {
	if !traceEvents[event] {
		return
	}
	e := diagnostics.Entry{Kind: "system", Level: "INFO", Source: "updater", Message: event, Status: status}
	if elapsed > 0 {
		e.DurationMs = elapsed.Milliseconds()
	}
	traceRecord(e)
}

var tracedPhase struct {
	sync.Mutex
	phase string
}

// tracePhase records a phase only when it changes; download ticks repeat it.
func tracePhase(phase string) {
	tracedPhase.Lock()
	if tracedPhase.phase == phase {
		tracedPhase.Unlock()
		return
	}
	tracedPhase.phase = phase
	tracedPhase.Unlock()
	if phase == "" {
		traceUpdate("updater phase cleared", 0, 0)
		return
	}
	traceUpdate("updater phase "+phase, 0, 0)
}
