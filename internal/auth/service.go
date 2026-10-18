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
	pending       *authorization
	generation    uint64
	launchBrowser func(string)
}

type authorization struct {
	state     string
	baseURL   string
	expiresAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
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
		launchBrowser: openBrowser,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	// 启动时从磁盘加载已有凭据
	if stored, err := svc.store.Load(); err == nil && stored != nil {
		svc.current = stored
		go svc.verifyBackground(stored, serverURL, svc.generation)
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
	baseURL := strings.TrimRight(urlStr, "/")
	if s.serverBaseURL != baseURL {
		s.cancelPendingLocked()
		s.generation++
		s.serverBaseURL = baseURL
	}
}

func (s *Service) Snapshot() AuthStatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := AuthStatusSnapshot{
		IsLoggedIn:    s.current != nil && s.current.DeviceToken != "",
		ServerBaseURL: s.serverBaseURL,
	}
	if s.pending != nil && time.Now().Before(s.pending.expiresAt) && s.pending.ctx.Err() == nil {
		snap.IsAuthorizing = true
		snap.PendingState = s.pending.state
	}

	if s.current != nil {
		user := s.current.User
		snap.User = &user
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

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("failed to generate random state: %w", err)
	}
	state := hex.EncodeToString(buf)
	s.cancelPendingLocked()
	s.generation++
	expiresAt := time.Now().Add(3 * time.Minute)
	pollCtx, cancel := context.WithDeadline(context.Background(), expiresAt)
	baseURL := s.serverBaseURL
	attempt := &authorization{state: state, baseURL: baseURL, expiresAt: expiresAt, ctx: pollCtx, cancel: cancel}
	s.pending = attempt
	s.mu.Unlock()

	authURL := fmt.Sprintf("%s/auth/authorize-desktop?state=%s", baseURL, state)

	// 启动浏览器
	if s.launchBrowser != nil {
		go s.launchBrowser(authURL)
	}

	// 启动后台轮询
	go s.runPollWorker(attempt)

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

	s.mu.RLock()
	attempt := s.pending
	valid := s.pendingValidLocked(attempt) && attempt.state == state
	s.mu.RUnlock()
	if !valid {
		return nil, errors.New("invalid or expired authorization state")
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(attempt.ctx, cancel)
	defer stop()
	defer cancel()
	baseURL := attempt.baseURL
	reqBody, _ := json.Marshal(map[string]string{
		"handoffToken": handoffToken,
		"state":        state,
		"platform":     fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH),
	})

	apiURL := fmt.Sprintf("%s/api/hoxi/device-tokens/authorize", baseURL)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
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

	if err := s.applyAuthorized(attempt, stored); err != nil {
		return nil, err
	}

	return stored, nil
}

func (s *Service) Logout(ctx context.Context) error {
	s.mu.Lock()
	s.cancelPendingLocked()
	s.generation++
	token := ""
	if s.current != nil {
		token = s.current.DeviceToken
	}
	s.current = nil
	baseURL := s.serverBaseURL
	err := s.store.Clear()
	s.mu.Unlock()

	if token != "" {
		revokeURL := fmt.Sprintf("%s/api/hoxi/device-tokens/revoke", baseURL)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, nil)
		if req != nil {
			req.Header.Set("Authorization", "Bearer "+token)
			if resp, err := s.httpClient.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
	}

	return err
}

func (s *Service) cancelPendingLocked() {
	if s.pending != nil {
		s.pending.cancel()
		s.pending = nil
	}
}

func (s *Service) pendingValidLocked(attempt *authorization) bool {
	return attempt != nil && s.pending == attempt && s.serverBaseURL == attempt.baseURL &&
		time.Now().Before(attempt.expiresAt) && attempt.ctx.Err() == nil
}

func (s *Service) applyAuthorized(attempt *authorization, auth *StoredAuth) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pendingValidLocked(attempt) {
		return errors.New("invalid or expired authorization state")
	}
	// Keep persistence ordered with logout and publish only successfully saved credentials.
	if err := s.store.Save(auth); err != nil {
		return err
	}
	s.current = auth
	s.generation++
	s.cancelPendingLocked()
	return nil
}

func (s *Service) runPollWorker(attempt *authorization) {
	ctx := attempt.ctx
	defer func() {
		s.mu.Lock()
		if s.pending == attempt {
			s.cancelPendingLocked()
		}
		s.mu.Unlock()
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	pollURL := fmt.Sprintf("%s/api/hoxi/device-tokens/poll?state=%s&platform=%s",
		attempt.baseURL, url.QueryEscape(attempt.state), url.QueryEscape(platform))

	for {
		select {
		case <-ctx.Done():
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

			decodeErr := json.NewDecoder(resp.Body).Decode(&result)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK || decodeErr != nil {
				continue
			}

			if result.Status == "authorized" && result.DeviceToken != "" && result.User != nil {
				stored := &StoredAuth{
					DeviceToken:    result.DeviceToken,
					User:           *result.User,
					ExpiresAt:      result.ExpiresAt,
					LastVerifiedAt: time.Now(),
				}
				if err := s.applyAuthorized(attempt, stored); err == nil && s.logger != nil {
					s.logger.Info("hoxi desktop authorized successfully via poll", "email", result.User.Email)
				}
				return
			} else if result.Status == "expired" || result.Status == "error" {
				return
			}
		}
	}
}

func (s *Service) verifyBackground(auth *StoredAuth, baseURL string, generation uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verifyURL := fmt.Sprintf("%s/api/hoxi/device-tokens/verify", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, verifyURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+auth.DeviceToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var verifyRes struct {
		Valid bool        `json:"valid"`
		User  UserProfile `json:"user"`
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&verifyRes); err != nil {
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != auth || s.generation != generation || s.serverBaseURL != baseURL {
		return
	}

	if resp.StatusCode == http.StatusUnauthorized {
		if s.logger != nil {
			s.logger.Warn("device token revoked on server, resetting local auth")
		}
		s.current = nil
		s.generation++
		if err := s.store.Clear(); err != nil && s.logger != nil {
			s.logger.Error("failed to clear revoked credentials", "error", err)
		}
		return
	}

	if resp.StatusCode == http.StatusOK && verifyRes.Valid {
		updated := *auth
		updated.User = verifyRes.User
		updated.LastVerifiedAt = time.Now()
		if err := s.store.Save(&updated); err == nil {
			s.current = &updated
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
