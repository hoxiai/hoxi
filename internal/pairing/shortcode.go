package pairing

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	// MaxShortCodeTTL caps how long a 6-digit pairing code remains redeemable.
	MaxShortCodeTTL = 60 * time.Second

	// ReservedDemoShortCode is reserved for mobile's local offline demo host
	// and is never allocated by the pairing ticket exchange.
	ReservedDemoShortCode = "888888"

	shortCodeMaxClockLead   = 5 * time.Second
	shortCodeMaxTicketBytes = 8 * 1024
	shortCodeMaxBodyBytes   = 12 * 1024
	shortCodeMaxActiveSlots = 256
	shortCodeMaxRateEntries = 8192

	shortCodeGlobalPerMinLimit  = 1200
	shortCodePublishPerMinLimit = 6
	shortCodeRedeemPerMinLimit  = 5
)

var (
	ErrInvalidShortCode     = errors.New("invalid 6-digit pairing code")
	ErrInvalidRequestID     = errors.New("invalid pairing request id")
	ErrShortCodeUnavailable = errors.New("pairing code is unavailable or expired")
	ErrShortCodeConflict    = errors.New("pairing request id conflict")
	ErrShortCodeRateLimited = errors.New("pairing service rate limit exceeded")
)

type shortCodeRecord struct {
	Code            string
	Ticket          string
	PublishReqID    string
	CancelToken     string
	ExpiresAtMs     int64
	WinnerReqID     string
	Cancelled       bool
	CreatedAtMinute int64
}

type rateWindowKey struct {
	minute int64
	action string
	source string
}

// ShortCodeExchange is a zero-dependency, concurrency-safe 6-digit one-time
// pairing ticket exchange that can run embedded in GoPanel, a standalone edge
// relay, or in-process tests.
//
// Security boundaries:
//   - Codes expire in at most 60 seconds and are single-winner on redemption.
//   - Codes and tickets travel only in HTTPS POST JSON bodies, never in URLs.
//   - Tickets must never contain e2eeKey or private credentials; clients run
//     X25519 KEX against the desktop directly after redeeming the ticket.
type ShortCodeExchange struct {
	mu          sync.Mutex
	now         func() time.Time
	byCode      map[string]*shortCodeRecord
	byPubReqID  map[string]*shortCodeRecord
	rateCounts  map[rateWindowKey]int
	globalCount map[string]int // key: "<minute>:<action>"
}

// NewShortCodeExchange creates a new in-memory pairing ticket exchange.
func NewShortCodeExchange(now func() time.Time) *ShortCodeExchange {
	if now == nil {
		now = time.Now
	}
	return &ShortCodeExchange{
		now:         now,
		byCode:      make(map[string]*shortCodeRecord),
		byPubReqID:  make(map[string]*shortCodeRecord),
		rateCounts:  make(map[rateWindowKey]int),
		globalCount: make(map[string]int),
	}
}

// NewShortCodeRequestID generates a 64-char lowercase hex request ID:
// 12-char Unix millisecond timestamp + 52 random hex chars (208 random bits).
func NewShortCodeRequestID(now time.Time) (string, error) {
	ms := now.UnixMilli()
	if ms < 0 {
		return "", ErrInvalidRequestID
	}
	var random [26]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%012x%s", uint64(ms), hex.EncodeToString(random[:])), nil
}

