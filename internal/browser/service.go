// Package browser provides isolated, lazily launched Chrome sessions.
package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/hoxiai/hoxi/internal/reclaim"
)

const maxScopes = 8

type scopeKey struct{ directory, session string }

type viewport struct {
	width, height int
	scale         float64
}

func (sc *scope) viewport() viewport {
	return viewport{sc.width, sc.height, sc.scale}
}

func (v viewport) action() *emulation.SetDeviceMetricsOverrideParams {
	return emulation.SetDeviceMetricsOverride(int64(v.width), int64(v.height), v.scale, false)
}

type scope struct {
	service           *Service
	sessionID         string
	directory         string
	diagnostics       map[string]*diagnosticTracker
	wheelGesture      wheelGesture
	gate              chan struct{}
	ctx               context.Context
	cancel            context.CancelFunc
	allocatorCancel   context.CancelFunc
	browser           context.Context
	tabs              map[string]context.Context
	tabMu             sync.Mutex
	sessionTabs       map[string]string
	tabOwners         map[string]string
	workflowTabID     string
	navigations       map[string]*navigationTracker
	tabCancels        map[string]context.CancelFunc
	browserCancel     context.CancelFunc
	tabID             string
	profile           string
	profileRootUsed   string
	persistentProfile bool
	window            bool
	ledger            *reclaim.Ledger
	ledgerID          string
	manual            bool
	width             int
	height            int
	scale             float64
	fullPage          bool
	viewports         map[string]viewport
	stream            *screencast
	closed            bool
	extension         *extensionBridge
	pairToken         string
	pairExpires       time.Time
	lastUsed          time.Time
	pointerState      pointerState
	pointerHold       time.Duration
	workflows         *workflowTracker
	transferLedgerID  string
	env               StoreEnvConfig
	envFingerprint    string
	proxyBridge       *loopbackProxyBridge
}

type Service struct {
	mu                        sync.Mutex
	ctx                       context.Context
	cancel                    context.CancelFunc
	scopes                    map[scopeKey]*scope
	token                     string
	linkToken                 string
	profileRoot               string
	defaultWindow             bool
	uiOrigins                 map[string]bool
	closed                    bool
	closeDone                 chan struct{}
	resolveSession            func(sessionID, directory string) (string, error)
	resolveProfile            func(sessionID, directory string) string
	resolveEnv                func(sessionID, directory string) (StoreEnvConfig, error)
	allowLoopbackProxyTargets bool
	isSessionBusy             func(sessionID string) bool
	bridgeURL                 string
	ledger                    *reclaim.Ledger
	now                       func() time.Time
	idleTimeout               time.Duration
	idleClosed                map[scopeKey]idleNotice
	reaperDone                chan struct{}
	// How long a held UI mouse button survives without pointer input.
	pointerHold    time.Duration
	saveFileDialog func(name, title string) (string, error)
	openFile       func(path string, reveal bool) error
}

// SetLedger records each Chrome process and profile in ledger so a later
// Hoxi start can reclaim them if this process dies without calling Close.
func (s *Service) SetLedger(ledger *reclaim.Ledger) {
	s.mu.Lock()
	s.ledger = ledger
	s.mu.Unlock()
}

// SetSessionResolver lets the host map an engine handle to its owning Hoxi
// session. It runs only after authentication and strict scope validation.
func (s *Service) SetSessionResolver(resolve func(sessionID, directory string) (string, error)) {
	s.mu.Lock()
	s.resolveSession = resolve
	s.mu.Unlock()
}

// SetSessionBusyChecker lets the host keep a session's browser alive while the
// session itself is still executing a turn.
func (s *Service) SetSessionBusyChecker(fn func(sessionID string) bool) {
	s.mu.Lock()
	s.isSessionBusy = fn
	s.mu.Unlock()
}

// SetProfileResolver lets the host map a browser session scope to an isolated
// persistent profile key (e.g., per bound Work store account).
func (s *Service) SetProfileResolver(resolve func(sessionID, directory string) string) {
	s.mu.Lock()
	s.resolveProfile = resolve
	s.mu.Unlock()
}

// New creates a service without starting Chrome. The initial agent token is
// random and undisclosed; callers must install their own secret to enable agents.
func New() *Service {
	return newService(time.Now, idleCheckInterval)
}

// newService checks for idle browsers every interval using now as the clock.
func newService(now func() time.Time, interval time.Duration) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	linkSecret := make([]byte, 32)
	if _, err := rand.Read(linkSecret); err != nil {
		panic(err)
	}
	s := &Service{
		ctx:         ctx,
		cancel:      cancel,
		scopes:      make(map[scopeKey]*scope),
		token:       hex.EncodeToString(secret),
		linkToken:   hex.EncodeToString(linkSecret),
		uiOrigins:   make(map[string]bool),
		closeDone:   make(chan struct{}),
		now:         now,
		idleTimeout: defaultIdleTimeoutFromEnv(),
		idleClosed:  make(map[scopeKey]idleNotice),
		reaperDone:  make(chan struct{}),
		pointerHold: defaultPointerHold,
	}
	go s.reapIdle(interval)
	return s
}

