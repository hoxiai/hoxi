package gateway

import (
	"net"
	"net/http"
	"strings"
)

// ServeLANRequest serves a request from a device that authenticated with this
// desktop's LAN pairing token (the value embedded in the QR code under
// Settings → Remote). It runs the same paired-controller route allow-list and
// file confinement as the GoPanel tunnel, so a phone on the local network gets
// exactly the remote capabilities and nothing more.
//
// Paired mobile clients carrying the E2EE key from the pairing QR authenticate
// and encrypt payloads end-to-end with AES-256-GCM (X-Hoxi-E2EE: v1); legacy
// clients without X-Hoxi-E2EE remain compatible on trusted local networks.
func (g *Gateway) ServeLANRequest(w http.ResponseWriter, r *http.Request) {
	// Refuse public source addresses before doing any token work: the port may
	// be reachable from the internet on a host without NAT, and Hoxi serves the
	// local network only.
	if !isLANClient(r.RemoteAddr) {
		writeJSONError(w, http.StatusForbidden, "the LAN pairing listener only serves local-network clients")
		return
	}
	if isIntakePublicSurfaceRequest(r) {
		g.serveIntakePublicSurface(w, r)
		return
	}
	if g.isExtensionPublicPortalRequest(r) {
		g.serveExtensionPublicPortal(w, r)
		return
	}
	g.servePairedRequest(w, r, remoteSourceLAN)
}

// isLANClient reports whether a request arrived from the local network: a
// loopback, RFC1918 / unique-local, link-local or 100.64.0.0/10 (carrier-grade
// NAT, e.g. Tailscale) address. Public source addresses are refused.
func isLANClient(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}
