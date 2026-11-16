package gateway

import (
	"context"
	"runtime"
	"runtime/debug"
	"time"
)

// Go memory tuning for the long-running desktop process. Streaming turns and
// history reads leave a large idle heap behind; returning it once Hoxi goes
// quiet frees it at once on Windows, and on macOS marks it reclaimable (the
// kernel takes those pages back under memory pressure).
const (
	idleMemoryCheckEvery   = time.Minute
	idleMemoryReleaseAfter = 2 * time.Minute
)

// goMemoryStats is the Go heap as the runtime sees it, for diagnostics.
type goMemoryStats struct {
	HeapInuseBytes    uint64 `json:"heapInuseBytes"`
	HeapIdleBytes     uint64 `json:"heapIdleBytes"`
	HeapReleasedBytes uint64 `json:"heapReleasedBytes"`
	SysBytes          uint64 `json:"sysBytes"`
	NumGC             uint32 `json:"numGC"`
	LimitBytes        int64  `json:"limitBytes"`
}

func readGoMemoryStats() goMemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return goMemoryStats{
		HeapInuseBytes:    m.HeapInuse,
		HeapIdleBytes:     m.HeapIdle,
		HeapReleasedBytes: m.HeapReleased,
		SysBytes:          m.Sys,
		NumGC:             m.NumGC,
		LimitBytes:        debug.SetMemoryLimit(-1),
	}
}

// isLocallyIdle reports whether no turn, subagent or approval is in flight,
// using only in-process state so the check costs no engine request.
func (g *Gateway) isLocallyIdle() bool {
	if g.runner != nil && g.runner.ActiveProcessCount() > 0 {
		return false
	}
	if g.status != nil {
		st := g.status.Snapshot()
		if st.Running > 0 || st.PendingApprovals > 0 {
			return false
		}
	}
	return true
}

// idleMemoryReleaser returns idle heap once per quiet period that lasts at
// least idleMemoryReleaseAfter. tick is called with the current time.
type idleMemoryReleaser struct {
	idle    func() bool
	release func()
	since   time.Time
	done    bool
}

func (r *idleMemoryReleaser) tick(now time.Time) {
	if !r.idle() {
		r.since, r.done = time.Time{}, false
		return
	}
	if r.since.IsZero() {
		r.since = now
		return
	}
	if !r.done && now.Sub(r.since) >= idleMemoryReleaseAfter {
		r.release()
		r.done = true
	}
}

// StartIdleMemoryRelease returns idle heap to the OS after each period of
// inactivity, until ctx is cancelled.
func (g *Gateway) StartIdleMemoryRelease(ctx context.Context) {
	r := &idleMemoryReleaser{idle: g.isLocallyIdle, release: debug.FreeOSMemory}
	go func() {
		ticker := time.NewTicker(idleMemoryCheckEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				r.tick(now)
			}
		}
	}()
}
