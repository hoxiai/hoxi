package agentcli

import (
	"context"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

const (
	systemProxyCacheTTL    = 15 * time.Second
	systemProxyCmdTimeout  = 1 * time.Second
	loopbackProxyDialLimit = 80 * time.Millisecond
)

var proxyCheckEnvKeys = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"ALL_PROXY",
	"http_proxy",
	"https_proxy",
	"all_proxy",
}

var loopbackNoProxyDefaults = []string{"127.0.0.1", "localhost", "::1"}

type systemProxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	SOCKSProxy string
	NoProxy    []string
}

var (
	sysProxyMu    sync.Mutex
	sysProxyAt    time.Time
	sysProxyCache systemProxyConfig
)

// WithSystemProxy populates HTTP(S)_PROXY / ALL_PROXY from the OS system proxy
// (macOS scutil --proxy or Windows Internet Settings) when env does not already
// define any proxy variable. Go-based CLIs such as `agy` only inspect
// environment variables via http.ProxyFromEnvironment and otherwise bypass the
// system proxy when Hoxi.app is launched from Finder/Dock/Start Menu.
//
// Whenever a proxy is present (inherited or detected), NO_PROXY and no_proxy
// always include loopback addresses so Hoxi's local approval hooks stay direct.
func WithSystemProxy(env []string) []string {
	return withResolvedSystemProxy(env, cachedSystemProxy, isProxyEndpointReachable)
}

func withResolvedSystemProxy(
	env []string,
	lookup func() systemProxyConfig,
	reachable func(string) bool,
) []string {
	if hasExplicitProxyEnv(env) {
		return ensureNoProxyEntries(env, loopbackNoProxyDefaults)
	}
	if lookup == nil {
		return env
	}
	cfg := lookup()
	if reachable != nil {
		if cfg.HTTPProxy != "" && !reachable(cfg.HTTPProxy) {
			cfg.HTTPProxy = ""
		}
		if cfg.HTTPSProxy != "" && !reachable(cfg.HTTPSProxy) {
			cfg.HTTPSProxy = ""
		}
		if cfg.SOCKSProxy != "" && !reachable(cfg.SOCKSProxy) {
			cfg.SOCKSProxy = ""
		}
	}

	httpURL := cfg.HTTPProxy
	if httpURL == "" {
		httpURL = cfg.SOCKSProxy
	}
	httpsURL := cfg.HTTPSProxy
	if httpsURL == "" {
		httpsURL = cfg.SOCKSProxy
	}
	allURL := cfg.SOCKSProxy

	if httpURL == "" && httpsURL == "" && allURL == "" {
		return env
	}

	out := append([]string(nil), env...)
	if httpURL != "" {
		out = setEnvExact(out, "HTTP_PROXY", httpURL)
		out = setEnvExact(out, "http_proxy", httpURL)
	}
	if httpsURL != "" {
		out = setEnvExact(out, "HTTPS_PROXY", httpsURL)
		out = setEnvExact(out, "https_proxy", httpsURL)
	}
	if allURL != "" {
		out = setEnvExact(out, "ALL_PROXY", allURL)
		out = setEnvExact(out, "all_proxy", allURL)
	}
	noProxy := append([]string(nil), cfg.NoProxy...)
	noProxy = append(noProxy, loopbackNoProxyDefaults...)
	return ensureNoProxyEntries(out, noProxy)
}

func hasExplicitProxyEnv(env []string) bool {
	for _, kv := range env {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(val) == "" {
			continue
		}
		for _, k := range proxyCheckEnvKeys {
			if name == k {
				return true
			}
		}
	}
	return false
}

func setEnvExact(env []string, key, value string) []string {
	for i, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && name == key {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}

func ensureNoProxyEntries(env []string, additions []string) []string {
	out := append([]string(nil), env...)
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		idx := -1
		var current string
		for i, kv := range out {
			name, val, ok := strings.Cut(kv, "=")
			if ok && name == key {
				idx = i
				current = val
				break
			}
		}
		merged := mergeCSVTokens(current, additions)
		if merged == "" {
			continue
		}
		if idx >= 0 {
			out[idx] = key + "=" + merged
		} else {
			out = append(out, key+"="+merged)
		}
	}
	return out
}

