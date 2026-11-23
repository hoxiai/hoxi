package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	defaultStoreEvaluateMaxBytes = 256 << 10 // 256 KiB
	maxStoreEvaluateMaxBytes     = 256 << 10 // 256 KiB
	maxStoreEvaluateExprBytes    = 128 << 10 // 128 KiB
	storeOpenMaxNavWait          = 15 * time.Second
	storeOpenStateReserve        = 3 * time.Second
	storeOpenMinNavWait          = 3 * time.Second
)

// browserOpCtx derives a chromedp operation context from sc.browser that cancels
// whenever the caller's request ctx ends. sc.state / sc.perform / chromedp.Targets
// require a chromedp browser context rather than a plain HTTP request context.
func (sc *scope) browserOpCtx(ctx context.Context) (context.Context, func()) {
	opctx, opcancel := context.WithCancel(sc.browser)
	stop := context.AfterFunc(ctx, opcancel)
	return opctx, func() {
		stop()
		opcancel()
	}
}

func isStoreNavRecoverableErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "err_aborted")
}

func computeStoreNavTimeout(reqCtx context.Context) time.Duration {
	timeout := storeOpenMaxNavWait
	if deadline, ok := reqCtx.Deadline(); ok {
		rem := time.Until(deadline) - storeOpenStateReserve
		if rem < storeOpenMinNavWait {
			rem = storeOpenMinNavWait
		}
		if rem < timeout {
			timeout = rem
		}
	}
	return timeout
}

// navigateStoreTab initiates navigation on the active store tab with a bounded
// wait so heavy SPA pages or mid-load login redirects (net::ERR_ABORTED / stalled
// window.onload) return the committed live tab state instead of failing HTTP 502.
func (sc *scope) navigateStoreTab(reqCtx, opctx context.Context, targetURL string) error {
	sc.clearSnapshotRefs(sc.tabID)
	navCtx, navCancel := context.WithTimeout(opctx, computeStoreNavTimeout(reqCtx))
	err := sc.withTab(navCtx, chromedp.Navigate(targetURL))
	navCancel()
	if err == nil {
		return nil
	}
	if reqCtx.Err() != nil {
		return reqCtx.Err()
	}
	if isStoreNavRecoverableErr(err) {
		if st, sErr := sc.state(opctx); sErr == nil && !isBlankTabURL(st.URL) {
			return nil
		}
	}
	return err
}

// StoreOpenOptions configures host-controlled store browser tab open/reuse.
type StoreOpenOptions struct {
	TabID    string
	NewTab   bool
	Focus    bool
	Navigate bool
}

// StoreEvaluateOptions configures host-controlled deterministic script evaluation.
type StoreEvaluateOptions struct {
	TabID          string
	MaxResultBytes int
}

// StoreEvaluateResult carries the live browser state and raw JSON evaluation payload.
type StoreEvaluateResult struct {
	State   State
	RawJSON string
	Blocked string // "", "not_running", "manual_control"
}

func (s *Service) resolveStoreScopeKey(sessionID, directory string) (scopeKey, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !strings.HasPrefix(sessionID, "workstore_") || !sessionPattern.MatchString(sessionID) {
		return scopeKey{}, errors.New("valid workstore_* sessionID is required")
	}
	dir := strings.TrimSpace(directory)
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
	s.mu.Lock()
	for existingKey, existingScope := range s.scopes {
		if existingKey.session == sessionID && existingScope != nil && !existingScope.closed {
			s.mu.Unlock()
			return existingKey, nil
		}
	}
	s.mu.Unlock()
	return scopeKey{directory: dir, session: sessionID}, nil
}

// StoreState returns the current state of an already-running store browser scope
// without launching Chrome.
func (s *Service) StoreState(ctx context.Context, sessionID, directory string) (State, bool, error) {
	key, err := s.resolveStoreScopeKey(sessionID, directory)
	if err != nil {
		return emptyState(), false, err
	}
	sc, err := s.acquire(ctx, key, false)
	if err != nil {
		return emptyState(), false, err
	}
	if sc == nil {
		return emptyState(), false, nil
	}
	defer func() { <-sc.gate }()
	if sc.browser == nil || sc.browser.Err() != nil {
		return emptyState(), false, nil
	}
	opctx, stopOp := sc.browserOpCtx(ctx)
	defer stopOp()
	st, err := sc.state(opctx)
	if err != nil {
		return emptyState(), false, err
	}
	return st, len(st.Tabs) > 0, nil
}

