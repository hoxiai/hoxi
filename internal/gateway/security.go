package gateway

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	gatewayTokenHeader     = "X-Hoxi-Token"
	gatewayTokenCookieName = "hoxi_gateway_token"
	gatewayTokenQueryParam = "hoxiToken"
)

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
	"/auth",
}

var upstreamPassthroughPrefixes = append(append([]string(nil), openCodeProxyPrefixes...), "/provider")

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
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || nativeHost(host)
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
	g.browser.SetUIOrigins(filtered...)
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

func isUpstreamPassthroughPath(requestPath string) bool {
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

func (g *Gateway) guardUpstreamPassthrough(w http.ResponseWriter, r *http.Request) bool {
	if !isUpstreamPassthroughPath(r.URL.Path) {
		r.Header.Del(gatewayTokenHeader)
		return true
	}
	got := requestGatewayToken(r)
	stripGatewayTokenFromRequest(r)
	tok := g.GatewayToken()
	if tok == "" || isInternalConversationRoute(r) || nativeWailsRequest(r) {
		return true
	}
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
		writeJSONError(w, http.StatusForbidden, "missing or invalid gateway token")
		return false
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
				http.Error(w, "untrusted Sec-Fetch-Site", http.StatusForbidden)
				return false
			}
		}
	}
	return true
}