func mergeCSVTokens(existing string, extra []string) string {
	seen := make(map[string]bool)
	var parts []string
	add := func(raw string) {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			return
		}
		lower := strings.ToLower(tok)
		if seen[lower] {
			return
		}
		seen[lower] = true
		parts = append(parts, tok)
	}
	for _, item := range strings.Split(existing, ",") {
		add(item)
	}
	for _, item := range extra {
		add(item)
	}
	return strings.Join(parts, ",")
}

func cachedSystemProxy() systemProxyConfig {
	sysProxyMu.Lock()
	defer sysProxyMu.Unlock()
	if !sysProxyAt.IsZero() && time.Since(sysProxyAt) < systemProxyCacheTTL {
		return sysProxyCache
	}
	cfg := queryOSSystemProxy()
	sysProxyCache = cfg
	sysProxyAt = time.Now()
	return cfg
}

func queryOSSystemProxy() systemProxyConfig {
	ctx, cancel := context.WithTimeout(context.Background(), systemProxyCmdTimeout)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		cmd := subproc.CommandContext(ctx, "/usr/sbin/scutil", "--proxy")
		cmd.WaitDelay = 500 * time.Millisecond
		out, err := cmd.Output()
		if err != nil {
			return systemProxyConfig{}
		}
		return parseDarwinScutilProxy(string(out))
	case "windows":
		cmd := subproc.CommandContext(ctx, "reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`)
		cmd.WaitDelay = 500 * time.Millisecond
		out, err := cmd.Output()
		if err != nil {
			return systemProxyConfig{}
		}
		return parseWindowsRegProxy(string(out))
	default:
		return systemProxyConfig{}
	}
}

// isProxyEndpointReachable verifies that a loopback system proxy is actually
// listening before injecting it, so a crashed/quit local proxy client that left
// stale OS proxy settings behind does not break direct connections.
func isProxyEndpointReachable(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" {
		return false
	}
	ip := net.ParseIP(host)
	isLoopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if !isLoopback {
		return true
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), loopbackProxyDialLimit)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func parseDarwinScutilProxy(raw string) systemProxyConfig {
	var (
		depth        int
		inExceptions bool
		httpEnable   bool
		httpHost     string
		httpPort     string
		httpsEnable  bool
		httpsHost    string
		httpsPort    string
		socksEnable  bool
		socksHost    string
		socksPort    string
		noProxy      []string
	)
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "{") {
			if depth == 1 && strings.HasPrefix(line, "ExceptionsList") {
				inExceptions = true
			}
			depth++
			continue
		}
		if line == "}" {
			if depth == 2 && inExceptions {
				inExceptions = false
			}
			if depth > 0 {
				depth--
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if depth == 2 && inExceptions {
			if tok := normalizeNoProxyToken(v); tok != "" {
				noProxy = append(noProxy, tok)
			}
			continue
		}
		if depth != 1 {
			continue
		}
		switch k {
		case "HTTPEnable":
			httpEnable = v == "1"
		case "HTTPProxy":
			httpHost = v
		case "HTTPPort":
			httpPort = v
		case "HTTPSEnable":
			httpsEnable = v == "1"
		case "HTTPSProxy":
			httpsHost = v
		case "HTTPSPort":
			httpsPort = v
		case "SOCKSEnable":
			socksEnable = v == "1"
		case "SOCKSProxy":
			socksHost = v
		case "SOCKSPort":
			socksPort = v
		}
	}
	var cfg systemProxyConfig
	if httpEnable {
		cfg.HTTPProxy = formatHostPortProxyURL("http", httpHost, httpPort)
	}
	if httpsEnable {
		cfg.HTTPSProxy = formatHostPortProxyURL("http", httpsHost, httpsPort)
	}
	if socksEnable {
		cfg.SOCKSProxy = formatHostPortProxyURL("socks5", socksHost, socksPort)
	}
	cfg.NoProxy = noProxy
	return cfg
}

