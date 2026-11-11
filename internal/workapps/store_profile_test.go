package workapps

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProxyTimezoneLocale(t *testing.T) {
	validProxies := []string{
		"http://127.0.0.1:7890",
		"https://user:pass@proxy.example.com:8443",
		"socks5://seller1:s3cr3t!@198.51.100.10:1080",
		"socks5h://127.0.0.1:42001",
	}
	for _, raw := range validProxies {
		if _, err := ValidateProxyURL(raw); err != nil {
			t.Fatalf("expected valid proxy %q, got error: %v", raw, err)
		}
	}

	invalidProxies := []string{
		"",
		"--proxy-pac-url=http://evil.com/pac",
		"-socks5://127.0.0.1:1080",
		"file:///etc/passwd",
		"ftp://127.0.0.1:21",
		"http://127.0.0.1",
		"http://127.0.0.1:99999",
		"http://127.0.0.1:7890/path",
		"http://127.0.0.1:7890?foo=bar",
		"socks5://127.0.0.1:1080 --no-sandbox",
	}
	for _, raw := range invalidProxies {
		if _, err := ValidateProxyURL(raw); err == nil {
			t.Fatalf("expected invalid proxy %q to be rejected", raw)
		}
	}

	if got := RedactProxyURL("socks5://alice:topSecret99@198.51.100.8:1080"); got != "socks5://***@198.51.100.8:1080" {
		t.Fatalf("unexpected RedactProxyURL: %q", got)
	}
	if got := RedactProxyURL("http://127.0.0.1:7890"); got != "http://127.0.0.1:7890" {
		t.Fatalf("unexpected RedactProxyURL without auth: %q", got)
	}

	if err := ValidateTimezone("America/Los_Angeles"); err != nil {
		t.Fatalf("expected valid timezone, got %v", err)
	}
	if err := ValidateTimezone("Mars/Olympus_Mons"); err == nil {
		t.Fatalf("expected unknown timezone to fail")
	}
	if err := ValidateLocale("en-US"); err != nil {
		t.Fatalf("expected valid locale, got %v", err)
	}
	if err := ValidateLocale("en_US; --evil"); err == nil {
		t.Fatalf("expected malformed locale to fail")
	}
}

