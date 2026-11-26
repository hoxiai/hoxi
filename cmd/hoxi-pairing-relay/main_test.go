package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/pairing"
)

func testIP(lastOctet byte) string {
	return net.IPv4(198, 51, 100, lastOctet).String()
}

func TestRelayHealthAndSecurityHeaders(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	exchange := pairing.NewShortCodeExchange(func() time.Time { return now })
	handler := buildHandler(exchange, false)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func TestRelayPublishRedeemCancelRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	exchange := pairing.NewShortCodeExchange(func() time.Time { return now })
	handler := buildHandler(exchange, false)

	pubReqID, err := pairing.NewShortCodeRequestID(now)
	if err != nil {
		t.Fatalf("NewShortCodeRequestID: %v", err)
	}
	pubBody, _ := json.Marshal(map[string]any{
		"request_id": pubReqID,
		"ticket":     "{\"v\":1,\"host\":\"desktop-node\",\"kexPub\":\"pub\"}",
	})
	pubReq := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(pubBody))
	pubReq.RemoteAddr = net.JoinHostPort(testIP(10), "43210")
	pubRec := httptest.NewRecorder()
	handler.ServeHTTP(pubRec, pubReq)

	if pubRec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/codes status = %d, body = %s", pubRec.Code, pubRec.Body.String())
	}
	var pubResp struct {
		Code        string `json:"code"`
		CancelToken string `json:"cancel_token"`
		ExpiresAtMs int64  `json:"expires_at_ms"`
	}
	if err := json.Unmarshal(pubRec.Body.Bytes(), &pubResp); err != nil {
		t.Fatalf("unmarshal publish response: %v", err)
	}
	if !pairing.ValidShortCode(pubResp.Code) {
		t.Fatalf("invalid allocated code %q", pubResp.Code)
	}

	redeemReqID, err := pairing.NewShortCodeRequestID(now)
	if err != nil {
		t.Fatalf("NewShortCodeRequestID: %v", err)
	}
	redeemBody, _ := json.Marshal(map[string]string{
		"request_id": redeemReqID,
		"code":       pubResp.Code,
	})
	redeemReq := httptest.NewRequest(http.MethodPost, "/v1/codes/redeem", bytes.NewReader(redeemBody))
	redeemReq.RemoteAddr = net.JoinHostPort(testIP(20), "51000")
	redeemRec := httptest.NewRecorder()
	handler.ServeHTTP(redeemRec, redeemReq)
	if redeemRec.Code != http.StatusOK {
		t.Fatalf("POST /v1/codes/redeem status = %d, body = %s", redeemRec.Code, redeemRec.Body.String())
	}

	otherReqID, err := pairing.NewShortCodeRequestID(now)
	if err != nil {
		t.Fatalf("NewShortCodeRequestID: %v", err)
	}
	otherBody, _ := json.Marshal(map[string]string{
		"request_id": otherReqID,
		"code":       pubResp.Code,
	})
	otherReq := httptest.NewRequest(http.MethodPost, "/v1/codes/redeem", bytes.NewReader(otherBody))
	otherReq.RemoteAddr = net.JoinHostPort(testIP(21), "51001")
	otherRec := httptest.NewRecorder()
	handler.ServeHTTP(otherRec, otherReq)
	if otherRec.Code != http.StatusNotFound {
		t.Fatalf("second claimant status = %d, want 404", otherRec.Code)
	}

	cancelBody, _ := json.Marshal(map[string]string{
		"code":         pubResp.Code,
		"cancel_token": pubResp.CancelToken,
	})
	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/codes/cancel", bytes.NewReader(cancelBody))
	cancelReq.RemoteAddr = net.JoinHostPort(testIP(10), "43211")
	cancelRec := httptest.NewRecorder()
	handler.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusNoContent {
		t.Fatalf("POST /v1/codes/cancel status = %d, want 204", cancelRec.Code)
	}
}

