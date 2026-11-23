package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebFetchSSRFBlocksResolvedPrivateHostBehindHTTPProxy(t *testing.T) {
	var proxyHits atomic.Int32
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("leaked internal response"))
	}))
	defer proxySrv.Close()

	t.Setenv("HTTP_PROXY", proxySrv.URL)
	t.Setenv("HTTPS_PROXY", proxySrv.URL)
	t.Setenv("NO_PROXY", "")

	origLookup := lookupFetchHostIPs
	t.Cleanup(func() { lookupFetchHostIPs = origLookup })

	cases := []struct {
		name string
		ip   string
	}{
		{name: "rfc1918", ip: "10.0.0.42"},
		{name: "loopback", ip: "127.0.0.1"},
		{name: "metadata", ip: "169.254.169.254"},
		{name: "cgnat", ip: "100.64.0.10"},
	}

	client := newSSRFSafeHTTPClient(5*time.Second, false)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookupFetchHostIPs = func(_ context.Context, _ string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(tc.ip)}}, nil
			}
			resp, err := client.Get("http://internal-target.example/secret")
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "blocked connection to non-public IP") {
				t.Fatalf("expected SSRF block for resolved IP %s behind proxy, got err=%v", tc.ip, err)
			}
		})
	}

	if hits := proxyHits.Load(); hits != 0 {
		t.Fatalf("expected 0 requests forwarded to HTTP proxy for blocked internal targets, got %d", hits)
	}
}

func TestValidateFetchURLBlocksTrailingDotInternalHosts(t *testing.T) {
	blocked := []string{
		"http://localhost./",
		"http://sub.localhost./",
		"http://printer.local./",
		"http://metadata.google.internal./computeMetadata/v1/",
		"http://metadata.goog./",
		"http://./",
	}
	for _, raw := range blocked {
		if _, err := validateFetchURL(raw, false); err == nil {
			t.Fatalf("expected validateFetchURL(%q) to be blocked, got nil error", raw)
		}
	}
}

func TestWebFetchClientStripsAuthHeadersOnCrossOriginRedirect(t *testing.T) {
	var targetAuth, targetAPIKey, targetSubToken, targetCookie string
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth = r.Header.Get("Authorization")
		targetAPIKey = r.Header.Get("X-Api-Key")
		targetSubToken = r.Header.Get("X-Subscription-Token")
		targetCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer dest.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/landed", http.StatusFound)
	}))
	defer origin.Close()

	client := newSSRFSafeHTTPClient(5*time.Second, true)
	req, err := http.NewRequest(http.MethodGet, origin.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-bearer")
	req.Header.Set("x-api-key", "secret-exa-key")
	req.Header.Set("X-Subscription-Token", "secret-brave-key")
	req.Header.Set("Cookie", "sid=secret-cookie")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected error following safe redirect: %v", err)
	}
	_ = resp.Body.Close()

	if targetAuth != "" || targetAPIKey != "" || targetSubToken != "" || targetCookie != "" {
		t.Fatalf("sensitive headers leaked across origins: auth=%q apiKey=%q subToken=%q cookie=%q",
			targetAuth, targetAPIKey, targetSubToken, targetCookie)
	}
}