// SetAgentToken replaces the agent credential. An empty token disables agents.
func (s *Service) SetAgentToken(token string) {
	s.mu.Lock()
	s.token = token
	s.mu.Unlock()
}

// SetUIOrigins replaces the exact UI origin allowlist. Invalid or
// non-local origins are ignored (fail closed). Native unported Wails origins are built in.
func (s *Service) SetUIOrigins(origins ...string) {
	allowed := make(map[string]bool)
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err == nil && (u.Scheme == "http" || u.Scheme == "https" || (u.Scheme == "wails" && wailsHost(u.Hostname()))) && validOrigin(u) && trustedAuthority(u.Host) {
			allowed[origin] = true
		}
	}
	s.mu.Lock()
	s.uiOrigins = allowed
	s.mu.Unlock()
}

// Close cancels in-flight work, waits for Chrome processes, and removes profiles.
// It is safe to call concurrently and repeatedly.
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return
	}
	s.closed = true
	s.cancel()
	all := make([]*scope, 0, len(s.scopes))
	for _, sc := range s.scopes {
		all = append(all, sc)
	}
	s.scopes = make(map[scopeKey]*scope)
	s.mu.Unlock()
	<-s.reaperDone
	for _, sc := range all {
		sc.gate <- struct{}{}
		sc.dispose()
		<-sc.gate
	}
	close(s.closeDone)
}

func (sc *scope) dispose() {
	sc.closed = true
	if sc.extension != nil {
		sc.extension.close()
	}
	sc.resetBrowser()
	sc.cancel()
}

var sessionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func requestScope(r *http.Request) (scopeKey, error) {
	q := r.URL.Query()
	if len(q["sessionID"]) != 1 || len(q["directory"]) != 1 || !sessionPattern.MatchString(q.Get("sessionID")) {
		return scopeKey{}, errors.New("one valid sessionID and directory are required")
	}
	dir := q.Get("directory")
	if !filepath.IsAbs(dir) {
		return scopeKey{}, errors.New("directory must be absolute")
	}
	dir, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return scopeKey{}, errors.New("directory does not exist")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return scopeKey{}, errors.New("directory must be an existing directory")
	}
	return scopeKey{dir, q.Get("sessionID")}, nil
}

func (s *Service) hasUnifiedWindowScope(sessionID, directory string) bool {
	if strings.HasPrefix(strings.TrimSpace(sessionID), "workstore_") {
		return false
	}
	s.mu.Lock()
	resolveProfile := s.resolveProfile
	defaultWindow := s.defaultWindow
	s.mu.Unlock()
	if resolveProfile == nil {
		return false
	}
	storeKey := strings.TrimSpace(resolveProfile(sessionID, directory))
	if storeKey == "" || (storeKey == "default" && !defaultWindow) {
		return false
	}
	return sessionPattern.MatchString("workstore_" + storeKey)
}

func (s *Service) requestScopeWithCaller(r *http.Request) (scopeKey, string, error) {
	key, err := requestScope(r)
	if err != nil {
		return scopeKey{}, "", err
	}
	s.mu.Lock()
	resolve := s.resolveSession
	resolveProfile := s.resolveProfile
	defaultWindow := s.defaultWindow
	s.mu.Unlock()
	if resolve != nil {
		key.session, err = resolve(key.session, key.directory)
		if err != nil {
			return scopeKey{}, "", err
		}
		if !sessionPattern.MatchString(key.session) {
			return scopeKey{}, "", errors.New("invalid resolved browser session")
		}
	}
	callerSession := key.session
	s.mu.Lock()
	if isolated := s.scopes[key]; isolated != nil && !isolated.closed && !isolated.window {
		s.mu.Unlock()
		return key, callerSession, nil
	}
	s.mu.Unlock()
	if resolveProfile != nil {
		if storeKey := strings.TrimSpace(resolveProfile(key.session, key.directory)); storeKey != "" {
			if storeKey == "default" && !defaultWindow {
				return key, callerSession, nil
			}
			unified := "workstore_" + storeKey
			if sessionPattern.MatchString(unified) {
				key.session = unified
				s.mu.Lock()
				for existingKey, existingScope := range s.scopes {
					if existingKey.session == unified && existingScope != nil && !existingScope.closed {
						key.directory = existingKey.directory
						break
					}
				}
				s.mu.Unlock()
			}
		}
	}
	return key, callerSession, nil
}

