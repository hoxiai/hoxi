package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

var ErrNotLoggedIn = errors.New("not logged in")

type UpstreamAPIError struct {
	StatusCode int
	Message    string
}

func (e *UpstreamAPIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("upstream returned HTTP %d", e.StatusCode)
}

// AuthSource provides the Hoxi server base URL, current device token, and 401 invalidation.
type AuthSource interface {
	ServerBaseURL() string
	CurrentDeviceToken() string
	InvalidateDeviceToken(token, baseURL string)
}

type Service struct {
	auth       AuthSource
	httpClient *http.Client
}

func NewService(auth AuthSource) *Service {
	return &Service{
		auth: auth,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (s *Service) baseURL() string {
	if s.auth != nil {
		if u := strings.TrimRight(strings.TrimSpace(s.auth.ServerBaseURL()), "/"); u != "" {
			return u
		}
	}
	return hoxipaths.ServerBaseURL()
}

func (s *Service) deviceToken() string {
	if s.auth == nil {
		return ""
	}
	return strings.TrimSpace(s.auth.CurrentDeviceToken())
}

// Discover searches available tools via /api/hoxi/tools/discover.
// Supports both anonymous and authenticated calls; if a stored device token
// has expired (HTTP 401), it invalidates the token and retries anonymously.
func (s *Service) Discover(ctx context.Context, body []byte) (map[string]any, error) {
	return s.callOptionalEndpoint(ctx, "/api/hoxi/tools/discover", body)
}

// Inspect fetches parameter schema and point pricing via /api/hoxi/tools/inspect.
// Supports both anonymous and authenticated calls; if a stored device token
// has expired (HTTP 401), it invalidates the token and retries anonymously.
func (s *Service) Inspect(ctx context.Context, body []byte) (map[string]any, error) {
	return s.callOptionalEndpoint(ctx, "/api/hoxi/tools/inspect", body)
}

// Run executes a tool endpoint using the logged-in Hoxi account.
func (s *Service) Run(ctx context.Context, body []byte) (map[string]any, error) {
	baseURL := s.baseURL()
	token := s.deviceToken()
	if token == "" {
		return nil, ErrNotLoggedIn
	}
	payload, status, err := s.doPostJSON(ctx, baseURL+"/api/hoxi/tools/run", token, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		if s.auth != nil {
			s.auth.InvalidateDeviceToken(token, baseURL)
		}
		return nil, ErrNotLoggedIn
	}
	if status < 200 || status >= 300 {
		return nil, upstreamErr(status, payload)
	}
	return payload, nil
}

func (s *Service) callOptionalEndpoint(ctx context.Context, endpoint string, body []byte) (map[string]any, error) {
	baseURL := s.baseURL()
	token := s.deviceToken()
	targetURL := baseURL + endpoint

	payload, status, err := s.doPostJSON(ctx, targetURL, token, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized && token != "" {
		if s.auth != nil {
			s.auth.InvalidateDeviceToken(token, baseURL)
		}
		payload, status, err = s.doPostJSON(ctx, targetURL, "", body)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status >= 300 {
		return nil, upstreamErr(status, payload)
	}
	return payload, nil
}

func (s *Service) doPostJSON(ctx context.Context, targetURL, token string, body []byte) (map[string]any, int, error) {
	if len(body) == 0 {
		body = []byte(`{}`)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload)
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, resp.StatusCode, nil
}

func upstreamErr(status int, payload map[string]any) error {
	msg := ""
	for _, key := range []string{"message", "statusMessage", "error"} {
		if m, ok := payload[key].(string); ok && m != "" {
			msg = m
			break
		}
	}
	return &UpstreamAPIError{
		StatusCode: status,
		Message:    msg,
	}
}
