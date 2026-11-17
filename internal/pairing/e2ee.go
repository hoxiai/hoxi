package pairing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// E2EEHeader is the HTTP header carrying per-request E2EE metadata on
	// requests and signaling envelope/stream mode ("v1" or "v1-sse") on responses.
	E2EEHeader = "X-Hoxi-E2EE"

	E2EEVersionHeader    = "v1"
	E2EEVersionSSEHeader = "v1-sse"

	e2eeNonceSize           = 12
	e2eeMaxClockSkewSeconds = 300
	e2eeMaxReplayEntries    = 4096
)

var (
	ErrInvalidE2EEKey      = errors.New("invalid e2ee key")
	ErrInvalidE2EEHeader   = errors.New("invalid e2ee header")
	ErrExpiredE2EERequest  = errors.New("e2ee timestamp out of range")
	ErrReplayedE2EERequest = errors.New("replayed e2ee request")
	ErrInvalidE2EEEnvelope = errors.New("invalid e2ee envelope")
)

// E2EEEnvelope is the JSON wire envelope carrying AES-256-GCM encrypted HTTP
// request or response payloads across LAN, Cloudflare, and GoPanel relays.
type E2EEEnvelope struct {
	Version     int    `json:"_hoxi_e2ee"`
	ContentType string `json:"ct,omitempty"`
	Seq         uint64 `json:"seq,omitempty"`
	Nonce       string `json:"n"`
	Ciphertext  string `json:"c"`
}

// E2EERequestMeta holds verified per-request parameters parsed from X-Hoxi-E2EE.
type E2EERequestMeta struct {
	ID      string
	TS      string
	HasBody bool
}

// E2EECipher performs directional AES-256-GCM AEAD and replay protection
// derived from a 32-byte (64-hex) pairing E2EE master key using pure Go stdlib.
type E2EECipher struct {
	keyHex  string
	reqAEAD cipher.AEAD
	resAEAD cipher.AEAD
	macKey  []byte
	now     func() time.Time

	replayMu sync.Mutex
	seenIDs  map[string]int64
}

// NewE2EECipher constructs a directional cipher suite from a 64-char hex master key.
func NewE2EECipher(hexKey string) (*E2EECipher, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(raw) != 32 {
		return nil, ErrInvalidE2EEKey
	}
	reqKey := deriveE2EESubkey(raw, "hoxi-e2ee-v1/req")
	resKey := deriveE2EESubkey(raw, "hoxi-e2ee-v1/resp")
	macKey := deriveE2EESubkey(raw, "hoxi-e2ee-v1/head")

	reqBlock, err := aes.NewCipher(reqKey)
	if err != nil {
		return nil, err
	}
	reqAEAD, err := cipher.NewGCM(reqBlock)
	if err != nil {
		return nil, err
	}
	resBlock, err := aes.NewCipher(resKey)
	if err != nil {
		return nil, err
	}
	resAEAD, err := cipher.NewGCM(resBlock)
	if err != nil {
		return nil, err
	}
	return &E2EECipher{
		keyHex:  strings.ToLower(strings.TrimSpace(hexKey)),
		reqAEAD: reqAEAD,
		resAEAD: resAEAD,
		macKey:  macKey,
		now:     time.Now,
		seenIDs: make(map[string]int64),
	}, nil
}

func deriveE2EESubkey(master []byte, label string) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte(label))
	return mac.Sum(nil)
}

// BuildRequestHeader formats a signed X-Hoxi-E2EE header for tests and Go clients.
func (c *E2EECipher) BuildRequestHeader(method, cleanPath, id string, unixTS int64, hasBody bool) string {
	ts := strconv.FormatInt(unixTS, 10)
	bodyFlag := "0"
	if hasBody {
		bodyFlag = "1"
	}
	mac := hmac.New(sha256.New, c.macKey)
	_, _ = fmt.Fprintf(mac, "hoxi-e2ee-v1:head:%s:%s:%s:%s:%s", strings.ToUpper(strings.TrimSpace(method)), cleanPath, id, ts, bodyFlag)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if hasBody {
		return fmt.Sprintf("v1;id=%s;ts=%s;body=1;sig=%s", id, ts, sig)
	}
	return fmt.Sprintf("v1;id=%s;ts=%s;sig=%s", id, ts, sig)
}