func (s *Service) acquire(ctx context.Context, key scopeKey, create bool) (*scope, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("service closed")
	}
	sc := s.scopes[key]
	if sc == nil && create {
		if len(s.scopes) >= maxScopes && !s.evictIdleScopeLocked() {
			s.mu.Unlock()
			return nil, errors.New("browser scope limit reached")
		}
		life, cancel := context.WithCancel(s.ctx)
		sc = &scope{service: s, sessionID: key.session, directory: key.directory, window: s.defaultWindow, gate: make(chan struct{}, 1), ctx: life, cancel: cancel, tabs: make(map[string]context.Context), sessionTabs: make(map[string]string), tabOwners: make(map[string]string), tabCancels: make(map[string]context.CancelFunc), width: 1280, height: 800, scale: 1, viewports: make(map[string]viewport), ledger: s.ledger, lastUsed: s.now(), pointerHold: s.pointerHold}
		sc.workflows = &workflowTracker{uploads: make(map[string]UploadRequest), dialogs: make(map[string]PageDialog), tabs: make(map[string]context.Context)}
		s.scopes[key] = sc
		delete(s.idleClosed, key)
	}
	s.mu.Unlock()
	if sc == nil {
		return nil, nil
	}
	select {
	case sc.gate <- struct{}{}:
		if sc.closed {
			<-sc.gate
			return nil, errors.New("scope closed; retry request")
		}
		return sc, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// evictIdleScopeLocked frees one slot held only by a saved viewport, or an
// already-expired browser whose idleTimeout elapsed before the periodic reaper
// ran. Active Chrome scopes within idleTimeout, manual control, or in-flight
// work are never evicted.
func (s *Service) evictIdleScopeLocked() bool {
	now := s.now()
	isBusy := s.isSessionBusy
	for key, sc := range s.scopes {
		select {
		case sc.gate <- struct{}{}:
		default:
			continue
		}
		if sc.browser != nil || sc.extension != nil || (sc.pairToken != "" && now.Before(sc.pairExpires)) || sc.manual || sc.closed {
			<-sc.gate
			continue
		}
		delete(s.scopes, key)
		sc.dispose()
		<-sc.gate
		return true
	}
	if s.idleTimeout <= 0 {
		return false
	}
	minutes := int(s.idleTimeout / time.Minute)
	for key, sc := range s.scopes {
		select {
		case sc.gate <- struct{}{}:
		default:
			continue
		}
		if sc.closed || sc.manual || sc.extension != nil || (sc.pairToken != "" && now.Before(sc.pairExpires)) ||
			sc.stream != nil || sc.hasBusySession(key.session, isBusy) || now.Sub(sc.lastUsed) < s.idleTimeout {
			<-sc.gate
			continue
		}
		if sc.browser != nil {
			s.noteIdleClosedLocked(key, now, minutes)
		}
		delete(s.scopes, key)
		sc.dispose()
		<-sc.gate
		return true
	}
	return false
}

func (s *Service) remove(key scopeKey, sc *scope) {
	// Keep the slot reserved until process/profile cleanup finishes.
	sc.dispose()
	s.mu.Lock()
	if s.scopes[key] == sc {
		delete(s.scopes, key)
	}
	s.mu.Unlock()
}

func (sc *scope) start(ctx context.Context) error {
	if sc.browser != nil && sc.browser.Err() != nil {
		sc.resetBrowser()
	}
	var desiredEnv StoreEnvConfig
	if sc.service != nil {
		var envErr error
		desiredEnv, envErr = sc.service.resolveScopeEnv(sc)
		if envErr != nil {
			if sc.browser != nil && !sc.manual {
				sc.resetBrowser()
			}
			return envErr
		}
	}
	upstreamURL, envErr := desiredEnv.validate()
	if envErr != nil {
		if sc.browser != nil && !sc.manual {
			sc.resetBrowser()
		}
		return envErr
	}
	fp := desiredEnv.fingerprint()
	if sc.browser != nil && sc.envFingerprint != fp {
		if sc.manual {
			return errors.New("store isolation settings changed while manual control is active; close browser first")
		}
		sc.resetBrowser()
	}
	if sc.browser != nil && sc.window && sc.service != nil {
		if desired := sc.service.desiredProfileRoot(sc); desired != "" && sc.profileRootUsed != "" && desired != sc.profileRootUsed {
			sc.resetBrowser()
		}
	}
	if sc.browser != nil {
		return nil
	}
	if upstreamURL != nil {
		allowLoopback := sc.service != nil && sc.service.allowLoopbackProxyTargets
		bridge, err := startLoopbackProxyBridge(ctx, upstreamURL, allowLoopback)
		if err != nil {
			return err
		}
		sc.proxyBridge = bridge
	}
	sc.env = desiredEnv
	sc.envFingerprint = fp
	var (
		profile  string
		ledgerID string
		err      error
	)
	if sc.window && sc.service != nil {
		profile, err = sc.service.acquirePersistentProfile(sc)
		if err != nil {
			sc.resetBrowser()
			return err
		}
		if profile != "" {
			sc.persistentProfile = true
		}
	}
	if profile == "" {
		profile, ledgerID, err = sc.ledger.MkdirTemp("browser-profile", "hoxi-browser-profile-")
		if err != nil {
			sc.resetBrowser()
			return err
		}
		sc.persistentProfile = false
	}
	sc.profile, sc.ledgerID = profile, ledgerID
	opts := sc.allocatorOptions(profile)
	alloc, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	sc.allocatorCancel = cancel
	browser, browserCancel := chromedp.NewContext(alloc)
	sc.browserCancel = browserCancel
	done := make(chan error, 1)
	go func() {
		initActions := sc.emulationActions()
		if !sc.window {
			initActions = append([]chromedp.Action{sc.viewport().action(), page.SetInterceptFileChooserDialog(true)}, initActions...)
		}
		done <- chromedp.Run(browser, initActions...)
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		sc.resetBrowser()
		<-done
		return ctx.Err()
	case <-sc.ctx.Done():
		sc.resetBrowser()
		<-done
		return sc.ctx.Err()
	}
	if err != nil {
		sc.resetBrowser()
		return err
	}
	if proc := chromedp.FromContext(browser).Browser.Process(); proc != nil {
		if sc.persistentProfile {
			sc.ledgerID = sc.ledger.RecordProcess("browser-window", proc.Pid, "--user-data-dir="+profile)
		} else {
			sc.ledger.AttachProcess(sc.ledgerID, proc.Pid, "--user-data-dir="+profile)
		}
	}
	sc.browser = browser
	if !sc.window {
		if err := sc.setupWorkflows(browser); err != nil {
			sc.resetBrowser()
			return err
		}
	}
	sc.setTabID(string(chromedp.FromContext(browser).Target.TargetID))
	sc.tabs[sc.tabID] = browser
	sc.trackNavigation(browser, sc.tabID)
	sc.viewports[sc.tabID] = sc.viewport()
	return nil
}

func (sc *scope) setTabID(id string) {
	sc.tabID = id
	sc.tabMu.Lock()
	sc.workflowTabID = id
	sc.tabMu.Unlock()
}

func (sc *scope) workflowTab(callerSession string) string {
	sc.tabMu.Lock()
	defer sc.tabMu.Unlock()
	if callerSession != "" && sc.sessionTabs != nil && sc.sessionTabs[callerSession] != "" {
		return sc.sessionTabs[callerSession]
	}
	return sc.workflowTabID
}

func (sc *scope) forgetCallerSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	sc.tabMu.Lock()
	delete(sc.sessionTabs, sessionID)
	for tabID, owner := range sc.tabOwners {
		if owner == sessionID {
			delete(sc.tabOwners, tabID)
		}
	}
	sc.tabMu.Unlock()
}