func TestRelayTrustProxyRateLimitBucketing(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	t.Run("trustProxy=false ignores forwarded headers", func(t *testing.T) {
		exchange := pairing.NewShortCodeExchange(func() time.Time { return now })
		handler := buildHandler(exchange, false)
		proxyPeer := net.JoinHostPort(testIP(1), "9000")

		for i := 0; i < 6; i++ {
			reqID, _ := pairing.NewShortCodeRequestID(now)
			body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
			req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
			req.RemoteAddr = proxyPeer
			req.Header.Set("CF-Connecting-IP", testIP(byte(100+i)))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("attempt %d status = %d, want 201", i+1, rec.Code)
			}
		}

		reqID, _ := pairing.NewShortCodeRequestID(now)
		body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
		req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
		req.RemoteAddr = proxyPeer
		req.Header.Set("CF-Connecting-IP", testIP(200))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("7th attempt status = %d, want 429", rec.Code)
		}
	})

	t.Run("trustProxy=true buckets by rightmost untrusted XFF from trusted proxy", func(t *testing.T) {
		exchange := pairing.NewShortCodeExchange(func() time.Time { return now })
		handler := buildHandler(exchange, true)
		proxyPeer := "127.0.0.1:9000"

		for i := 0; i < 7; i++ {
			reqID, _ := pairing.NewShortCodeRequestID(now)
			body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
			req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
			req.RemoteAddr = proxyPeer
			req.Header.Set("X-Forwarded-For", "203.0.113.99, "+testIP(byte(100+i))+", 127.0.0.1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("client %d status = %d, want 201", i+1, rec.Code)
			}
		}
	})

	t.Run("trustProxy=true ignores forwarded headers from untrusted public peer", func(t *testing.T) {
		exchange := pairing.NewShortCodeExchange(func() time.Time { return now })
		handler := buildHandler(exchange, true)
		attackerPeer := net.JoinHostPort(testIP(1), "9000")

		for i := 0; i < 6; i++ {
			reqID, _ := pairing.NewShortCodeRequestID(now)
			body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
			req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
			req.RemoteAddr = attackerPeer
			req.Header.Set("X-Forwarded-For", testIP(byte(100+i)))
			req.Header.Set("CF-Connecting-IP", testIP(byte(150+i)))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("attempt %d status = %d, want 201", i+1, rec.Code)
			}
		}

		reqID, _ := pairing.NewShortCodeRequestID(now)
		body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
		req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
		req.RemoteAddr = attackerPeer
		req.Header.Set("X-Forwarded-For", testIP(200))
		req.Header.Set("CF-Connecting-IP", testIP(201))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("7th spoofed attempt from untrusted peer status = %d, want 429", rec.Code)
		}
	})

	t.Run("trustProxy=true rejects spoofed left XFF and CF-Connecting-IP by default", func(t *testing.T) {
		exchange := pairing.NewShortCodeExchange(func() time.Time { return now })
		handler := buildHandler(exchange, true)
		proxyPeer := "127.0.0.1:9000"
		realClient := testIP(77)

		for i := 0; i < 6; i++ {
			reqID, _ := pairing.NewShortCodeRequestID(now)
			body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
			req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
			req.RemoteAddr = proxyPeer
			req.Header.Set("CF-Connecting-IP", testIP(byte(10+i)))
			req.Header.Set("X-Forwarded-For", testIP(byte(120+i))+", "+realClient)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("attempt %d status = %d, want 201", i+1, rec.Code)
			}
		}

		reqID, _ := pairing.NewShortCodeRequestID(now)
		body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
		req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
		req.RemoteAddr = proxyPeer
		req.Header.Set("CF-Connecting-IP", testIP(99))
		req.Header.Set("X-Forwarded-For", testIP(220)+", "+realClient)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("7th attempt with spoofed left XFF/CF header status = %d, want 429", rec.Code)
		}
	})

	t.Run("trustProxy=true with trustCF=true honors CF-Connecting-IP from trusted proxy", func(t *testing.T) {
		exchange := pairing.NewShortCodeExchange(func() time.Time { return now })
		handler := buildHandlerWithConfig(exchange, relayConfig{
			TrustProxy:     true,
			TrustedProxies: defaultTrustedProxyPrefixes(),
			TrustCF:        true,
		})
		proxyPeer := "127.0.0.1:9000"

		for i := 0; i < 7; i++ {
			reqID, _ := pairing.NewShortCodeRequestID(now)
			body, _ := json.Marshal(map[string]any{"request_id": reqID, "ticket": "t"})
			req := httptest.NewRequest(http.MethodPost, "/v1/codes", bytes.NewReader(body))
			req.RemoteAddr = proxyPeer
			req.Header.Set("CF-Connecting-IP", testIP(byte(100+i)))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("CF client %d status = %d, want 201", i+1, rec.Code)
			}
		}
	})
}

func TestLoadConfigAndParseBoolEnv(t *testing.T) {
	t.Setenv("HOXI_PAIRING_RELAY_ADDR", "")
	t.Setenv("HOXI_PAIRING_RELAY_TRUST_PROXY", "")
	t.Setenv("HOXI_PAIRING_RELAY_TRUSTED_PROXIES", "")
	t.Setenv("HOXI_PAIRING_RELAY_TRUST_CF", "")

	cases := map[string]bool{
		"1":     true,
		"true":  true,
		"TRUE":  true,
		"yes":   true,
		"on":    true,
		"0":     false,
		"false": false,
		"":      false,
		"other": false,
	}
	for raw, want := range cases {
		if got := parseBoolEnv(raw); got != want {
			t.Errorf("parseBoolEnv(%q) = %t, want %t", raw, got, want)
		}
	}

	cfg, err := loadConfig([]string{"-addr", ":9090", "-trust-proxy", "-trust-cf", "-trusted-proxies", "127.0.0.1/32,10.0.0.0/8"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Addr != ":9090" || !cfg.TrustProxy || !cfg.TrustCF || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("unexpected cfg = %+v", cfg)
	}
	if _, err := loadConfig([]string{"-trusted-proxies", "not-a-cidr"}); err == nil {
		t.Fatal("expected error for invalid -trusted-proxies value")
	}
	if !isLoopbackListenAddr("127.0.0.1:8787") || !isLoopbackListenAddr("[::1]:8787") || isLoopbackListenAddr(":8787") {
		t.Fatal("unexpected isLoopbackListenAddr results")
	}
}
