package browser

import "time"

// Each isolated Chrome costs close to 1 GB and sessions rarely close their
// browser explicitly, so one that nobody has used for idleTimeout is stopped.
const (
	idleTimeout       = 30 * time.Minute
	idleCheckInterval = time.Minute
	// Only the inspector's explanation is lost when a notice is dropped.
	maxIdleNotices = 64
)

// reapIdle closes idle browsers until the service closes.
func (s *Service) reapIdle(interval time.Duration) {
	defer close(s.reaperDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.closeIdleScopes()
		}
	}
}

// closeIdleScopes stops every Chrome that has gone unused for idleTimeout and
// returns how many it stopped. Use is any UI or agent state, frame or action
// request, and a stream connection while it is open; the inspector polls state
// only while its panel shows the session in a visible window. Manual control,
// Chrome Link (pairing or connected), an open stream, or an operation holding
// the gate always keep the browser.
func (s *Service) closeIdleScopes() int {
	type victim struct {
		key scopeKey
		sc  *scope
	}
	var victims []victim
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0
	}
	now := s.now()
	for key, sc := range s.scopes {
		select {
		case sc.gate <- struct{}{}:
		default:
			continue
		}
		if sc.closed || sc.browser == nil || sc.manual || sc.extension != nil || sc.pairToken != "" ||
			sc.stream != nil || now.Sub(sc.lastUsed) < idleTimeout {
			<-sc.gate
			continue
		}
		// Record the notice while the old scope still holds the key, so no
		// newer scope can be created (and clear it) before it is written.
		s.noteIdleClosedLocked(key, now)
		victims = append(victims, victim{key, sc})
	}
	s.mu.Unlock()
	// Chrome shutdown and profile removal never run under the service lock.
	for _, v := range victims {
		s.remove(v.key, v.sc)
		<-v.sc.gate
	}
	return len(victims)
}

func (s *Service) noteIdleClosedLocked(key scopeKey, at time.Time) {
	s.idleClosed[key] = at
	if len(s.idleClosed) <= maxIdleNotices {
		return
	}
	var oldest scopeKey
	found := false
	for k, t := range s.idleClosed {
		if k != key && (!found || t.Before(s.idleClosed[oldest])) {
			oldest, found = k, true
		}
	}
	delete(s.idleClosed, oldest)
}

// idleState is the state of a scope that does not exist. It says whether the
// browser was stopped for inactivity until the scope is created again.
func (s *Service) idleState(key scopeKey) State {
	state := emptyState()
	s.mu.Lock()
	_, closed := s.idleClosed[key]
	s.mu.Unlock()
	if closed {
		state.IdleClosed, state.IdleMinutes = true, int(idleTimeout/time.Minute)
	}
	return state
}
