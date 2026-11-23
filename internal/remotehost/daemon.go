// Package remotehost implements Hoxi's cross-platform headless development host
// (`hoxi host` on Linux, WSL, or remote macOS) and Hoxi Desktop's authenticated
// remote host registry, health/RTT probe, and streaming reverse-proxy mount.
package remotehost

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/buildinfo"
)

const (
	DefaultHostListenAddr = "127.0.0.1:5430"
	PairURIScheme         = "hoxi-host"
)

// DaemonConfig persists the local `hoxi host` daemon identity and bearer credential.
type DaemonConfig struct {
	HostID     string `json:"hostId"`
	Name       string `json:"name"`
	ListenAddr string `json:"listenAddr"`
	RootDir    string `json:"rootDir"`
	Token      string `json:"token"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// HostInfo is returned by GET /api/hoxi/host-info to authenticate and identify a headless host.
type HostInfo struct {
	HostID   string `json:"hostId,omitempty"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	RootDir  string `json:"rootDir"`
	Engine   string `json:"engine"`
}

func randomHexToken(prefix string, numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}

func writeAtomic0600(targetPath string, raw []byte) error {
	if strings.TrimSpace(targetPath) == "" {
		return errors.New("storage path is empty")
	}
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-host-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// LoadDaemonConfig reads the persisted DaemonConfig from disk.
func LoadDaemonConfig(configPath string) (DaemonConfig, error) {
	raw, err := os.ReadFile(strings.TrimSpace(configPath))
	if err != nil {
		return DaemonConfig{}, err
	}
	var cfg DaemonConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DaemonConfig{}, err
	}
	return cfg, nil
}

// EnsureDaemonConfig loads or initializes the persistent `hoxi host` configuration.
func EnsureDaemonConfig(configPath, name, listenAddr, rootDir string, rotateToken bool) (DaemonConfig, error) {
	cfg, err := LoadDaemonConfig(configPath)
	if err != nil && !os.IsNotExist(err) {
		return DaemonConfig{}, err
	}
	if strings.TrimSpace(cfg.HostID) == "" {
		id, gErr := randomHexToken("host_", 8)
		if gErr != nil {
			return DaemonConfig{}, gErr
		}
		cfg.HostID = id
	}
	if strings.TrimSpace(cfg.Token) == "" || rotateToken {
		tok, gErr := randomHexToken("hxh_", 32)
		if gErr != nil {
			return DaemonConfig{}, gErr
		}
		cfg.Token = tok
	}
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		cfg.Name = trimmed
	} else if strings.TrimSpace(cfg.Name) == "" {
		if hn, hErr := os.Hostname(); hErr == nil && strings.TrimSpace(hn) != "" {
			cfg.Name = strings.TrimSpace(hn)
		} else {
			cfg.Name = "hoxi-host"
		}
	}
	if trimmed := strings.TrimSpace(listenAddr); trimmed != "" {
		cfg.ListenAddr = trimmed
	} else if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = DefaultHostListenAddr
	}
	if trimmed := strings.TrimSpace(rootDir); trimmed != "" {
		if abs, aErr := filepath.Abs(trimmed); aErr == nil {
			cfg.RootDir = abs
		} else {
			cfg.RootDir = trimmed
		}
	}
	cfg.UpdatedAt = time.Now().UnixMilli()
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return DaemonConfig{}, err
	}
	if err := writeAtomic0600(configPath, append(encoded, '\n')); err != nil {
		return DaemonConfig{}, err
	}
	return cfg, nil
}

// FormatPairURI builds a `hoxi-host://pair?url=...&token=...&name=...` pairing string.
func FormatPairURI(endpointURL, name, token string) string {
	q := url.Values{}
	q.Set("url", strings.TrimRight(strings.TrimSpace(endpointURL), "/"))
	q.Set("token", strings.TrimSpace(token))
	if strings.TrimSpace(name) != "" {
		q.Set("name", strings.TrimSpace(name))
	}
	return PairURIScheme + "://pair?" + q.Encode()
}