func parseWindowsRegProxy(raw string) systemProxyConfig {
	var (
		enabled  bool
		server   string
		override string
	)
	for _, rawLine := range strings.Split(raw, "\n") {
		fields := strings.Fields(rawLine)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		val := strings.Join(fields[2:], " ")
		switch {
		case strings.EqualFold(name, "ProxyEnable"):
			enabled = val == "0x1" || val == "1"
		case strings.EqualFold(name, "ProxyServer"):
			server = strings.TrimSpace(val)
		case strings.EqualFold(name, "ProxyOverride"):
			override = strings.TrimSpace(val)
		}
	}
	if !enabled || server == "" {
		return systemProxyConfig{}
	}
	var cfg systemProxyConfig
	if strings.Contains(server, "=") {
		for _, part := range strings.Split(server, ";") {
			scheme, target, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(scheme)) {
			case "http":
				cfg.HTTPProxy = parseProxyEndpoint(target, "http")
			case "https":
				cfg.HTTPSProxy = parseProxyEndpoint(target, "http")
			case "socks", "socks5":
				cfg.SOCKSProxy = parseProxyEndpoint(target, "socks5")
			}
		}
	} else {
		if strings.HasPrefix(strings.ToLower(server), "socks") {
			cfg.SOCKSProxy = parseProxyEndpoint(server, "socks5")
		} else {
			u := parseProxyEndpoint(server, "http")
			cfg.HTTPProxy = u
			cfg.HTTPSProxy = u
		}
	}
	if override != "" {
		for _, item := range strings.Split(override, ";") {
			if tok := normalizeNoProxyToken(item); tok != "" {
				cfg.NoProxy = append(cfg.NoProxy, tok)
			}
		}
	}
	return cfg
}

func parseProxyEndpoint(raw, defaultScheme string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.Port() == "" {
			return ""
		}
		return formatHostPortProxyURL(strings.ToLower(u.Scheme), u.Hostname(), u.Port())
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return ""
	}
	return formatHostPortProxyURL(defaultScheme, host, port)
}

func formatHostPortProxyURL(scheme, host, port string) string {
	host = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(host), "["), "]"))
	port = strings.TrimSpace(port)
	if host == "" || port == "" || strings.ContainsAny(host, " \t\r\n/@?#\\") {
		return ""
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return ""
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(p))
}

func normalizeNoProxyToken(raw string) string {
	tok := strings.TrimSpace(raw)
	if tok == "" || tok == "*" || strings.EqualFold(tok, "<local>") || strings.ContainsAny(tok, " \t\r\n,;") {
		return ""
	}
	// macOS scutil writes shorthand CIDR like "169.254/16"; expand to 4 octets
	// so Go's golang.org/x/net/http/httpproxy accepts it via net.ParseCIDR.
	if ipPart, maskPart, ok := strings.Cut(tok, "/"); ok {
		octets := strings.Split(ipPart, ".")
		if len(octets) >= 1 && len(octets) < 4 && isDigitsOnlyList(octets) {
			for len(octets) < 4 {
				octets = append(octets, "0")
			}
			candidate := strings.Join(octets, ".") + "/" + maskPart
			if _, _, err := net.ParseCIDR(candidate); err == nil {
				return candidate
			}
		}
		if _, _, err := net.ParseCIDR(tok); err == nil {
			return tok
		}
		return ""
	}
	// Windows ProxyOverride uses IP prefix wildcards such as "127.*" or "10.*",
	// which are not valid NO_PROXY host suffixes in Go/curl.
	if strings.Contains(tok, "*") && !strings.HasPrefix(tok, "*.") {
		return ""
	}
	return tok
}

func isDigitsOnlyList(parts []string) bool {
	for _, p := range parts {
		if p == "" || !isAllDigits(p) {
			return false
		}
	}
	return true
}
