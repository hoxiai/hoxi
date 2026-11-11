package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gorilla/websocket"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// canonicalKey is the key requestScope derives for dir (TMPDIR is a symlink on macOS).
func canonicalKey(t *testing.T, dir, session string) scopeKey {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return scopeKey{real, session}
}

func liveScope(s *Service, key scopeKey) *scope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scopes[key]
}

// withGate runs edit on the scope while holding its gate, like a request does.
func withGate(sc *scope, edit func(sc *scope)) {
	sc.gate <- struct{}{}
	edit(sc)
	<-sc.gate
}

// fakeRunning reserves a scope that looks like it runs Chrome; disposing it
// starts and stops nothing.
func fakeRunning(t *testing.T, s *Service, key scopeKey) *scope {
	t.Helper()
	sc, err := s.acquire(context.Background(), key, true)
	if err != nil {
		t.Fatal(err)
	}
	sc.browser = context.Background()
	<-sc.gate
	return sc
}

func decodeState(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var state map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestIdleBrowserIsClosedAndExplained(t *testing.T) {
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")
	clock := newFakeClock()
	s := newService(clock.Now, time.Hour)
	defer s.Close()
	if got := s.IdleMinutes(); got != 30 {
		t.Fatalf("default IdleMinutes() = %d, want 30", got)
	}
	s.SetAgentToken("secret")
	dir := t.TempDir()
	key := canonicalKey(t, dir, "ses_idle")
	decodeState(t, request(t, s, dir, "ses_idle", "action", `{"action":"control","manual":false}`, false))
	sc := liveScope(s, key)
	if sc == nil {
		t.Fatal("control did not reserve the scope")
	}
	// The inspector's poll and an agent's request both count as use.
	for _, agent := range []bool{false, true} {
		clock.Advance(DefaultIdleTimeout - time.Minute)
		decodeState(t, request(t, s, dir, "ses_idle", "state", "", agent))
		withGate(sc, func(sc *scope) {
			if !sc.lastUsed.Equal(clock.Now()) {
				t.Fatalf("agent=%v request did not count as use: %v", agent, sc.lastUsed)
			}
		})
	}
	withGate(sc, func(sc *scope) { sc.browser = context.Background() })
	clock.Advance(DefaultIdleTimeout - time.Second)
	if n := s.closeIdleScopes(); n != 0 || liveScope(s, key) != sc {
		t.Fatalf("closed a browser used %v ago", DefaultIdleTimeout-time.Second)
	}
	clock.Advance(time.Second)
	if n := s.closeIdleScopes(); n != 1 || liveScope(s, key) != nil || !sc.closed {
		t.Fatalf("idle browser kept: closed %d", n)
	}
	for _, agent := range []bool{false, true} {
		state := decodeState(t, request(t, s, dir, "ses_idle", "state", "", agent))
		if state["idleClosed"] != true || state["idleMinutes"] != float64(30) || len(state["tabs"].([]any)) != 0 {
			t.Fatalf("agent=%v: idle closure not reported: %v", agent, state)
		}
	}
	if w := request(t, s, dir, "ses_idle", "frame", "", false); w.Code != 404 {
		t.Fatal("frame of a closed browser", w.Code)
	}
	if state := decodeState(t, request(t, s, dir, "ses_other", "state", "", false)); state["idleClosed"] != nil {
		t.Fatal("notice leaked to another session", state)
	}
	// Using the scope again retires the notice, even after a later close.
	decodeState(t, request(t, s, dir, "ses_idle", "action", `{"action":"control","manual":false}`, false))
	decodeState(t, request(t, s, dir, "ses_idle", "action", `{"action":"close"}`, false))
	if state := decodeState(t, request(t, s, dir, "ses_idle", "state", "", false)); state["idleClosed"] != nil {
		t.Fatal("stale idle notice", state)
	}
}

func TestConfigurableIdleTimeoutAndPersistence(t *testing.T) {
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")
	clock := newFakeClock()
	s := newService(clock.Now, time.Hour)
	defer s.Close()

	// Reject out-of-range values without mutating current timeout.
	for _, bad := range []int{-1, -30, MaxIdleMinutes + 1} {
		if err := s.SetIdleMinutes(bad); err == nil {
			t.Fatalf("expected error for invalid idleMinutes=%d", bad)
		}
	}
	if got := s.IdleMinutes(); got != 30 {
		t.Fatalf("IdleMinutes() mutated after invalid input: %d", got)
	}

	// Custom 10-minute idle timeout closes at 10m and records 10m on the notice.
	if err := s.SetIdleMinutes(10); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	key := canonicalKey(t, dir, "ses_10m")
	sc := fakeRunning(t, s, key)
	clock.Advance(9 * time.Minute)
	if n := s.closeIdleScopes(); n != 0 || sc.closed {
		t.Fatalf("closed early at 9m (configured 10m): %d", n)
	}
	clock.Advance(time.Minute)
	if n := s.closeIdleScopes(); n != 1 || !sc.closed {
		t.Fatalf("did not close at 10m: %d", n)
	}

	// Changing timeout afterward must not rewrite already-recorded closure minutes.
	if err := s.SetIdleMinutes(0); err != nil {
		t.Fatal(err)
	}
	state := decodeState(t, request(t, s, dir, "ses_10m", "state", "", false))
	if state["idleClosed"] != true || state["idleMinutes"] != float64(10) {
		t.Fatalf("expected recorded idleMinutes=10, got %v", state)
	}

	// 0 disables automatic idle shutdown completely.
	neverKey := canonicalKey(t, dir, "ses_never")
	neverScope := fakeRunning(t, s, neverKey)
	clock.Advance(24 * time.Hour)
	if n := s.closeIdleScopes(); n != 0 || neverScope.closed {
		t.Fatalf("idle auto-close ran while disabled (idleMinutes=0): %d", n)
	}

	// Persistence round-trip & corrupt/missing fallback.
	cfgPath := filepath.Join(t.TempDir(), "browser-settings.json")
	if got := LoadSettings(cfgPath); got.IdleMinutes != 30 {
		t.Fatalf("missing file LoadSettings = %+v, want 30", got)
	}
	if err := SaveSettings(cfgPath, 15); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(cfgPath); got.IdleMinutes != 15 {
		t.Fatalf("saved LoadSettings = %+v, want 15", got)
	}
	if err := SaveSettings(cfgPath, 0); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(cfgPath); got.IdleMinutes != 0 {
		t.Fatalf("saved 0 LoadSettings = %+v, want 0", got)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"idleMinutes":99999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(cfgPath); got.IdleMinutes != 30 {
		t.Fatalf("out-of-range file LoadSettings = %+v, want fallback 30", got)
	}
}

func TestIdleCloseKeepsManualStreamChromeLinkAndBusy(t *testing.T) {
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")
	clock := newFakeClock()
	s := newService(clock.Now, time.Hour)
	defer s.Close()
	for _, tc := range []struct {
		name          string
		keep, release func(sc *scope)
	}{
		{"manual control", func(sc *scope) { sc.manual = true }, func(sc *scope) { sc.manual = false }},
		{"open stream", func(sc *scope) {
			ctx, cancel := context.WithCancel(context.Background())
			sc.stream = &screencast{ctx: ctx, cancel: cancel}
		}, func(sc *scope) { sc.stopStream() }},
		{"Chrome Link connected", func(sc *scope) {
			sc.extension = &extensionBridge{done: make(chan struct{})}
		}, func(sc *scope) { sc.extension.close(); sc.extension = nil }},
		{"Chrome Link pairing", func(sc *scope) {
			sc.pairToken, sc.pairExpires = strings.Repeat("a", 64), clock.Now().Add(time.Hour)
		}, func(sc *scope) { sc.pairToken = "" }},
	} {
		key := scopeKey{"/tmp", strings.ReplaceAll(tc.name, " ", "_")}
		sc := fakeRunning(t, s, key)
		withGate(sc, tc.keep)
		clock.Advance(2 * DefaultIdleTimeout)
		if n := s.closeIdleScopes(); n != 0 || liveScope(s, key) != sc || sc.closed {
			t.Fatalf("%s: idle close stopped the browser", tc.name)
		}
		// The guard alone kept it: without it the same scope is closed.
		withGate(sc, tc.release)
		if n := s.closeIdleScopes(); n != 1 || liveScope(s, key) != nil {
			t.Fatalf("%s: released scope kept (%d)", tc.name, n)
		}
	}

	key := scopeKey{"/tmp", "busy"}
	sc := fakeRunning(t, s, key)
	clock.Advance(2 * DefaultIdleTimeout)
	if _, err := s.acquire(context.Background(), key, false); err != nil {
		t.Fatal(err)
	}
	if n := s.closeIdleScopes(); n != 0 || sc.closed {
		t.Fatal("closed a browser with an operation in flight")
	}
	<-sc.gate
	if n := s.closeIdleScopes(); n != 1 {
		t.Fatal("finished operation still kept the browser")
	}

	busySession := true
	s.SetSessionBusyChecker(func(sid string) bool { return sid == "session_busy" && busySession })
	busyKey := scopeKey{"/tmp", "session_busy"}
	busyScope := fakeRunning(t, s, busyKey)
	clock.Advance(2 * DefaultIdleTimeout)
	if n := s.closeIdleScopes(); n != 0 || busyScope.closed {
		t.Fatal("closed a browser while its owning session was still busy")
	}
	busySession = false
	if n := s.closeIdleScopes(); n != 1 || !busyScope.closed {
		t.Fatal("idle browser kept after its session became idle")
	}

	// Window-mode scopes (default on desktop) must also close on idle while keeping persistent profile files.
	profileRoot := filepath.Join(t.TempDir(), "win-profile")
	s.SetProfileRoot(profileRoot)
	s.SetDefaultWindowMode(true)
	winKey := scopeKey{"/tmp", "window_idle"}
	winScope := fakeRunning(t, s, winKey)
	withGate(winScope, func(sc *scope) {
		sc.window = true
		sc.persistentProfile = true
		sc.profile = profileRoot
	})
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cookieMarker := filepath.Join(profileRoot, "Cookies")
	if err := os.WriteFile(cookieMarker, []byte("session-cookie"), 0o600); err != nil {
		t.Fatal(err)
	}
	clock.Advance(DefaultIdleTimeout)
	if n := s.closeIdleScopes(); n != 1 || !winScope.closed {
		t.Fatalf("expected idle window-mode browser to close, got %d", n)
	}
	if _, err := os.Stat(cookieMarker); err != nil {
		t.Fatalf("persistent profile deleted on idle window close: %v", err)
	}
}

func TestIdleCloseKeepsRealChromeLinkPairing(t *testing.T) {
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")
	clock := newFakeClock()
	s := newService(clock.Now, time.Hour)
	defer s.Close()
	s.SetBridgeURL("ws://127.0.0.1:5420")
	dir := t.TempDir()
	if w := request(t, s, dir, "ses_link", "action", `{"action":"pair"}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	clock.Advance(2 * DefaultIdleTimeout)
	s.closeIdleScopes()
	if state := decodeState(t, request(t, s, dir, "ses_link", "state", "", false)); state["pairing"] != true || state["idleClosed"] != nil {
		t.Fatal("pairing scope lost to idle close", state)
	}
}

func TestIdleNoticesAreBounded(t *testing.T) {
	s := newService(time.Now, time.Hour)
	defer s.Close()
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i <= maxIdleNotices; i++ {
		s.noteIdleClosedLocked(scopeKey{"/tmp", string(rune('A' + i))}, start.Add(time.Duration(i)*time.Second), DefaultIdleMinutes)
	}
	if len(s.idleClosed) != maxIdleNotices {
		t.Fatal(len(s.idleClosed))
	}
	if _, ok := s.idleClosed[scopeKey{"/tmp", "A"}]; ok {
		t.Fatal("oldest notice kept")
	}
	// The notice just written is never the one dropped, even with the oldest time.
	newest := scopeKey{"/tmp", "newest"}
	s.noteIdleClosedLocked(newest, start.Add(-time.Hour), DefaultIdleMinutes)
	if _, ok := s.idleClosed[newest]; !ok || len(s.idleClosed) != maxIdleNotices {
		t.Fatal("newest notice dropped")
	}
}

func TestIdleReaperRunsInBackgroundAndStopsOnClose(t *testing.T) {
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")
	clock := newFakeClock()
	s := newService(clock.Now, 5*time.Millisecond)
	key := scopeKey{"/tmp", "background"}
	sc := fakeRunning(t, s, key)
	clock.Advance(DefaultIdleTimeout)
	for deadline := time.Now().Add(5 * time.Second); liveScope(s, key) != nil; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("background check never closed the idle browser")
		}
	}
	if !sc.closed {
		t.Fatal("scope removed without disposal")
	}
	s.Close()
	select {
	case <-s.reaperDone:
	default:
		t.Fatal("idle check still running after Close")
	}
}

func TestChromeIdleCloseStopsProcessAndRemovesProfile(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")
	clock := newFakeClock()
	s := newService(clock.Now, time.Hour)
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	key := canonicalKey(t, dir, "ses_chrome_idle")
	decodeState(t, request(t, s, dir, "ses_chrome_idle", "action", `{"action":"navigate","url":"about:blank"}`, true))
	sc := liveScope(s, key)
	var profile string
	var pid int
	withGate(sc, func(sc *scope) {
		profile, pid = sc.profile, chromedp.FromContext(sc.browser).Browser.Process().Pid
	})

	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	u := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/hoxi/browser/stream?sessionID=ses_chrome_idle&directory=" + url.QueryEscape(dir)
	conn, _, err := websocket.DefaultDialer.Dial(u, http.Header{"Origin": []string{"wails://wails"}})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * DefaultIdleTimeout)
	if n := s.closeIdleScopes(); n != 0 || sc.closed {
		t.Fatal("closed a browser with an open stream")
	}
	_ = conn.Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var streaming bool
		withGate(sc, func(sc *scope) { streaming = sc.stream != nil })
		if !streaming {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stream never ended")
		}
	}
	// Idle time starts when the stream ends, not when it started.
	if n := s.closeIdleScopes(); n != 0 {
		t.Fatal("stream end did not count as use")
	}
	clock.Advance(DefaultIdleTimeout)
	if n := s.closeIdleScopes(); n != 1 {
		t.Fatal("idle Chrome kept")
	}
	if testProcessAlive(pid) {
		t.Fatal("Chrome still running after idle close")
	}
	waitNoProfileProcesses(t, profile)
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("profile survived idle close: %v", err)
	}
	if state := decodeState(t, request(t, s, dir, "ses_chrome_idle", "state", "", false)); state["idleClosed"] != true {
		t.Fatal("idle closure not reported", state)
	}
	restarted := decodeState(t, request(t, s, dir, "ses_chrome_idle", "action", `{"action":"navigate","url":"about:blank"}`, true))
	if restarted["idleClosed"] != nil || len(restarted["tabs"].([]any)) == 0 {
		t.Fatal("browser did not restart cleanly", restarted)
	}
}

func TestUnifiedWindowScopeKeptWhileBoundCodingSessionBusy(t *testing.T) {
	t.Setenv("HOXI_BROWSER_IDLE_MINUTES", "")
	clock := newFakeClock()
	s := newService(clock.Now, time.Hour)
	defer s.Close()
	s.SetDefaultWindowMode(true)
	s.SetProfileResolver(func(_, _ string) string { return "default" })

	codingBusy := true
	s.SetSessionBusyChecker(func(sid string) bool {
		return sid == "ses_coding_busy" && codingBusy
	})

	dir := t.TempDir()
	sharedKey := canonicalKey(t, dir, "workstore_default")
	shared := fakeRunning(t, s, sharedKey)
	withGate(shared, func(sc *scope) {
		sc.window = true
		sc.bindCallerTab("ses_coding_busy", "tab_1")
	})

	// Advance past idleTimeout while ses_coding_busy is still executing a turn.
	clock.Advance(2 * DefaultIdleTimeout)
	if n := s.closeIdleScopes(); n != 0 || shared.closed || liveScope(s, sharedKey) != shared {
		t.Fatalf("closed unified window scope while bound coding session was busy: n=%d closed=%v", n, shared.closed)
	}

	// Fill remaining slots with fresh active scopes and verify evictIdleScopeLocked
	// also protects the unified scope while ses_coding_busy is busy.
	for i := 1; i < maxScopes; i++ {
		filler := fakeRunning(t, s, scopeKey{"/tmp", "active_" + string(rune('a'+i))})
		withGate(filler, func(sc *scope) { sc.lastUsed = clock.Now() })
	}
	if _, err := s.acquire(context.Background(), scopeKey{"/tmp", "overflow_while_busy"}, true); err == nil {
		t.Fatal("evictIdleScopeLocked evicted unified scope while bound coding session was busy")
	}

	// Once ses_coding_busy finishes its turn, the expired unified scope can be closed on idle.
	codingBusy = false
	if n := s.closeIdleScopes(); n != 1 || !shared.closed || liveScope(s, sharedKey) != nil {
		t.Fatalf("expected unified scope to close once bound coding session became idle: n=%d closed=%v", n, shared.closed)
	}
}

func TestChromeGracefulShutdownLeavesCleanExit(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := newService(time.Now, time.Hour)
	s.SetAgentToken("secret")
	dir := t.TempDir()
	key := canonicalKey(t, dir, "ses_graceful")
	decodeState(t, request(t, s, dir, "ses_graceful", "action", `{"action":"navigate","url":"about:blank"}`, true))
	sc := liveScope(s, key)
	var pid int
	var usedProfile string
	withGate(sc, func(sc *scope) {
		pid = chromedp.FromContext(sc.browser).Browser.Process().Pid
		usedProfile = sc.profile
		// Preserve directory across Close so we can inspect SingletonLock & Preferences.
		sc.persistentProfile = true
	})
	defer os.RemoveAll(usedProfile)

	s.Close()
	if testProcessAlive(pid) {
		t.Fatal("Chrome process still alive after Service.Close()")
	}
	waitNoProfileProcesses(t, usedProfile)
	if _, err := os.Lstat(filepath.Join(usedProfile, "SingletonLock")); !os.IsNotExist(err) {
		t.Fatalf("SingletonLock remained after graceful Close (Chrome was likely SIGKILLed): %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(usedProfile, "Default", "Preferences")); err == nil {
		if strings.Contains(string(raw), `"exit_type":"Crashed"`) {
			t.Fatalf("Chrome Preferences recorded unclean crash exit: %s", raw)
		}
	}
}