// ValidShortCode reports whether code is a 6-digit ASCII numeric PIN.
func ValidShortCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < 6; i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// ValidShortCodeRequestID reports whether id is 64 lowercase hex characters.
func ValidShortCodeRequestID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := id[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func parseRequestIDTimestampMs(id string) (int64, bool) {
	if !ValidShortCodeRequestID(id) {
		return 0, false
	}
	var ts uint64
	for i := 0; i < 12; i++ {
		c := id[i]
		ts <<= 4
		if c >= '0' && c <= '9' {
			ts |= uint64(c - '0')
		} else {
			ts |= uint64(c-'a') + 10
		}
	}
	return int64(ts), true
}

func (e *ShortCodeExchange) validateRequestIDFresh(id string, nowMs int64) error {
	tsMs, ok := parseRequestIDTimestampMs(id)
	if !ok {
		return ErrInvalidRequestID
	}
	if tsMs > nowMs+shortCodeMaxClockLead.Milliseconds() {
		return ErrInvalidRequestID
	}
	if nowMs-tsMs > MaxShortCodeTTL.Milliseconds() {
		return ErrShortCodeUnavailable
	}
	return nil
}

func (e *ShortCodeExchange) pruneLocked(nowMs int64) {
	curMin := nowMs / 60_000
	for code, rec := range e.byCode {
		if rec.Cancelled || nowMs >= rec.ExpiresAtMs {
			delete(e.byCode, code)
			delete(e.byPubReqID, rec.PublishReqID)
		}
	}
	for k := range e.rateCounts {
		if curMin-k.minute >= 2 {
			delete(e.rateCounts, k)
		}
	}
	for gk := range e.globalCount {
		var m int64
		if _, err := fmt.Sscanf(gk, "%d:", &m); err == nil && curMin-m >= 2 {
			delete(e.globalCount, gk)
		}
	}
}

func (e *ShortCodeExchange) consumeRateLocked(nowMs int64, action, source string, perSourceLimit int) bool {
	curMin := nowMs / 60_000
	if source == "" {
		source = "unknown"
	}
	sKey := rateWindowKey{minute: curMin, action: action, source: source}
	if e.rateCounts[sKey] >= perSourceLimit {
		return false
	}
	gKey := fmt.Sprintf("%d:%s", curMin, action)
	if e.globalCount[gKey] >= shortCodeGlobalPerMinLimit {
		return false
	}
	if _, exists := e.rateCounts[sKey]; !exists && len(e.rateCounts) >= shortCodeMaxRateEntries {
		return false
	}
	e.rateCounts[sKey]++
	e.globalCount[gKey]++
	return true
}

func (e *ShortCodeExchange) allocateCodeLocked() (string, error) {
	for attempt := 0; attempt < 64; attempt++ {
		var buf [4]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", err
		}
		n := binary.BigEndian.Uint32(buf[:]) % 1_000_000
		code := fmt.Sprintf("%06d", n)
		if code == ReservedDemoShortCode {
			continue
		}
		if _, exists := e.byCode[code]; !exists {
			return code, nil
		}
	}
	return "", errors.New("failed to allocate unique pairing code")
}

