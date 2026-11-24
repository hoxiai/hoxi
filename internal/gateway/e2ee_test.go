package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/pairing"
)

func TestRemoteE2EEEnvelopeProtectsRelayAndEnforcesBodyGuards(t *testing.T) {
	g, lanToken, relayToken := ingressGateway(t)
	e2eeKey, err := g.pairing.E2EEKey()
	if err != nil {
		t.Fatalf("E2EEKey: %v", err)
	}
	clientCipher, err := pairing.NewE2EECipher(e2eeKey)
	if err != nil {
		t.Fatalf("NewE2EECipher: %v", err)
	}

	lanBootReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	lanBootReq.Host = "192.168.1.5:5417"
	lanBootReq.RemoteAddr = "192.168.1.30:40000"
	lanBootReq.Header.Set("X-Auth", lanToken)
	lanBootRec := httptest.NewRecorder()
	g.ServeLANRequest(lanBootRec, lanBootReq)
	if lanBootRec.Code != http.StatusOK {
		t.Fatalf("LAN bootstrap = %d", lanBootRec.Code)
	}
	var lanBoot map[string]any
	if err := json.Unmarshal(lanBootRec.Body.Bytes(), &lanBoot); err != nil {
		t.Fatal(err)
	}
	if lanBoot["e2eeKey"] != e2eeKey {
		t.Fatalf("LAN bootstrap e2eeKey = %v, want %q", lanBoot["e2eeKey"], e2eeKey)
	}

	now := time.Now().Unix()
	reqID := "e2ee_boot_req_0001"
	hdr := clientCipher.BuildRequestHeader(http.MethodGet, "/api/hoxi/bootstrap", reqID, now, false)
	relayReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	relayReq.Host = "abc123.node.hoxi.ai"
	relayReq.RemoteAddr = "203.0.113.9:51000"
	relayReq.Header.Set("X-Auth", relayToken)
	relayReq.Header.Set(pairing.E2EEHeader, hdr)
	relayRec := httptest.NewRecorder()
	g.ServeIngressRequest(relayRec, relayReq)
	if relayRec.Code != http.StatusOK {
		t.Fatalf("encrypted ingress bootstrap = %d %s", relayRec.Code, relayRec.Body.String())
	}
	if got := relayRec.Header().Get(pairing.E2EEHeader); got != pairing.E2EEVersionHeader {
		t.Fatalf("response X-Hoxi-E2EE = %q, want v1", got)
	}
	wireStr := relayRec.Body.String()
	if strings.Contains(wireStr, g.rootDir) || strings.Contains(wireStr, `"appVersion"`) {
		t.Fatalf("wire response leaked plaintext bootstrap fields: %s", wireStr)
	}
	plainBoot, ct, err := clientCipher.OpenResponse(reqID, http.MethodGet, "/api/hoxi/bootstrap", http.StatusOK, relayRec.Body.Bytes())
	if err != nil {
		t.Fatalf("OpenResponse: %v", err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("decrypted content-type = %q", ct)
	}
	var relayBoot map[string]any
	if err := json.Unmarshal(plainBoot, &relayBoot); err != nil {
		t.Fatal(err)
	}
	if _, leaked := relayBoot["e2eeKey"]; leaked {
		t.Fatal("relay bootstrap must never include e2eeKey")
	}
	if relayBoot["rootDir"] != g.rootDir {
		t.Fatalf("decrypted rootDir = %v, want %q", relayBoot["rootDir"], g.rootDir)
	}

	replayReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	replayReq.Host = "abc123.node.hoxi.ai"
	replayReq.RemoteAddr = "203.0.113.9:51000"
	replayReq.Header.Set("X-Auth", relayToken)
	replayReq.Header.Set(pairing.E2EEHeader, hdr)
	replayRec := httptest.NewRecorder()
	g.ServeIngressRequest(replayRec, replayReq)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed E2EE request status = %d, want 401", replayRec.Code)
	}

	commitID := "e2ee_commit_push_1"
	commitTS := fmt.Sprintf("%d", now)
	commitHdr := clientCipher.BuildRequestHeader(http.MethodPost, "/api/hoxi/worktree/commit", commitID, now, true)
	sealedCommit, err := clientCipher.SealRequest(
		pairing.E2EERequestMeta{ID: commitID, TS: commitTS, HasBody: true},
		http.MethodPost,
		"/api/hoxi/worktree/commit",
		"application/json",
		[]byte(`{"sessionID":"ses_1","message":"sneaky","push":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	commitReq := httptest.NewRequest(http.MethodPost, "/api/hoxi/worktree/commit", bytes.NewReader(sealedCommit))
	commitReq.Host = "abc123.node.hoxi.ai"
	commitReq.RemoteAddr = "203.0.113.9:51000"
	commitReq.Header.Set("X-Auth", relayToken)
	commitReq.Header.Set("Content-Type", "application/json")
	commitReq.Header.Set(pairing.E2EEHeader, commitHdr)
	commitRec := httptest.NewRecorder()
	g.ServeIngressRequest(commitRec, commitReq)
	if commitRec.Code != http.StatusForbidden {
		t.Fatalf("encrypted commit with push=true = %d, want 403", commitRec.Code)
	}
	if commitRec.Header().Get(pairing.E2EEHeader) != pairing.E2EEVersionHeader {
		t.Fatalf("403 response X-Hoxi-E2EE = %q, want v1", commitRec.Header().Get(pairing.E2EEHeader))
	}
	plainErr, _, err := clientCipher.OpenResponse(commitID, http.MethodPost, "/api/hoxi/worktree/commit", http.StatusForbidden, commitRec.Body.Bytes())
	if err != nil || !strings.Contains(string(plainErr), "not available") {
		t.Fatalf("decrypted 403 body = %q (err=%v)", plainErr, err)
	}
}

func TestRemoteE2EEStreamsSSEFramesIncrementally(t *testing.T) {
	g, _, relayToken := ingressGateway(t)
	e2eeKey, err := g.pairing.E2EEKey()
	if err != nil {
		t.Fatal(err)
	}
	clientCipher, err := pairing.NewE2EECipher(e2eeKey)
	if err != nil {
		t.Fatal(err)
	}

	var leakedHeader string
	g.mux = http.NewServeMux()
	g.mux.HandleFunc("/api/hoxi/event", func(w http.ResponseWriter, r *http.Request) {
		leakedHeader = r.Header.Get(pairing.E2EEHeader)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		ioWriteStringHelper(w, ": keepalive\n\ndata: {\"delta\":\"chunk-one\"}\n\ndata: {\"delta\":\"chunk-two\"}\n\n")
	})

	now := time.Now().Unix()
	reqID := "e2ee_sse_stream_01"
	hdr := clientCipher.BuildRequestHeader(http.MethodGet, "/api/hoxi/event", reqID, now, false)
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/event", nil)
	req.Host = "abc123.node.hoxi.ai"
	req.RemoteAddr = "203.0.113.9:51000"
	req.Header.Set("X-Auth", relayToken)
	req.Header.Set(pairing.E2EEHeader, hdr)
	rec := httptest.NewRecorder()
	g.ServeIngressRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SSE status = %d", rec.Code)
	}
	if leakedHeader != "" {
		t.Fatalf("X-Hoxi-E2EE leaked to downstream handler: %q", leakedHeader)
	}
	if got := rec.Header().Get(pairing.E2EEHeader); got != pairing.E2EEVersionSSEHeader {
		t.Fatalf("SSE X-Hoxi-E2EE = %q, want v1-sse", got)
	}
	rawSSE := rec.Body.String()
	if strings.Contains(rawSSE, "chunk-one") {
		t.Fatalf("SSE stream leaked plaintext: %s", rawSSE)
	}
	var decrypted []string
	var seq uint64
	for _, line := range strings.Split(rawSSE, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		seq++
		plain, openErr := clientCipher.OpenSSEData(reqID, "/api/hoxi/event", seq, []byte(strings.TrimPrefix(line, "data: ")))
		if openErr != nil {
			t.Fatalf("OpenSSEData(seq=%d): %v", seq, openErr)
		}
		decrypted = append(decrypted, string(plain))
	}
	if len(decrypted) != 2 || decrypted[0] != `{"delta":"chunk-one"}` || decrypted[1] != `{"delta":"chunk-two"}` {
		t.Fatalf("decrypted SSE lines = %v", decrypted)
	}
}

func TestRemoteX25519KEXBootstrapAndPtyBlocked(t *testing.T) {
	g, _, relayToken := ingressGateway(t)
	wantKeyHex, err := g.pairing.E2EEKey()
	if err != nil {
		t.Fatal(err)
	}

	clientPriv, err := ecdhGenerateX25519ForTest(t)
	if err != nil {
		t.Fatal(err)
	}
	kexReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	kexReq.Host = "abc123.node.hoxi.ai"
	kexReq.RemoteAddr = "203.0.113.9:51000"
	kexReq.Header.Set("X-Auth", relayToken)
	kexReq.Header.Set(pairing.KEXClientPubHeader, clientPriv.pubB64)
	kexRec := httptest.NewRecorder()
	g.ServeIngressRequest(kexRec, kexReq)
	if kexRec.Code != http.StatusOK {
		t.Fatalf("relay KEX bootstrap = %d %s", kexRec.Code, kexRec.Body.String())
	}
	if strings.Contains(kexRec.Body.String(), wantKeyHex) {
		t.Fatal("relay KEX bootstrap must never expose plaintext e2eeKey on wire")
	}
	var bootPayload struct {
		E2EEKey string                 `json:"e2eeKey"`
		E2EEKex *pairing.E2EEKEXBundle `json:"e2eeKex"`
	}
	if err := json.Unmarshal(kexRec.Body.Bytes(), &bootPayload); err != nil {
		t.Fatal(err)
	}
	if bootPayload.E2EEKey != "" || bootPayload.E2EEKex == nil {
		t.Fatalf("expected e2eeKex bundle without plaintext e2eeKey, got: %+v", bootPayload)
	}
	unwrappedHex, err := clientPriv.open(*bootPayload.E2EEKex)
	if err != nil || unwrappedHex != wantKeyHex {
		t.Fatalf("unwrapped E2EE key = %q (err=%v), want %q", unwrappedHex, err, wantKeyHex)
	}

	cipherSuite, err := pairing.NewE2EECipher(unwrappedHex)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	reqID := "kex_upgraded_req_01"
	hdr := cipherSuite.BuildRequestHeader(http.MethodGet, "/healthz", reqID, now, false)
	encReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	encReq.Host = "abc123.node.hoxi.ai"
	encReq.RemoteAddr = "203.0.113.9:51000"
	encReq.Header.Set("X-Auth", relayToken)
	encReq.Header.Set(pairing.E2EEHeader, hdr)
	encRec := httptest.NewRecorder()
	g.ServeIngressRequest(encRec, encReq)
	if encRec.Code != http.StatusOK || encRec.Header().Get(pairing.E2EEHeader) != pairing.E2EEVersionHeader {
		t.Fatalf("post-KEX encrypted /healthz failed: %d %s", encRec.Code, encRec.Body.String())
	}

	for _, ptyPath := range []string{"/pty", "/api/pty"} {
		ptyReq := httptest.NewRequest(http.MethodGet, ptyPath, nil)
		ptyReq.Host = "abc123.node.hoxi.ai"
		ptyReq.RemoteAddr = "203.0.113.9:51000"
		ptyReq.Header.Set("X-Auth", relayToken)
		ptyRec := httptest.NewRecorder()
		g.ServeIngressRequest(ptyRec, ptyReq)
		if ptyRec.Code != http.StatusForbidden {
			t.Fatalf("expected remote %s to return 403 Forbidden, got %d", ptyPath, ptyRec.Code)
		}
	}
}

func ioWriteStringHelper(w http.ResponseWriter, s string) {
	_, _ = w.Write([]byte(s))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestRemoteE2EERejectsEnvelopeWhenRelayStripsHeader(t *testing.T) {
	g, _, relayToken := ingressGateway(t)
	e2eeKey, err := g.pairing.E2EEKey()
	if err != nil {
		t.Fatal(err)
	}
	clientCipher, err := pairing.NewE2EECipher(e2eeKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	reqID := "e2ee_stripped_hdr_01"
	sealed, err := clientCipher.SealRequest(
		pairing.E2EERequestMeta{ID: reqID, TS: fmt.Sprintf("%d", now), HasBody: true},
		http.MethodPost,
		"/api/hoxi/sessions/ses_1/prompt_async",
		"application/json",
		[]byte(`{"text":"hello world"}`),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an older GoPanel relay that forwards the encrypted JSON body
	// while stripping the X-Hoxi-E2EE request header.
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/sessions/ses_1/prompt_async", bytes.NewReader(sealed))
	req.Host = "abc123.node.hoxi.ai"
	req.RemoteAddr = "203.0.113.9:51000"
	req.Header.Set("X-Auth", relayToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.ServeIngressRequest(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-Hoxi-E2EE") {
		t.Fatalf("expected error mentioning X-Hoxi-E2EE, got: %s", rec.Body.String())
	}
}
