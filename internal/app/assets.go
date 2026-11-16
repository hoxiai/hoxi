package app

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

var bypassLoopbackProxyOnce sync.Once

func ensureLoopbackBypassesDefaultProxy() {
	bypassLoopbackProxyOnce.Do(func() {
		tr, ok := http.DefaultTransport.(*http.Transport)
		if !ok || tr == nil {
			return
		}
		origProxy := tr.Proxy
		if origProxy == nil {
			return
		}
		tr.Proxy = func(req *http.Request) (*url.URL, error) {
			if req != nil && req.URL != nil && isLoopbackHost(req.URL.Hostname()) {
				return nil, nil
			}
			return origProxy(req)
		}
	})
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// sanitizeFrontendDevServerEnv checks whether a Wails `frontenddevserverurl`
// environment variable points to a live dev server. When `wails dev`'s Vite
// process has exited, leaving `frontenddevserverurl` set causes Wails to block
// startup for 60 seconds and then proxy every WebView request to the dead port
// (returning 502 Bad Gateway / a blank white window). Clearing the unreachable
// variable lets Wails fall back immediately to `assetdir` / embedded `distFS`.
func sanitizeFrontendDevServerEnv(logger *slog.Logger, timeout time.Duration) {
	ensureLoopbackBypassesDefaultProxy()

	if assetDir := strings.TrimSpace(os.Getenv("assetdir")); assetDir != "" {
		if info, err := os.Stat(filepath.Join(assetDir, "index.html")); err != nil || info.IsDir() {
			if logger != nil {
				logger.Warn("assetdir missing index.html; falling back to embedded assets", "assetdir", assetDir)
			}
			_ = os.Unsetenv("assetdir")
		}
	}

	raw := strings.TrimSpace(os.Getenv("frontenddevserverurl"))
	if raw == "" {
		return
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || !probeFrontendDevServer(parsed.Host, timeout) {
		if logger != nil {
			logger.Warn("frontend dev server unreachable; falling back to local assets", "url", raw)
		}
		_ = os.Unsetenv("frontenddevserverurl")
	}
}

func probeFrontendDevServer(hostPort string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		step := 250 * time.Millisecond
		if remaining < step {
			step = remaining
		}
		conn, err := net.DialTimeout("tcp", hostPort, step)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().Add(50 * time.Millisecond).After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// assetServerMiddleware routes `/api/*` directly to the in-process gateway and
// falls back to the gateway's embedded/disk assets if the external frontend dev
// server proxy returns 502 Bad Gateway mid-session.
func assetServerMiddleware(gw http.Handler) assetserver.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				gw.ServeHTTP(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			origHost := r.Host
			origURL := *r.URL
			origHeader := w.Header().Clone()
			fbw := &badGatewayFallbackWriter{
				ResponseWriter: w,
				header:         origHeader.Clone(),
			}
			next.ServeHTTP(fbw, r)
			if fbw.badGateway {
				r.Host = origHost
				*r.URL = origURL
				replaceHeaders(w.Header(), origHeader)
				gw.ServeHTTP(w, r)
				return
			}
			if !fbw.wroteHeader {
				fbw.copyHeaders()
			}
		})
	}
}

func replaceHeaders(dst, src http.Header) {
	for k := range dst {
		delete(dst, k)
	}
	for k, vals := range src {
		dst[k] = append([]string(nil), vals...)
	}
}

type badGatewayFallbackWriter struct {
	http.ResponseWriter
	header      http.Header
	wroteHeader bool
	badGateway  bool
}

func (w *badGatewayFallbackWriter) Header() http.Header {
	return w.header
}

func (w *badGatewayFallbackWriter) copyHeaders() {
	replaceHeaders(w.ResponseWriter.Header(), w.header)
}

func (w *badGatewayFallbackWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if code == http.StatusBadGateway {
		w.badGateway = true
		return
	}
	w.copyHeaders()
	w.ResponseWriter.WriteHeader(code)
}

func (w *badGatewayFallbackWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.badGateway {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
