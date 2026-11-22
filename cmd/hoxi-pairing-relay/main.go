// Command hoxi-pairing-relay runs the standalone zero-knowledge 6-digit
// one-time pairing ticket exchange service for link.hoxi.ai.
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
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hoxiai/hoxi/internal/pairing"
)

const (
	defaultListenAddr       = ":8787"
	defaultShutdownTimeout  = 5 * time.Second
	defaultReadHeaderPeriod = 5 * time.Second
	defaultReadTimeout      = 10 * time.Second
	defaultWriteTimeout     = 10 * time.Second
	defaultIdleTimeout      = 30 * time.Second
	defaultMaxHeaderBytes   = 8 << 10
)

type relayConfig struct {
	Addr       string
	TrustProxy bool
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

func loadConfig(args []string) (relayConfig, error) {
	fs := flag.NewFlagSet("hoxi-pairing-relay", flag.ContinueOnError)
	cfg := relayConfig{
		Addr:       envOrDefault("HOXI_PAIRING_RELAY_ADDR", defaultListenAddr),
		TrustProxy: parseBoolEnv(os.Getenv("HOXI_PAIRING_RELAY_TRUST_PROXY")),
	}
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "TCP listen address (env: HOXI_PAIRING_RELAY_ADDR)")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", cfg.TrustProxy, "trust CF-Connecting-IP / X-Forwarded-For / X-Real-IP headers for per-IP rate limiting (env: HOXI_PAIRING_RELAY_TRUST_PROXY)")
	if err := fs.Parse(args); err != nil {
		return relayConfig{}, err
	}
	cfg.Addr = strings.TrimSpace(cfg.Addr)
	if cfg.Addr == "" {
		cfg.Addr = defaultListenAddr
	}
	return cfg, nil
}

// buildHandler wraps exchange.Handler() with defensive headers and optional
// reverse-proxy client IP normalization for per-IP rate limiting.
func buildHandler(exchange *pairing.ShortCodeExchange, trustProxy bool) http.Handler {
	base := exchange.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")

		req := r
		if trustProxy {
			if forwardedIP := extractTrustedProxyIP(r); forwardedIP != "" {
				cloned := r.Clone(r.Context())
				cloned.RemoteAddr = net.JoinHostPort(forwardedIP, "0")
				req = cloned
			}
		}
		base.ServeHTTP(w, req)
	})
}

func extractTrustedProxyIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := parseSingleIP(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, part := range strings.Split(xff, ",") {
			if ip := parseSingleIP(part); ip != "" {
				return ip
			}
		}
	}
	if ip := parseSingleIP(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	return ""
}

func parseSingleIP(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	trimmed = strings.Trim(trimmed, "[]")
	parsed := net.ParseIP(trimmed)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}

func newRelayServer(cfg relayConfig, exchange *pairing.ShortCodeExchange) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           buildHandler(exchange, cfg.TrustProxy),
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

	exchange := pairing.NewShortCodeExchange(nil)
	srv := newRelayServer(cfg, exchange)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("hoxi-pairing-relay: listening on %s (trust_proxy=%t)", cfg.Addr, cfg.TrustProxy)
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