// ParsePairURI parses a `hoxi-host://pair?...` string into its endpoint URL, name, and token.
func ParsePairURI(raw string) (endpointURL, name, token string, err error) {
	u, pErr := url.Parse(strings.TrimSpace(raw))
	if pErr != nil {
		return "", "", "", pErr
	}
	if !strings.EqualFold(u.Scheme, PairURIScheme) {
		return "", "", "", fmt.Errorf("expected %s:// URI scheme", PairURIScheme)
	}
	q := u.Query()
	endpointURL = strings.TrimRight(strings.TrimSpace(q.Get("url")), "/")
	token = strings.TrimSpace(q.Get("token"))
	name = strings.TrimSpace(q.Get("name"))
	if endpointURL == "" || token == "" {
		return "", "", "", errors.New("pair URI must contain both url and token parameters")
	}
	parsedEndpoint, eErr := url.Parse(endpointURL)
	if eErr != nil || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") || parsedEndpoint.Host == "" {
		return "", "", "", errors.New("pair URI url parameter must be a valid http:// or https:// endpoint")
	}
	return endpointURL, name, token, nil
}

// BuildHostInfo constructs a HostInfo snapshot for the current machine.
func BuildHostInfo(hostID, name, rootDir string) HostInfo {
	hn, _ := os.Hostname()
	hn = strings.TrimSpace(hn)
	lbl := strings.TrimSpace(name)
	if lbl == "" {
		lbl = hn
	}
	if lbl == "" {
		lbl = "hoxi-host"
	}
	return HostInfo{
		HostID:   strings.TrimSpace(hostID),
		Name:     lbl,
		Hostname: hn,
		Version:  buildinfo.Version,
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
		RootDir:  strings.TrimSpace(rootDir),
		Engine:   "native",
	}
}

func extractRequestHostToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if hdr := strings.TrimSpace(r.Header.Get("X-Hoxi-Host-Token")); hdr != "" {
		return hdr
	}
	return ""
}

// WrapBearerGuard enforces constant-time bearer token verification on every
// incoming request to a `hoxi host` daemon and serves `/api/hoxi/host-info`.
func WrapBearerGuard(expectedToken string, info HostInfo, next http.Handler) http.Handler {
	return WrapBearerGuardWithGatewayToken(expectedToken, "", info, next)
}

// WrapBearerGuardWithGatewayToken enforces constant-time bearer verification for
// remote `hoxi host` callers, strips external bearer headers so downstream logs
// or handlers never observe the host pairing credential, and injects the inner
// loopback gateway's defense-in-depth `X-Hoxi-Token` when configured.
func WrapBearerGuardWithGatewayToken(expectedToken, internalGatewayToken string, info HostInfo, next http.Handler) http.Handler {
	expectedBytes := []byte(strings.TrimSpace(expectedToken))
	innerGatewayTok := strings.TrimSpace(internalGatewayToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(extractRequestHostToken(r))
		if len(expectedBytes) == 0 || subtle.ConstantTimeCompare(got, expectedBytes) != 1 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized host token"})
			return
		}
		if r.URL.Path == "/api/hoxi/host-info" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(info)
			return
		}
		// Mark loopbacks/trusted host headers and translate outer host auth into inner gateway token.
		r2 := r.Clone(r.Context())
		r2.Host = "127.0.0.1"
		r2.Header.Del("Authorization")
		r2.Header.Del("X-Hoxi-Host-Token")
		r2.Header.Del("X-Hoxi-Token")
		if innerGatewayTok != "" {
			r2.Header.Set("X-Hoxi-Token", innerGatewayTok)
		}
		next.ServeHTTP(w, r2)
	})
}

// IsPrivateNetworkClient reports whether remoteAddr belongs to loopback,
// RFC1918/ULA private ranges, link-local unicast, or CGNAT (100.64.0.0/10,
// e.g. Tailscale). Public internet source addresses return false.
func IsPrivateNetworkClient(remoteAddr string) bool {
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

// WrapPrivateNetworkGuard rejects public internet source addresses before token
// verification so an accidental wildcard bind on a cloud host never exposes the
// plaintext host daemon over the public internet.
func WrapPrivateNetworkGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsPrivateNetworkClient(r.RemoteAddr) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "host daemon only accepts loopback or private-network clients (pass --allow-public when placed behind a TLS reverse proxy)",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
