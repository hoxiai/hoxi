package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// securityGateState groups loopback origin/token guards and native computer-use
// confirmation state.
type securityGateState struct {
	originMu           sync.RWMutex
	uiOrigins          map[string]bool
	gatewayToken       string
	browserAgentToken  string
	computerAgentToken string
	confirmComputerApp func(context.Context, ComputerAppConfirmation) (bool, error)
	computerLocale     string
	computerConfirming atomic.Bool
	// confirmFileAccess asks the desktop user before reading or opening a
	// credential/protected path. Remote controllers never reach it.
	confirmFileAccess    func(context.Context, FileAccessConfirmation) (bool, error)
	fileAccessConfirming atomic.Bool
}

const (
	gatewayTokenHeader     = "X-Hoxi-Token"
	gatewayTokenCookieName = "hoxi_gateway_token"
	gatewayTokenQueryParam = "hoxiToken"
)

type verifiedGatewayTokenKey struct{}

func withVerifiedGatewayToken(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), verifiedGatewayTokenKey{}, true))
}

func hasVerifiedGatewayToken(r *http.Request) bool {
	if r == nil {
		return false
	}
	ok, _ := r.Context().Value(verifiedGatewayTokenKey{}).(bool)
	return ok
}

// verifyStrictDesktopTokenRequest enforces that a local desktop management
// endpoint carries the real per-boot gateway token whenever configured, and
// never trusts spoofable Wails Host/Origin headers alone.
func (g *Gateway) verifyStrictDesktopTokenRequest(surfaceName string) func(*http.Request) error {
	return func(r *http.Request) error {
		if isRemoteRequest(r) || !trustedHost(r.Host) {
			return errors.New(surfaceName + " requires the desktop UI")
		}
		tok := g.GatewayToken()
		if tok == "" {
			return nil
		}
		if hasVerifiedGatewayToken(r) {
			return nil
		}
		got := requestGatewayToken(r)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
			return errors.New(surfaceName + " requires the local gateway token")
		}
		return nil
	}
}

var openCodeProxyPrefixes = []string{
	"/session",
	"/event",
	"/global",
	"/permission",
	"/question",
	"/pty",
	"/vcs",
	"/file",
	"/find",
	"/config",
	"/command",
	"/agent",
	"/lsp",
	"/mcp",
	"/formatter",
	"/skill",
	"/experimental",
	"/path",
	"/project",
}

var upstreamPassthroughPrefixes = append(append([]string(nil), openCodeProxyPrefixes...), "/provider", "/auth")

func nativeHost(host string) bool {
	return host == "wails" || host == "wails.localhost" || host == "app.wails.localhost"
}

func nativeWailsRequest(r *http.Request) bool {
	if nativeHost(r.Host) {
		return true
	}
	origin := r.Header.Get("Origin")
	return origin == "wails://wails" || origin == "wails://wails.localhost" || origin == "wails://app.wails.localhost"
}

// Trust literal loopback authorities, not DNS names that might resolve to them.
// Native Wails uses a synthetic authority without a port.
func trustedHost(authority string) bool {
	if nativeHost(authority) {
		return true
	}
	host := authority
	if strings.Contains(authority, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(authority)
		if err != nil {
			return false
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return false
		}
	}
	return host == "localhost" || net.ParseIP(host).IsLoopback() || nativeHost(host)
}

func validUIOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https" || (u.Scheme == "wails" && nativeHost(u.Hostname()))) &&
		u.Host != "" && trustedHost(u.Host) && u.User == nil && u.Path == "" &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		!strings.Contains(origin, "#") && u.Opaque == ""
}

// SetBrowserUIOrigins replaces the gateway's exact UI origins and forwards the
// HTTP(S) allowlist to the browser service. Invalid entries fail closed. Native
// Wails origins without ports are built in; development ports must be explicit.
func (g *Gateway) SetBrowserUIOrigins(origins ...string) {
	allowed := make(map[string]bool)
	var filtered []string
	for _, origin := range origins {
		if validUIOrigin(origin) {
			allowed[origin] = true
			filtered = append(filtered, origin)
		}
	}
	g.originMu.Lock()
	g.uiOrigins = allowed
	if g.browser != nil {
		g.browser.SetUIOrigins(filtered...)
	}
	g.originMu.Unlock()
}

// SetGatewayToken configures the per-boot defense-in-depth token required on
// loopback upstream passthrough routes (/session, /pty, /auth, etc.).
func (g *Gateway) SetGatewayToken(token string) {
	g.originMu.Lock()
	g.gatewayToken = strings.TrimSpace(token)
	g.originMu.Unlock()
}

func (g *Gateway) GatewayToken() string {
	g.originMu.RLock()
	defer g.originMu.RUnlock()
	return g.gatewayToken
}

