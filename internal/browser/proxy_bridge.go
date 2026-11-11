package browser

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

const proxyBridgeDialTimeout = 5 * time.Second

var (
	browserLocaleRe = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8}){0,2}$`)
	browserTZRe     = regexp.MustCompile(`^(?:UTC|GMT|[A-Za-z]+(?:_[A-Za-z0-9+-]+)*(?:/[A-Za-z0-9_+-]+){1,2})$`)
)

// StoreEnvConfig describes per-store anti-association proxy and fingerprint
// settings resolved before launching or reusing a Chrome instance.
type StoreEnvConfig struct {
	StoreID       string
	ProxyURL      string
	RedactedProxy string
	ProxyRequired bool
	Timezone      string
	Locale        string
}

// SetEnvironmentResolver lets the host map a browser session scope to a bound
// store's isolated proxy, timezone, and locale settings.
func (s *Service) SetEnvironmentResolver(fn func(sessionID, directory string) (StoreEnvConfig, error)) {
	s.mu.Lock()
	s.resolveEnv = fn
	s.mu.Unlock()
}

func (s *Service) resolveScopeEnv(sc *scope) (StoreEnvConfig, error) {
	if s == nil {
		return StoreEnvConfig{}, nil
	}
	s.mu.Lock()
	fn := s.resolveEnv
	s.mu.Unlock()
	if fn == nil {
		return StoreEnvConfig{}, nil
	}
	return fn(sc.sessionID, sc.directory)
}

func (s *Service) applyStoreMetadata(sessionID, directory string, state *State) {
	if s == nil || state == nil {
		return
	}
	s.mu.Lock()
	fn := s.resolveEnv
	s.mu.Unlock()
	if fn == nil {
		return
	}
	env, err := fn(sessionID, directory)
	if err != nil && env.StoreID == "" {
		return
	}
	state.StoreID = env.StoreID
	if env.RedactedProxy != "" {
		state.StoreProxy = env.RedactedProxy
	} else if u, parseErr := parseAndValidateUpstreamProxy(env.ProxyURL); parseErr == nil {
		state.StoreProxy = redactUpstreamProxy(u)
	}
	state.StoreTimezone = env.Timezone
	state.StoreLocale = env.Locale
}

func (e StoreEnvConfig) fingerprint() string {
	return strings.Join([]string{
		strings.TrimSpace(e.StoreID),
		strings.TrimSpace(e.ProxyURL),
		strconv.FormatBool(e.ProxyRequired),
		strings.TrimSpace(e.Timezone),
		strings.TrimSpace(e.Locale),
	}, "|")
}

func (e StoreEnvConfig) validate() (*url.URL, error) {
	rawProxy := strings.TrimSpace(e.ProxyURL)
	if rawProxy == "" {
		if e.ProxyRequired {
			label := strings.TrimSpace(e.StoreID)
			if label == "" {
				label = "bound store"
			}
			return nil, fmt.Errorf("store %q requires an isolated proxy, but no proxyURL is configured", label)
		}
	}
	if tz := strings.TrimSpace(e.Timezone); tz != "" {
		if len(tz) > 64 || !browserTZRe.MatchString(tz) {
			return nil, fmt.Errorf("invalid store timezone %q", tz)
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("unknown store timezone %q", tz)
		}
	}
	if loc := strings.TrimSpace(e.Locale); loc != "" {
		if len(loc) > 24 || !browserLocaleRe.MatchString(loc) {
			return nil, fmt.Errorf("invalid store locale %q", loc)
		}
	}
	if rawProxy == "" {
		return nil, nil
	}
	return parseAndValidateUpstreamProxy(rawProxy)
}

func parseAndValidateUpstreamProxy(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "-") || strings.ContainsAny(trimmed, " \t\r\n\"'`\\;") {
		return nil, errors.New("proxy URL contains disallowed characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, errors.New("invalid proxy URL")
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	if u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("proxy URL must not contain path, query, or fragment")
	}
	host := strings.TrimSpace(u.Hostname())
	portStr := strings.TrimSpace(u.Port())
	if host == "" || strings.HasPrefix(host, "-") || portStr == "" {
		return nil, errors.New("proxy URL must specify host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("proxy URL port out of range")
	}
	if u.User != nil {
		_, hasPass := u.User.Password()
		if u.User.Username() == "***" && !hasPass {
			return nil, errors.New("proxy credentials are redacted")
		}
	}
	u.Scheme = scheme
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	u.Path = ""
	return u, nil
}

