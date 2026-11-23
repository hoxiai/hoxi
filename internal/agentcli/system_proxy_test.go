package agentcli

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func envMapForTest(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestParseDarwinScutilProxy(t *testing.T) {
	t.Parallel()
	raw := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
    2 : corp.internal
  }
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7890
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 7891
  SOCKSProxy : 127.0.0.1
}`
	got := parseDarwinScutilProxy(raw)
	if got.HTTPProxy != "http://127.0.0.1:7890" {
		t.Fatalf("HTTPProxy = %q, want http://127.0.0.1:7890", got.HTTPProxy)
	}
	if got.HTTPSProxy != "http://127.0.0.1:7890" {
		t.Fatalf("HTTPSProxy = %q, want http://127.0.0.1:7890", got.HTTPSProxy)
	}
	if got.SOCKSProxy != "socks5://127.0.0.1:7891" {
		t.Fatalf("SOCKSProxy = %q, want socks5://127.0.0.1:7891", got.SOCKSProxy)
	}
	wantNoProxy := "*.local,169.254.0.0/16,corp.internal"
	if joined := strings.Join(got.NoProxy, ","); joined != wantNoProxy {
		t.Fatalf("NoProxy = %q, want %q", joined, wantNoProxy)
	}

	disabled := parseDarwinScutilProxy(`<dictionary> {
  HTTPEnable : 0
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 99999
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : bad/host
}`)
	if disabled.HTTPProxy != "" || disabled.HTTPSProxy != "" || disabled.SOCKSProxy != "" {
		t.Fatalf("expected disabled or malformed entries to be rejected, got %+v", disabled)
	}
}

func TestParseWindowsRegProxy(t *testing.T) {
	t.Parallel()
	single := `
HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    MigrateProxy    REG_DWORD    0x1
    ProxyEnable    REG_DWORD    0x1
    ProxyServer    REG_SZ    127.0.0.1:7890
    ProxyOverride    REG_SZ    localhost;127.*;*.corp.local;<local>
`
	got := parseWindowsRegProxy(single)
	if got.HTTPProxy != "http://127.0.0.1:7890" || got.HTTPSProxy != "http://127.0.0.1:7890" {
		t.Fatalf("unexpected single Windows proxy config: %+v", got)
	}
	if joined := strings.Join(got.NoProxy, ","); joined != "localhost,*.corp.local" {
		t.Fatalf("unexpected Windows NoProxy: %q", joined)
	}

	perScheme := `
HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    ProxyEnable    REG_DWORD    0x1
    ProxyServer    REG_SZ    http=127.0.0.1:7890;https=127.0.0.1:7891;socks=127.0.0.1:7892
