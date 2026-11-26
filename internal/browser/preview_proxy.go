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

const (
	previewProxyDialTimeout    = 5 * time.Second
	previewMaxSubresourceHosts = 256
)

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
	allowedOrigin        string
	allowedScheme        string
	allowedHost          string
	allowedPort          int
	lookupIP             func(ctx context.Context, host string) ([]netip.Addr, error)
	dialer               *net.Dialer
	ln                   net.Listener
	srv                  *http.Server
	transport            *http.Transport
	subresourceTransport *http.Transport
	blocked              atomic.Uint64
	navBlocked           atomic.Uint64
	subresourceMu        sync.Mutex
	subresourceHosts     map[string]time.Time
	closeOnce            sync.Once
	mu                   sync.Mutex
	closed               bool
	conns                map[net.Conn]struct{}
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
		ln:               ln,
		subresourceHosts: make(map[string]time.Time),
		conns:            make(map[net.Conn]struct{}),
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
	p.subresourceTransport = &http.Transport{
		Proxy:                 nil,
		DialContext:           p.dialPublicSubresourceTarget,
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

func (p *previewOriginProxy) NavBlockedCount() uint64 {
	if p == nil {
		return 0
	}
	return p.navBlocked.Load()
}

func (p *previewOriginProxy) AllowSubresourceHost(host string, port int, ttl time.Duration) {
	if p == nil || (port != 80 && port != 443) {
		return
	}
	norm := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if !isPublicSubresourceHostname(norm) {
		return
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	now := time.Now()
	key := net.JoinHostPort(norm, strconv.Itoa(port))
	p.subresourceMu.Lock()
	defer p.subresourceMu.Unlock()
	if len(p.subresourceHosts) >= previewMaxSubresourceHosts {
		for k, exp := range p.subresourceHosts {
			if now.After(exp) {
				delete(p.subresourceHosts, k)
			}
		}
		if len(p.subresourceHosts) >= previewMaxSubresourceHosts {
			// Evict oldest entry to keep map strictly bounded.
			var oldestKey string
			var oldestExp time.Time
			for k, exp := range p.subresourceHosts {
				if oldestKey == "" || exp.Before(oldestExp) {
					oldestKey, oldestExp = k, exp
				}
			}
			if oldestKey != "" {
				delete(p.subresourceHosts, oldestKey)
			}
		}
	}
	p.subresourceHosts[key] = now.Add(ttl)
}

func (p *previewOriginProxy) isAllowedSubresourceHost(host string, port int) bool {
	if p == nil || (port != 80 && port != 443) {
		return false
	}
	norm := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if !isPublicSubresourceHostname(norm) {
		return false
	}
	key := net.JoinHostPort(norm, strconv.Itoa(port))
	now := time.Now()
	p.subresourceMu.Lock()
	exp, ok := p.subresourceHosts[key]
	if ok && now.After(exp) {
		delete(p.subresourceHosts, key)
		ok = false
	}
	p.subresourceMu.Unlock()
	return ok
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
		if p.subresourceTransport != nil {
			p.subresourceTransport.CloseIdleConnections()
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

func (p *previewOriginProxy) dialPublicSubresourceTarget(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		p.blocked.Add(1)
		return nil, errPreviewTargetBlocked
	}
	normHost := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	port, err := strconv.Atoi(portStr)
	if err != nil || (port != 80 && port != 443) || !p.isAllowedSubresourceHost(normHost, port) {
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
	for _, ip := range ips {
		if !isPublicSubresourceIP(ip) {
			p.blocked.Add(1)
			return nil, errPreviewTargetBlocked
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

func isTopLevelNavHTTPRequest(r *http.Request, onAllowedOrigin bool) bool {
	mode := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode")))
	dest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")))
	if dest == "document" {
		return true
	}
	if dest != "" {
		return false
	}
	// Absent Sec-Fetch-Dest: treat as top-level nav only when Sec-Fetch-Mode
	// explicitly says navigate, or on a same-origin redirect (never Chrome's
	// background telemetry probes to foreign hosts).
	return mode == "navigate" || onAllowedOrigin
}

func isNavigationLikeHTTPRequest(r *http.Request) bool {
	mode := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode")))
	dest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")))
	if mode == "navigate" || dest == "" {
		return true
	}
	switch dest {
	case "document", "iframe", "frame", "fencedframe", "object", "embed":
		return true
	default:
		return false
	}
}

func isAllowedSubresourceHTTPDest(r *http.Request) bool {
	mode := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode")))
	dest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")))
	if mode == "navigate" {
		return false
	}
	switch dest {
	case "image", "font", "style", "audio", "video", "script", "manifest", "empty":
		return true
	default:
		return false
	}
}

func copyPreviewProxyResponseBody(w http.ResponseWriter, resp *http.Response) {
	limited := io.LimitReader(resp.Body, 256<<20)
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	flusher, canFlush := w.(http.Flusher)
	if !canFlush || !strings.Contains(ct, "text/event-stream") {
		_, _ = io.Copy(w, limited)
		return
	}
	buf := make([]byte, 4096)
	for {
		n, err := limited.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				return
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
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
	if err != nil {
		p.blocked.Add(1)
		http.Error(w, "invalid preview tunnel port", http.StatusForbidden)
		return
	}
	isSameOrigin := normHost == p.allowedHost && port == p.allowedPort
	isPublicSub := !isSameOrigin && port == 443 && p.isAllowedSubresourceHost(normHost, port)
	if !isSameOrigin && !isPublicSub {
		// Still block unapproved CONNECT tunnels (including Chrome background
		// services) at the transport layer without attributing them to a user
		// top-level navigation; page navigations are tracked via CDP Fetch.
		p.blocked.Add(1)
		http.Error(w, "cross-origin preview tunnel blocked", http.StatusForbidden)
		return
	}
	var upConn net.Conn
	if isSameOrigin {
		upConn, err = p.dialAllowedTarget(r.Context(), "tcp", net.JoinHostPort(p.allowedHost, strconv.Itoa(p.allowedPort)))
	} else {
		upConn, err = p.dialPublicSubresourceTarget(r.Context(), "tcp", net.JoinHostPort(normHost, strconv.Itoa(port)))
	}
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
	// On an http:// preview origin, same-origin CONNECT is only needed for plain ws://
	// upgrades; reject TLS ClientHello (record type 0x16) so https:// cannot
	// bypass scheme lock on a dual-protocol port.
	if isSameOrigin && p.allowedScheme == "http" {
		br := bufio.NewReader(cConn)
		_ = clientConn.SetReadDeadline(time.Now().Add(previewProxyDialTimeout))
		first, peekErr := br.Peek(1)
		_ = clientConn.SetReadDeadline(time.Time{})
		if peekErr != nil {
			return
		}
		if len(first) == 1 && first[0] == 0x16 {
			p.blocked.Add(1)
			p.navBlocked.Add(1)
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
		p.navBlocked.Add(1)
		http.Error(w, "absolute http(s) URL required", http.StatusBadRequest)
		return
	}
	gotOrigin, err := canonicalPreviewOrigin(r.URL)
	isSameOrigin := err == nil && gotOrigin == p.allowedOrigin
	transport := p.transport
	if !isSameOrigin {
		subHost, subPort, ok := validatePublicSubresourceURL(r.URL)
		if !ok || r.URL.Scheme != "http" || subPort != 80 || !isAllowedSubresourceHTTPDest(r) || !p.isAllowedSubresourceHost(subHost, subPort) {
			p.blocked.Add(1)
			if isTopLevelNavHTTPRequest(r, false) {
				p.navBlocked.Add(1)
			}
			http.Error(w, "cross-origin preview request blocked", http.StatusForbidden)
			return
		}
		transport = p.subresourceTransport
	}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	for _, h := range previewHopByHopHeaders {
		outReq.Header.Del(h)
	}
	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		if errors.Is(err, errPreviewTargetBlocked) {
			if isTopLevelNavHTTPRequest(r, isSameOrigin) {
				p.navBlocked.Add(1)
			}
			http.Error(w, "preview target address is not allowed", http.StatusForbidden)
			return
		}
		http.Error(w, "preview upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Block cross-origin redirects on navigations and any redirect to non-public targets.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc := strings.TrimSpace(resp.Header.Get("Location")); loc != "" {
			ref, parseErr := url.Parse(loc)
			if parseErr != nil {
				p.blocked.Add(1)
				if isTopLevelNavHTTPRequest(r, isSameOrigin) {
					p.navBlocked.Add(1)
				}
				http.Error(w, "cross-origin preview redirect blocked", http.StatusForbidden)
				return
			}
			dest := r.URL.ResolveReference(ref)
			destOrigin, oErr := canonicalPreviewOrigin(dest)
			if oErr != nil || destOrigin != p.allowedOrigin {
				allowSubRedir := false
				if !isNavigationLikeHTTPRequest(r) && isAllowedSubresourceHTTPDest(r) {
					if destHost, destPort, ok := validatePublicSubresourceURL(dest); ok {
						p.AllowSubresourceHost(destHost, destPort, 30*time.Second)
						allowSubRedir = true
					}
				}
				if !allowSubRedir {
					p.blocked.Add(1)
					if isTopLevelNavHTTPRequest(r, isSameOrigin) {
						p.navBlocked.Add(1)
					}
					http.Error(w, "cross-origin preview redirect blocked", http.StatusForbidden)
					return
				}
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
	copyPreviewProxyResponseBody(w, resp)
}
