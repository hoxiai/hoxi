package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Service struct {
	mu            sync.RWMutex
	store         *TokenStore
	logger        *slog.Logger
	serverBaseURL string
	httpClient    *http.Client

	current       *StoredAuth
	pendingState  string
	pollCancel    context.CancelFunc
}

func NewService(logger *slog.Logger) *Service {
	serverURL := strings.TrimRight(os.Getenv("HOXI_SERVER_URL"), "/")
	if serverURL == "" {
		serverURL = "https://hoxi.ai"
	}

	svc := &Service{
		store:         NewTokenStore(),
		logger:        logger,
		serverBaseURL: serverURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	// 启动时从磁盘加载已有凭据
	if stored, err := svc.store.Load(); err == nil && stored != nil {
		svc.current = stored
		go svc.verifyBackground(stored.DeviceToken)
	}

	return svc
}

func (s *Service) ServerBaseURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serverBaseURL
}

func (s *Service) SetServerBaseURL(urlStr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverBaseURL = strings.TrimRight(urlStr, "/")
}

func (s *Service) Snapshot() AuthStatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := AuthStatusSnapshot{
		IsLoggedIn:    s.current != nil && s.current.DeviceToken != "",
		IsAuthorizing: s.pendingState != "",
		PendingState:  s.pendingState,
		ServerBaseURL: s.serverBaseURL,
	}

	if s.current != nil {
		snap.User = &s.current.User
		snap.ExpiresAt = s.current.ExpiresAt
		if !s.current.LastVerifiedAt.IsZero() {
			snap.LastVerifiedAt = s.current.LastVerifiedAt.Format(time.RFC3339)
		}
	}

	return snap
}

func (s *Service) CurrentDeviceToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return ""
	}
	return s.current.DeviceToken
}

// StartAuthorization 发起一键授权流程
func (s *Service) StartAuthorization(ctx context.Context) (*StartAuthResponse, error) {
	s.mu.Lock()
	if s.pollCancel != nil {
		s.pollCancel()
		s.pollCancel = nil
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("failed to generate random state: %w", err)
	}
	state := hex.EncodeToString(buf)
	s.pendingState = state

	pollCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	s.pollCancel = cancel
	baseURL := s.serverBaseURL
	s.mu.Unlock()

	authURL := fmt.Sprintf("%s/auth/authorize-desktop?state=%s", baseURL, state)

	// 启动浏览器
	go openBrowser(authURL)

	// 启动后台轮询
	go s.runPollWorker(pollCtx, state, baseURL)

	return &StartAuthResponse{
		State:   state,
		AuthURL: authURL,
	}, nil
}

// ExchangeToken 外部唤起（如 Deep Link）时主动兑换令牌
func (s *Service) ExchangeToken(ctx context.Context, handoffToken, state string) (*StoredAuth, error) {
	if handoffToken == "" || state == "" {
		return nil, errors.New("missing handoffToken or state")
	}

	baseURL := s.ServerBaseURL()
	reqBody, _ := json.Marshal(map[string]string{
		"handoffToken": handoffToken,
		"state":        state,
		"platform":     fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH),
	})

	apiURL := fmt.Sprintf("%s/api/hoxi/device-tokens/authorize", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exchange failed with HTTP %d", resp.StatusCode)
	}

	var res struct {
		Success     bool        `json:"success"`
		User        UserProfile `json:"user"`
		DeviceToken string      `json:"deviceToken"`
		ExpiresAt   string      `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if !res.Success || res.DeviceToken == "" {
		return nil, errors.New("server returned unsuccessful authorization")
	}

	stored := &StoredAuth{
		DeviceToken:    res.DeviceToken,
		User:           res.User,
		ExpiresAt:      res.ExpiresAt,
		LastVerifiedAt: time.Now(),
	}

	if err := s.applyAuthorized(stored); err != nil {
		return nil, err
	}

	return stored, nil
}

func (s *Service) Logout(ctx context.Context) error {
	s.mu.Lock()
	if s.pollCancel != nil {
		s.pollCancel()
		s.pollCancel = nil
	}
	s.pendingState = ""
	token := ""
	if s.current != nil {
		token = s.current.DeviceToken
	}
	s.current = nil
	baseURL := s.serverBaseURL
	s.mu.Unlock()

	_ = s.store.Clear()

	if token != "" {
		revokeURL := fmt.Sprintf("%s/api/hoxi/device-tokens/revoke", baseURL)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, nil)
		if req != nil {
			req.Header.Set("Authorization", "Bearer "+token)
			_, _ = s.httpClient.Do(req)
		}
	}

	return nil
}

func (s *Service) applyAuthorized(auth *StoredAuth) error {
	s.mu.Lock()
	s.current = auth
	s.pendingState = ""
	if s.pollCancel != nil {
		s.pollCancel()
		s.pollCancel = nil
	}
	s.mu.Unlock()

	return s.store.Save(auth)
}

func (s *Service) runPollWorker(ctx context.Context, state, baseURL string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	pollURL := fmt.Sprintf("%s/api/hoxi/device-tokens/poll?state=%s&platform=%s",
		baseURL, url.QueryEscape(state), url.QueryEscape(platform))

	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			if s.pendingState == state {
				s.pendingState = ""
			}
			s.mu.Unlock()
			return
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
			if err != nil {
				continue
			}
			resp, err := s.httpClient.Do(req)
			if err != nil {
				continue
			}

			var result struct {
				Status      string       `json:"status"`
				Message     string       `json:"message"`
				User        *UserProfile `json:"user,omitempty"`
				DeviceToken string       `json:"deviceToken,omitempty"`
				ExpiresAt   string       `json:"expiresAt,omitempty"`
			}

			_ = json.NewDecoder(resp.Body).Decode(&result)
			_ = resp.Body.Close()

			if result.Status == "authorized" && result.DeviceToken != "" && result.User != nil {
				stored := &StoredAuth{
					DeviceToken:    result.DeviceToken,
					User:           *result.User,
					ExpiresAt:      result.ExpiresAt,
					LastVerifiedAt: time.Now(),
				}
				_ = s.applyAuthorized(stored)
				if s.logger != nil {
					s.logger.Info("hoxi desktop authorized successfully via poll", "email", result.User.Email)
				}
				return
			} else if result.Status == "expired" || result.Status == "error" {
				s.mu.Lock()
				if s.pendingState == state {
					s.pendingState = ""
				}
				s.mu.Unlock()
				return
			}
		}
	}
}

func (s *Service) verifyBackground(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseURL := s.ServerBaseURL()
	verifyURL := fmt.Sprintf("%s/api/hoxi/device-tokens/verify", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, verifyURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if s.logger != nil {
			s.logger.Warn("device token revoked on server, resetting local auth")
		}
		_ = s.Logout(context.Background())
		return
	}

	if resp.StatusCode == http.StatusOK {
		var verifyRes struct {
			Valid bool        `json:"valid"`
			User  UserProfile `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&verifyRes); err == nil && verifyRes.Valid {
			s.mu.Lock()
			if s.current != nil && s.current.DeviceToken == token {
				s.current.User = verifyRes.User
				s.current.LastVerifiedAt = time.Now()
				_ = s.store.Save(s.current)
			}
			s.mu.Unlock()
		}
	}
}

func openBrowser(targetURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	_ = cmd.Start()
}