// VerifyRequestHeader authenticates the X-Hoxi-E2EE header MAC, enforces the
// 5-minute clock-skew window, and rejects replayed request IDs.
func (c *E2EECipher) VerifyRequestHeader(rawHeader, method, cleanPath string) (E2EERequestMeta, error) {
	parts := strings.Split(strings.TrimSpace(rawHeader), ";")
	if len(parts) < 3 || strings.TrimSpace(parts[0]) != E2EEVersionHeader {
		return E2EERequestMeta{}, ErrInvalidE2EEHeader
	}
	var id, ts, sig string
	hasBody := false
	for _, part := range parts[1:] {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return E2EERequestMeta{}, ErrInvalidE2EEHeader
		}
		switch strings.TrimSpace(k) {
		case "id":
			id = strings.TrimSpace(v)
		case "ts":
			ts = strings.TrimSpace(v)
		case "sig":
			sig = strings.TrimSpace(v)
		case "body":
			hasBody = strings.TrimSpace(v) == "1"
		}
	}
	if len(id) < 12 || len(id) > 64 || ts == "" || sig == "" {
		return E2EERequestMeta{}, ErrInvalidE2EEHeader
	}
	parsedTS, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return E2EERequestMeta{}, ErrInvalidE2EEHeader
	}
	nowUnix := c.now().Unix()
	diff := nowUnix - parsedTS
	if diff < -e2eeMaxClockSkewSeconds || diff > e2eeMaxClockSkewSeconds {
		return E2EERequestMeta{}, ErrExpiredE2EERequest
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || len(gotSig) != sha256.Size {
		return E2EERequestMeta{}, ErrInvalidE2EEHeader
	}
	bodyFlag := "0"
	if hasBody {
		bodyFlag = "1"
	}
	mac := hmac.New(sha256.New, c.macKey)
	_, _ = fmt.Fprintf(mac, "hoxi-e2ee-v1:head:%s:%s:%s:%s:%s", strings.ToUpper(strings.TrimSpace(method)), cleanPath, id, ts, bodyFlag)
	wantSig := mac.Sum(nil)
	if subtle.ConstantTimeCompare(gotSig, wantSig) != 1 {
		return E2EERequestMeta{}, ErrInvalidE2EEHeader
	}
	if !c.recordReplayID(id, nowUnix) {
		return E2EERequestMeta{}, ErrReplayedE2EERequest
	}
	return E2EERequestMeta{ID: id, TS: ts, HasBody: hasBody}, nil
}

func (c *E2EECipher) recordReplayID(id string, nowUnix int64) bool {
	c.replayMu.Lock()
	defer c.replayMu.Unlock()
	cutoff := nowUnix - e2eeMaxClockSkewSeconds
	if exp, exists := c.seenIDs[id]; exists && exp >= cutoff {
		return false
	}
	if len(c.seenIDs) >= e2eeMaxReplayEntries {
		for k, ts := range c.seenIDs {
			if ts < cutoff {
				delete(c.seenIDs, k)
			}
		}
		if len(c.seenIDs) >= e2eeMaxReplayEntries {
			var oldestKey string
			var oldestTS int64
			first := true
			for k, ts := range c.seenIDs {
				if first || ts < oldestTS {
					oldestKey, oldestTS, first = k, ts, false
				}
			}
			if !first {
				delete(c.seenIDs, oldestKey)
			}
		}
	}
	c.seenIDs[id] = nowUnix
	return true
}

// SealRequest encrypts a client->desktop request body into a JSON envelope.
func (c *E2EECipher) SealRequest(meta E2EERequestMeta, method, cleanPath, contentType string, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, e2eeNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.sealRequestWithNonce(nonce, meta, method, cleanPath, contentType, plaintext)
}

func (c *E2EECipher) sealRequestWithNonce(nonce []byte, meta E2EERequestMeta, method, cleanPath, contentType string, plaintext []byte) ([]byte, error) {
	aad := requestAAD(meta.ID, method, cleanPath, meta.TS)
	sealed := c.reqAEAD.Seal(nil, nonce, plaintext, aad)
	return json.Marshal(E2EEEnvelope{
		Version:     1,
		ContentType: contentType,
		Nonce:       base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:  base64.RawURLEncoding.EncodeToString(sealed),
	})
}