// StoreOpen launches or reuses the store's persistent browser window, selects or
// opens a tab for targetURL without clobbering unrelated tabs, and optionally
// focuses the window.
func (s *Service) StoreOpen(ctx context.Context, sessionID, directory, targetURL string, opts StoreOpenOptions) (State, error) {
	targetURL = strings.TrimSpace(targetURL)
	wantTabID := strings.TrimSpace(opts.TabID)
	focusOnlyExisting := targetURL == "" && wantTabID != "" && !opts.Navigate && !opts.NewTab
	var parsedTarget *url.URL
	if !focusOnlyExisting {
		u, err := url.Parse(targetURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return emptyState(), errors.New("store browser target URL must be a valid https URL without credentials")
		}
		parsedTarget = u
	}
	key, err := s.resolveStoreScopeKey(sessionID, directory)
	if err != nil {
		return emptyState(), err
	}
	sc, err := s.acquire(ctx, key, !focusOnlyExisting)
	if err != nil {
		return emptyState(), err
	}
	if sc == nil {
		return emptyState(), errors.New("store browser is not running")
	}
	defer func() { <-sc.gate }()
	if focusOnlyExisting {
		if sc.browser == nil || sc.browser.Err() != nil {
			return emptyState(), errors.New("store browser is not running")
		}
	} else {
		sc.lastUsed = s.now()
		if err := sc.start(ctx); err != nil {
			return emptyState(), err
		}
	}
	opctx, stopOp := sc.browserOpCtx(ctx)
	defer stopOp()
	st, err := sc.state(opctx)
	if err != nil {
		return emptyState(), err
	}

	if opts.NewTab {
		if _, err := sc.perform(opctx, Action{Action: "tab_new", URL: ""}); err != nil {
			return emptyState(), err
		}
		if err := sc.navigateStoreTab(ctx, opctx, targetURL); err != nil {
			return emptyState(), err
		}
	} else {
		tabID := ""
		needsNav := opts.Navigate
		if wantTabID != "" && hasLiveTab(st, wantTabID) {
			tabID = wantTabID
			needsNav = opts.Navigate && targetURL != ""
		} else if focusOnlyExisting {
			return emptyState(), errors.New("requested tabId does not exist in store browser")
		} else {
			tabID, needsNav = pickReusableStoreTab(st, parsedTarget, opts.Navigate)
		}
		if tabID != "" {
			if sc.tabID != tabID {
				sc.stopStream()
				sc.setTabID(tabID)
			}
			activate := chromedp.ActionFunc(func(ctx context.Context) error {
				return target.ActivateTarget(target.ID(tabID)).Do(ctx)
			})
			if err := sc.withTab(opctx, activate); err != nil {
				return emptyState(), err
			}
			if needsNav {
				if err := sc.navigateStoreTab(ctx, opctx, targetURL); err != nil {
					return emptyState(), err
				}
			}
		} else {
			if _, err := sc.perform(opctx, Action{Action: "tab_new", URL: ""}); err != nil {
				return emptyState(), err
			}
			if err := sc.navigateStoreTab(ctx, opctx, targetURL); err != nil {
				return emptyState(), err
			}
		}
	}

	if opts.Focus {
		_ = sc.focusWindow(opctx)
	}
	sc.lastUsed = s.now()
	return sc.state(opctx)
}

