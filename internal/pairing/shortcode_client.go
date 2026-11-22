package pairing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const shortCodeHTTPTimeout = 10 * time.Second

// DefaultShortCodeRelayURL returns the effective default 6-digit pairing relay
// origin. HOXI_PAIRING_RELAY_URL takes precedence over the build-time default
// (empty in -tags oss builds; official relay origin in commercial builds).
func DefaultShortCodeRelayURL() string {
	if env := strings.TrimSpace(os.Getenv("HOXI_PAIRING_RELAY_URL")); env != "" {
		return strings.TrimRight(env, "/")
	}
	return strings.TrimRight(strings.TrimSpace(defaultShortCodeRelayURL), "/")
}

// ShortCodeTicket is the zero-knowledge connection payload published to a
// 6-digit pairing relay. It deliberately omits E2EEKey so third-party relays
// never observe the end-to-end encryption master key; clients derive or obtain
// E2EEKey directly from the desktop via X25519 KEX (/api/hoxi/bootstrap).
type ShortCodeTicket struct {
	Type       string   `json:"type"`
	URL        string   `json:"url"`
	Peers      []string `json:"peers,omitempty"`
	Relay      string   `json:"relay,omitempty"`
	Token      string   `json:"token"`
	RelayToken string   `json:"relayToken,omitempty"`
	Hostname   string   `json:"hostname,omitempty"`
	KEX        string   `json:"kex"`
}

// PublishedShortCode holds the active short-code allocation for this desktop.
type PublishedShortCode struct {
	Code        string `json:"code"`
	ExpiresAtMs int64  `json:"expiresAtMs"`
	RelayURL    string `json:"relayUrl"`
	cancelToken string
}

// ShortCodeClaim preserves a claimant's idempotent request_id across transient
// redemption retries while redacting itself in logs.
type ShortCodeClaim struct {
	mu        sync.Mutex
	origin    string
	code      string
	requestID string
}

func (*ShortCodeClaim) String() string   { return "ShortCodeClaim([REDACTED])" }
func (*ShortCodeClaim) GoString() string { return "ShortCodeClaim([REDACTED])" }

func (c *ShortCodeClaim) ensureRequestID(origin, code string, now time.Time) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requestID == "" || c.origin != origin || c.code != code {
		id, err := NewShortCodeRequestID(now)
		if err != nil {
			return "", err
		}
		c.origin = origin
		c.code = code
		c.requestID = id
	}
	return c.requestID, nil
}

// ShortCodeClient talks to a v1 6-digit pairing ticket exchange over HTTPS
// (or literal loopback HTTP for local testing).
type ShortCodeClient struct {
	base   *url.URL
	client *http.Client
	now    func() time.Time
}

// NewShortCodeClient validates origin and builds an HTTP client with redirects
// disabled and loopback proxy bypass. Pass a custom transport in tests to
// exercise the full HTTP wire contract without binding a TCP port.
func NewShortCodeClient(origin string, transport http.RoundTripper, now func() time.Time) (*ShortCodeClient, error) {
	raw := strings.TrimSpace(origin)
	if raw == "" {
		return nil, errors.New("pairing relay origin is required")
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid pairing relay origin")
	}
	host := base.Hostname()
	ip := net.ParseIP(host)
	loopback := ip != nil && ip.IsLoopback()
	if !(base.Scheme == "https" || (base.Scheme == "http" && loopback)) ||
		host == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery ||
		base.Fragment != "" || strings.Contains(raw, "#") || base.Opaque != "" ||
		(base.Path != "" && base.Path != "/") {
		return nil, errors.New("pairing relay origin must be https://host (or http://loopback)")
	}
	base.Path = "/"
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.DisableKeepAlives = true
		t.ForceAttemptHTTP2 = false
		t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		if loopback {
			t.Proxy = nil
		}
		transport = t
	}
	if now == nil {
		now = time.Now
	}
	return &ShortCodeClient{
		base: base,
		client: &http.Client{
			Transport: transport,
			Timeout:   shortCodeHTTPTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: now,
	}, nil
}

// Publish uploads an opaque ticket string and returns the allocated 6-digit code.
func (c *ShortCodeClient) Publish(ctx context.Context, ticket string, ttl time.Duration) (PublishedShortCode, error) {
	if ttl <= 0 || ttl > MaxShortCodeTTL {
		ttl = MaxShortCodeTTL
	}
	now := c.now()
	reqID, err := NewShortCodeRequestID(now)
	if err != nil {
		return PublishedShortCode{}, err
	}
	wantExp := now.Add(ttl).UnixMilli()
	var out struct {
		Code        string `json:"code"`
		CancelToken string `json:"cancel_token"`
		ExpiresAtMs int64  `json:"expires_at_ms"`
	}
	err = c.postJSON(ctx, "v1/codes", map[string]any{
		"request_id":    reqID,
		"ticket":        ticket,
		"expires_at_ms": wantExp,
	}, http.StatusCreated, &out)
	if err != nil {
		return PublishedShortCode{}, err
	}
	if !ValidShortCode(out.Code) || out.Code == ReservedDemoShortCode || !ValidShortCodeRequestID(out.CancelToken) {
		return PublishedShortCode{}, ErrInvalidShortCode
	}
	if out.ExpiresAtMs > wantExp {
		out.ExpiresAtMs = wantExp
	}
	if out.ExpiresAtMs <= c.now().UnixMilli() {
		return PublishedShortCode{}, ErrShortCodeUnavailable
	}
	return PublishedShortCode{
		Code:        out.Code,
		ExpiresAtMs: out.ExpiresAtMs,
		RelayURL:    strings.TrimRight(c.base.String(), "/"),
		cancelToken: out.CancelToken,
	}, nil
}