`
	gotScheme := parseWindowsRegProxy(perScheme)
	if gotScheme.HTTPProxy != "http://127.0.0.1:7890" ||
		gotScheme.HTTPSProxy != "http://127.0.0.1:7891" ||
		gotScheme.SOCKSProxy != "socks5://127.0.0.1:7892" {
		t.Fatalf("unexpected per-scheme Windows proxy config: %+v", gotScheme)
	}

	off := parseWindowsRegProxy(strings.ReplaceAll(single, "0x1", "0x0"))
	if off.HTTPProxy != "" || off.HTTPSProxy != "" {
		t.Fatalf("disabled Windows proxy must be ignored: %+v", off)
	}
}

func TestWithResolvedSystemProxyInjectionAndOverrides(t *testing.T) {
	t.Parallel()

	// 1. Explicit user proxy is preserved untouched, while loopback NO_PROXY is guaranteed.
	lookupCalled := false
	explicit := withResolvedSystemProxy(
		[]string{"PATH=/usr/bin", "HTTPS_PROXY=http://127.0.0.1:6152", "NO_PROXY=internal.corp"},
		func() systemProxyConfig {
			lookupCalled = true
			return systemProxyConfig{HTTPSProxy: "http://127.0.0.1:7890"}
		},
		func(string) bool { return true },
	)
	if lookupCalled {
		t.Fatal("OS lookup should not run when proxy env is already set")
	}
	m1 := envMapForTest(explicit)
	if m1["HTTPS_PROXY"] != "http://127.0.0.1:6152" || m1["HTTP_PROXY"] != "" {
		t.Fatalf("explicit proxy altered: %v", m1)
	}
	if m1["NO_PROXY"] != "internal.corp,127.0.0.1,localhost,::1" {
		t.Fatalf("unexpected NO_PROXY: %q", m1["NO_PROXY"])
	}

	// 2. SOCKS-only OS proxy populates HTTP_PROXY and HTTPS_PROXY too so Go CLIs (agy) use it.
	socksOnly := withResolvedSystemProxy(
		[]string{"PATH=/usr/bin"},
		func() systemProxyConfig {
			return systemProxyConfig{
				SOCKSProxy: "socks5://127.0.0.1:7891",
				NoProxy:    []string{"*.local", "127.0.0.1"},
			}
		},
		func(string) bool { return true },
	)
	m2 := envMapForTest(socksOnly)
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		if m2[k] != "socks5://127.0.0.1:7891" {
			t.Fatalf("%s = %q, want socks5://127.0.0.1:7891", k, m2[k])
		}
	}
	if m2["NO_PROXY"] != "*.local,127.0.0.1,localhost,::1" {
		t.Fatalf("NO_PROXY = %q", m2["NO_PROXY"])
	}
}

func TestIsProxyEndpointReachableSkipsDeadLoopbackProxy(t *testing.T) {
	t.Parallel()

	// Deterministic check: unreachable loopback proxy must not be injected.
	deadURL := "http://127.0.0.1:59999"
	out := withResolvedSystemProxy(
		[]string{"PATH=/usr/bin"},
		func() systemProxyConfig {
			return systemProxyConfig{HTTPProxy: deadURL, HTTPSProxy: deadURL}
		},
		func(string) bool { return false },
	)
	if m := envMapForTest(out); m["HTTPS_PROXY"] != "" || m["HTTP_PROXY"] != "" {
		t.Fatalf("dead loopback proxy should not be injected: %v", m)
	}
	if !isProxyEndpointReachable("http://proxy.corp.internal:8080") {
		t.Fatal("non-loopback proxy host should not be rejected by local port probe")
	}
	if isProxyEndpointReachable("not-a-valid-url") {
		t.Fatal("malformed URL must be rejected")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listen unavailable: %v", err)
	}
	probe, dialErr := net.DialTimeout("tcp", ln.Addr().String(), loopbackProxyDialLimit)
	if dialErr != nil {
		_ = ln.Close()
		if errors.Is(dialErr, syscall.EPERM) || errors.Is(dialErr, syscall.EACCES) {
			t.Skipf("outbound loopback dial blocked by outer sandbox: %v", dialErr)
		}
		t.Fatalf("unexpected loopback dial error: %v", dialErr)
	}
	_ = probe.Close()

	liveURL := "http://" + ln.Addr().String()
	if !isProxyEndpointReachable(liveURL) {
		_ = ln.Close()
		t.Fatalf("expected live loopback listener %s to be reachable", liveURL)
	}
	_ = ln.Close()
	if isProxyEndpointReachable(liveURL) {
		t.Fatalf("expected closed loopback listener %s to be unreachable", liveURL)
	}
}

func TestResolveSystemProxyURLForHTTPTransport(t *testing.T) {
	t.Parallel()

	osCfg := systemProxyConfig{
		HTTPProxy:  "http://127.0.0.1:7890",
		HTTPSProxy: "http://127.0.0.1:7891",
		SOCKSProxy: "socks5://127.0.0.1:7892",
		NoProxy:    []string{"*.local", "169.254.0.0/16", "corp.internal"},
	}
	noEnv := func(*http.Request) (*url.URL, error) { return nil, nil }
	alwaysUp := func(string) bool { return true }

	// 1. Loopback always bypasses proxy even when OS proxy is active.
	for _, raw := range []string{"http://127.0.0.1:5410/api", "http://localhost:5400/x", "http://[::1]:5410/x"} {
		req, _ := http.NewRequest(http.MethodGet, raw, nil)
		got, err := resolveSystemProxyURL(req, noEnv, func() systemProxyConfig { return osCfg }, alwaysUp)
		if err != nil || got != nil {
			t.Fatalf("loopback %s must bypass proxy, got (%v, %v)", raw, got, err)
		}
	}

	// 2. Explicit environment proxy takes precedence over OS proxy.
	envHit, _ := url.Parse("http://127.0.0.1:6152")
	reqHTTPS, _ := http.NewRequest(http.MethodGet, "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi.dmg", nil)
	gotEnv, err := resolveSystemProxyURL(reqHTTPS, func(*http.Request) (*url.URL, error) { return envHit, nil }, func() systemProxyConfig {
		t.Fatal("OS lookup should not run when env proxy is set")
		return systemProxyConfig{}
	}, alwaysUp)
	if err != nil || gotEnv == nil || gotEnv.String() != envHit.String() {
		t.Fatalf("env proxy = (%v, %v), want %s", gotEnv, err, envHit)
	}

	// 3. Falls back to OS HTTPS/HTTP/SOCKS5 proxy when env is unset.
	gotHTTPS, err := resolveSystemProxyURL(reqHTTPS, noEnv, func() systemProxyConfig { return osCfg }, alwaysUp)
	if err != nil || gotHTTPS == nil || gotHTTPS.String() != "http://127.0.0.1:7891" {
		t.Fatalf("OS HTTPS proxy = (%v, %v)", gotHTTPS, err)
	}
	gotSOCKS, err := resolveSystemProxyURL(reqHTTPS, noEnv, func() systemProxyConfig {
		return systemProxyConfig{SOCKSProxy: "socks5://127.0.0.1:7892"}
	}, alwaysUp)
	if err != nil || gotSOCKS == nil || gotSOCKS.String() != "socks5://127.0.0.1:7892" {
		t.Fatalf("OS SOCKS proxy = (%v, %v)", gotSOCKS, err)
	}

	// 4. Honors NoProxy domain suffix and CIDR rules.
	for _, bypass := range []string{"https://printer.local/status", "https://api.corp.internal/v1", "http://169.254.1.2/meta"} {
		req, _ := http.NewRequest(http.MethodGet, bypass, nil)
		got, err := resolveSystemProxyURL(req, noEnv, func() systemProxyConfig { return osCfg }, alwaysUp)
		if err != nil || got != nil {
			t.Fatalf("NoProxy target %s should stay direct, got (%v, %v)", bypass, got, err)
		}
	}

	// 5. Skips unreachable local OS proxy cleanly.
	gotDead, err := resolveSystemProxyURL(reqHTTPS, noEnv, func() systemProxyConfig { return osCfg }, func(string) bool { return false })
	if err != nil || gotDead != nil {
		t.Fatalf("unreachable proxy must fall back to direct, got (%v, %v)", gotDead, err)
	}
}
