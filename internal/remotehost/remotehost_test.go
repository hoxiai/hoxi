package remotehost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type handlerRoundTripper struct {
	handler http.Handler
}

func (rt handlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rt.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func TestDaemonPairURIAndBearerGuard(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "host-daemon.json")
	cfg, err := EnsureDaemonConfig(cfgPath, "linux-devbox", "0.0.0.0:5430", t.TempDir(), false)
	if err != nil {
		t.Fatalf("EnsureDaemonConfig failed: %v", err)
	}
	if !strings.HasPrefix(cfg.Token, "hxh_") {
		t.Fatalf("expected hxh_ prefixed token, got %q", cfg.Token)
	}

	sampleURL := "https://example.invalid:5430"
	uri := FormatPairURI(sampleURL, cfg.Name, cfg.Token)
	gotURL, gotName, gotTok, err := ParsePairURI(uri)
	if err != nil {
		t.Fatalf("ParsePairURI failed: %v", err)
	}
	if gotURL != sampleURL || gotName != "linux-devbox" || gotTok != cfg.Token {
		t.Fatalf("round-trip mismatch: url=%q name=%q tok=%q", gotURL, gotName, gotTok)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sessions":[]}`))
	})
	guarded := WrapBearerGuard(cfg.Token, BuildHostInfo(cfg.HostID, cfg.Name, cfg.RootDir), inner)

	unauthReq := httptest.NewRequest(http.MethodGet, sampleURL+"/api/hoxi/host-info", nil)
	unauthRec := httptest.NewRecorder()
	guarded.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing bearer token, got %d", unauthRec.Code)
	}

	reg := NewService(filepath.Join(t.TempDir(), "remote-hosts.json"), handlerRoundTripper{handler: guarded})
	pub, err := reg.Upsert(UpsertInput{
		PairURI: FormatPairURI(sampleURL, "linux-devbox", cfg.Token),
	})
	if err != nil {
		t.Fatalf("reg.Upsert via PairURI failed: %v", err)
	}
	if !pub.HasToken {
		t.Fatalf("expected HasToken=true on PublicHost")
	}
	rawPub, _ := json.Marshal(pub)
	if strings.Contains(string(rawPub), cfg.Token) {
		t.Fatalf("PublicHost JSON leaked secret token: %s", string(rawPub))
	}

	probeRes, err := reg.Probe(t.Context(), pub.ID)
	if err != nil || !probeRes.OK || probeRes.Info == nil {
		t.Fatalf("expected Probe OK, got res=%+v err=%v", probeRes, err)
	}
	if probeRes.Info.Name != "linux-devbox" {
		t.Fatalf("expected host name linux-devbox, got %q", probeRes.Info.Name)
	}

	mux := http.NewServeMux()
	reg.RegisterRoutes(mux, func(r *http.Request) bool {
		return r.Header.Get("X-Remote") == "1"
	})

	remoteReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/hosts", nil)
	remoteReq.Header.Set("X-Remote", "1")
	remoteRec := httptest.NewRecorder()
	mux.ServeHTTP(remoteRec, remoteReq)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on remote plane, got %d", remoteRec.Code)
	}

	proxyReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/hosts/proxy/"+pub.ID+"/api/hoxi/sessions", nil)
	proxyRec := httptest.NewRecorder()
	mux.ServeHTTP(proxyRec, proxyReq)
	if proxyRec.Code != http.StatusOK || !strings.Contains(proxyRec.Body.String(), `"sessions"`) {
		t.Fatalf("expected proxied 200 sessions JSON, got code=%d body=%s", proxyRec.Code, proxyRec.Body.String())
	}
}

