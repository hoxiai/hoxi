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
}

type roundTripInspector func(*http.Request) (*http.Response, error)

func (fn roundTripInspector) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
