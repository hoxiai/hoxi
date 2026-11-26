package browser

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	previewScopePrefix       = "preview_"
	previewDefaultWidth      = 1280
	previewDefaultHeight     = 800
	previewMinDimension      = 320
	previewMaxWidth          = 1920
	previewMaxHeight         = 1440
	previewMaxEmulatedPixels = 4_194_304
	previewMaxTypeTextBytes  = 2000
	previewMaxWheelDelta     = 2400
	previewMaxNavWait        = 12 * time.Second
)

// PreviewSnapshot describes the current state of an isolated headless preview scope.
type PreviewSnapshot struct {
	Running            bool   `json:"running"`
	URL                string `json:"url,omitempty"`
	Path               string `json:"path,omitempty"`
	Title              string `json:"title,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	BlockedExternalNav bool   `json:"blockedExternalNav,omitempty"`
}

// PreviewActionRequest defines the restricted UI-only interaction vocabulary for
// an origin-locked headless preview scope.
type PreviewActionRequest struct {
	Action            string   `json:"action"`
	Path              string   `json:"path,omitempty"`
	Width             *int     `json:"width,omitempty"`
	Height            *int     `json:"height,omitempty"`
	DeviceScaleFactor *float64 `json:"deviceScaleFactor,omitempty"`
	X                 float64  `json:"x,omitempty"`
	Y                 float64  `json:"y,omitempty"`
	DeltaX            float64  `json:"deltaX,omitempty"`
	DeltaY            float64  `json:"deltaY,omitempty"`
	Text              string   `json:"text,omitempty"`
	Key               string   `json:"key,omitempty"`
	Phase             string   `json:"phase,omitempty"`
	Button            string   `json:"button,omitempty"`
	ClickCount        int      `json:"clickCount,omitempty"`
	Modifiers         int      `json:"modifiers,omitempty"`
}

func hasASCIIControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func isBlockedPreviewIP(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	ip := addr.Unmap()
	return ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast()
}

func canonicalPreviewOrigin(u *url.URL) (string, error) {
	if u == nil {
		return "", errors.New("preview URL is required")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", errors.New("preview URL must use http:// or https://")
	}
	if u.User != nil || u.Opaque != "" {
		return "", errors.New("preview URL must not contain credentials")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return "", errors.New("preview URL is missing a hostname")
	}
	if ip, err := netip.ParseAddr(host); err == nil && isBlockedPreviewIP(ip) {
		return "", fmt.Errorf("preview host address %s is not allowed", ip.String())
	}
	if isBlockedPreviewWildcardHost(host) {
		return "", fmt.Errorf("preview host address %s is not allowed", host)
	}
	portStr := strings.TrimSpace(u.Port())
	portNum := 80
	if scheme == "https" {
		portNum = 443
	}
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return "", errors.New("preview URL has an invalid port")
		}
		portNum = p
		if (scheme == "http" && p == 80) || (scheme == "https" && p == 443) {
			portStr = ""
		} else {
			portStr = strconv.Itoa(p)
		}
	}
	if isLoopbackPreviewHost(host) && isReservedHoxiLoopbackPort(portNum) {
		return "", fmt.Errorf("preview cannot target Hoxi internal port %d", portNum)
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if portStr != "" {
		authority = net.JoinHostPort(host, portStr)
	}
	return scheme + "://" + authority, nil
}

// NormalizePreviewOrigin validates rawURL and returns its canonical origin
// (scheme://host[:port]) and normalized initial path (always starting with "/").
func NormalizePreviewOrigin(rawURL string) (origin string, initialPath string, err error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || len(trimmed) > 2048 || hasASCIIControl(trimmed) {
		return "", "", errors.New("valid http:// or https:// preview URL is required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", "", errors.New("invalid preview URL")
	}
	origin, err = canonicalPreviewOrigin(u)
	if err != nil {
		return "", "", err
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	} else if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		p += "#" + u.Fragment
	}
	return origin, p, nil
}

// ResolvePreviewTargetURL resolves a relative path or same-origin URL strictly
// within allowedOrigin, rejecting scheme-relative URLs, non-HTTP schemes, and
// any attempt to navigate to a different host, scheme, or port.
func ResolvePreviewTargetURL(allowedOrigin, rawTarget string) (string, error) {
	origin, _, err := NormalizePreviewOrigin(allowedOrigin)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		return origin + "/", nil
	}
	if len(target) > 2048 || hasASCIIControl(target) {
		return "", errors.New("invalid preview navigation target")
	}
	if strings.HasPrefix(target, "//") {
		return "", errors.New("cross-origin preview navigation is not allowed")
	}
	var resolved *url.URL
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "?") || strings.HasPrefix(target, "#") {
		ref, pErr := url.Parse(target)
		if pErr != nil || ref.Scheme != "" || ref.Host != "" || ref.User != nil {
			return "", errors.New("invalid preview path")
		}
		resolved = base.ResolveReference(ref)
	} else {
		// Reject scheme-like targets that are not http/https (e.g. javascript:, file:, data:).
		if strings.Contains(target, "://") || strings.Contains(target, ":") {
			u, pErr := url.Parse(target)
			if pErr != nil {
				return "", errors.New("invalid preview URL")
			}
			resolved = u
		} else {
			ref, pErr := url.Parse("/" + target)
			if pErr != nil || ref.Scheme != "" || ref.Host != "" || ref.User != nil {
				return "", errors.New("invalid preview path")
			}
			resolved = base.ResolveReference(ref)
		}
	}
	gotOrigin, oErr := canonicalPreviewOrigin(resolved)
	if oErr != nil || gotOrigin != origin {
		return "", errors.New("navigation outside the authorized preview origin is not allowed")
	}
	p := resolved.EscapedPath()
	if p == "" {
		p = "/"
	}
	out := origin + p
	if resolved.RawQuery != "" {
		out += "?" + resolved.RawQuery
	}
	if resolved.Fragment != "" {
		out += "#" + resolved.Fragment
	}
	return out, nil
}

func previewPathFromURL(allowedOrigin, rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "/"
	}
	gotOrigin, err := canonicalPreviewOrigin(u)
	if err != nil || gotOrigin != allowedOrigin {
		return "/"
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		p += "#" + u.Fragment
	}
	return p
}

func isAllowedPreviewTabURL(allowedOrigin, rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	gotOrigin, err := canonicalPreviewOrigin(u)
	return err == nil && gotOrigin == allowedOrigin
}

func isPreviewErrorTabURL(rawURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.HasPrefix(lower, "chrome-error://")
}

func (s *Service) resolvePreviewScopeKey(scopeID, directory string) (scopeKey, error) {
	scopeID = strings.TrimSpace(scopeID)
	if !strings.HasPrefix(scopeID, previewScopePrefix) || !sessionPattern.MatchString(scopeID) {
		return scopeKey{}, errors.New("valid preview_* scopeID is required")
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
	return scopeKey{directory: dir, session: scopeID}, nil
}

func (s *Service) acquirePreviewScope(ctx context.Context, key scopeKey, origin string, create bool) (*scope, error) {
	sc, err := s.acquire(ctx, key, create)
	if err != nil || sc == nil {
		return sc, err
	}
	// Preview scopes must NEVER launch a visible desktop Chrome window or touch persistent profiles.
	if sc.window {
		if sc.browser != nil {
			sc.resetBrowser()
		}
		sc.window = false
	}
	if sc.previewOrigin != "" && sc.previewOrigin != origin {
		if sc.browser != nil {
			sc.resetBrowser()
		}
	}
	sc.previewOrigin = origin
	if sc.width < previewMinDimension || sc.height < previewMinDimension {
		sc.width = previewDefaultWidth
		sc.height = previewDefaultHeight
		sc.scale = 1
	}
	return sc, nil
}

func validatePreviewAction(req PreviewActionRequest, allowedOrigin string) (Action, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Action))
	switch kind {
	case "open", "navigate":
		targetURL, err := ResolvePreviewTargetURL(allowedOrigin, req.Path)
		if err != nil {
			return Action{}, err
		}
		return Action{Action: "navigate", URL: targetURL}, nil
	case "reload":
		return Action{Action: "reload"}, nil
	case "back":
		return Action{Action: "back"}, nil
	case "forward":
		return Action{Action: "forward"}, nil
	case "close":
		return Action{Action: "close"}, nil
	case "resize":
		if req.Width == nil || req.Height == nil {
			return Action{}, errors.New("width and height are required for resize")
		}
		w, h := *req.Width, *req.Height
		scale := 1.0
		if req.DeviceScaleFactor != nil {
			scale = *req.DeviceScaleFactor
		}
		if w < previewMinDimension || w > previewMaxWidth || h < previewMinDimension || h > previewMaxHeight ||
			math.IsNaN(scale) || math.IsInf(scale, 0) || scale < 1 || scale > 2 ||
			math.Ceil(float64(w)*scale)*math.Ceil(float64(h)*scale) > previewMaxEmulatedPixels {
			return Action{}, errors.New("preview viewport dimensions out of range")
		}
		return Action{Action: "resize", Width: &w, Height: &h, DeviceScaleFactor: &scale}, nil
	case "click":
		if math.IsNaN(req.X) || math.IsInf(req.X, 0) || math.IsNaN(req.Y) || math.IsInf(req.Y, 0) ||
			req.X < 0 || req.X > 4096 || req.Y < 0 || req.Y > 4096 {
			return Action{}, errors.New("click coordinates out of range")
		}
		return Action{Action: "click", X: req.X, Y: req.Y}, nil
	case "pointer":
		if math.IsNaN(req.X) || math.IsInf(req.X, 0) || math.IsNaN(req.Y) || math.IsInf(req.Y, 0) ||
			req.X < 0 || req.X > 4096 || req.Y < 0 || req.Y > 4096 {
			return Action{}, errors.New("pointer coordinates out of range")
		}
		act := Action{
			Action:     "pointer",
			Phase:      strings.ToLower(strings.TrimSpace(req.Phase)),
			Button:     strings.ToLower(strings.TrimSpace(req.Button)),
			X:          req.X,
			Y:          req.Y,
			ClickCount: req.ClickCount,
			Modifiers:  req.Modifiers,
		}
		if err := validatePointer(act, false); err != nil {
			return Action{}, err
		}
		return act, nil
	case "wheel":
		if math.IsNaN(req.X) || math.IsInf(req.X, 0) || math.IsNaN(req.Y) || math.IsInf(req.Y, 0) ||
			req.X < 0 || req.X > 4096 || req.Y < 0 || req.Y > 4096 ||
			math.IsNaN(req.DeltaX) || math.IsInf(req.DeltaX, 0) ||
			math.IsNaN(req.DeltaY) || math.IsInf(req.DeltaY, 0) {
			return Action{}, errors.New("wheel parameters out of range")
		}
		dx := math.Max(-previewMaxWheelDelta, math.Min(previewMaxWheelDelta, req.DeltaX))
		dy := math.Max(-previewMaxWheelDelta, math.Min(previewMaxWheelDelta, req.DeltaY))
		return Action{Action: "wheel", X: req.X, Y: req.Y, DeltaX: dx, DeltaY: dy}, nil
	case "type":
		if len(req.Text) > previewMaxTypeTextBytes {
			return Action{}, errors.New("text input exceeds limit")
		}
		if req.Text == "" && req.Key == "" {
			return Action{}, errors.New("text or key is required")
		}
		act := Action{Action: "type", Text: req.Text, Key: req.Key}
		if err := validateAction(act, false); err != nil {
			return Action{}, err
		}
		return act, nil
	case "key":
		act := Action{Action: "key", Key: req.Key}
		if err := validateAction(act, false); err != nil {
			return Action{}, err
		}
		return act, nil
	default:
		return Action{}, errors.New("unsupported preview action")
	}
}

func (sc *scope) navigatePreviewBounded(reqCtx, opctx context.Context, targetURL string) error {
	sc.clearSnapshotRefs(sc.tabID)
	timeout := previewMaxNavWait
	if deadline, ok := reqCtx.Deadline(); ok {
		if rem := time.Until(deadline) - 2*time.Second; rem > 2*time.Second && rem < timeout {
			timeout = rem
		}
	}
	navCtx, cancel := context.WithTimeout(opctx, timeout)
	err := sc.withTab(navCtx, chromedp.Navigate(targetURL))
	cancel()
	if err == nil {
		return nil
	}
	if reqCtx.Err() != nil {
		return reqCtx.Err()
	}
	if isStoreNavRecoverableErr(err) {
		if st, sErr := sc.state(opctx); sErr == nil && !isBlankTabURL(st.URL) && !isPreviewErrorTabURL(st.URL) {
			return nil
		}
	}
	return err
}

func (sc *scope) enforcePreviewOrigin(reqCtx, opctx context.Context, allowedOrigin, fallbackURL string, allowErrorTab bool) (State, bool, error) {
	st, err := sc.state(opctx)
	if err != nil {
		return emptyState(), false, err
	}
	hadBlockedNav := sc.consumePreviewNavBlocked()
	// Close stray popup tabs opened by target="_blank" so the preview remains single-tab and isolated.
	// If a same-origin link used target="_blank", follow it in the main preview tab instead of dropping it.
	if len(st.Tabs) > 1 {
		var sameOriginPopupURL string
		for _, tab := range st.Tabs {
			if tab.ID == sc.tabID {
				continue
			}
			if sameOriginPopupURL == "" && isAllowedPreviewTabURL(allowedOrigin, tab.URL) {
				sameOriginPopupURL = tab.URL
			} else if !isBlankTabURL(tab.URL) && !isAllowedPreviewTabURL(allowedOrigin, tab.URL) {
				hadBlockedNav = true
			}
			_ = target.CloseTarget(target.ID(tab.ID)).Do(opctx)
			sc.releaseTab(tab.ID)
		}
		if sameOriginPopupURL != "" && !hadBlockedNav {
			_ = sc.navigatePreviewBounded(reqCtx, opctx, sameOriginPopupURL)
		}
		if refreshed, rErr := sc.state(opctx); rErr == nil {
			st = refreshed
		}
		if sc.consumePreviewNavBlocked() {
			hadBlockedNav = true
		}
	}
	revertTarget := sc.previewLastAllowedURL
	if !isAllowedPreviewTabURL(allowedOrigin, revertTarget) {
		revertTarget = fallbackURL
	}
	if !isAllowedPreviewTabURL(allowedOrigin, revertTarget) {
		revertTarget = allowedOrigin + "/"
	}
	if isPreviewErrorTabURL(st.URL) {
		if hadBlockedNav {
			if isAllowedPreviewTabURL(allowedOrigin, revertTarget) {
				_ = sc.navigatePreviewBounded(reqCtx, opctx, revertTarget)
				if reverted, rErr := sc.state(opctx); rErr == nil && isAllowedPreviewTabURL(allowedOrigin, reverted.URL) {
					sc.previewLastAllowedURL = reverted.URL
					return reverted, true, nil
				}
			}
			return st, true, errors.New("navigation outside the authorized preview origin is not allowed")
		}
		if allowErrorTab {
			return st, false, nil
		}
		return st, false, errors.New("preview target is unreachable")
	}
	if isAllowedPreviewTabURL(allowedOrigin, st.URL) {
		sc.previewLastAllowedURL = st.URL
		return st, hadBlockedNav, nil
	}
	if isBlankTabURL(st.URL) {
		return st, hadBlockedNav, nil
	}
	// Tab navigated outside allowedOrigin (e.g. external link or redirect): revert immediately.
	_ = sc.navigatePreviewBounded(reqCtx, opctx, revertTarget)
	st, err = sc.state(opctx)
	if err != nil {
		return emptyState(), true, err
	}
	if isAllowedPreviewTabURL(allowedOrigin, st.URL) {
		sc.previewLastAllowedURL = st.URL
	}
	return st, true, nil
}

func (sc *scope) previewSnapshotFromState(allowedOrigin string, st State, blocked bool) PreviewSnapshot {
	snap := PreviewSnapshot{
		Running:            sc.browser != nil && sc.browser.Err() == nil,
		Title:              st.Title,
		Width:              sc.width,
		Height:             sc.height,
		BlockedExternalNav: blocked,
	}
	if snap.Running && isAllowedPreviewTabURL(allowedOrigin, st.URL) {
		snap.URL = st.URL
		snap.Path = previewPathFromURL(allowedOrigin, st.URL)
	}
	return snap
}

// PreviewState returns the state of an already-running headless preview scope
// without launching Chrome.
func (s *Service) PreviewState(ctx context.Context, scopeID, directory, allowedOrigin string) (PreviewSnapshot, error) {
	origin, _, err := NormalizePreviewOrigin(allowedOrigin)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	key, err := s.resolvePreviewScopeKey(scopeID, directory)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	sc, err := s.acquirePreviewScope(ctx, key, origin, false)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	if sc == nil {
		return PreviewSnapshot{Running: false, Width: previewDefaultWidth, Height: previewDefaultHeight}, nil
	}
	defer func() { <-sc.gate }()
	if sc.browser == nil || sc.browser.Err() != nil {
		return PreviewSnapshot{Running: false, Width: sc.width, Height: sc.height}, nil
	}
	opctx, stopOp := sc.browserOpCtx(ctx)
	defer stopOp()
	st, blocked, err := sc.enforcePreviewOrigin(ctx, opctx, origin, origin+"/", true)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	return sc.previewSnapshotFromState(origin, st, blocked), nil
}

// PreviewAction executes a restricted UI interaction against an origin-locked
// headless preview scope, lazily launching an isolated temporary Chrome on "open"/"navigate".
func (s *Service) PreviewAction(ctx context.Context, scopeID, directory, allowedOrigin string, req PreviewActionRequest) (PreviewSnapshot, error) {
	origin, defaultPath, err := NormalizePreviewOrigin(allowedOrigin)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	act, err := validatePreviewAction(req, origin)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	key, err := s.resolvePreviewScopeKey(scopeID, directory)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	if act.Action == "close" {
		s.ClosePreview(scopeID, directory)
		return PreviewSnapshot{Running: false, Width: previewDefaultWidth, Height: previewDefaultHeight}, nil
	}

	canColdStart := act.Action == "navigate" || act.Action == "resize"
	sc, err := s.acquirePreviewScope(ctx, key, origin, canColdStart)
	if err != nil {
		return PreviewSnapshot{}, err
	}
	if sc == nil {
		return PreviewSnapshot{Running: false, Width: previewDefaultWidth, Height: previewDefaultHeight}, errors.New("preview browser is not running")
	}
	defer func() {
		sc.lastUsed = s.now()
		<-sc.gate
	}()

	if act.Action == "resize" {
		scale := 1.0
		if act.DeviceScaleFactor != nil {
			scale = *act.DeviceScaleFactor
		}
		sc.width, sc.height, sc.scale = *act.Width, *act.Height, scale
		sc.fullPage = false
		if sc.browser == nil || sc.browser.Err() != nil {
			return PreviewSnapshot{Running: false, Width: sc.width, Height: sc.height}, nil
		}
	}

	justStarted := false
	if sc.browser == nil || sc.browser.Err() != nil {
		if act.Action != "navigate" {
			return PreviewSnapshot{Running: false, Width: sc.width, Height: sc.height}, errors.New("preview browser is not running")
		}
		if err := sc.start(ctx); err != nil {
			s.remove(key, sc)
			return PreviewSnapshot{}, err
		}
		justStarted = true
	}

	opctx, stopOp := sc.browserOpCtx(ctx)
	defer stopOp()

	fallbackURL := origin + defaultPath
	hasPrevPage := false
	if curState, stErr := sc.state(opctx); stErr == nil && isAllowedPreviewTabURL(origin, curState.URL) {
		fallbackURL = curState.URL
		sc.previewLastAllowedURL = curState.URL
		hasPrevPage = true
	}

	switch act.Action {
	case "navigate":
		if err := sc.navigatePreviewBounded(ctx, opctx, act.URL); err != nil {
			if sc.consumePreviewNavBlocked() {
				if hasPrevPage && !justStarted {
					_ = sc.navigatePreviewBounded(ctx, opctx, fallbackURL)
					if reverted, rErr := sc.state(opctx); rErr == nil && isAllowedPreviewTabURL(origin, reverted.URL) {
						sc.previewLastAllowedURL = reverted.URL
						return sc.previewSnapshotFromState(origin, reverted, true), nil
					}
				}
				if justStarted {
					sc.resetBrowser()
				}
				return PreviewSnapshot{}, errors.New("navigation outside the authorized preview origin is not allowed")
			}
			if justStarted {
				sc.resetBrowser()
			}
			return PreviewSnapshot{}, err
		}
	case "resize":
		if err := sc.withTab(opctx, sc.viewport().action()); err != nil {
			return PreviewSnapshot{}, err
		}
		sc.viewports[sc.tabID] = sc.viewport()
	default:
		if _, err := sc.perform(opctx, act); err != nil {
			return PreviewSnapshot{}, err
		}
	}

	st, blocked, err := sc.enforcePreviewOrigin(ctx, opctx, origin, fallbackURL, act.Action == "resize")
	if err != nil {
		if justStarted {
			sc.resetBrowser()
		}
		return PreviewSnapshot{}, err
	}
	return sc.previewSnapshotFromState(origin, st, blocked), nil
}

// PreviewFrame captures a JPEG frame from a running origin-locked headless
// preview scope, guaranteeing that pixels from a foreign origin are never captured.
func (s *Service) PreviewFrame(ctx context.Context, scopeID, directory, allowedOrigin string) ([]byte, PreviewSnapshot, error) {
	origin, defaultPath, err := NormalizePreviewOrigin(allowedOrigin)
	if err != nil {
		return nil, PreviewSnapshot{}, err
	}
	key, err := s.resolvePreviewScopeKey(scopeID, directory)
	if err != nil {
		return nil, PreviewSnapshot{}, err
	}
	sc, err := s.acquirePreviewScope(ctx, key, origin, false)
	if err != nil {
		return nil, PreviewSnapshot{}, err
	}
	if sc == nil {
		return nil, PreviewSnapshot{Running: false, Width: previewDefaultWidth, Height: previewDefaultHeight}, errors.New("preview browser is not running")
	}
	defer func() {
		sc.lastUsed = s.now()
		<-sc.gate
	}()
	if sc.browser == nil || sc.browser.Err() != nil {
		return nil, PreviewSnapshot{Running: false, Width: sc.width, Height: sc.height}, errors.New("preview browser is not running")
	}
	opctx, stopOp := sc.browserOpCtx(ctx)
	defer stopOp()

	fallbackURL := sc.previewLastAllowedURL
	if !isAllowedPreviewTabURL(origin, fallbackURL) {
		fallbackURL = origin + defaultPath
	}
	st, blocked, err := sc.enforcePreviewOrigin(ctx, opctx, origin, fallbackURL, false)
	if err != nil {
		return nil, PreviewSnapshot{}, err
	}
	if !isAllowedPreviewTabURL(origin, st.URL) && !isBlankTabURL(st.URL) {
		return nil, sc.previewSnapshotFromState(origin, st, true), errors.New("preview tab is outside authorized origin")
	}
	img, err := sc.capture(opctx)
	if err != nil {
		return nil, PreviewSnapshot{}, err
	}
	return img, sc.previewSnapshotFromState(origin, st, blocked), nil
}

// ClosePreview terminates and disposes a headless preview scope if active.
func (s *Service) ClosePreview(scopeID, directory string) {
	if s == nil {
		return
	}
	key, err := s.resolvePreviewScopeKey(scopeID, directory)
	if err != nil {
		return
	}
	s.mu.Lock()
	sc := s.scopes[key]
	if sc == nil {
		s.mu.Unlock()
		return
	}
	delete(s.scopes, key)
	delete(s.idleClosed, key)
	s.mu.Unlock()

	sc.gate <- struct{}{}
	sc.dispose()
	<-sc.gate
}
