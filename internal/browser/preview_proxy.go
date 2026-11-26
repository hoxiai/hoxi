package browser

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

const previewProxyDialTimeout = 5 * time.Second

var (
	errPreviewTargetBlocked = errors.New("preview target address is blocked")

	reservedHoxiLoopbackPorts = map[int]bool{
		5400: true, // Hoxi backend / dev gateway
		5410: true, // Hoxi release desktop bridge
		5417: true, // Hoxi LAN pairing server
		5418: true, // Hoxi Cloudflare ingress server
		5420: true, // Hoxi dev desktop bridge
	}

	previewHopByHopHeaders = []string{
		"Proxy-Authorization",
		"Proxy-Connection",
		"Connection",
		"Keep-Alive",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	}
)

func isReservedHoxiLoopbackPort(port int) bool {
	return reservedHoxiLoopbackPorts[port]
}

// parseNonCanonicalIPv4 recognizes legacy inet_aton numeric IPv4 forms that
// browsers/OS resolvers accept (e.g. 127.1, 0177.0.0.1, 0x7f000001, 2130706433).
func parseNonCanonicalIPv4(host string) (netip.Addr, bool) {
	if host == "" {
		return netip.Addr{}, false
	}
	parts := strings.Split(host, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return netip.Addr{}, false
	}
	vals := make([]uint64, len(parts))
	for i, part := range parts {
		if part == "" {
			return netip.Addr{}, false
		}
		v, err := strconv.ParseUint(part, 0, 64)
		if err != nil {
			return netip.Addr{}, false
		}
		vals[i] = v
	}
	var ip32 uint32
	switch len(vals) {
	case 1:
		if vals[0] > 0xffffffff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0])
	case 2:
		if vals[0] > 0xff || vals[1] > 0xffffff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0]<<24) | uint32(vals[1])
	case 3:
		if vals[0] > 0xff || vals[1] > 0xff || vals[2] > 0xffff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0]<<24) | uint32(vals[1]<<16) | uint32(vals[2])
	case 4:
		if vals[0] > 0xff || vals[1] > 0xff || vals[2] > 0xff || vals[3] > 0xff {
			return netip.Addr{}, false
		}
		ip32 = uint32(vals[0]<<24) | uint32(vals[1]<<16) | uint32(vals[2]<<8) | uint32(vals[3])
	}
	return netip.AddrFrom4([4]byte{byte(ip32 >> 24), byte(ip32 >> 16), byte(ip32 >> 8), byte(ip32)}), true
}

// extractWildcardDNSIP extracts an embedded IPv4 address from wildcard DNS
// hostnames such as 127.0.0.1.nip.io, app-127-0-0-1.sslip.io, or 7f000001.nip.io.
func extractWildcardDNSIP(host string) (netip.Addr, bool) {
	var prefix string
	for _, suffix := range []string{".nip.io", ".sslip.io", ".xip.io"} {
		if strings.HasSuffix(host, suffix) {
			prefix = strings.TrimSuffix(host, suffix)
			break
		}
	}
	if prefix == "" {
		return netip.Addr{}, false
	}
	labels := strings.Split(prefix, ".")
	// Dotted IPv4 at the tail of the prefix: e.g. foo.127.0.0.1.nip.io
	if len(labels) >= 4 {
		cand := strings.Join(labels[len(labels)-4:], ".")
		if ip, err := netip.ParseAddr(cand); err == nil {
			return ip.Unmap(), true
		}
	}
	last := labels[len(labels)-1]
	// Hex IPv4 label: e.g. 7f000001.nip.io or app-7f000001.nip.io
	if len(last) >= 8 {
		hexPart := last[len(last)-8:]
		if v, err := strconv.ParseUint(hexPart, 16, 32); err == nil {
			return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
		}
	}
	// Dash-separated IPv4: e.g. 127-0-0-1.nip.io or foo-127-0-0-1.sslip.io
	dashParts := strings.Split(last, "-")
	if len(dashParts) >= 4 {
		cand := strings.Join(dashParts[len(dashParts)-4:], ".")
		if ip, err := netip.ParseAddr(cand); err == nil {
			return ip.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

func isLoopbackPreviewHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), "."))
	if h == "" {
		return false
	}
	if h == "localhost" || h == "localhost.localdomain" || h == "wails" ||
		h == "wails.localhost" || h == "app.wails.localhost" ||
		strings.HasSuffix(h, ".localhost") ||
		h == "localtest.me" || strings.HasSuffix(h, ".localtest.me") ||
		h == "lvh.me" || strings.HasSuffix(h, ".lvh.me") ||
		h == "vcap.me" || strings.HasSuffix(h, ".vcap.me") {
		return true
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		return ip.Unmap().IsLoopback()
	}
	if ip, ok := parseNonCanonicalIPv4(h); ok {
		return ip.IsLoopback()
	}
	if ip, ok := extractWildcardDNSIP(h); ok {
		return ip.IsLoopback()
	}
	return false
}

func isBlockedPreviewWildcardHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), "."))
	if ip, ok := parseNonCanonicalIPv4(h); ok && isBlockedPreviewIP(ip) {
		return true
	}
	if ip, ok := extractWildcardDNSIP(h); ok && isBlockedPreviewIP(ip) {
		return true
	}
	return false
}

func parseOriginAuthority(allowedOrigin string) (scheme, host string, port int, canonical string, err error) {
	canonical, _, err = NormalizePreviewOrigin(allowedOrigin)
	if err != nil {
		return "", "", 0, "", err
	}
	u, err := url.Parse(canonical)
	if err != nil {
		return "", "", 0, "", err
	}
	scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	host = strings.ToLower(strings.Trim(strings.TrimSpace(u.Hostname()), "[]"))
	port = 80
	if scheme == "https" {
		port = 443
	}
	if portStr := strings.TrimSpace(u.Port()); portStr != "" {
		p, pErr := strconv.Atoi(portStr)
		if pErr != nil || p < 1 || p > 65535 {
			return "", "", 0, "", errors.New("invalid preview origin port")
		}
		port = p
	}
	return scheme, host, port, canonical, nil
}

// previewOriginProxy runs a local 127.0.0.1:0 HTTP/CONNECT proxy dedicated to
// one headless preview_* scope. Every Chrome request (top-level navigation,
// iframe, subresource, redirect, WebSocket, worker) is routed through it with
// --proxy-bypass-list=<-loopback> so foreign origins, cross-origin iframes,
// cross-origin redirects, blocked IPs, and DNS-resolved Hoxi loopback ports are
// blocked at the transport layer before any socket is dialed.
type previewOriginProxy struct {
	allowedOrigin string
	allowedScheme string
	allowedHost   string
	allowedPort   int
	lookupIP      func(ctx context.Context, host string) ([]netip.Addr, error)
	dialer        *net.Dialer
	ln            net.Listener
	srv           *http.Server
	transport     *http.Transport
	blocked       atomic.Uint64
	closeOnce     sync.Once
	mu            sync.Mutex
	closed        bool
	conns         map[net.Conn]struct{}
}

func startPreviewOriginProxy(allowedOrigin string) (*previewOriginProxy, error) {
	return startPreviewOriginProxyWithLookup(allowedOrigin, nil)
}

func startPreviewOriginProxyWithLookup(allowedOrigin string, lookup func(ctx context.Context, host string) ([]netip.Addr, error)) (*previewOriginProxy, error) {
	scheme, host, port, canonical, err := parseOriginAuthority(allowedOrigin)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind local preview proxy: %w", err)
	}
	if lookup == nil {
		lookup = func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		}
	}
	p := &previewOriginProxy{
		allowedOrigin: canonical,
		allowedScheme: scheme,
		allowedHost:   host,
		allowedPort:   port,
		lookupIP:      lookup,
		dialer: &net.Dialer{
			Timeout:   previewProxyDialTimeout,
			KeepAlive: 30 * time.Second,
		},
		ln:    ln,
		conns: make(map[net.Conn]struct{}),
	}
	p.transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           p.dialAllowedTarget,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          32,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   previewProxyDialTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	p.srv = &http.Server{
		Handler:           http.HandlerFunc(p.serveHTTP),
		ErrorLog:          diagnostics.NewQuietHTTPServerLogger(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = p.srv.Serve(ln)
	}()
	return p, nil
}

func (p *previewOriginProxy) Addr() string {
	if p == nil || p.ln == nil {
		return ""
	}
	return p.ln.Addr().String()
}

func (p *previewOriginProxy) BlockedCount() uint64 {
	if p == nil {
		return 0
	}
	return p.blocked.Load()
}

func (p *previewOriginProxy) trackConn(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = conn.Close()
		return false
	}
	p.conns[conn] = struct{}{}
	return true
}

func (p *previewOriginProxy) untrackConn(conn net.Conn) {
	p.mu.Lock()
	delete(p.conns, conn)
	p.mu.Unlock()
	_ = conn.Close()
}

func (p *previewOriginProxy) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		active := make([]net.Conn, 0, len(p.conns))
		for c := range p.conns {
			active = append(active, c)
		}
		p.conns = nil
		p.mu.Unlock()
		for _, c := range active {
			_ = c.Close()
		}
		if p.srv != nil {
			_ = p.srv.Close()
		}
		if p.ln != nil {
			_ = p.ln.Close()
		}
		if p.transport != nil {
			p.transport.CloseIdleConnections()
		}
	})
}

func (p *previewOriginProxy) validateResolvedAddr(ip netip.Addr, port int) error {
	if isBlockedPreviewIP(ip) {
		return errPreviewTargetBlocked
	}
	if ip.Unmap().IsLoopback() && isReservedHoxiLoopbackPort(port) {
		return errPreviewTargetBlocked
	}
	return nil
}

