package runner

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The stall watchdog measures silence, not total runtime: a busy turn whose
// CLI (or OpenCode session) has produced no output for warnAfter is flagged
// as possibly stuck (status bar Attention + a `session.stall` event), and is
// stopped after abortAfter. Long but chatty turns are never cut short, and
// time spent waiting on the user's approval does not count as silence.

const (
	defaultStallWarnAfter  = 5 * time.Minute
	defaultStallAbortAfter = 30 * time.Minute

	// Durations such as "90s" or "10m"; "0" disables warning / auto-stop.
	envStallWarnAfter  = "HOXI_RUNNER_STALL_WARN"
	envStallAbortAfter = "HOXI_RUNNER_STALL_ABORT"

	stallErrorName = "RunnerStalledError"
)

type stallPolicy struct {
	warnAfter  time.Duration // 0 disables the watchdog
	abortAfter time.Duration // 0 only warns
	interval   time.Duration
}

func stallPolicyFromEnv() stallPolicy {
	p := stallPolicy{warnAfter: defaultStallWarnAfter, abortAfter: defaultStallAbortAfter}
	if d, ok := envDuration(envStallWarnAfter); ok {
		p.warnAfter = d
	}
	if d, ok := envDuration(envStallAbortAfter); ok {
		p.abortAfter = d
	}
	return p.normalized()
}

func (p stallPolicy) normalized() stallPolicy {
	if p.abortAfter > 0 && p.abortAfter < p.warnAfter {
		p.abortAfter = p.warnAfter
	}
	if p.interval <= 0 {
		p.interval = min(max(p.warnAfter/10, time.Second), 15*time.Second)
	}
	return p
}

func envDuration(key string) (time.Duration, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// stallState is the watchdog's view of one active turn.
type stallState struct {
	lastActivity atomic.Int64 // unix ms of the latest output
	stalledSince atomic.Int64 // lastActivity when flagged; 0 while healthy

	// mu serializes stall publication so the latest state is always the one
	// delivered last, and guards the fields below.
	mu       sync.Mutex
	awaiting map[string]bool // OpenCode permission/question IDs open on the user
	abortErr *MessageError
}

func (st *stallState) touch(now time.Time) {
	st.lastActivity.Store(now.UnixMilli())
}

func (st *stallState) abortError() *MessageError {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.abortErr
}

func (st *stallState) setAwaiting(id string, waiting bool) {
	if id == "" {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if waiting {
		if st.awaiting == nil {
			st.awaiting = make(map[string]bool)
		}
		st.awaiting[id] = true
	} else {
		delete(st.awaiting, id)
	}
}

func (st *stallState) hasAwaiting() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.awaiting) > 0
}

// noteActivity records output from the turn and clears a stall flag.
func (s *Service) noteActivity(ap *activeProcess) {
	ap.stall.touch(time.Now())
	if ap.stall.stalledSince.Swap(0) != 0 {
		s.publishStall(ap)
	}
}

// noteOpenCodeActivity feeds OpenCode session events into the watchdog of
// the conversation's active turn, tracking questions asked of the user.
func (s *Service) noteOpenCodeActivity(sessionID string, evt SSEEvent) {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap == nil {
		return
	}
	switch evt.Type {
	case "permission.asked", "question.asked", "question.v2.asked":
		id, _ := evt.Properties["id"].(string)
		ap.stall.setAwaiting(id, true)
	case "permission.replied", "question.replied", "question.rejected", "question.v2.replied", "question.v2.rejected":
		id, _ := evt.Properties["requestID"].(string)
		ap.stall.setAwaiting(id, false)
	}
	s.noteActivity(ap)
}

// stalledSessions maps each flagged active turn to its stalledSince.
func (s *Service) stalledSessions() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64)
	for id, ap := range s.procs {
		if since := ap.stall.stalledSince.Load(); since != 0 {
			out[id] = since
		}
	}
	return out
}

func (s *Service) watchStall(ap *activeProcess) {
	if s.stall.warnAfter <= 0 {
		return
	}
	ticker := time.NewTicker(s.stall.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ap.done:
			return
		case now := <-ticker.C:
			if !s.checkStall(ap, now) {
				return
			}
		}
	}
}

// checkStall evaluates one tick; it reports false once the turn was stopped.
func (s *Service) checkStall(ap *activeProcess, now time.Time) bool {
	if s.awaitingUser(ap) {
		// The clock restarts when the user answers.
		s.noteActivity(ap)
		return true
	}
	last := ap.stall.lastActivity.Load()
	idle := now.Sub(time.UnixMilli(last))
	if s.stall.abortAfter > 0 && idle >= s.stall.abortAfter {
		s.abortStalled(ap, idle)
		return false
	}
	if idle >= s.stall.warnAfter && ap.stall.stalledSince.CompareAndSwap(0, last) {
		s.publishStall(ap)
	}
	return true
}

func (s *Service) awaitingUser(ap *activeProcess) bool {
	if ap.stall.hasAwaiting() {
		return true
	}
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	for _, p := range s.gate.pending {
		if p.sid == ap.sessionID {
			return true
		}
	}
	return false
}

// publishStall broadcasts the current stall state. Clients only show it
// while the session is busy, so a late event after the turn ends is harmless.
func (s *Service) publishStall(ap *activeProcess) {
	ap.stall.mu.Lock()
	defer ap.stall.mu.Unlock()
	since := ap.stall.stalledSince.Load()
	if s.status != nil {
		s.status.SetSessionStalled(ap.sessionID, since != 0)
	}
	s.broadcast(ap.sessionID, SSEEvent{
		Type:       "session.stall",
		Properties: map[string]interface{}{"sessionID": ap.sessionID, "stalledSince": since},
	})
}

func (s *Service) abortStalled(ap *activeProcess, idle time.Duration) {
	msg := fmt.Sprintf("No output for %s, so Hoxi stopped this run. Ask it to continue, or narrow down the task.", idle.Round(time.Second))
	s.abortTurn(ap, &MessageError{Name: stallErrorName, Data: &MessageErrorData{Message: msg}})
}

// abortTurn stops the turn; err is reported as the turn's error once it ends.
func (s *Service) abortTurn(ap *activeProcess, err *MessageError) {
	ap.stall.mu.Lock()
	if ap.stall.abortErr == nil {
		ap.stall.abortErr = err
	}
	ap.stall.mu.Unlock()
	s.abortActiveProcess(ap.sessionID)
}
