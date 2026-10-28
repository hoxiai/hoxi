package pairing

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
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

func TestPrimaryLANIPv4IsNeverLoopbackOrPublic(t *testing.T) {
	got := primaryLANIPv4()
	if got == "" {
		t.Skip("no LAN address on this host")
	}
	ip := net.ParseIP(got)
	if ip == nil || ip.To4() == nil {
		t.Fatalf("primaryLANIPv4 = %q, not an IPv4 address", got)
	}
	v4 := ip.To4()
	if v4.IsLoopback() {
		t.Fatalf("primaryLANIPv4 = %q must not be loopback", got)
	}
	if !v4.IsPrivate() && !isCGNATIPv4(v4) && !v4.IsLinkLocalUnicast() {
		t.Fatalf("primaryLANIPv4 = %q must not be a public IPv4 address", got)
	}
}

func TestSelectLANIPv4NeverFallsBackToPublic(t *testing.T) {
	if got := selectLANIPv4([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("203.0.113.10"), net.ParseIP("127.0.0.1")}); got != "" {
		t.Fatalf("selectLANIPv4(public + loopback) = %q, want empty", got)
	}
	if got := selectLANIPv4([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("169.254.10.20")}); got != "169.254.10.20" {
		t.Fatalf("selectLANIPv4(public + link-local) = %q, want 169.254.10.20", got)
	}
	if got := selectLANIPv4([]net.IP{net.ParseIP("169.254.10.20"), net.ParseIP("100.100.10.5")}); got != "100.100.10.5" {
		t.Fatalf("selectLANIPv4(link-local + CGNAT) = %q, want 100.100.10.5", got)
	}
	if got := selectLANIPv4([]net.IP{net.ParseIP("100.100.10.5"), net.ParseIP("192.168.1.9")}); got != "192.168.1.9" {
		t.Fatalf("selectLANIPv4(CGNAT + RFC1918) = %q, want 192.168.1.9", got)
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
	if len(code.RelayToken) != 64 || code.RelayToken == code.Token {
		t.Fatalf("relayToken = %q (len=%d), want distinct 64-hex token from LAN token %q", code.RelayToken, len(code.RelayToken), code.Token)
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
	if code.Relay != "" || code.RelayToken != "" {
		t.Fatalf("relay = %q, relayToken = %q, want both empty", code.Relay, code.RelayToken)
	}
}

func TestAuthorizeIsolatesLANAndRelayTokens(t *testing.T) {
	svc, path := newTestService(t, "192.168.1.20")
	svc.SetRelayURL(func() string { return "https://abc123.node.hoxi.work" })

	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	lanReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	lanReq.Header.Set("X-Auth", code.Token)
	relayReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	relayReq.Header.Set("X-Auth", code.RelayToken)

	if !svc.Authorize(lanReq) {
		t.Fatal("LAN token must authorize LAN request")
	}
	if svc.Authorize(relayReq) {
		t.Fatal("relay token must NOT authorize LAN request")
	}
	if !svc.AuthorizeRelay(relayReq) {
		t.Fatal("relay token must authorize relay request")
	}
	if svc.AuthorizeRelay(lanReq) {
		t.Fatal("LAN token must NOT authorize relay request")
	}

	// Verify upgrading a legacy single-token file preserves the LAN token and
	// mints a distinct relay token.
	legacyToken := strings.Repeat("a", 64)
	if err := os.WriteFile(path, []byte(`{"token":"`+legacyToken+`"}`), 0o600); err != nil {
		t.Fatalf("write legacy token: %v", err)
	}
	reloaded := NewService(path, 5417)
	gotLAN, err := reloaded.Token()
	if err != nil || gotLAN != legacyToken {
		t.Fatalf("reloaded Token() = %q (err=%v), want %q", gotLAN, err, legacyToken)
	}
	gotRelay, err := reloaded.RelayToken()
	if err != nil || len(gotRelay) != 64 || gotRelay == legacyToken {
		t.Fatalf("reloaded RelayToken() = %q (err=%v), want distinct 64-hex token", gotRelay, err)
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

func TestAuthorizeWithoutTokenHeaderDoesNotCreateTokenFile(t *testing.T) {
	svc, path := newTestService(t, "192.168.1.5")
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	if svc.Authorize(req) {
		t.Fatal("request without token must not authorize")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unauthenticated request should not create %s: err = %v", path, err)
	}
}

func TestCorruptTokenFileIsBackedUpBeforeRegeneration(t *testing.T) {
	svc, path := newTestService(t, "192.168.1.5")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if len(code.Token) != 64 {
		t.Fatalf("token = %q, want 64-hex token", code.Token)
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected 1 corrupt backup file, got %v (err=%v)", matches, err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil || string(raw) != "{not-json" {
		t.Fatalf("corrupt backup content = %q (err=%v), want {not-json", string(raw), err)
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