func (p *previewOriginProxy) dialAllowedTarget(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		p.blocked.Add(1)
		return nil, errPreviewTargetBlocked
	}
	normHost := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	port, err := strconv.Atoi(portStr)
	if err != nil || normHost != p.allowedHost || port != p.allowedPort {
		p.blocked.Add(1)
		return nil, errPreviewTargetBlocked
	}
	var ips []netip.Addr
	if literal, parseErr := netip.ParseAddr(normHost); parseErr == nil {
		ips = []netip.Addr{literal}
	} else {
		resolved, lookErr := p.lookupIP(ctx, normHost)
		if lookErr != nil {
			return nil, lookErr
		}
		if len(resolved) == 0 {
			return nil, fmt.Errorf("no IP addresses resolved for %s", normHost)
		}
		ips = resolved
	}
	// Fail closed if ANY resolved IP violates the address or Hoxi-port policy.
	for _, ip := range ips {
		if err := p.validateResolvedAddr(ip, port); err != nil {
			p.blocked.Add(1)
			return nil, err
		}
	}
	var lastErr error
	for _, ip := range ips {
		target := net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(port))
		conn, dialErr := p.dialer.DialContext(ctx, network, target)
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("failed to connect to %s", addr)
}

func (p *previewOriginProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleForwardHTTP(w, r)
}

func (p *previewOriginProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(r.Host))
	if err != nil {
		p.blocked.Add(1)
		http.Error(w, "invalid preview tunnel target", http.StatusForbidden)
		return
	}
	normHost := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	port, err := strconv.Atoi(portStr)
	if err != nil || normHost != p.allowedHost || port != p.allowedPort {
		p.blocked.Add(1)
		http.Error(w, "cross-origin preview tunnel blocked", http.StatusForbidden)
		return
	}
	upConn, err := p.dialAllowedTarget(r.Context(), "tcp", net.JoinHostPort(p.allowedHost, strconv.Itoa(p.allowedPort)))
	if err != nil {
		if errors.Is(err, errPreviewTargetBlocked) {
			http.Error(w, "preview target address is not allowed", http.StatusForbidden)
			return
		}
		http.Error(w, "preview upstream connect failed", http.StatusBadGateway)
		return
	}
	if !p.trackConn(upConn) {
		return
	}
	defer p.untrackConn(upConn)

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		return
	}
	if !p.trackConn(clientConn) {
		return
	}
	defer p.untrackConn(clientConn)

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	var cConn net.Conn = clientConn
	if clientBuf != nil && clientBuf.Reader.Buffered() > 0 {
		cConn = &bufferedConn{Conn: clientConn, r: clientBuf.Reader}
	}
	// On an http:// preview origin, CONNECT is only needed for plain ws://
	// upgrades; reject TLS ClientHello (record type 0x16) so https:// cannot
	// bypass scheme lock on a dual-protocol port.
	if p.allowedScheme == "http" {
		br := bufio.NewReader(cConn)
		_ = clientConn.SetReadDeadline(time.Now().Add(previewProxyDialTimeout))
		first, peekErr := br.Peek(1)
		_ = clientConn.SetReadDeadline(time.Time{})
		if peekErr != nil {
			return
		}
		if len(first) == 1 && first[0] == 0x16 {
			p.blocked.Add(1)
			return
		}
		cConn = &bufferedConn{Conn: clientConn, r: br}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upConn, cConn)
		_ = upConn.Close()
		_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(cConn, upConn)
		_ = clientConn.Close()
		_ = upConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	}()
	wg.Wait()
}

func (p *previewOriginProxy) handleForwardHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || (r.URL.Scheme != "http" && r.URL.Scheme != "https") || r.URL.Host == "" {
		p.blocked.Add(1)
		http.Error(w, "absolute http(s) URL required", http.StatusBadRequest)
		return
	}
	gotOrigin, err := canonicalPreviewOrigin(r.URL)
	if err != nil || gotOrigin != p.allowedOrigin {
		p.blocked.Add(1)
		http.Error(w, "cross-origin preview request blocked", http.StatusForbidden)
		return
	}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	for _, h := range previewHopByHopHeaders {
		outReq.Header.Del(h)
	}
	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		if errors.Is(err, errPreviewTargetBlocked) {
			http.Error(w, "preview target address is not allowed", http.StatusForbidden)
			return
		}
		http.Error(w, "preview upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Block cross-origin redirects before Chrome can follow them.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc := strings.TrimSpace(resp.Header.Get("Location")); loc != "" {
			ref, parseErr := url.Parse(loc)
			if parseErr != nil {
				p.blocked.Add(1)
				http.Error(w, "cross-origin preview redirect blocked", http.StatusForbidden)
				return
			}
			dest := r.URL.ResolveReference(ref)
			destOrigin, oErr := canonicalPreviewOrigin(dest)
			if oErr != nil || destOrigin != p.allowedOrigin {
				p.blocked.Add(1)
				http.Error(w, "cross-origin preview redirect blocked", http.StatusForbidden)
				return
			}
		}
	}

	for _, h := range previewHopByHopHeaders {
		resp.Header.Del(h)
	}
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 256<<20))
}