// OpenRequest verifies and decrypts a client->desktop JSON request envelope.
func (c *E2EECipher) OpenRequest(meta E2EERequestMeta, method, cleanPath string, rawEnvelope []byte) (plaintext []byte, contentType string, err error) {
	var env E2EEEnvelope
	if err := json.Unmarshal(rawEnvelope, &env); err != nil || env.Version != 1 {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	nonce, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(env.Nonce))
	if err != nil || len(nonce) != e2eeNonceSize {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	ct, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(env.Ciphertext))
	if err != nil {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	aad := requestAAD(meta.ID, method, cleanPath, meta.TS)
	plain, err := c.reqAEAD.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	return plain, strings.TrimSpace(env.ContentType), nil
}

// SealResponse encrypts a desktop->client HTTP response payload into a JSON envelope.
func (c *E2EECipher) SealResponse(reqID, method, cleanPath string, status int, contentType string, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, e2eeNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.sealResponseWithNonce(nonce, reqID, method, cleanPath, status, contentType, plaintext)
}

func (c *E2EECipher) sealResponseWithNonce(nonce []byte, reqID, method, cleanPath string, status int, contentType string, plaintext []byte) ([]byte, error) {
	aad := responseAAD(reqID, method, cleanPath, status)
	sealed := c.resAEAD.Seal(nil, nonce, plaintext, aad)
	return json.Marshal(E2EEEnvelope{
		Version:     1,
		ContentType: contentType,
		Nonce:       base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:  base64.RawURLEncoding.EncodeToString(sealed),
	})
}

// OpenResponse verifies and decrypts a desktop->client JSON response envelope.
func (c *E2EECipher) OpenResponse(reqID, method, cleanPath string, status int, rawEnvelope []byte) (plaintext []byte, contentType string, err error) {
	var env E2EEEnvelope
	if err := json.Unmarshal(rawEnvelope, &env); err != nil || env.Version != 1 {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	nonce, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(env.Nonce))
	if err != nil || len(nonce) != e2eeNonceSize {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	ct, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(env.Ciphertext))
	if err != nil {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	aad := responseAAD(reqID, method, cleanPath, status)
	plain, err := c.resAEAD.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, "", ErrInvalidE2EEEnvelope
	}
	return plain, strings.TrimSpace(env.ContentType), nil
}

// SealSSEData encrypts a single SSE "data:" line payload with sequence binding.
func (c *E2EECipher) SealSSEData(reqID, cleanPath string, seq uint64, line []byte) ([]byte, error) {
	nonce := make([]byte, e2eeNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	aad := sseAAD(reqID, cleanPath, seq)
	sealed := c.resAEAD.Seal(nil, nonce, line, aad)
	return json.Marshal(E2EEEnvelope{
		Version:    1,
		Seq:        seq,
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(sealed),
	})
}

// OpenSSEData verifies sequence continuity and decrypts an SSE "data:" frame.
func (c *E2EECipher) OpenSSEData(reqID, cleanPath string, expectedSeq uint64, rawEnvelope []byte) ([]byte, error) {
	var env E2EEEnvelope
	if err := json.Unmarshal(rawEnvelope, &env); err != nil || env.Version != 1 || env.Seq != expectedSeq {
		return nil, ErrInvalidE2EEEnvelope
	}
	nonce, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(env.Nonce))
	if err != nil || len(nonce) != e2eeNonceSize {
		return nil, ErrInvalidE2EEEnvelope
	}
	ct, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(env.Ciphertext))
	if err != nil {
		return nil, ErrInvalidE2EEEnvelope
	}
	plain, err := c.resAEAD.Open(nil, nonce, ct, sseAAD(reqID, cleanPath, expectedSeq))
	if err != nil {
		return nil, ErrInvalidE2EEEnvelope
	}
	return plain, nil
}

func requestAAD(id, method, cleanPath, ts string) []byte {
	return []byte(fmt.Sprintf("hoxi-e2ee-v1:req:%s:%s:%s:%s", id, strings.ToUpper(strings.TrimSpace(method)), cleanPath, ts))
}

func responseAAD(id, method, cleanPath string, status int) []byte {
	return []byte(fmt.Sprintf("hoxi-e2ee-v1:resp:%s:%s:%s:%d", id, strings.ToUpper(strings.TrimSpace(method)), cleanPath, status))
}

func sseAAD(id, cleanPath string, seq uint64) []byte {
	return []byte(fmt.Sprintf("hoxi-e2ee-v1:sse:%s:%s:%d", id, cleanPath, seq))
}
