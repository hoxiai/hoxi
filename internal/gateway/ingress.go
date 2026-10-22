package gateway

import "net/http"

// servePairedRequest authenticates a paired controller with the LAN pairing
// token and serves the request under the paired-controller route allow-list.
// The LAN listener and the reverse-proxy ingress both use it; they differ only
// in how the request reaches the gateway.
func (g *Gateway) servePairedRequest(w http.ResponseWriter, r *http.Request, source string) {
	if r.URL.RawPath != "" {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	if !g.pairing.Authorize(r) {
		writeJSONError(w, http.StatusUnauthorized, "invalid pairing token")
		return
	}
	// The shared host/origin guard trusts loopback authorities only. The pairing
	// token, not the Host header, is the access control here, so present a
	// trusted authority and strip untrusted browser/proxy headers before the
	// request reaches that guard.
	sanitizeRemoteRequest(r)
	g.serveRemoteRequest(w, r, source)
}

// ServeIngressRequest serves a request forwarded by a local reverse proxy (for
// example cloudflared for a Cloudflare Tunnel). The listener binds loopback, so
// only a local forwarder can reach it, and every request is still gated by the
// pairing token and the remote route allow-list.
//
// Unlike the LAN listener there is no source-address filter: the forwarder's own
// public-facing authentication (for example Cloudflare Access) decides who may
// reach this gateway, and the forwarder dials in over loopback. The transport is
// the proxy's TLS, terminated at its edge, so an end-to-end encrypted envelope
// is still required before this path carries code or session content to a
// provider that must not read it.
func (g *Gateway) ServeIngressRequest(w http.ResponseWriter, r *http.Request) {
	g.servePairedRequest(w, r, remoteSourceRelay)
}
