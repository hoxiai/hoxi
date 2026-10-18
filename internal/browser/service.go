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

	"github.com/chromedp/chromedp"
)

const maxScopes = 8

type scopeKey struct{ directory, session string }

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
	closed          bool
}

type Service struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	scopes    map[scopeKey]*scope
	token     string
	uiOrigins map[string]bool
	closed    bool
	closeDone chan struct{}
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

// SetUIOrigins replaces the exact HTTP(S) UI origin allowlist. Invalid or
// non-local origins are ignored (fail closed). Native Wails origins are built in.
func (s *Service) SetUIOrigins(origins ...string) {
	allowed := make(map[string]bool)
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && validOrigin(u) && trustedAuthority(u.Host) {
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

func (s *Service) acquire(ctx context.Context, key scopeKey, create bool) (*scope, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("service closed")
	}
	sc := s.scopes[key]
	if sc == nil && create {
		if len(s.scopes) >= maxScopes {
			s.mu.Unlock()
			return nil, errors.New("browser scope limit reached")
		}
		life, cancel := context.WithCancel(s.ctx)
		sc = &scope{gate: make(chan struct{}, 1), ctx: life, cancel: cancel, tabs: make(map[string]context.Context), tabCancels: make(map[string]context.CancelFunc)}
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
	opts = append(opts, chromedp.UserDataDir(profile), chromedp.WindowSize(1280, 800))
	const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(macChrome); err == nil {
		opts = append(opts, chromedp.ExecPath(macChrome))
	}
	alloc, cancel := chromedp.NewExecAllocator(sc.ctx, opts...)
	sc.allocatorCancel = cancel
	browser, browserCancel := chromedp.NewContext(alloc)
	sc.browserCancel = browserCancel
	done := make(chan error, 1)
	go func() { done <- chromedp.Run(browser, chromedp.EmulateViewport(1280, 800)) }()
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
	return nil
}