func isTokenProtectedHoxiHostPath(requestPath string) bool {
	switch requestPath {
	case "/api/hoxi/pty",
		"/api/hoxi/agents",
		"/api/hoxi/commands",
		"/api/hoxi/lsp",
		"/api/hoxi/schedules",
		"/api/hoxi/system/cli-link",
		"/api/hoxi/clipboard/image",
		"/api/hoxi/clipboard/images",
		"/api/hoxi/file/open",
		"/api/hoxi/file/save",
		"/api/hoxi/file/save-images",
		"/api/hoxi/provider/catalog",
		"/api/hoxi/provider/auth-methods",
		"/api/hoxi/provider/config",
		"/api/hoxi/provider/dispose",
		"/api/hoxi/provider/credentials",
		"/api/hoxi/provider/disconnect",
		"/api/hoxi/local-cli/disconnect",
		"/api/hoxi/work/joined-collabs":
		return true
	}
	if strings.HasPrefix(requestPath, "/api/hoxi/intake/") &&
		!strings.HasPrefix(requestPath, "/api/hoxi/intake/portal/") {
		return true
	}
	if strings.HasPrefix(requestPath, "/api/hoxi/collab/") &&
		!strings.HasPrefix(requestPath, "/api/hoxi/collab/portal/") {
		return true
	}
	return strings.HasPrefix(requestPath, "/api/hoxi/pty/") ||
		strings.HasPrefix(requestPath, "/api/hoxi/schedules/") ||
		strings.HasPrefix(requestPath, "/api/hoxi/provider/credentials/") ||
		strings.HasPrefix(requestPath, "/api/hoxi/provider/oauth/") ||
		strings.HasPrefix(requestPath, "/api/hoxi/work/joined-collabs/")
}

func isUpstreamPassthroughPath(requestPath string) bool {
	if isTokenProtectedHoxiHostPath(requestPath) {
		return true
	}
	if requestPath == "/api/hoxi" || strings.HasPrefix(requestPath, "/api/hoxi/") ||
		requestPath == "/file/content" || requestPath == "/api/file/content" ||
		requestPath == "/healthz" {
		return false
	}
	p := strings.TrimPrefix(requestPath, "/api")
	for _, prefix := range upstreamPassthroughPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// rawOpenCodePermissionReply reports a request that would answer an OpenCode
// permission past Hoxi: the approval floors, session grants and audit only
// apply on /api/hoxi/permission/{id}/reply, and OpenCode keeps a raw "always"
// for its whole instance, ahead of its config (runner
// approval_opencode_always.go). Listing stays open.
func rawOpenCodePermissionReply(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return false
	}
	p := strings.ToLower(r.URL.Path)
	if strings.HasPrefix(p, "/api/") {
		p = strings.TrimPrefix(p, "/api")
	}
	parts := strings.Split(strings.Trim(path.Clean("/"+p), "/"), "/")
	switch {
	case len(parts) >= 2 && parts[0] == "permission":
		return true
	case len(parts) >= 3 && parts[0] == "session" && parts[2] == "permissions":
		return true
	}
	return false
}

func requestGatewayToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(gatewayTokenHeader)); v != "" {
		return v
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		if tok := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")); tok != "" {
			return tok
		}
	}
	if c, err := r.Cookie(gatewayTokenCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		return strings.TrimSpace(c.Value)
	}
	if strings.Contains(r.URL.RawQuery, gatewayTokenQueryParam) {
		if q := strings.TrimSpace(r.URL.Query().Get(gatewayTokenQueryParam)); q != "" {
			return q
		}
	}
	return ""
}

func stripGatewayTokenFromRequest(r *http.Request) {
	r.Header.Del(gatewayTokenHeader)
	if strings.Contains(r.URL.RawQuery, gatewayTokenQueryParam) {
		if values, err := url.ParseQuery(r.URL.RawQuery); err == nil {
			values.Del(gatewayTokenQueryParam)
			r.URL.RawQuery = values.Encode()
		}
	}
}

func (g *Gateway) guardUpstreamPassthrough(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if !isUpstreamPassthroughPath(r.URL.Path) {
		r.Header.Del(gatewayTokenHeader)
		return r, true
	}
	got := requestGatewayToken(r)
	stripGatewayTokenFromRequest(r)
	tok := g.GatewayToken()
	matched := tok != "" && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1
	if matched {
		r = withVerifiedGatewayToken(r)
	}
	if tok == "" || isInternalConversationRoute(r) || nativeWailsRequest(r) || matched {
		return r, true
	}
	writeJSONError(w, http.StatusForbidden, "missing or invalid gateway token")
	return r, false
}

