package agentcli

import (
	"errors"
	"net"
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