func (sc *scope) hasBusySession(keySession string, isBusy func(string) bool) bool {
	if isBusy == nil {
		return false
	}
	if keySession != "" && isBusy(keySession) {
		return true
	}
	if sc.sessionID != "" && sc.sessionID != keySession && isBusy(sc.sessionID) {
		return true
	}
	sc.tabMu.Lock()
	sessions := make([]string, 0, len(sc.sessionTabs)+len(sc.tabOwners))
	for sid := range sc.sessionTabs {
		if sid != "" && sid != keySession && sid != sc.sessionID {
			sessions = append(sessions, sid)
		}
	}
	for _, owner := range sc.tabOwners {
		if owner != "" && owner != keySession && owner != sc.sessionID {
			sessions = append(sessions, owner)
		}
	}
	sc.tabMu.Unlock()
	seen := make(map[string]struct{}, len(sessions))
	for _, sid := range sessions {
		if _, dup := seen[sid]; dup {
			continue
		}
		seen[sid] = struct{}{}
		if isBusy(sid) {
			return true
		}
	}
	return false
}

func resolveBrowserExecPath() string {
	switch runtime.GOOS {
	case "darwin":
		const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
		if st, err := os.Stat(macChrome); err == nil && !st.IsDir() {
			return macChrome
		}
	case "windows":
		var roots []string
		for _, k := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				roots = append(roots, v)
			}
		}
		for _, r := range roots {
			p := filepath.Join(r, "Google", "Chrome", "Application", "chrome.exe")
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
		if p, err := exec.LookPath("chrome"); err == nil {
			return p
		}
		for _, r := range roots {
			p := filepath.Join(r, "Microsoft", "Edge", "Application", "msedge.exe")
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
		if p, err := exec.LookPath("msedge"); err == nil {
			return p
		}
	}
	return ""
}
