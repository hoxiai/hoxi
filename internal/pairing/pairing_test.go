package pairing

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestService(t *testing.T, host string) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pairing.json")
	svc := NewService(path, 5417)
	svc.hostname = func() string { return "Studio" }
	svc.lanHost = func() string { return host }
	return svc, path
}

func TestCodeBuildsScannablePayload(t *testing.T) {
	svc, _ := newTestService(t, "192.168.1.20")

	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if !code.Available {
		t.Fatal("expected available code when a LAN host exists")
	}
	if code.Type != "hoxi" {
		t.Fatalf("type = %q, want hoxi", code.Type)
	}
	if code.URL != "http://192.168.1.20:5417" {
		t.Fatalf("url = %q", code.URL)
	}
	if code.Port != 5417 || code.Host != "192.168.1.20" || code.Hostname != "Studio" {
		t.Fatalf("unexpected payload: %+v", code.Payload)
	}
	if len(code.Token) != 64 {
		t.Fatalf("token length = %d, want 64", len(code.Token))
	}
	if !strings.HasPrefix(code.QRPng, "data:image/png;base64,") {
		t.Fatalf("qrPng prefix = %q", code.QRPng)
	}
}

func TestCodePersistsTokenWith0600(t *testing.T) {
	svc, path := newTestService(t, "10.0.0.5")

	first, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token perms = %o, want 600", perm)
	}
	if !strings.Contains(codeTokenString(t, path), first.Token) {
		t.Fatal("token file must hold the issued token")
	}

	again := NewService(path, 5417)
	again.hostname = svc.hostname
	again.lanHost = svc.lanHost
	second, err := again.Code()
	if err != nil {
		t.Fatalf("Code (reload): %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("token changed across restarts: %q != %q", second.Token, first.Token)
	}
}

func TestCodeWithoutLANHostIsUnavailable(t *testing.T) {
	svc, _ := newTestService(t, "")

	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if code.Available || code.URL != "" || code.QRPng != "" {
		t.Fatalf("expected unavailable code, got %+v", code)
	}
}

func TestPrimaryLANIPv4IsNeverLoopbackOrLinkLocal(t *testing.T) {
	got := primaryLANIPv4()
	if got == "" {
		t.Skip("no LAN address on this host")
	}
	ip := net.ParseIP(got)
	if ip == nil || ip.To4() == nil {
		t.Fatalf("primaryLANIPv4 = %q, not an IPv4 address", got)
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatalf("primaryLANIPv4 = %q must not be loopback or link-local", got)
	}
}

func TestCodeIncludesRelayWhenConfigured(t *testing.T) {
	svc, _ := newTestService(t, "192.168.1.20")
	svc.SetRelayURL(func() string { return "https://abc123.node.hoxi.work" })

	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if code.Relay != "https://abc123.node.hoxi.work" {
		t.Fatalf("relay = %q", code.Relay)
	}
	if code.URL != "http://192.168.1.20:5417" {
		t.Fatalf("url = %q", code.URL)
	}
}

func TestCodeOmitsRelayWhenUnset(t *testing.T) {
	svc, _ := newTestService(t, "192.168.1.20")

	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if code.Relay != "" {
		t.Fatalf("relay = %q, want empty", code.Relay)
	}
}

func TestRotateChangesTokenAndInvalidatesOld(t *testing.T) {
	svc, _ := newTestService(t, "192.168.1.20")
	first, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	authorize := func(token string) bool {
		req := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
		req.Header.Set("X-Auth", token)
		return svc.Authorize(req)
	}
	if !authorize(first.Token) {
		t.Fatal("first token should authorize")
	}

	rotated, err := svc.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Token == first.Token {
		t.Fatal("rotate must issue a new token")
	}
	if authorize(first.Token) {
		t.Fatal("old token must be rejected after rotation")
	}
	if !authorize(rotated.Token) {
		t.Fatal("rotated token should authorize")
	}
}

func TestAuthorizeMatchesPairingToken(t *testing.T) {
	svc, _ := newTestService(t, "192.168.1.5")
	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}

	cases := []struct {
		name  string
		field string
		value string
		want  bool
	}{
		{"x-auth", "X-Auth", code.Token, true},
		{"bearer", "Authorization", "Bearer " + code.Token, true},
		{"wrong token", "X-Auth", strings.Repeat("f", 64), false},
		{"missing token", "X-Auth", "", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
		if tc.value != "" {
			req.Header.Set(tc.field, tc.value)
		}
		if got := svc.Authorize(req); got != tc.want {
			t.Errorf("%s: Authorize = %v, want %v", tc.name, got, tc.want)
		}
	}
	if svc.Authorize(nil) {
		t.Error("nil request must not authorize")
	}
}

func codeTokenString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	var rec struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal token: %v", err)
	}
	return rec.Token
}
