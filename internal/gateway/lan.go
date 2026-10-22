package gateway

import "net/http"

// ServeLANRequest serves a request from a device that authenticated with this
// desktop's LAN pairing token (the value embedded in the QR code under
// Settings → Remote). It runs the same paired-controller route allow-list and
// file confinement as the GoPanel tunnel, so a phone on the local network gets
// exactly the remote capabilities and nothing more.
//
// The phone reaches this Mac over plain HTTP on the local network, so the token
// is bearer-style and the transport is unencrypted: anyone who can passively
// read the LAN can recover the token, and anyone who has it can act as the
// phone. Keep the pairing QR private and treat the LAN as trusted. An
// end-to-end encrypted transport is tracked separately.
func (g *Gateway) ServeLANRequest(w http.ResponseWriter, r *http.Request) {
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
	// trusted authority before the request reaches that guard.
	r.Host = "127.0.0.1"
	g.serveRemoteRequest(w, r, remoteSourceLAN)
}