func redactUpstreamProxy(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.User != nil {
		return fmt.Sprintf("%s://***@%s", u.Scheme, u.Host)
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}

func (sc *scope) emulationActions() []chromedp.Action {
	var actions []chromedp.Action
	if tz := strings.TrimSpace(sc.env.Timezone); tz != "" {
		actions = append(actions, emulation.SetTimezoneOverride(tz))
	}
	if loc := strings.TrimSpace(sc.env.Locale); loc != "" {
		icu := strings.ReplaceAll(loc, "-", "_")
		actions = append(actions, emulation.SetLocaleOverride().WithLocale(icu))
	}
	return actions
}

// loopbackProxyBridge runs a local 127.0.0.1:0 HTTP/CONNECT proxy for a single
// Chrome scope so upstream HTTP/HTTPS/SOCKS5 credentials never appear on the
// Chrome command line and SOCKS5 username/password auth works transparently.
type loopbackProxyBridge struct {
	upstream             *url.URL
	redacted             string
	allowLoopbackTargets bool
	ln                   net.Listener
	srv                  *http.Server
	transport            *http.Transport
	closeOnce            sync.Once
	mu                   sync.Mutex
	closed               bool
	conns                map[net.Conn]struct{}
}

func startLoopbackProxyBridge(ctx context.Context, upstream *url.URL, allowLoopbackTargets bool) (*loopbackProxyBridge, error) {
	if upstream == nil {
		return nil, errors.New("upstream proxy is nil")
	}
	redacted := redactUpstreamProxy(upstream)
	if err := preflightUpstreamProxy(ctx, upstream, redacted); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind local proxy bridge: %w", err)
	}
	b := &loopbackProxyBridge{
		upstream:             upstream,
		redacted:             redacted,
		allowLoopbackTargets: allowLoopbackTargets,
		ln:                   ln,
		conns:                make(map[net.Conn]struct{}),
	}
	if upstream.Scheme == "http" || upstream.Scheme == "https" {
		b.transport = &http.Transport{
			Proxy:                 http.ProxyURL(upstream),
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          32,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   proxyBridgeDialTimeout,
			ResponseHeaderTimeout: 30 * time.Second,
		}
	} else {
		b.transport = &http.Transport{
			Proxy: nil,
			DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
				if err := validateProxyTarget(addr, allowLoopbackTargets); err != nil {
					return nil, err
				}
				return dialViaUpstream(dialCtx, upstream, redacted, addr)
			},
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          32,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   proxyBridgeDialTimeout,
			ResponseHeaderTimeout: 30 * time.Second,
		}
	}
	b.srv = &http.Server{
		Handler:           http.HandlerFunc(b.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = b.srv.Serve(ln)
	}()
	return b, nil
}

func (b *loopbackProxyBridge) Addr() string {
	if b == nil || b.ln == nil {
		return ""
	}
	return b.ln.Addr().String()
}

func (b *loopbackProxyBridge) trackConn(conn net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		_ = conn.Close()
		return false
	}
	b.conns[conn] = struct{}{}
	return true
}

func (b *loopbackProxyBridge) untrackConn(conn net.Conn) {
	b.mu.Lock()
	delete(b.conns, conn)
	b.mu.Unlock()
	_ = conn.Close()
}

func (b *loopbackProxyBridge) Close() {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		active := make([]net.Conn, 0, len(b.conns))
		for c := range b.conns {
			active = append(active, c)
		}
		b.conns = nil
		b.mu.Unlock()
		for _, c := range active {
			_ = c.Close()
		}
		if b.srv != nil {
			_ = b.srv.Close()
		}
		if b.ln != nil {
			_ = b.ln.Close()
		}
		if b.transport != nil {
			b.transport.CloseIdleConnections()
		}
	})
}