func TestHostProxyStripsDesktopTokenAndInjectsInnerGatewayToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "host-daemon.json")
	cfg, err := EnsureDaemonConfig(cfgPath, "wsl-host", DefaultHostListenAddr, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	const desktopLocalToken = "desktop_secret_token_must_never_leave_mac"
	const remoteInnerGatewayToken = "remote_host_inner_gateway_token_99"
	const sampleEndpoint = "https://example.invalid:5430"

	var sawAuthHeader, sawGatewayToken, sawQuery string
	innerGateway := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization")
		sawGatewayToken = r.Header.Get("X-Hoxi-Token")
		sawQuery = r.URL.RawQuery
		if sawGatewayToken != remoteInnerGatewayToken {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"missing or invalid gateway token"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"agents":["build","plan"]}`))
	})
	guarded := WrapBearerGuardWithGatewayToken(
		cfg.Token,
		remoteInnerGatewayToken,
		BuildHostInfo(cfg.HostID, cfg.Name, cfg.RootDir),
		innerGateway,
	)

	var wireSeenDesktopToken bool
	wireCheck := roundTripInspector(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.Header.Get("X-Hoxi-Token"), desktopLocalToken) ||
			strings.Contains(req.Header.Get("X-Hoxi-Host-Token"), desktopLocalToken) ||
			strings.Contains(req.URL.RawQuery, desktopLocalToken) {
			wireSeenDesktopToken = true
		}
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, req)
		return rec.Result(), nil
	})

	reg := NewService(filepath.Join(t.TempDir(), "remote-hosts.json"), wireCheck)
	pub, err := reg.Upsert(UpsertInput{
		PairURI: FormatPairURI(sampleEndpoint, "wsl-host", cfg.Token),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reg.RegisterRoutes(mux, nil)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/hoxi/hosts/proxy/"+pub.ID+"/api/hoxi/agents?directory=%2Fworkspace&hoxiToken="+desktopLocalToken,
		nil,
	)
	req.Header.Set("X-Hoxi-Token", desktopLocalToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if wireSeenDesktopToken {
		t.Fatal("desktop X-Hoxi-Token or hoxiToken query leaked over the wire to remote host")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on token-protected /api/hoxi/agents via WrapBearerGuardWithGatewayToken, got %d: %s", rec.Code, rec.Body.String())
	}
	if sawAuthHeader != "" {
		t.Fatalf("expected outer host Authorization header stripped before inner gateway, got %q", sawAuthHeader)
	}
	if sawGatewayToken != remoteInnerGatewayToken {
		t.Fatalf("expected inner X-Hoxi-Token=%q, got %q", remoteInnerGatewayToken, sawGatewayToken)
	}
	if strings.Contains(sawQuery, "hoxiToken") || !strings.Contains(sawQuery, "directory=%2Fworkspace") {
		t.Fatalf("unexpected upstream query %q", sawQuery)
	}

	// Malformed percent-escapes, semicolon separators, case-variant query keys,
	// and X-Hoxi-Host-Token headers must still never leak desktop tokens over the wire.
	for _, rawQ := range []string{
		"bad=%ZZ&hoxiToken=" + desktopLocalToken,
		"directory=%2Fworkspace;HOXITOKEN=" + desktopLocalToken,
		"hoxi%54oken=" + desktopLocalToken + "&keep=1",
	} {
		wireSeenDesktopToken = false
		mReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/hosts/proxy/"+pub.ID+"/api/hoxi/agents?"+rawQ, nil)
		mReq.Header.Set("X-Hoxi-Token", desktopLocalToken)
		mReq.Header.Set("X-Hoxi-Host-Token", desktopLocalToken)
		mRec := httptest.NewRecorder()
		mux.ServeHTTP(mRec, mReq)
		if wireSeenDesktopToken {
			t.Fatalf("desktop token leaked on malformed/variant query %q", rawQ)
		}
	}
}

func TestHostProbeRefusesRedirects(t *testing.T) {
	var redirectHits int
	rt := roundTripInspector(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "attacker.invalid" {
			redirectHits++
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			return rec.Result(), nil
		}
		rec := httptest.NewRecorder()
		rec.Header().Set("Location", "https://attacker.invalid/steal")
		rec.WriteHeader(http.StatusFound)
		return rec.Result(), nil
	})
	reg := NewService(filepath.Join(t.TempDir(), "remote-hosts.json"), rt)
	pub, err := reg.Upsert(UpsertInput{
		PairURI: FormatPairURI("https://devbox.invalid:5430", "devbox", "hxh_secret_token_123"),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := reg.Probe(t.Context(), pub.ID)
	if err != nil {
		t.Fatalf("unexpected Probe error: %v", err)
	}
	if res.OK || !strings.Contains(res.Error, "redirects are not allowed") {
		t.Fatalf("expected redirect refusal in ProbeResult, got %+v", res)
	}
	if redirectHits != 0 {
		t.Fatalf("expected 0 requests followed to redirect target, got %d", redirectHits)
	}
}

type roundTripInspector func(*http.Request) (*http.Response, error)

func (fn roundTripInspector) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestDefaultListenAddrIsLoopbackAndPrivateNetworkGuard(t *testing.T) {
	if DefaultHostListenAddr != "127.0.0.1:5430" {
		t.Fatalf("DefaultHostListenAddr = %q, want 127.0.0.1:5430", DefaultHostListenAddr)
	}

	guarded := WrapPrivateNetworkGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowedAddrs := []string{
		"127.0.0.1:50000",
		"[::1]:50000",
		"10.0.0.12:41234",
		"192.168.1.55:41234",
		"172.16.4.9:41234",
		"100.64.10.2:41234",
		"[fd12:3456::1]:41234",
		"[fe80::1%en0]:41234",
	}
	for _, addr := range allowedAddrs {
		req := httptest.NewRequest(http.MethodGet, "/api/hoxi/host-info", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected private/loopback addr %q allowed, got %d (%s)", addr, rec.Code, rec.Body.String())
		}
	}

	deniedAddrs := []string{
		"8.8.8.8:443",
		"198.51.100.10:12345",
		"1.1.1.1:5430",
		"[2001:4860:4860::8888]:443",
		"",
		"not-an-ip",
	}
	for _, addr := range deniedAddrs {
		req := httptest.NewRequest(http.MethodGet, "/api/hoxi/host-info", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected public/invalid addr %q forbidden (403), got %d", addr, rec.Code)
		}
	}
}