// Redeem exchanges a 6-digit code for the publisher's opaque ticket string.
func (c *ShortCodeClient) Redeem(ctx context.Context, code string, claim *ShortCodeClaim) (string, int64, error) {
	if !ValidShortCode(code) || code == ReservedDemoShortCode {
		return "", 0, ErrInvalidShortCode
	}
	if claim == nil {
		claim = &ShortCodeClaim{}
	}
	reqID, err := claim.ensureRequestID(c.base.String(), code, c.now())
	if err != nil {
		return "", 0, err
	}
	var out struct {
		Ticket      string `json:"ticket"`
		ExpiresAtMs int64  `json:"expires_at_ms"`
	}
	err = c.postJSON(ctx, "v1/codes/redeem", map[string]any{
		"request_id": reqID,
		"code":       code,
	}, http.StatusOK, &out)
	if err != nil {
		return "", 0, err
	}
	if out.Ticket == "" {
		return "", 0, ErrShortCodeUnavailable
	}
	return out.Ticket, out.ExpiresAtMs, nil
}

// Cancel revokes an active published short code on the relay.
func (c *ShortCodeClient) Cancel(ctx context.Context, pub PublishedShortCode) error {
	if !ValidShortCode(pub.Code) || !ValidShortCodeRequestID(pub.cancelToken) {
		return ErrInvalidShortCode
	}
	return c.postJSON(ctx, "v1/codes/cancel", map[string]any{
		"code":         pub.Code,
		"cancel_token": pub.cancelToken,
	}, http.StatusNoContent, nil)
}

func (c *ShortCodeClient) postJSON(ctx context.Context, relPath string, payload any, wantStatus int, out any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := c.base.ResolveReference(&url.URL{Path: relPath})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		switch resp.StatusCode {
		case http.StatusNotFound, http.StatusGone:
			return ErrShortCodeUnavailable
		case http.StatusConflict:
			return ErrShortCodeConflict
		case http.StatusTooManyRequests:
			return ErrShortCodeRateLimited
		default:
			return fmt.Errorf("pairing relay returned HTTP %d", resp.StatusCode)
		}
	}
	if wantStatus == http.StatusNoContent || out == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, shortCodeMaxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(body) > shortCodeMaxBodyBytes {
		return errors.New("pairing relay response exceeds limit")
	}
	return json.Unmarshal(body, out)
}

// ShortCodeTicket builds the zero-knowledge pairing ticket for this desktop,
// omitting E2EEKey so third-party pairing relays never observe encryption keys.
func (s *Service) ShortCodeTicket() (ShortCodeTicket, error) {
	code, err := s.Code()
	if err != nil {
		return ShortCodeTicket{}, err
	}
	if !code.Available {
		return ShortCodeTicket{}, errors.New("desktop has no reachable LAN or relay endpoint")
	}
	return ShortCodeTicket{
		Type:       code.Type,
		URL:        code.URL,
		Peers:      code.Peers,
		Relay:      code.Relay,
		Token:      code.Token,
		RelayToken: code.RelayToken,
		Hostname:   code.Hostname,
		KEX:        KEXAlgorithm,
	}, nil
}

// SetShortCodeTransportForTest overrides the HTTP transport used by
// PublishShortCode and CancelShortCode in unit tests.
func (s *Service) SetShortCodeTransportForTest(rt http.RoundTripper) {
	s.mu.Lock()
	s.shortCodeTransport = rt
	s.mu.Unlock()
}

// PublishShortCode publishes a zero-knowledge pairing ticket (without E2EEKey)
// to relayOrigin and caches the active cancellation token on this Service.
func (s *Service) PublishShortCode(ctx context.Context, relayOrigin string) (PublishedShortCode, error) {
	ticketObj, err := s.ShortCodeTicket()
	if err != nil {
		return PublishedShortCode{}, err
	}
	rawTicket, err := json.Marshal(ticketObj)
	if err != nil {
		return PublishedShortCode{}, err
	}
	s.mu.Lock()
	rt := s.shortCodeTransport
	prev := s.activeShortCode
	s.mu.Unlock()

	if prev != nil && prev.RelayURL != "" {
		if prevClient, cErr := NewShortCodeClient(prev.RelayURL, rt, nil); cErr == nil {
			_ = prevClient.Cancel(ctx, *prev)
		}
	}

	client, err := NewShortCodeClient(relayOrigin, rt, nil)
	if err != nil {
		return PublishedShortCode{}, err
	}
	pub, err := client.Publish(ctx, string(rawTicket), MaxShortCodeTTL)
	if err != nil {
		return PublishedShortCode{}, err
	}
	s.mu.Lock()
	cp := pub
	s.activeShortCode = &cp
	s.mu.Unlock()
	return pub, nil
}

// CancelShortCode revokes the desktop's currently published 6-digit pairing
// code on its relay (best-effort) and clears local state.
func (s *Service) CancelShortCode(ctx context.Context) error {
	s.mu.Lock()
	active := s.activeShortCode
	rt := s.shortCodeTransport
	s.activeShortCode = nil
	s.mu.Unlock()
	if active == nil {
		return nil
	}
	client, err := NewShortCodeClient(active.RelayURL, rt, nil)
	if err != nil {
		return nil
	}
	err = client.Cancel(ctx, *active)
	if errors.Is(err, ErrShortCodeUnavailable) {
		return nil
	}
	return err
}
