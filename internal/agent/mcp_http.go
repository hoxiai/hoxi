package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	safeMCPDialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	safeMCPDirectTransport = &http.Transport{
		Proxy:               nil,
		DialContext:         safeMCPDialContext,
		MaxIdleConns:        32,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	safeMCPProxiedTransport = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         safeMCPDialer.DialContext,
		MaxIdleConns:        32,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	safeMCPHTTPTransport http.RoundTripper = &mcpSafeRoundTripper{
		direct:  safeMCPDirectTransport,
		proxied: safeMCPProxiedTransport,
	}

	safeMCPLoopbackTransport = &http.Transport{
		Proxy:               nil,
		DialContext:         safeMCPDialContext,
		MaxIdleConns:        16,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	// lookupMCPHostIPs resolves remote MCP hostnames for SSRF validation; unit tests may override it.
	lookupMCPHostIPs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
)

type mcpSafeRoundTripper struct {
	direct  *http.Transport
	proxied *http.Transport
}

func (rt *mcpSafeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, err := validateRemoteMCPURL(req.URL.String()); err != nil {
		return nil, err
	}
	if proxyURL, _ := http.ProxyFromEnvironment(req); proxyURL != nil {
		// When routing via an HTTP proxy, DialContext dials the proxy rather
		// than the target host, so validate resolved target IPs explicitly
		// before forwarding to prevent cloud-metadata SSRF through proxies.
		if _, err := resolveSafeMCPTargetIP(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
		return rt.proxied.RoundTrip(req)
	}
	return rt.direct.RoundTrip(req)
}

func isBlockedMCPTargetIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// Block cloud metadata (169.254.0.0/16) and unspecified network (0.0.0.0/8).
		if (v4[0] == 169 && v4[1] == 254) || v4[0] == 0 {
			return true
		}
	}
	return false
}

func validateRemoteMCPURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("MCP server url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid MCP server url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported MCP server url scheme %q: only http and https are allowed", u.Scheme)
	}
	host := strings.TrimSpace(u.Hostname())
	lowerHost := strings.TrimRight(strings.ToLower(host), ".")
	if lowerHost == "" {
		return nil, fmt.Errorf("MCP server url host is required")
	}
	if lowerHost == "metadata.google.internal" || lowerHost == "metadata.goog" {
		return nil, fmt.Errorf("blocked MCP metadata host %q", host)
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedMCPTargetIP(ip) {
		return nil, fmt.Errorf("blocked MCP target IP %s", ip.String())
	}
	return u, nil
}

func resolveSafeMCPTargetIP(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedMCPTargetIP(ip) {
			return nil, fmt.Errorf("blocked MCP connection to IP %s for host %s", ip.String(), host)
		}
		return ip, nil
	}
	ips, err := lookupMCPHostIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses resolved for MCP host %s", host)
	}
	for _, ipAddr := range ips {
		if isBlockedMCPTargetIP(ipAddr.IP) {
			return nil, fmt.Errorf("blocked MCP connection to IP %s for host %s", ipAddr.IP.String(), host)
		}
	}
	return ips[0].IP, nil
}

func safeMCPDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	safeIP, err := resolveSafeMCPTargetIP(ctx, host)
	if err != nil {
		return nil, err
	}
	return safeMCPDialer.DialContext(ctx, network, net.JoinHostPort(safeIP.String(), port))
}

func isMCPLoopbackHost(host string) bool {
	lower := strings.TrimRight(strings.ToLower(strings.TrimSpace(host)), ".")
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	if ip := net.ParseIP(lower); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

func newSafeMCPHTTPClient(timeout time.Duration, targetURL *url.URL, customHeaders map[string]string) *http.Client {
	rt := mcpHTTPTransport
	if rt == nil {
		if targetURL != nil && isMCPLoopbackHost(targetURL.Hostname()) {
			rt = safeMCPLoopbackTransport
		} else {
			rt = safeMCPHTTPTransport
		}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			nextURL, err := validateRemoteMCPURL(req.URL.String())
			if err != nil {
				return err
			}
			if len(via) > 0 && (!strings.EqualFold(via[0].URL.Scheme, nextURL.Scheme) || !strings.EqualFold(via[0].URL.Host, nextURL.Host)) {
				req.Header.Del("Authorization")
				req.Header.Del("Proxy-Authorization")
				req.Header.Del("Cookie")
				req.Header.Del("X-Api-Key")
				for k := range customHeaders {
					req.Header.Del(k)
				}
			}
			return nil
		},
	}
}
