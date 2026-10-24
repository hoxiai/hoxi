package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func devicePlatform() string {
	return fmt.Sprintf("%s-%s", hoxipaths.NormalizedPlatform(runtime.GOOS), hoxipaths.NormalizedArch(runtime.GOARCH))
}

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
	// pollInterval paces device-token polling; zero means defaultPollInterval.
	// Tests shorten it so they do not wait on real seconds.
	pollInterval time.Duration
}

const defaultPollInterval = 2 * time.Second

type authorization struct {
	state     string
	baseURL   string
	expiresAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

func NewService(logger *slog.Logger) *Service {
	svc := &Service{
		store:         NewTokenStore(),
		logger:        logger,
		serverBaseURL: hoxipaths.ServerBaseURL(),
		launchBrowser: openBrowser,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	// 启动时从磁盘加载已有凭据
	if stored, err := svc.store.Load(); err == nil && stored != nil {
		svc.current = stored
		go svc.verifyBackground(stored, svc.serverBaseURL, svc.generation)
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

// HandleCallback accepts only the desktop login callback, never an arbitrary exchange URL.
func (s *Service) HandleCallback(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "hoxi" || u.Host != "auth-callback" ||
		u.User != nil || u.Path != "" || u.Opaque != "" || u.Fragment != "" {
		return errors.New("invalid desktop authorization callback")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query["handoff"]) != 1 || len(query["state"]) != 1 ||
		strings.TrimSpace(query.Get("handoff")) == "" || strings.TrimSpace(query.Get("state")) == "" {
		return errors.New("invalid desktop authorization callback parameters")
	}
	_, err = s.ExchangeToken(ctx, query.Get("handoff"), query.Get("state"))
	return err
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
		"platform":     devicePlatform(),
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&res); err != nil {
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
	if err := s.store.Clear(); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to clear local credentials: %w", err)
	}
	s.cancelPendingLocked()
	s.generation++
	token := ""
	if s.current != nil {
		token = s.current.DeviceToken
	}
	s.current = nil
	baseURL := s.serverBaseURL
	s.mu.Unlock()

	if token != "" {
		revokeURL := fmt.Sprintf("%s/api/hoxi/device-tokens/revoke", baseURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+token)
			var resp *http.Response
			resp, err = s.httpClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					err = fmt.Errorf("revoke returned HTTP %d", resp.StatusCode)
				}
			}
		}
		if err != nil && s.logger != nil {
			s.logger.Warn("local logout completed; remote device token revocation was not confirmed", "error", err)
		}
	}

	return nil
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
	interval := s.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	platform := devicePlatform()
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

			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
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
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&verifyRes); err != nil {
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
		if err := s.store.Clear(); err != nil {
			if s.logger != nil {
				s.logger.Error("failed to clear revoked credentials", "error", err)
			}
			return
		}
		s.current = nil
		s.generation++
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

func (s *Service) FetchMemberStatus(ctx context.Context) (map[string]any, error) {
	return s.callMemberEndpoint(ctx, http.MethodGet, "/api/hoxi/member/checkin", nil)
}

func (s *Service) PerformCheckin(ctx context.Context) (map[string]any, error) {
	return s.callMemberEndpoint(ctx, http.MethodPost, "/api/hoxi/member/checkin", []byte(`{}`))
}

// MintMCPToken 使用本地已授权的设备凭据向 hoxi.ai 申请签发符合 APay MCP 规范的 apay_mcp_... Scoped Token
func (s *Service) MintMCPToken(ctx context.Context, req MCPTokenRequest) (map[string]any, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return s.callMemberEndpoint(ctx, http.MethodPost, "/api/hoxi/mcp/token", body)
}

// FetchWorkApps 从 hoxi.ai 拉取官方工作模式应用目录，并合并本地持久化的应用安装/技能绑定配置
func (s *Service) FetchWorkApps(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	baseURL := s.serverBaseURL
	token := ""
	if s.current != nil {
		token = s.current.DeviceToken
	}
	s.mu.RUnlock()

	localState, _ := s.store.LoadWorkState()

	targetURL := fmt.Sprintf("%s/api/hoxi/work/apps", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return map[string]any{"localState": localState}, nil
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return map[string]any{"localState": localState}, nil
	}
	defer resp.Body.Close()

	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload)
	if payload == nil {
		payload = map[string]any{}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var items any
		if dataMap, ok := payload["data"].(map[string]any); ok && dataMap["items"] != nil {
			items = dataMap["items"]
		} else if payload["items"] != nil {
			items = payload["items"]
		} else if payload["apps"] != nil {
			items = payload["apps"]
		}
		if items != nil {
			if localState == nil {
				localState = map[string]any{}
			}
			localState["cachedItems"] = items
			_ = s.store.SaveWorkState(localState)
		}
	}
	payload["localState"] = localState
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return payload, nil
	}
	return payload, nil
}

// SaveWorkAppsState 保存用户本地安装的应用、自定义应用、MCP 链接配置及绑定技能到 ~/.hoxi/work-apps.json
func (s *Service) SaveWorkAppsState(state map[string]any) error {
	existing, _ := s.store.LoadWorkState()
	if existing == nil {
		existing = map[string]any{}
	}
	for k, v := range state {
		existing[k] = v
	}
	return s.store.SaveWorkState(existing)
}

func (s *Service) callMemberEndpoint(ctx context.Context, method, endpoint string, body []byte) (map[string]any, error) {
	s.mu.RLock()
	var auth *StoredAuth
	if s.current != nil && s.current.DeviceToken != "" {
		copied := *s.current
		auth = &copied
	}
	baseURL := s.serverBaseURL
	generation := s.generation
	s.mu.RUnlock()

	if auth == nil {
		return nil, ErrNotLoggedIn
	}

	targetURL := fmt.Sprintf("%s%s", baseURL, endpoint)
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, targetURL, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.DeviceToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload)

	if resp.StatusCode == http.StatusUnauthorized {
		s.mu.Lock()
		if s.current != nil && s.current.DeviceToken == auth.DeviceToken && s.generation == generation && s.serverBaseURL == baseURL {
			if err := s.store.Clear(); err == nil {
				s.current = nil
				s.generation++
			}
		}
		s.mu.Unlock()
		return nil, ErrNotLoggedIn
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := ""
		if payload != nil {
			if m, ok := payload["message"].(string); ok && m != "" {
				msg = m
			} else if m, ok := payload["statusMessage"].(string); ok && m != "" {
				msg = m
			} else if m, ok := payload["error"].(string); ok && m != "" {
				msg = m
			}
		}
		return nil, &UpstreamAPIError{
			StatusCode: resp.StatusCode,
			Message:    msg,
		}
	}

	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}

func (s *Service) OpenPortalPage(target string) (string, error) {
	baseURL := s.ServerBaseURL()
	var fullURL string
	switch strings.TrimSpace(target) {
	case "dashboard", "":
		fullURL = baseURL + "/user/dashboard"
	case "rewards":
		fullURL = baseURL + "/user/rewards"
	case "models":
		fullURL = baseURL + "/user/models"
	case "keys":
		fullURL = baseURL + "/user/keys"
	case "billing":
		fullURL = baseURL + "/user/billing"
	case "account":
		fullURL = baseURL + "/user/account"
	case "feedback":
		fullURL = "https://github.com/hoxiai/hoxi/issues"
	default:
		return "", errors.New("unsupported portal target")
	}

	if s.launchBrowser != nil {
		go s.launchBrowser(fullURL)
	}
	return fullURL, nil
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
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}
