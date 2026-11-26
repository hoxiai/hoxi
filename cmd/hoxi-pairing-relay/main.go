// Command hoxi-pairing-relay runs the standalone zero-knowledge 6-digit
// one-time pairing ticket exchange service for self-hosted deployments.
//
// It directly reuses internal/pairing.ShortCodeExchange.Handler() with
// hardened HTTP server timeouts, optional reverse-proxy client IP extraction,
// and graceful shutdown. Request bodies (codes, cancel tokens, tickets) are
// never logged.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hoxiai/hoxi/internal/pairing"
)

const (
	defaultListenAddr       = ":8787"
	defaultTrustedProxies   = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"
	defaultShutdownTimeout  = 5 * time.Second
	defaultReadHeaderPeriod = 5 * time.Second
	defaultReadTimeout      = 10 * time.Second
	defaultWriteTimeout     = 10 * time.Second
	defaultIdleTimeout      = 30 * time.Second
	defaultMaxHeaderBytes   = 8 << 10
)

type relayConfig struct {
	Addr           string
	TrustProxy     bool
	TrustedProxies []netip.Prefix
	TrustCF        bool
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parseBoolEnv(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseTrustedProxyPrefixes(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		part := strings.TrimSpace(item)
		if part == "" {
			continue
		}
		if strings.Contains(part, "/") {
			p, err := netip.ParsePrefix(part)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", part, err)
			}
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(strings.Trim(part, "[]"))
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy IP %q: %w", part, err)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

func defaultTrustedProxyPrefixes() []netip.Prefix {
	prefixes, _ := parseTrustedProxyPrefixes(defaultTrustedProxies)
	return prefixes
}

func isLocalOrWildcardListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	clean := ip.Unmap()
	return clean.IsLoopback() || clean.IsUnspecified()
}

func hasForwardedHeadersFromTrustedProxy(r *http.Request, trusted []netip.Prefix) bool {
	if r == nil {
		return false
	}
	if r.Header.Get("X-Forwarded-For") == "" &&
		r.Header.Get("X-Real-IP") == "" &&
		r.Header.Get("CF-Connecting-IP") == "" {
		return false
	}
	peerAddr, ok := parseSingleAddr(r.RemoteAddr)
	return ok && addrInPrefixes(peerAddr, trusted)
}

func loadConfig(args []string) (relayConfig, error) {
	fs := flag.NewFlagSet("hoxi-pairing-relay", flag.ContinueOnError)
	trustedProxiesRaw := envOrDefault("HOXI_PAIRING_RELAY_TRUSTED_PROXIES", defaultTrustedProxies)
	cfg := relayConfig{
		Addr:       envOrDefault("HOXI_PAIRING_RELAY_ADDR", defaultListenAddr),
		TrustProxy: parseBoolEnv(os.Getenv("HOXI_PAIRING_RELAY_TRUST_PROXY")),
		TrustCF:    parseBoolEnv(os.Getenv("HOXI_PAIRING_RELAY_TRUST_CF")),
	}
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "TCP listen address (env: HOXI_PAIRING_RELAY_ADDR)")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", cfg.TrustProxy, "trust reverse-proxy X-Forwarded-For (rightmost untrusted hop) from trusted proxy peers (env: HOXI_PAIRING_RELAY_TRUST_PROXY)")
	fs.StringVar(&trustedProxiesRaw, "trusted-proxies", trustedProxiesRaw, "comma-separated proxy IPs/CIDRs permitted to supply forwarded client IP headers (env: HOXI_PAIRING_RELAY_TRUSTED_PROXIES)")
	fs.BoolVar(&cfg.TrustCF, "trust-cf", cfg.TrustCF, "when -trust-proxy is enabled, trust CF-Connecting-IP from Cloudflare/trusted edge proxies (env: HOXI_PAIRING_RELAY_TRUST_CF)")
	if err := fs.Parse(args); err != nil {
		return relayConfig{}, err
	}
	cfg.Addr = strings.TrimSpace(cfg.Addr)
	if cfg.Addr == "" {
		cfg.Addr = defaultListenAddr
	}
	prefixes, err := parseTrustedProxyPrefixes(trustedProxiesRaw)
	if err != nil {
		return relayConfig{}, err
	}
	cfg.TrustedProxies = prefixes
	return cfg, nil
}

// buildHandler wraps exchange.Handler() with defensive headers and optional
// reverse-proxy client IP normalization for per-IP rate limiting.
func buildHandler(exchange *pairing.ShortCodeExchange, trustProxy bool) http.Handler {
	return buildHandlerWithConfig(exchange, relayConfig{
		TrustProxy:     trustProxy,
		TrustedProxies: defaultTrustedProxyPrefixes(),
	})
}

func buildHandlerWithConfig(exchange *pairing.ShortCodeExchange, cfg relayConfig) http.Handler {
	base := exchange.Handler()
	trusted := cfg.TrustedProxies
	if len(trusted) == 0 {
		trusted = defaultTrustedProxyPrefixes()
	}
	var warnUntrustedProxyOnce sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")

		req := r
		if cfg.TrustProxy {
			if forwardedIP := extractTrustedProxyIP(r, trusted, cfg.TrustCF); forwardedIP != "" {
				cloned := r.Clone(r.Context())
				cloned.RemoteAddr = net.JoinHostPort(forwardedIP, "0")
				req = cloned
			}
		} else if hasForwardedHeadersFromTrustedProxy(r, trusted) {
			warnUntrustedProxyOnce.Do(func() {
				log.Printf("hoxi-pairing-relay: WARNING received forwarded headers from trusted proxy peer %s while -trust-proxy=false; all proxied clients share one rate-limit bucket (pass -trust-proxy)", r.RemoteAddr)
			})
		}
		base.ServeHTTP(w, req)
	})
}

func addrInPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	clean := addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(clean) {
			return true
		}
	}
	return false
}

// extractTrustedProxyIP resolves the real client IP ONLY when the direct TCP
// peer belongs to trustedProxies. It ignores CF-Connecting-IP unless trustCF is
// explicitly enabled, and walks X-Forwarded-For from right to left so spoofed
// left-hand entries injected by attackers are never trusted.
func extractTrustedProxyIP(r *http.Request, trustedProxies []netip.Prefix, trustCF bool) string {
	if r == nil {
		return ""
	}
	peerAddr, ok := parseSingleAddr(r.RemoteAddr)
	if !ok || !addrInPrefixes(peerAddr, trustedProxies) {
		return ""
	}
	if trustCF {
		if ip, ok := parseSingleAddr(r.Header.Get("CF-Connecting-IP")); ok {
			return ip.String()
		}
	}
	if xffVals := r.Header.Values("X-Forwarded-For"); len(xffVals) > 0 {
		parts := strings.Split(strings.Join(xffVals, ","), ",")
		var fallbackTrusted netip.Addr
		for i := len(parts) - 1; i >= 0; i-- {
			addr, valid := parseSingleAddr(parts[i])
			if !valid {
				// Stop walking at malformed entries so attacker junk cannot bypass.
				break
			}
			if !addrInPrefixes(addr, trustedProxies) {
				return addr.String()
			}
			fallbackTrusted = addr
		}
		if fallbackTrusted.IsValid() {
			return fallbackTrusted.String()
		}
		return ""
	}
	if ip, ok := parseSingleAddr(r.Header.Get("X-Real-IP")); ok {
		return ip.String()
	}
	return ""
}

func parseSingleAddr(raw string) (netip.Addr, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	trimmed = strings.Trim(trimmed, "[]")
	parsed, err := netip.ParseAddr(trimmed)
	if err != nil {
		return netip.Addr{}, false
	}
	return parsed.Unmap(), true
}

func newRelayServer(cfg relayConfig, exchange *pairing.ShortCodeExchange) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           buildHandlerWithConfig(exchange, cfg),
		ReadHeaderTimeout: defaultReadHeaderPeriod,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
}

func main() {
	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		log.Fatalf("hoxi-pairing-relay: invalid flags: %v", err)
	}
	if !cfg.TrustProxy && isLocalOrWildcardListenAddr(cfg.Addr) {
		log.Printf("hoxi-pairing-relay: NOTE listening on %s with -trust-proxy=false; if placed behind Caddy/Nginx, pass -trust-proxy so clients do not share one loopback rate-limit bucket", cfg.Addr)
	}

	exchange := pairing.NewShortCodeExchange(nil)
	srv := newRelayServer(cfg, exchange)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("hoxi-pairing-relay: listening on %s (trust_proxy=%t, trust_cf=%t)", cfg.Addr, cfg.TrustProxy, cfg.TrustCF)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("hoxi-pairing-relay: server error: %v", err)
		}
	case <-ctx.Done():
		log.Printf("hoxi-pairing-relay: shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Fatalf("hoxi-pairing-relay: shutdown error: %v", err)
		}
	}
}
