package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxPollBackoff caps the wait between pairing polls. The loop still stops at
// the overall deadline or when canceled, so it never runs forever.
const maxPollBackoff = 15 * time.Second

// pendingPair is an in-flight one-click pairing request. Every field is read
// and written while holding Manager.mu, except the ones set before the poll
// goroutine starts. cancel/done stop and join the goroutine.
type pendingPair struct {
	controllerURL string
	name          string
	requestID     string
	userCode      string
	pollToken     string
	expiresAt     time.Time
	state         string
	cancel        context.CancelFunc
	done          chan struct{}
}

// RequestPair starts a one-click pairing: it asks GoPanel to open a pending
// request and returns immediately. A background poll waits for an administrator
// to approve the request in the GoPanel console (节点 → 隧道设置), then redeems
// the issued one-time code and connects. It replaces any request already in
// flight. The GoPanel pairing endpoints are exempt from the safety entrance;
// the user only needs the GoPanel host:port.
func (m *Manager) RequestPair(ctx context.Context, controllerURL, name string) error {
	if m.store().path == "" {
		return errors.New("remote tunnel is not available in this Hoxi process")
	}
	normalized, err := normalizeControllerURL(controllerURL)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	hostname := m.opts.Hostname()
	if name == "" {
		name = hostname
	}
	payload, err := json.Marshal(map[string]string{"name": name, "hostname": hostname, "version": m.opts.Version})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, normalized+"/api/tunnel/pair/request", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := *m.opts.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect GoPanel: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	// GoPanel wraps results as {code, msg, data}; code 0 means success.
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			RequestID string `json:"requestId"`
			UserCode  string `json:"userCode"`
			ExpiresAt string `json:"expiresAt"`
			PollToken string `json:"pollToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("GoPanel returned an unrecognized pairing response (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK || result.Code != 0 || strings.TrimSpace(result.Data.RequestID) == "" || strings.TrimSpace(result.Data.UserCode) == "" {
		message := strings.TrimSpace(result.Msg)
		if message == "" {
			message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("GoPanel rejected the pairing request: %s", message)
	}
	// ExpiresAt drives the countdown; a missing or malformed value just leaves
	// the overall PollTimeout in charge.
	expiresAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(result.Data.ExpiresAt))

	// Replace any in-flight request before starting the new one.
	m.CancelPair()

	pollCtx, cancel := context.WithCancel(context.Background())
	p := &pendingPair{
		controllerURL: normalized,
		name:          name,
		requestID:     strings.TrimSpace(result.Data.RequestID),
		userCode:      strings.TrimSpace(result.Data.UserCode),
		pollToken:     strings.TrimSpace(result.Data.PollToken),
		expiresAt:     expiresAt,
		state:         PendingWaiting,
		cancel:        cancel,
		done:          make(chan struct{}),
	}
	m.mu.Lock()
	prev := m.pending
	if prev != nil {
		prev.cancel()
	}
	m.pending = p
	if !m.status.Enabled {
		m.status.Message = ""
	}
	m.mu.Unlock()
	if prev != nil {
		<-prev.done
	}
	go m.pollPending(pollCtx, p)
	return nil
}

// CancelPair stops an in-flight one-click pairing request and clears it. It is
// safe to call when nothing is pending. It never persists a credential.
func (m *Manager) CancelPair() {
	m.mu.Lock()
	p := m.pending
	m.pending = nil
	m.mu.Unlock()
	if p != nil {
		p.cancel()
		<-p.done
	}
}

func (m *Manager) setPendingState(p *pendingPair, state string) {
	m.mu.Lock()
	if m.pending == p {
		p.state = state
	}
	m.mu.Unlock()
}

func (m *Manager) clearPending(p *pendingPair) {
	m.mu.Lock()
	if m.pending == p {
		m.pending = nil
	}
	m.mu.Unlock()
}

func (m *Manager) failPending(p *pendingPair, err error) {
	m.mu.Lock()
	if m.pending == p {
		m.pending = nil
	}
	if err != nil {
		m.status.Message = err.Error()
	}
	m.mu.Unlock()
}

// pollPending polls GoPanel until the request is approved, denied or expired,
// the overall deadline passes, or the context is canceled.
func (m *Manager) pollPending(ctx context.Context, p *pendingPair) {
	defer close(p.done)
	deadline := p.expiresAt
	if limit := time.Now().Add(m.opts.PollTimeout); deadline.IsZero() || deadline.After(limit) {
		deadline = limit
	}
	backoff := m.opts.PollInterval
	for {
		if ctx.Err() != nil {
			return
		}
		if !time.Now().Before(deadline) {
			m.setPendingState(p, PendingExpired)
			return
		}
		state, code, err := m.pollPair(ctx, p)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			switch state {
			case PendingApproved:
				if strings.TrimSpace(code) != "" {
					m.setPendingState(p, PendingApproved)
					if err := m.exchangeAndConnect(ctx, p.controllerURL, code, p.name); err != nil {
						if ctx.Err() == nil {
							m.failPending(p, err)
						} else {
							m.clearPending(p)
						}
						return
					}
					m.clearPending(p)
					return
				}
			case PendingDenied, PendingExpired:
				m.setPendingState(p, state)
				return
			}
		}
		wait := backoff
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if backoff < maxPollBackoff {
			backoff = min(backoff*2, maxPollBackoff)
		}
	}
}

// pollPair asks GoPanel for the current state of the pending request. The
// pollToken (when issued) authenticates the poll; the endpoint stays exempt
// from the safety entrance.
func (m *Manager) pollPair(ctx context.Context, p *pendingPair) (string, string, error) {
	endpoint := p.controllerURL + "/api/tunnel/pair/request/" + url.PathEscape(p.requestID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	if p.pollToken != "" {
		req.Header.Set("X-GoPanel-Tunnel-PollToken", p.pollToken)
	}
	client := *m.opts.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			State string `json:"state"`
			Code  string `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("GoPanel returned an unrecognized polling response (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK || result.Code != 0 {
		message := strings.TrimSpace(result.Msg)
		if message == "" {
			message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return "", "", fmt.Errorf("GoPanel polling failed: %s", message)
	}
	return strings.TrimSpace(result.Data.State), strings.TrimSpace(result.Data.Code), nil
}
