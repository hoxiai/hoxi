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
	diagnostics       map[string]*diagnosticTracker
	wheelGesture      wheelGesture
	gate              chan struct{}
	ctx               context.Context
	cancel            context.CancelFunc
	allocatorCancel   context.CancelFunc
	browser           context.Context
	tabs              map[string]context.Context
	navigations       map[string]*navigationTracker
	tabCancels        map[string]context.CancelFunc
	browserCancel     context.CancelFunc
	tabID             string
	profile           string
	persistentProfile bool
	window            bool
	ledger            *reclaim.Ledger
	ledgerID          string
	manual            bool
	width             int
	height            int
	scale             float64
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
}

type Service struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	scopes         map[scopeKey]*scope
	token          string
	linkToken      string
	profileRoot    string
	defaultWindow  bool
	uiOrigins      map[string]bool
	closed         bool
	closeDone      chan struct{}
	resolveSession func(sessionID, directory string) (string, error)
	bridgeURL      string
	ledger         *reclaim.Ledger
	now            func() time.Time
	idleClosed     map[scopeKey]time.Time
	reaperDone     chan struct{}
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
		idleClosed:  make(map[scopeKey]time.Time),
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

func (s *Service) requestScope(r *http.Request) (scopeKey, error) {
	key, err := requestScope(r)
	if err != nil {
		return scopeKey{}, err
	}
	s.mu.Lock()
	resolve := s.resolveSession
	s.mu.Unlock()
	if resolve != nil {
		key.session, err = resolve(key.session, key.directory)
		if err != nil {
			return scopeKey{}, err
		}
		if !sessionPattern.MatchString(key.session) {
			return scopeKey{}, errors.New("invalid resolved browser session")
		}
	}
	return key, nil
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
		sc = &scope{service: s, sessionID: key.session, window: s.defaultWindow, gate: make(chan struct{}, 1), ctx: life, cancel: cancel, tabs: make(map[string]context.Context), tabCancels: make(map[string]context.CancelFunc), width: 1280, height: 800, scale: 1, viewports: make(map[string]viewport), ledger: s.ledger, lastUsed: s.now(), pointerHold: s.pointerHold}
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

// evictIdleScopeLocked frees one slot held only by a saved viewport. UI resizes
// create scopes lazily, so they must not starve sessions that start Chrome.
// Scopes with Chrome, manual control, or in-flight work are never evicted.
func (s *Service) evictIdleScopeLocked() bool {
	for key, sc := range s.scopes {
		select {
		case sc.gate <- struct{}{}:
		default:
			continue
		}
		if sc.browser != nil || sc.extension != nil || (sc.pairToken != "" && time.Now().Before(sc.pairExpires)) || sc.manual || sc.closed {
			<-sc.gate
			continue
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
	if sc.browser != nil {
		return nil
	}
	var (
		profile  string
		ledgerID string
		err      error
	)
	if sc.window && sc.service != nil {
		profile, err = sc.service.acquirePersistentProfile(sc)
		if err != nil {
			return err
		}
		if profile != "" {
			sc.persistentProfile = true
		}
	}
	if profile == "" {
		profile, ledgerID, err = sc.ledger.MkdirTemp("browser-profile", "hoxi-browser-profile-")
		if err != nil {
			return err
		}
		sc.persistentProfile = false
	}
	sc.profile, sc.ledgerID = profile, ledgerID
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.UserDataDir(profile), chromedp.WindowSize(sc.width, sc.height),
		// Back/forward-cache restores fire no document load, so chromedp's
		// history actions would wait until the request deadline.
		chromedp.Flag("disable-back-forward-cache", true))
	if sc.window {
		opts = append(opts,
			chromedp.Flag("headless", false),
			chromedp.Flag("hide-scrollbars", false),
			chromedp.Flag("mute-audio", false),
		)
	}
	if execPath := resolveBrowserExecPath(); execPath != "" {
		opts = append(opts, chromedp.ExecPath(execPath))
	}
	alloc, cancel := chromedp.NewExecAllocator(sc.ctx, opts...)
	sc.allocatorCancel = cancel
	browser, browserCancel := chromedp.NewContext(alloc)
	sc.browserCancel = browserCancel
	done := make(chan error, 1)
	go func() {
		if sc.window {
			done <- chromedp.Run(browser)
		} else {
			done <- chromedp.Run(browser, sc.viewport().action(), page.SetInterceptFileChooserDialog(true))
		}
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		sc.resetBrowser()
		<-done
		return ctx.Err()
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
			return err
		}
	}
	sc.tabID = string(chromedp.FromContext(browser).Target.TargetID)
	sc.tabs[sc.tabID] = browser
	sc.trackNavigation(browser, sc.tabID)
	sc.viewports[sc.tabID] = sc.viewport()
	return nil
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
