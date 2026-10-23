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
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
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
	gate            chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	allocatorCancel context.CancelFunc
	browser         context.Context
	tabs            map[string]context.Context
	tabCancels      map[string]context.CancelFunc
	browserCancel   context.CancelFunc
	tabID           string
	profile         string
	manual          bool
	width           int
	height          int
	scale           float64
	viewports       map[string]viewport
	stream          *screencast
	closed          bool
	extension       *extensionBridge
	pairToken       string
	pairExpires     time.Time
}

type Service struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	scopes         map[scopeKey]*scope
	token          string
	uiOrigins      map[string]bool
	closed         bool
	closeDone      chan struct{}
	resolveSession func(sessionID, directory string) (string, error)
	bridgeURL      string
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
	ctx, cancel := context.WithCancel(context.Background())
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return &Service{
		ctx:       ctx,
		cancel:    cancel,
		scopes:    make(map[scopeKey]*scope),
		token:     hex.EncodeToString(secret),
		uiOrigins: make(map[string]bool),
		closeDone: make(chan struct{}),
	}
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
	if sc.stream != nil {
		sc.stream.cancel()
		sc.stream = nil
	}
	sc.cancel()
	for _, cancel := range sc.tabCancels {
		cancel()
	}
	if sc.browserCancel != nil {
		sc.browserCancel()
	}
	clear(sc.tabs)
	clear(sc.tabCancels)
	if sc.allocatorCancel != nil {
		sc.allocatorCancel()
	}
	if sc.profile != "" {
		_ = os.RemoveAll(sc.profile)
	}
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
		sc = &scope{gate: make(chan struct{}, 1), ctx: life, cancel: cancel, tabs: make(map[string]context.Context), tabCancels: make(map[string]context.CancelFunc), width: 1280, height: 800, scale: 1, viewports: make(map[string]viewport)}
		s.scopes[key] = sc
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
	if sc.browser != nil {
		return nil
	}
	profile, err := os.MkdirTemp("", "hoxi-browser-*")
	if err != nil {
		return err
	}
	sc.profile = profile
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.UserDataDir(profile), chromedp.WindowSize(sc.width, sc.height))
	const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(macChrome); err == nil {
		opts = append(opts, chromedp.ExecPath(macChrome))
	}
	alloc, cancel := chromedp.NewExecAllocator(sc.ctx, opts...)
	sc.allocatorCancel = cancel
	browser, browserCancel := chromedp.NewContext(alloc)
	sc.browserCancel = browserCancel
	done := make(chan error, 1)
	go func() { done <- chromedp.Run(browser, sc.viewport().action()) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		sc.cancel()
		<-done
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	sc.browser = browser
	sc.tabID = string(chromedp.FromContext(browser).Target.TargetID)
	sc.tabs[sc.tabID] = browser
	sc.viewports[sc.tabID] = sc.viewport()
	return nil
}