// StoreEvaluate runs a bounded JavaScript expression (supporting async/Promise
// results) inside an already-running store browser tab. If preCheck is provided,
// it runs atomically under the scope lock against the live tab state before any
// JavaScript executes so host/login guards cannot be raced.
func (s *Service) StoreEvaluate(
	ctx context.Context,
	sessionID, directory, expression string,
	opts StoreEvaluateOptions,
	preCheck func(State) error,
) (StoreEvaluateResult, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return StoreEvaluateResult{State: emptyState()}, errors.New("expression is required")
	}
	if len(expression) > maxStoreEvaluateExprBytes {
		return StoreEvaluateResult{State: emptyState()}, fmt.Errorf("expression exceeds %d bytes", maxStoreEvaluateExprBytes)
	}
	maxBytes := opts.MaxResultBytes
	if maxBytes <= 0 {
		maxBytes = defaultStoreEvaluateMaxBytes
	}
	if maxBytes > maxStoreEvaluateMaxBytes {
		maxBytes = maxStoreEvaluateMaxBytes
	}

	key, err := s.resolveStoreScopeKey(sessionID, directory)
	if err != nil {
		return StoreEvaluateResult{State: emptyState()}, err
	}
	sc, err := s.acquire(ctx, key, false)
	if err != nil {
		return StoreEvaluateResult{State: emptyState()}, err
	}
	if sc == nil {
		return StoreEvaluateResult{State: emptyState(), Blocked: "not_running"}, nil
	}
	defer func() { <-sc.gate }()

	if sc.browser == nil || sc.browser.Err() != nil {
		return StoreEvaluateResult{State: emptyState(), Blocked: "not_running"}, nil
	}
	opctx, stopOp := sc.browserOpCtx(ctx)
	defer stopOp()
	if sc.manual {
		st, _ := sc.state(opctx)
		return StoreEvaluateResult{State: st, Blocked: "manual_control"}, nil
	}

	st, err := sc.state(opctx)
	if err != nil {
		return StoreEvaluateResult{State: emptyState()}, err
	}
	if len(st.Tabs) == 0 {
		return StoreEvaluateResult{State: st, Blocked: "not_running"}, nil
	}

	if wantTab := strings.TrimSpace(opts.TabID); wantTab != "" && wantTab != sc.tabID {
		found := false
		for _, t := range st.Tabs {
			if t.ID == wantTab {
				found = true
				break
			}
		}
		if !found {
			return StoreEvaluateResult{State: st}, errors.New("requested tabId does not exist in store browser")
		}
		sc.stopStream()
		sc.setTabID(wantTab)
		if err := sc.withTab(opctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return target.ActivateTarget(target.ID(wantTab)).Do(ctx)
		})); err != nil {
			return StoreEvaluateResult{State: st}, err
		}
		st, err = sc.state(opctx)
		if err != nil {
			return StoreEvaluateResult{State: emptyState()}, err
		}
	}

	if preCheck != nil {
		if err := preCheck(st); err != nil {
			return StoreEvaluateResult{State: st}, err
		}
	}

	wrapped := fmt.Sprintf(
		`(async()=>{const v=await (0,eval)(%q);const s=JSON.stringify(v);if(s===undefined)return "null";if(s.length>%d)throw new Error("store browser evaluate result exceeds %d bytes");return s})()`,
		expression,
		maxBytes,
		maxBytes,
	)
	var rawJSON string
	evalAction := chromedp.Evaluate(wrapped, &rawJSON, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return p.WithAwaitPromise(true)
	})
	if err := sc.withTab(opctx, evalAction); err != nil {
		return StoreEvaluateResult{State: st}, err
	}
	sc.lastUsed = s.now()
	if after, sErr := sc.state(opctx); sErr == nil {
		st = after
	}
	return StoreEvaluateResult{State: st, RawJSON: rawJSON}, nil
}

func hasLiveTab(st State, tabID string) bool {
	for _, t := range st.Tabs {
		if t.ID == tabID {
			return true
		}
	}
	return false
}

func sameTargetURL(tabRaw string, target *url.URL) bool {
	u, err := url.Parse(strings.TrimSpace(tabRaw))
	if err != nil || target == nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, target.Scheme) || !strings.EqualFold(u.Hostname(), target.Hostname()) {
		return false
	}
	if strings.TrimRight(u.Path, "/") != strings.TrimRight(target.Path, "/") {
		return false
	}
	if target.RawQuery != "" && u.RawQuery != target.RawQuery {
		return false
	}
	return true
}

func pickReusableStoreTab(st State, target *url.URL, forceNavigate bool) (tabID string, needsNav bool) {
	// 1. Exact URL match: reuse this tab (reload only when forceNavigate is requested).
	for i := len(st.Tabs) - 1; i >= 0; i-- {
		t := st.Tabs[i]
		if sameTargetURL(t.URL, target) {
			return t.ID, forceNavigate
		}
	}
	// 2. Same hostname tab: reuse without clobbering unrelated sites.
	for i := len(st.Tabs) - 1; i >= 0; i-- {
		t := st.Tabs[i]
		u, err := url.Parse(strings.TrimSpace(t.URL))
		if err == nil && strings.EqualFold(u.Hostname(), target.Hostname()) {
			return t.ID, forceNavigate
		}
	}
	// 3. Unused blank tab (e.g. freshly launched Chrome window).
	for _, t := range st.Tabs {
		if t.ID == st.TabID && isBlankTabURL(t.URL) {
			return t.ID, true
		}
	}
	for _, t := range st.Tabs {
		if isBlankTabURL(t.URL) {
			return t.ID, true
		}
	}
	return "", true
}