func TestStoreIsolationRedactionAndPromptContext(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "work-apps.json"))
	svc := NewService(nil, store)

	// Reject malformed proxy upfront with ErrInvalidStoreConfig.
	err := svc.SaveState(map[string]any{
		"storesByApp": map[string]any{
			"amazon": []any{
				map[string]any{
					"id":       "amz-us-main",
					"proxyURL": "--headless=new",
				},
			},
		},
	})
	if !errors.Is(err, ErrInvalidStoreConfig) {
		t.Fatalf("expected ErrInvalidStoreConfig on flag-injected proxy, got %v", err)
	}

	// Save rich Amazon US primary store with isolation + unit economics.
	secretPass := "Sup3rS3cretProxyPass!"
	err = svc.SaveState(map[string]any{
		"storesByApp": map[string]any{
			"amazon": []any{
				map[string]any{
					"id":                 "amz-us-main",
					"name":               "AnkerHome US",
					"marketplace":        "US",
					"currency":           "USD",
					"browserProfileKey":  "amz-us-main",
					"proxyURL":           "socks5://seller_us:" + secretPass + "@198.51.100.22:1080",
					"proxyRequired":      true,
					"timezone":           "America/Los_Angeles",
					"locale":             "en-US",
					"brandName":          "AnkerHome",
					"referralFeePct":     15,
					"targetNetMarginPct": 18,
					"shippingRatePerKg":  42,
					"shippingCurrency":   "CNY",
					"fxRate":             7.2,
					"returnRatePct":      5,
					"targetACOSPct":      25,
					"forbiddenTerms":     []string{"cure", "antibacterial"},
				},
			},
			"tiktok-shop": []any{
				map[string]any{
					"id":                "tts-uk-1",
					"name":              "TikTok UK",
					"browserProfileKey": "custom-uk-prof",
					"proxyRequired":     true,
				},
			},
		},
		"sessionStoreBindings": map[string]any{
			"ses_amz_1": map[string]any{
				"appId":   "amazon",
				"storeId": "amz-us-main",
			},
			"ses_tts_1": map[string]any{
				"appId":   "tiktok-shop",
				"storeId": "tts-uk-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveState valid stores failed: %v", err)
	}

	// Isolated idx==0 store MUST NOT collapse to shared "default" profile!
	if got := svc.ResolveBrowserProfileKey("ses_amz_1"); got != "amz-us-main" {
		t.Fatalf("expected isolated primary store to keep profile key amz-us-main, got %q", got)
	}

	// ResolveBrowserEnvironment succeeds for configured store and fails closed when proxyRequired lacks proxyURL.
	env, err := svc.ResolveBrowserEnvironment("ses_amz_1")
	if err != nil {
		t.Fatalf("ResolveBrowserEnvironment failed: %v", err)
	}
	if env.StoreID != "amz-us-main" || env.Timezone != "America/Los_Angeles" || env.Locale != "en-US" {
		t.Fatalf("unexpected BrowserEnvironment: %#v", env)
	}
	if !strings.Contains(env.ProxyURL, secretPass) || strings.Contains(env.RedactedProxy, secretPass) {
		t.Fatalf("expected internal ProxyURL and scrubbed RedactedProxy, got %#v", env)
	}
	if _, err := svc.ResolveBrowserEnvironment("ses_tts_1"); err == nil {
		t.Fatalf("expected fail-closed error when proxyRequired=true has empty proxyURL")
	}
	if _, err := svc.ResolveBrowserEnvironment("workstore_custom-uk-prof"); err == nil {
		t.Fatalf("expected custom browserProfileKey workstore_ scope to resolve back to tts-uk-1 and fail closed")
	}
	defaultEnv, err := svc.ResolveBrowserEnvironment("workstore_default")
	if err != nil || defaultEnv.StoreID != "" {
		t.Fatalf("expected shared workstore_default scope to remain unbound, got %#v, err=%v", defaultEnv, err)
	}

	// Prompt context block must include store economics and NEVER leak proxy credentials.
	prompt := svc.FormatSessionStoreContext("ses_amz_1")
	if !strings.Contains(prompt, "<hoxi_work_store_profile>") ||
		!strings.Contains(prompt, "AnkerHome US") ||
		!strings.Contains(prompt, "Target Net Margin >= 18.00%") ||
		!strings.Contains(prompt, "socks5://***@198.51.100.22:1080") {
		t.Fatalf("unexpected store prompt block:\n%s", prompt)
	}
	if strings.Contains(prompt, secretPass) {
		t.Fatalf("CRITICAL: raw proxy password leaked into LLM prompt block!")
	}

	// FetchApps / ListStores must scrub credentials, and round-tripping redacted localState must preserve password.
	fetched, _ := svc.FetchApps(context.Background())
	rawJSON, _ := json.Marshal(fetched)
	if strings.Contains(string(rawJSON), secretPass) {
		t.Fatalf("CRITICAL: raw proxy password leaked in FetchApps response: %s", string(rawJSON))
	}
	localState := fetched["localState"].(map[string]any)
	if err := svc.SaveState(map[string]any{"storesByApp": localState["storesByApp"]}); err != nil {
		t.Fatalf("round-trip SaveState with redacted proxy placeholder failed: %v", err)
	}
	stored, ok := svc.GetStore("amz-us-main")
	if !ok || !strings.Contains(stored.ProxyURL, secretPass) {
		t.Fatalf("expected original proxy credentials preserved after redacted round-trip, got %#v", stored)
	}

	// Changing proxy host while keeping "***@" placeholder must be rejected (HTTP 400).
	h := NewHTTPHandler(svc)
	badBody := `{"storesByApp":{"amazon":[{"id":"amz-us-main","proxyURL":"socks5://***@203.0.113.9:1080"}]}}`
	req := httptest.NewRequest(http.MethodPut, "/api/hoxi/work/apps", strings.NewReader(badBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when changing proxy host without credentials, got %d (%s)", rec.Code, rec.Body.String())
	}

	// GET /api/hoxi/work/stores returns public scrubbed stores.
	listReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/work/stores", nil)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || strings.Contains(listRec.Body.String(), secretPass) {
		t.Fatalf("unexpected /api/hoxi/work/stores response %d: %s", listRec.Code, listRec.Body.String())
	}

	// ForgetSession removes sessionStoreBindings on session deletion without touching stores.
	svc.ForgetSession("ses_amz_1")
	if got := svc.FormatSessionStoreContext("ses_amz_1"); got != "" {
		t.Fatalf("expected deleted session binding cleared, still got: %s", got)
	}
}

func TestProbeProxySOCKS5AndHTTPHandler(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock socks5: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hdr := make([]byte, 2)
				if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 0x05 {
					return
				}
				methods := make([]byte, int(hdr[1]))
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				hasUserPass := false
				for _, m := range methods {
					if m == 0x02 {
						hasUserPass = true
					}
				}
				if !hasUserPass {
					_, _ = c.Write([]byte{0x05, 0xFF})
					return
				}
				_, _ = c.Write([]byte{0x05, 0x02})
				ver := make([]byte, 2)
				if _, err := io.ReadFull(c, ver); err != nil {
					return
				}
				uBuf := make([]byte, int(ver[1]))
				_, _ = io.ReadFull(c, uBuf)
				pLen := make([]byte, 1)
				_, _ = io.ReadFull(c, pLen)
				pBuf := make([]byte, int(pLen[0]))
				_, _ = io.ReadFull(c, pBuf)
				if string(uBuf) == "seller" && string(pBuf) == "goodpass" {
					_, _ = c.Write([]byte{0x01, 0x00})
				} else {
					_, _ = c.Write([]byte{0x01, 0x01})
				}
			}(conn)
		}
	}()

	addr := ln.Addr().String()
	okRes, err := ProbeProxy(context.Background(), "socks5://seller:goodpass@"+addr)
	if err != nil || !okRes.OK {
		t.Fatalf("expected SOCKS5 probe with valid auth to succeed, got %#v, err=%v", okRes, err)
	}
	if strings.Contains(okRes.RedactedProxy, "goodpass") {
		t.Fatalf("credentials leaked in ProbeProxy result: %#v", okRes)
	}

	badRes, err := ProbeProxy(context.Background(), "socks5://seller:wrongpass@"+addr)
	if err == nil || badRes.OK {
		t.Fatalf("expected SOCKS5 probe with wrong password to fail, got %#v", badRes)
	}
	if strings.Contains(badRes.Error, "wrongpass") {
		t.Fatalf("credentials leaked in ProbeProxy error: %q", badRes.Error)
	}

	// Verify POST /api/hoxi/work/proxy-check by storeId.
	store := NewStore(filepath.Join(t.TempDir(), "work-apps.json"))
	svc := NewService(nil, store)
	_ = svc.SaveState(map[string]any{
		"storeConfigs": map[string]any{
			"amz-1": map[string]any{
				"proxyURL": "socks5://seller:goodpass@" + addr,
			},
		},
	})
	h := NewHTTPHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/work/proxy-check", strings.NewReader(`{"storeId":"amz-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected 200 ok:true from proxy-check, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "goodpass") {
		t.Fatalf("credentials leaked in /api/hoxi/work/proxy-check response: %s", rec.Body.String())
	}
}