// Publish stores ticket under a freshly allocated 6-digit code with a hard
// 60-second TTL cap. Retrying with the same requestID and identical ticket
// returns the original allocation idempotently; changing the ticket for an
// existing requestID returns ErrShortCodeConflict.
func (e *ShortCodeExchange) Publish(source, requestID, ticket string, requestedExpiresAtMs int64) (code, cancelToken string, expiresAtMs int64, err error) {
	if len(ticket) == 0 || len(ticket) > shortCodeMaxTicketBytes {
		return "", "", 0, errors.New("invalid pairing ticket size")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.now()
	nowMs := now.UnixMilli()
	e.pruneLocked(nowMs)

	if err := e.validateRequestIDFresh(requestID, nowMs); err != nil {
		return "", "", 0, err
	}
	if existing, ok := e.byPubReqID[requestID]; ok {
		if existing.Ticket != ticket {
			return "", "", 0, ErrShortCodeConflict
		}
		if existing.Cancelled || nowMs >= existing.ExpiresAtMs {
			return "", "", 0, ErrShortCodeUnavailable
		}
		return existing.Code, existing.CancelToken, existing.ExpiresAtMs, nil
	}
	if !e.consumeRateLocked(nowMs, "publish", source, shortCodePublishPerMinLimit) {
		return "", "", 0, ErrShortCodeRateLimited
	}
	if len(e.byCode) >= shortCodeMaxActiveSlots {
		return "", "", 0, ErrShortCodeRateLimited
	}

	maxExp := nowMs + MaxShortCodeTTL.Milliseconds()
	exp := maxExp
	if requestedExpiresAtMs > 0 && requestedExpiresAtMs < exp {
		exp = requestedExpiresAtMs
	}
	if exp <= nowMs {
		return "", "", 0, ErrShortCodeUnavailable
	}

	allocated, err := e.allocateCodeLocked()
	if err != nil {
		return "", "", 0, err
	}
	cTok, err := NewShortCodeRequestID(now)
	if err != nil {
		return "", "", 0, err
	}

	rec := &shortCodeRecord{
		Code:            allocated,
		Ticket:          ticket,
		PublishReqID:    requestID,
		CancelToken:     cTok,
		ExpiresAtMs:     exp,
		CreatedAtMinute: nowMs / 60_000,
	}
	e.byCode[allocated] = rec
	e.byPubReqID[requestID] = rec
	return rec.Code, rec.CancelToken, rec.ExpiresAtMs, nil
}

// Redeem claims the ticket for code. Redemption has strictly one winner:
// retrying with the winning requestID returns the same ticket until expiry;
// any other claimant receives ErrShortCodeUnavailable.
func (e *ShortCodeExchange) Redeem(source, requestID, code string) (ticket string, expiresAtMs int64, err error) {
	if !ValidShortCode(code) || code == ReservedDemoShortCode {
		return "", 0, ErrInvalidShortCode
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	nowMs := e.now().UnixMilli()
	e.pruneLocked(nowMs)

	if err := e.validateRequestIDFresh(requestID, nowMs); err != nil {
		return "", 0, err
	}
	if !e.consumeRateLocked(nowMs, "redeem", source, shortCodeRedeemPerMinLimit) {
		return "", 0, ErrShortCodeRateLimited
	}
	rec, ok := e.byCode[code]
	if !ok || rec.Cancelled || nowMs >= rec.ExpiresAtMs {
		return "", 0, ErrShortCodeUnavailable
	}
	if rec.WinnerReqID == "" {
		rec.WinnerReqID = requestID
		return rec.Ticket, rec.ExpiresAtMs, nil
	}
	if subtle.ConstantTimeCompare([]byte(rec.WinnerReqID), []byte(requestID)) == 1 {
		return rec.Ticket, rec.ExpiresAtMs, nil
	}
	return "", 0, ErrShortCodeUnavailable
}

// Cancel revokes an active code using its publisher-owned cancelToken.
func (e *ShortCodeExchange) Cancel(source, code, cancelToken string) error {
	if !ValidShortCode(code) || !ValidShortCodeRequestID(cancelToken) {
		return ErrInvalidShortCode
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	nowMs := e.now().UnixMilli()
	e.pruneLocked(nowMs)

	if !e.consumeRateLocked(nowMs, "redeem", source, shortCodeRedeemPerMinLimit) {
		return ErrShortCodeRateLimited
	}
	rec, ok := e.byCode[code]
	if !ok || rec.Cancelled || nowMs >= rec.ExpiresAtMs {
		return ErrShortCodeUnavailable
	}
	if subtle.ConstantTimeCompare([]byte(rec.CancelToken), []byte(cancelToken)) != 1 {
		return ErrShortCodeUnavailable
	}
	rec.Cancelled = true
	delete(e.byCode, code)
	delete(e.byPubReqID, rec.PublishReqID)
	return nil
}

// Handler returns an http.Handler serving /v1/codes, /v1/codes/redeem,
// /v1/codes/cancel, and /healthz with Cache-Control: no-store on all replies.
func (e *ShortCodeExchange) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/v1/codes", e.handlePublish)
	mux.HandleFunc("/v1/codes/redeem", e.handleRedeem)
	mux.HandleFunc("/v1/codes/cancel", e.handleCancel)
	return mux
}

func (e *ShortCodeExchange) handlePublish(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		RequestID   string `json:"request_id"`
		Ticket      string `json:"ticket"`
		ExpiresAtMs int64  `json:"expires_at_ms"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, shortCodeMaxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	code, cancelTok, expMs, err := e.Publish(clientSourceIP(r), req.RequestID, req.Ticket, req.ExpiresAtMs)
	if err != nil {
		writeShortCodeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":          code,
		"cancel_token":  cancelTok,
		"expires_at_ms": expMs,
	})
}

func (e *ShortCodeExchange) handleRedeem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, shortCodeMaxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ticket, expMs, err := e.Redeem(clientSourceIP(r), req.RequestID, req.Code)
	if err != nil {
		writeShortCodeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":        ticket,
		"expires_at_ms": expMs,
	})
}

func (e *ShortCodeExchange) handleCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Code        string `json:"code"`
		CancelToken string `json:"cancel_token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, shortCodeMaxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := e.Cancel(clientSourceIP(r), req.Code, req.CancelToken); err != nil {
		writeShortCodeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func clientSourceIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	return normalizeRateLimitIP(r.RemoteAddr)
}

// normalizeRateLimitIP extracts a canonical per-client rate-limit bucket key.
// IPv4 and loopback addresses stay exact; non-loopback IPv6 addresses are
// aggregated to their /64 prefix so residential IPv6 interface-ID rotation
// cannot bypass 6-digit short-code brute-force limits.
func normalizeRateLimitIP(remoteAddr string) string {
	raw := strings.TrimSpace(remoteAddr)
	if raw == "" {
		return "unknown"
	}
	host := raw
	if h, _, err := net.SplitHostPort(raw); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return "unknown"
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if addr.Is6() && !addr.IsLoopback() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

func writeShortCodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrShortCodeConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrShortCodeRateLimited):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, ErrShortCodeUnavailable):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
