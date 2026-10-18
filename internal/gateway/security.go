package gateway

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func nativeHost(host string) bool {
	return host == "wails" || host == "wails.localhost" || host == "app.wails.localhost"
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