func (b *loopbackProxyBridge) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		b.handleConnect(w, r)
		return
	}
	b.handleForwardHTTP(w, r)
}

func (b *loopbackProxyBridge) handleConnect(w http.ResponseWriter, r *http.Request) {
	if err := validateProxyTarget(r.Host, b.allowLoopbackTargets); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	upConn, err := dialViaUpstream(r.Context(), b.upstream, b.redacted, r.Host)
	if err != nil {
		http.Error(w, "upstream proxy connect failed", http.StatusBadGateway)
		return
	}
	if !b.trackConn(upConn) {
		return
	}
	defer b.untrackConn(upConn)
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		return
	}
	if !b.trackConn(clientConn) {
		return
	}
	defer b.untrackConn(clientConn)
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	var cConn net.Conn = clientConn
	if clientBuf != nil && clientBuf.Reader.Buffered() > 0 {
		cConn = &bufferedConn{Conn: clientConn, r: clientBuf.Reader}
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

func (b *loopbackProxyBridge) handleForwardHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || (r.URL.Scheme != "http" && r.URL.Scheme != "https") || r.URL.Host == "" {
		http.Error(w, "absolute http(s) URL required", http.StatusBadRequest)
		return
	}
	targetAddr := r.URL.Host
	if _, _, splitErr := net.SplitHostPort(targetAddr); splitErr != nil {
		if r.URL.Scheme == "https" {
			targetAddr = net.JoinHostPort(r.URL.Hostname(), "443")
		} else {
			targetAddr = net.JoinHostPort(r.URL.Hostname(), "80")
		}
	}
	if err := validateProxyTarget(targetAddr, b.allowLoopbackTargets); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	for _, h := range []string{"Proxy-Authorization", "Proxy-Connection", "Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		outReq.Header.Del(h)
	}
	resp, err := b.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "upstream proxy request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 256<<20))
}

func isDeniedLocalIP(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate())
}

// validateProxyTarget rejects literal loopback/LAN/link-local IPs and local
// pseudo-TLDs without invoking the local OS DNS resolver (avoiding both local
// DNS leaks on SOCKS5/HTTP store proxies and false 403s under DNS poisoning).
func validateProxyTarget(hostPort string, allowLoopback bool) error {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(hostPort))
	if err != nil || host == "" {
		return errors.New("target host:port required")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("target port out of range")
	}
	if allowLoopback {
		return nil
	}
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") ||
		strings.HasSuffix(lower, ".home.arpa") {
		return errors.New("proxy target local hostname denied")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isDeniedLocalIP(ip) {
			return errors.New("proxy target local address denied")
		}
	}
	return nil
}

func preflightUpstreamProxy(ctx context.Context, upstream *url.URL, redacted string) error {
	probeCtx, cancel := context.WithTimeout(ctx, proxyBridgeDialTimeout)
	defer cancel()
	conn, err := dialUpstreamControlConn(probeCtx, upstream, redacted)
	if err != nil {
		return err
	}
	defer conn.Close()
	if upstream.Scheme == "socks5" || upstream.Scheme == "socks5h" {
		if deadline, ok := probeCtx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		if err := negotiateSOCKS5Auth(conn, upstream.User); err != nil {
			return fmt.Errorf("store proxy %s authentication failed: %v", redacted, err)
		}
	}
	return nil
}

func dialUpstreamControlConn(ctx context.Context, upstream *url.URL, redacted string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: proxyBridgeDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", upstream.Host)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to store proxy %s", redacted)
	}
	if upstream.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: upstream.Hostname(),
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("TLS handshake failed with store proxy %s", redacted)
		}
		return tlsConn, nil
	}
	return conn, nil
}