// isCrossSiteDesktopMediaPath reports whether requestPath is an exact read-only
// local media or attachment path that a native desktop WebView (e.g. macOS
// wails://wails) may load via a loopback <img>/<video>/<audio> tag.
func isCrossSiteDesktopMediaPath(requestPath string) bool {
	if requestPath == "" || path.Clean(requestPath) != requestPath {
		return false
	}
	switch requestPath {
	case "/file/content", "/api/file/content", "/api/hoxi/file/content":
		return true
	}
	if strings.HasPrefix(requestPath, "/api/hoxi/attachments/") {
		rest := strings.TrimPrefix(requestPath, "/api/hoxi/attachments/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 && parts[0] != "" {
			return true
		}
		return len(parts) == 2 && parts[0] != "" && parts[1] == "content"
	}
	if strings.HasPrefix(requestPath, "/api/hoxi/work/joined-collabs/") {
		rest := strings.TrimPrefix(requestPath, "/api/hoxi/work/joined-collabs/")
		parts := strings.Split(rest, "/")
		return len(parts) == 4 && parts[0] != "" && parts[1] == "proxy" && parts[2] == "attachments" && parts[3] != ""
	}
	return false
}

// allowsCrossSiteDesktopMedia permits cross-site GET/HEAD no-CORS media loads
// (image/video/audio) from a native desktop WebView (e.g. macOS wails://wails)
// to read-only loopback attachment/media paths ONLY when the caller presents
// the exact per-boot gateway token. Unauthenticated third-party web pages never
// possess the boot token and remain blocked with 403.
func (g *Gateway) allowsCrossSiteDesktopMedia(r *http.Request) bool {
	if isRemoteRequest(r) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	if !isCrossSiteDesktopMediaPath(r.URL.Path) {
		return false
	}
	dests := r.Header.Values("Sec-Fetch-Dest")
	if len(dests) != 1 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(dests[0])) {
	case "image", "video", "audio":
	default:
		return false
	}
	modes := r.Header.Values("Sec-Fetch-Mode")
	if len(modes) > 1 {
		return false
	}
	if len(modes) == 1 {
		mode := strings.TrimSpace(modes[0])
		if mode != "" && !strings.EqualFold(mode, "no-cors") {
			return false
		}
	}
	tok := g.GatewayToken()
	if tok == "" {
		return false
	}
	got := requestGatewayToken(r)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1
}

// allowsCrossSiteDesktopIframe permits cross-site GET/HEAD iframe navigations
// from a native desktop WebView (e.g. macOS wails://wails) to exact,
// zero-secret loopback HTML bridge paths declared by an installed extension
// and guarded by CSP frame-ancestors. Non-iframe fetches, remote requests, and
// mutating methods remain blocked.
func (g *Gateway) allowsCrossSiteDesktopIframe(r *http.Request) bool {
	if isRemoteRequest(r) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	if !g.isDesktopIframeRoute(r.URL.Path) {
		return false
	}
	dests := r.Header.Values("Sec-Fetch-Dest")
	if len(dests) != 1 || !strings.EqualFold(strings.TrimSpace(dests[0]), "iframe") {
		return false
	}
	modes := r.Header.Values("Sec-Fetch-Mode")
	if len(modes) > 1 {
		return false
	}
	if len(modes) == 1 {
		mode := strings.TrimSpace(modes[0])
		if mode != "" && !strings.EqualFold(mode, "navigate") {
			return false
		}
	}
	return true
}

func (g *Gateway) allowRequest(w http.ResponseWriter, r *http.Request) bool {
	if !trustedHost(r.Host) {
		http.Error(w, "untrusted Host", http.StatusForbidden)
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) != 0 {
		origin := r.Header.Get("Origin")
		g.originMu.RLock()
		allowed := g.uiOrigins[origin]
		g.originMu.RUnlock()
		// Exact strings also exclude null, malformed origins, and origin lists.
		allowed = allowed || origin == "wails://wails" || origin == "wails://wails.localhost" || origin == "wails://app.wails.localhost"
		if len(origins) != 1 || !allowed {
			http.Error(w, "untrusted Origin", http.StatusForbidden)
			return false
		}
	} else {
		// Cross-site navigations and no-CORS requests can omit Origin. Keep
		// no-Origin CLI/OpenCode calls and native WebView requests working.
		for _, site := range r.Header.Values("Sec-Fetch-Site") {
			if site != "" && site != "same-origin" && site != "same-site" && site != "none" {
				if site == "cross-site" && (g.allowsCrossSiteDesktopIframe(r) || g.allowsCrossSiteDesktopMedia(r)) {
					continue
				}
				http.Error(w, "untrusted Sec-Fetch-Site", http.StatusForbidden)
				return false
			}
		}
	}
	return true
}
