package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Each isolated Chrome costs close to 1 GB and sessions rarely close their
// browser explicitly, so one that nobody has used for the configured idle
// timeout (30 minutes by default) is stopped automatically.
const (
	DefaultIdleMinutes = 30
	MaxIdleMinutes     = 1440
	DefaultIdleTimeout = DefaultIdleMinutes * time.Minute
	idleCheckInterval  = time.Minute
	// Only the inspector's explanation is lost when a notice is dropped.
	maxIdleNotices = 64
)

type idleNotice struct {
	at      time.Time
	minutes int
}

// Settings holds persisted lifecycle preferences for the built-in browser.
type Settings struct {
	IdleMinutes int `json:"idleMinutes"`
}

// SettingsPath returns ~/.hoxi/browser-settings.json.
func SettingsPath() string {
	return hoxipaths.BrowserSettingsPath()
}

// ValidateIdleMinutes validates a user-supplied Chrome idle timeout in minutes.
// Zero disables automatic idle closing; positive values are capped at 24 hours.
func ValidateIdleMinutes(minutes int) error {
	if minutes < 0 || minutes > MaxIdleMinutes {
		return fmt.Errorf("idleMinutes must be between 0 and %d", MaxIdleMinutes)
	}
	return nil
}

func defaultIdleMinutesFromEnv() int {
	if raw := strings.TrimSpace(os.Getenv("HOXI_BROWSER_IDLE_MINUTES")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && ValidateIdleMinutes(n) == nil {
			return n
		}
	}
	return DefaultIdleMinutes
}

func defaultIdleTimeoutFromEnv() time.Duration {
	return time.Duration(defaultIdleMinutesFromEnv()) * time.Minute
}

// LoadSettings reads persisted browser settings from path, falling back to
// HOXI_BROWSER_IDLE_MINUTES or DefaultIdleMinutes (30) when absent or invalid.
func LoadSettings(path string) Settings {
	fallback := Settings{IdleMinutes: defaultIdleMinutesFromEnv()}
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	var parsed struct {
		IdleMinutes *int `json:"idleMinutes"`
	}
	if json.Unmarshal(raw, &parsed) != nil || parsed.IdleMinutes == nil {
		return fallback
	}
	if ValidateIdleMinutes(*parsed.IdleMinutes) != nil {
		return fallback
	}
	return Settings{IdleMinutes: *parsed.IdleMinutes}
}

// SaveSettings atomically persists browser settings to path.
func SaveSettings(path string, minutes int) error {
	if err := ValidateIdleMinutes(minutes); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return hoxipaths.WriteJSONAtomic(path, Settings{IdleMinutes: minutes}, 0o600)
}

// IdleTimeout returns the active Chrome inactivity timeout (0 when disabled).
func (s *Service) IdleTimeout() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idleTimeout
}

// IdleMinutes returns the active Chrome inactivity timeout in whole minutes (0 when disabled).
func (s *Service) IdleMinutes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idleTimeout <= 0 {
		return 0
	}
	return int(s.idleTimeout / time.Minute)
}

// SetIdleMinutes updates the Chrome inactivity timeout at runtime.
// Passing 0 disables automatic idle shutdown.
func (s *Service) SetIdleMinutes(minutes int) error {
	if err := ValidateIdleMinutes(minutes); err != nil {
		return err
	}
	s.mu.Lock()
	s.idleTimeout = time.Duration(minutes) * time.Minute
	s.mu.Unlock()
	return nil
}

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

// closeIdleScopes stops every Chrome that has gone unused for s.idleTimeout and
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
	timeout := s.idleTimeout
	if s.closed || timeout <= 0 {
		s.mu.Unlock()
		return 0
	}
	now := s.now()
	isBusy := s.isSessionBusy
	minutes := int(timeout / time.Minute)
	for key, sc := range s.scopes {
		select {
		case sc.gate <- struct{}{}:
		default:
			continue
		}
		if sc.closed || sc.browser == nil || sc.manual || sc.extension != nil || sc.pairToken != "" ||
			sc.stream != nil || sc.hasBusySession(key.session, isBusy) || now.Sub(sc.lastUsed) < timeout {
			<-sc.gate
			continue
		}
		// Record the notice while the old scope still holds the key, so no
		// newer scope can be created (and clear it) before it is written.
		s.noteIdleClosedLocked(key, now, minutes)
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

func (s *Service) noteIdleClosedLocked(key scopeKey, at time.Time, minutes int) {
	s.idleClosed[key] = idleNotice{at: at, minutes: minutes}
	if len(s.idleClosed) <= maxIdleNotices {
		return
	}
	var oldest scopeKey
	found := false
	for k, n := range s.idleClosed {
		if k != key && (!found || n.at.Before(s.idleClosed[oldest].at)) {
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
	notice, closed := s.idleClosed[key]
	window := s.defaultWindow
	s.mu.Unlock()
	if window {
		state.Mode = "window"
	}
	if closed {
		state.IdleClosed, state.IdleMinutes = true, notice.minutes
	}
	s.applyStoreMetadata(key.session, key.directory, &state)
	return state
}