func dialViaUpstream(ctx context.Context, upstream *url.URL, redacted, targetAddr string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := dialUpstreamControlConn(dialCtx, upstream, redacted)
	if err != nil {
		return nil, err
	}
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	switch upstream.Scheme {
	case "http", "https":
		var hdr strings.Builder
		hdr.WriteString(fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr))
		if upstream.User != nil {
			pass, _ := upstream.User.Password()
			cred := base64.StdEncoding.EncodeToString([]byte(upstream.User.Username() + ":" + pass))
			hdr.WriteString("Proxy-Authorization: Basic " + cred + "\r\n")
		}
		hdr.WriteString("\r\n")
		if _, err := io.WriteString(conn, hdr.String()); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("upstream CONNECT write failed via %s", redacted)
		}
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("upstream CONNECT read failed via %s", redacted)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_ = conn.Close()
			return nil, fmt.Errorf("upstream proxy %s rejected CONNECT (%d)", redacted, resp.StatusCode)
		}
		_ = conn.SetDeadline(time.Time{})
		if br.Buffered() > 0 {
			return &bufferedConn{Conn: conn, r: br}, nil
		}
		return conn, nil
	case "socks5", "socks5h":
		if err := negotiateSOCKS5Auth(conn, upstream.User); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("SOCKS5 auth failed via %s: %v", redacted, err)
		}
		if err := connectSOCKS5Target(conn, targetAddr); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("SOCKS5 target connect failed via %s: %v", redacted, err)
		}
		_ = conn.SetDeadline(time.Time{})
		return conn, nil
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("unsupported proxy scheme %q", upstream.Scheme)
	}
}

func negotiateSOCKS5Auth(conn net.Conn, user *url.Userinfo) error {
	var username, password string
	hasAuth := false
	if user != nil {
		username = user.Username()
		password, _ = user.Password()
		hasAuth = username != "" || password != ""
	}
	if hasAuth {
		if len(username) > 255 || len(password) > 255 {
			return errors.New("credentials exceed 255 bytes")
		}
		if _, err := conn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
			return errors.New("greeting write failed")
		}
	} else {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			return errors.New("greeting write failed")
		}
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return errors.New("greeting read failed")
	}
	if reply[0] != 0x05 {
		return errors.New("invalid SOCKS5 version")
	}
	switch reply[1] {
	case 0x00:
		return nil
	case 0x02:
		if !hasAuth {
			return errors.New("proxy requires credentials")
		}
		pkt := make([]byte, 0, 3+len(username)+len(password))
		pkt = append(pkt, 0x01, byte(len(username)))
		pkt = append(pkt, username...)
		pkt = append(pkt, byte(len(password)))
		pkt = append(pkt, password...)
		if _, err := conn.Write(pkt); err != nil {
			return errors.New("auth write failed")
		}
		var authReply [2]byte
		if _, err := io.ReadFull(conn, authReply[:]); err != nil {
			return errors.New("auth read failed")
		}
		if authReply[1] != 0x00 {
			return errors.New("invalid proxy credentials")
		}
		return nil
	default:
		return errors.New("no acceptable auth method")
	}
}

func connectSOCKS5Target(conn net.Conn, targetAddr string) error {
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return errors.New("invalid target address")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid target port")
	}
	req := make([]byte, 0, 7+len(host))
	req = append(req, 0x05, 0x01, 0x00)
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, 0x01)
			req = append(req, ip4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("target domain too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port&0xff))
	if _, err := conn.Write(req); err != nil {
		return errors.New("target request write failed")
	}
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return errors.New("target reply read failed")
	}
	if hdr[0] != 0x05 || hdr[1] != 0x00 {
		return fmt.Errorf("SOCKS5 connect rejected (0x%02x)", hdr[1])
	}
	var skip int
	switch hdr[3] {
	case 0x01:
		skip = 4 + 2
	case 0x04:
		skip = 16 + 2
	case 0x03:
		var dlen [1]byte
		if _, err := io.ReadFull(conn, dlen[:]); err != nil {
			return errors.New("target bind domain length read failed")
		}
		skip = int(dlen[0]) + 2
	default:
		return errors.New("unsupported SOCKS5 bind address type")
	}
	discard := make([]byte, skip)
	if _, err := io.ReadFull(conn, discard); err != nil {
		return errors.New("target bind address read failed")
	}
	return nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.r != nil && c.r.Buffered() > 0 {
		return c.r.Read(p)
	}
	return c.Conn.Read(p)
}
