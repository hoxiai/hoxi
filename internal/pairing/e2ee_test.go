package pairing

import (
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestE2EECipherRoundTripAndCrossVectors(t *testing.T) {
	buf := make([]byte, 32)
	for i := range buf {
		buf[i] = byte(i)
	}
	c, err := NewE2EECipher(hex.EncodeToString(buf))
	if err != nil {
		t.Fatalf("NewE2EECipher: %v", err)
	}
	fixedNow := time.Unix(1763300000, 0)
	c.now = func() time.Time { return fixedNow }

	nonce := make([]byte, 12)
	for i := range nonce {
		nonce[i] = byte(0xa0 + i)
	}
	reqID := "vec_req_id_12345"
	hdr := c.BuildRequestHeader(http.MethodPost, "/api/hoxi/sessions/s1/prompt", reqID, fixedNow.Unix(), true)
	meta, err := c.VerifyRequestHeader(hdr, http.MethodPost, "/api/hoxi/sessions/s1/prompt")
	if err != nil {
		t.Fatalf("VerifyRequestHeader: %v", err)
	}
	if _, err := c.VerifyRequestHeader(hdr, http.MethodPost, "/api/hoxi/sessions/s1/prompt"); !errors.Is(err, ErrReplayedE2EERequest) {
		t.Fatalf("replay error = %v, want ErrReplayedE2EERequest", err)
	}

	expiredHdr := c.BuildRequestHeader(http.MethodGet, "/api/hoxi/bootstrap", "req_expired_id_0001", fixedNow.Unix()-301, false)
	if _, err := c.VerifyRequestHeader(expiredHdr, http.MethodGet, "/api/hoxi/bootstrap"); !errors.Is(err, ErrExpiredE2EERequest) {
		t.Fatalf("expired header error = %v, want ErrExpiredE2EERequest", err)
	}

	sealedReq, err := c.sealRequestWithNonce(nonce, meta, http.MethodPost, "/api/hoxi/sessions/s1/prompt", "application/json", []byte(`{"hello":"hoxi"}`))
	if err != nil {
		t.Fatalf("sealRequestWithNonce: %v", err)
	}
	if strings.Contains(string(sealedReq), "hello") {
		t.Fatalf("ciphertext leaked plaintext: %s", sealedReq)
	}
	gotReq, ct, err := c.OpenRequest(meta, http.MethodPost, "/api/hoxi/sessions/s1/prompt", sealedReq)
	if err != nil || string(gotReq) != `{"hello":"hoxi"}` || ct != "application/json" {
		t.Fatalf("OpenRequest = %q, %q, err=%v", gotReq, ct, err)
	}
	if _, _, err := c.OpenRequest(meta, http.MethodPost, "/api/hoxi/worktree/commit", sealedReq); !errors.Is(err, ErrInvalidE2EEEnvelope) {
		t.Fatalf("cross-path OpenRequest = %v, want ErrInvalidE2EEEnvelope", err)
	}

	sealedResp, err := c.sealResponseWithNonce(nonce, reqID, http.MethodPost, "/api/hoxi/sessions/s1/prompt", 200, "application/json", []byte(`{"status":"ok"}`))
	if err != nil {
		t.Fatalf("sealResponseWithNonce: %v", err)
	}
	if _, _, err := c.OpenRequest(meta, http.MethodPost, "/api/hoxi/sessions/s1/prompt", sealedResp); !errors.Is(err, ErrInvalidE2EEEnvelope) {
		t.Fatalf("reflected response into OpenRequest = %v, want ErrInvalidE2EEEnvelope", err)
	}
	if _, _, err := c.OpenResponse(reqID, http.MethodPost, "/api/hoxi/sessions/s1/prompt", 500, sealedResp); !errors.Is(err, ErrInvalidE2EEEnvelope) {
		t.Fatalf("status-tampered OpenResponse = %v, want ErrInvalidE2EEEnvelope", err)
	}
	gotResp, _, err := c.OpenResponse(reqID, http.MethodPost, "/api/hoxi/sessions/s1/prompt", 200, sealedResp)
	if err != nil || string(gotResp) != `{"status":"ok"}` {
		t.Fatalf("OpenResponse = %q, err=%v", gotResp, err)
	}

	sseFrame, err := c.SealSSEData(reqID, "/api/hoxi/event", 1, []byte(`{"delta":"abc"}`))
	if err != nil {
		t.Fatalf("SealSSEData: %v", err)
	}
	if _, err := c.OpenSSEData(reqID, "/api/hoxi/event", 2, sseFrame); err == nil {
		t.Fatal("wrong SSE seq must fail")
	}
	gotSSE, err := c.OpenSSEData(reqID, "/api/hoxi/event", 1, sseFrame)
	if err != nil || string(gotSSE) != `{"delta":"abc"}` {
		t.Fatalf("OpenSSEData = %q, err=%v", gotSSE, err)
	}

	t.Logf("VECTOR_HDR=%s", hdr)
	t.Logf("VECTOR_REQ=%s", sealedReq)
	t.Logf("VECTOR_RESP=%s", sealedResp)
}

func TestDirectEndpointsIncludePrimaryAndOverlayPeers(t *testing.T) {
	ulaIP := net.IP{0xfd, 0x7a, 0x11, 0x5c, 0xa1, 0xe0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 7}
	if !isTailscaleULAIPv6(ulaIP) {
		t.Fatalf("expected Tailscale ULA IPv6 match for %v", ulaIP)
	}
	svc, _ := newTestService(t, "192.168.1.20")
	svc.lanPeers = func() []string {
		return []string{"192.168.1.20", "100.90.1.2", ulaIP.String()}
	}
	code, err := svc.Code()
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if len(code.E2EEKey) != 64 || code.E2EEKey == code.Token {
		t.Fatalf("unexpected E2EEKey = %q", code.E2EEKey)
	}
	direct := svc.DirectEndpoints()
	if len(direct) != 3 || direct[0] != "http://192.168.1.20:5417" || direct[1] != "http://100.90.1.2:5417" {
		t.Fatalf("DirectEndpoints() = %v", direct)
	}
	rotated, err := svc.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.E2EEKey == code.E2EEKey || len(rotated.E2EEKey) != 64 {
		t.Fatalf("Rotate must issue fresh E2EEKey")
	}
}
