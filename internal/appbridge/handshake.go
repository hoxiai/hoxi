package appbridge

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ConnectModeRedirect = "redirect"
	ConnectModePoll     = "poll"

	connectRequestTTL  = 120 * time.Second
	authCodeTTL        = 60 * time.Second
	authCodePrefix     = "hxc_"
	maxPendingRequests = 128
)

// ConnectInput carries validated fields from hoxi://connect or /auth/poll-init.
type ConnectInput struct {
	AppID               string
	Name                string
	Scopes              []string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
	Mode                string
}

// ConnectRequest is the public snapshot of a pending or decided connect handshake.
type ConnectRequest struct {
	RequestID     string   `json:"requestId"`
	AppID         string   `json:"appId"`
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	RedirectURI   string   `json:"redirectUri,omitempty"`
	State         string   `json:"state,omitempty"`
	Mode          string   `json:"mode"`
	Status        string   `json:"status"` // pending | approved | denied
	GrantedScopes []string `json:"grantedScopes,omitempty"`
	CreatedAt     int64    `json:"createdAt"`
	ExpiresAt     int64    `json:"expiresAt"`
}

// ConnectDecisionResult is returned to the desktop UI after user approval/denial.
type ConnectDecisionResult struct {
	RequestID      string   `json:"requestId"`
	Status         string   `json:"status"`
	GrantedScopes  []string `json:"grantedScopes,omitempty"`
	RedirectTarget string   `json:"redirectTarget,omitempty"`
}

// PollResult represents the outcome of POST /api/hoxi/cap/v1/auth/poll-token.
type PollResult struct {
	Status string     `json:"status"` // pending | approved | denied | expired
	Grant  *GrantInfo `json:"grant,omitempty"`
	Token  string     `json:"token,omitempty"`
}

type connectState struct {
	req               ConnectRequest
	codeChallenge     string
	allowedWorkspaces []string
	consumed          bool
}

type authCodeState struct {
	code              string
	appID             string
	name              string
	grantedScopes     []string
	allowedWorkspaces []string
	redirectURI       string
	codeChallenge     string
	expiresAt         time.Time
}

// ValidateRedirectURI allows loopback HTTP callbacks (127.0.0.1 / [::1]) or
// non-reserved custom app schemes, rejecting public HTTP(S) and dangerous schemes.
func ValidateRedirectURI(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8192 {
		return errors.New("redirectUri is empty or too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return errors.New("redirectUri is not a valid URL")
	}
	if u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" || u.Hostname() == "" {
		return errors.New("redirectUri must not include userinfo or fragment")
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return errors.New("invalid redirectUri query")
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("http redirectUri must use a loopback IP (127.0.0.1 or ::1)")
		}
		if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
			return errors.New("loopback redirectUri must specify an explicit port")
		}
		return nil
	case "https", "javascript", "data", "file", "vbscript", "about", "blob", "hoxi", "hoxi-remote":
		return fmt.Errorf("redirectUri scheme %q is not allowed", scheme)
	default:
		if !strings.Contains(scheme, ".") || !validCustomScheme(scheme) {
			return fmt.Errorf("redirectUri scheme %q is invalid", scheme)
		}
		return nil
	}
}

func validCustomScheme(scheme string) bool {
	if len(scheme) < 2 || len(scheme) > 32 {
		return false
	}
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		isLetter := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

// ValidateCodeChallenge enforces PKCE S256 challenge syntax.
func ValidateCodeChallenge(challenge, method string) error {
	method = strings.TrimSpace(method)
	if method != "" && !strings.EqualFold(method, "S256") {
		return errors.New("only PKCE codeChallengeMethod=S256 is supported")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(challenge)
	if err != nil || len(decoded) != sha256.Size || len(challenge) != 43 {
		return errors.New("invalid PKCE S256 challenge")
	}
	return nil
}

// VerifyPKCE checks codeVerifier against a stored S256 codeChallenge in constant time.
func VerifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || challenge == "" {
		return false
	}
	for _, c := range verifier {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	derived := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(derived), []byte(challenge)) == 1
}

// BeginConnect validates and registers a pending authorization handshake.
func (s *Service) BeginConnect(in ConnectInput) (ConnectRequest, error) {
	appID := strings.TrimSpace(in.AppID)
	if !ValidAppID(appID) {
		return ConnectRequest{}, errors.New("invalid appId")
	}
	scopes, err := NormalizeScopes(in.Scopes)
	if err != nil {
		return ConnectRequest{}, err
	}
	if err := ValidateCodeChallenge(in.CodeChallenge, in.CodeChallengeMethod); err != nil {
		return ConnectRequest{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		if strings.TrimSpace(in.RedirectURI) != "" {
			mode = ConnectModeRedirect
		} else {
			mode = ConnectModePoll
		}
	}
	if mode != ConnectModeRedirect && mode != ConnectModePoll {
		return ConnectRequest{}, errors.New("mode must be redirect or poll")
	}
	redirectURI := strings.TrimSpace(in.RedirectURI)
	if mode == ConnectModeRedirect {
		if len(redirectURI) > 1024 {
			return ConnectRequest{}, errors.New("redirectUri is too long")
		}
		if err := ValidateRedirectURI(redirectURI); err != nil {
			return ConnectRequest{}, err
		}
	} else {
		redirectURI = ""
	}
	name, err := sanitizeAppName(appID, in.Name)
	if err != nil {
		return ConnectRequest{}, err
	}
	if len(in.State) > 512 {
		return ConnectRequest{}, errors.New("state is too long")
	}
	reqSuffix, err := randomHex(8)
	if err != nil {
		return ConnectRequest{}, err
	}
	now := time.Now()
	req := ConnectRequest{
		RequestID:   "con_" + reqSuffix,
		AppID:       appID,
		Name:        name,
		Scopes:      scopes,
		RedirectURI: redirectURI,
		State:       in.State,
		Mode:        mode,
		Status:      "pending",
		CreatedAt:   now.UnixMilli(),
		ExpiresAt:   now.Add(connectRequestTTL).UnixMilli(),
	}

	s.handshakeMu.Lock()
	s.pruneHandshakesLocked(now)
	if len(s.connects) >= maxPendingRequests || len(s.authCodes) >= maxPendingRequests {
		s.handshakeMu.Unlock()
		return ConnectRequest{}, ErrQueueFull
	}
	s.connects[req.RequestID] = &connectState{
		req:           req,
		codeChallenge: strings.TrimSpace(in.CodeChallenge),
	}
	s.handshakeMu.Unlock()
	return cloneConnect(req), nil
}

func cloneConnect(req ConnectRequest) ConnectRequest {
	req.Scopes = slices.Clone(req.Scopes)
	req.GrantedScopes = slices.Clone(req.GrantedScopes)
	return req
}

// ListPendingConnects returns unexpired pending connect requests for desktop UI.
func (s *Service) ListPendingConnects() []ConnectRequest {
	now := time.Now()
	s.handshakeMu.Lock()
	defer s.handshakeMu.Unlock()
	s.pruneHandshakesLocked(now)
	out := make([]ConnectRequest, 0, len(s.connects))
	for _, st := range s.connects {
		if st.req.Status == "pending" {
			out = append(out, cloneConnect(st.req))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// GetPendingConnect fetches one connect request by ID if unexpired.
func (s *Service) GetPendingConnect(requestID string) (ConnectRequest, bool) {
	now := time.Now()
	s.handshakeMu.Lock()
	defer s.handshakeMu.Unlock()
	s.pruneHandshakesLocked(now)
	st, ok := s.connects[strings.TrimSpace(requestID)]
	if !ok {
		return ConnectRequest{}, false
	}
	return cloneConnect(st.req), true
}

// DecideConnect records the user's desktop decision for a pending connect request.
func (s *Service) DecideConnect(requestID string, approve bool, grantedScopes, allowedWorkspaces []string, hostBaseURL string) (ConnectDecisionResult, error) {
	now := time.Now()
	s.handshakeMu.Lock()
	defer s.handshakeMu.Unlock()
	s.pruneHandshakesLocked(now)

	st, ok := s.connects[strings.TrimSpace(requestID)]
	if !ok {
		return ConnectDecisionResult{}, errors.New("connect request not found or expired")
	}
	if st.req.Status != "pending" {
		return ConnectDecisionResult{}, errors.New("connect request has already been decided")
	}

	if !approve {
		st.req.Status = "denied"
		res := ConnectDecisionResult{RequestID: st.req.RequestID, Status: "denied"}
		if st.req.Mode == ConnectModeRedirect {
			res.RedirectTarget = appendQueryParams(st.req.RedirectURI, map[string]string{
				"error": "access_denied",
				"state": st.req.State,
			})
		}
		return res, nil
	}

	if grantedScopes == nil {
		grantedScopes = st.req.Scopes
	}
	normScopes, err := NormalizeScopes(grantedScopes)
	if err != nil {
		return ConnectDecisionResult{}, err
	}
	// Never grant scopes that were not requested in the original handshake.
	for _, sc := range normScopes {
		if !HasScope(st.req.Scopes, sc) {
			return ConnectDecisionResult{}, fmt.Errorf("scope %q was not requested by %s", sc, st.req.AppID)
		}
	}
	normDirs, err := sanitizeWorkspaces(allowedWorkspaces)
	if err != nil {
		return ConnectDecisionResult{}, err
	}

	st.req.Status = "approved"
	st.req.GrantedScopes = normScopes
	st.allowedWorkspaces = normDirs
	res := ConnectDecisionResult{
		RequestID:     st.req.RequestID,
		Status:        "approved",
		GrantedScopes: slices.Clone(normScopes),
	}

	if st.req.Mode == ConnectModeRedirect {
		codeSuffix, err := randomHex(20)
		if err != nil {
			st.req.Status, st.req.GrantedScopes, st.allowedWorkspaces = "pending", nil, nil
			return ConnectDecisionResult{}, err
		}
		code := authCodePrefix + codeSuffix
		s.authCodes[code] = &authCodeState{
			code:              code,
			appID:             st.req.AppID,
			name:              st.req.Name,
			grantedScopes:     normScopes,
			allowedWorkspaces: normDirs,
			redirectURI:       st.req.RedirectURI,
			codeChallenge:     st.codeChallenge,
			expiresAt:         now.Add(authCodeTTL),
		}
		st.consumed = true
		params := map[string]string{"code": code, "state": st.req.State}
		if strings.TrimSpace(hostBaseURL) != "" {
			params["baseURL"] = strings.TrimRight(strings.TrimSpace(hostBaseURL), "/")
		}
		res.RedirectTarget = appendQueryParams(st.req.RedirectURI, params)
	}
	return res, nil
}

// ExchangeAuthCode verifies a single-use hxc_* code with PKCE S256 and mints a
// persistent hk_app_* token.
func (s *Service) ExchangeAuthCode(code, codeVerifier, redirectURI string) (GrantInfo, string, error) {
	now := time.Now()
	s.handshakeMu.Lock()
	s.pruneHandshakesLocked(now)
	st, ok := s.authCodes[strings.TrimSpace(code)]
	if !ok {
		s.handshakeMu.Unlock()
		return GrantInfo{}, "", errors.New("authorization code is invalid or expired")
	}
	if redirectURI != st.redirectURI {
		s.handshakeMu.Unlock()
		return GrantInfo{}, "", errors.New("redirectUri mismatch")
	}
	if !VerifyPKCE(codeVerifier, st.codeChallenge) {
		s.handshakeMu.Unlock()
		return GrantInfo{}, "", errors.New("PKCE codeVerifier mismatch")
	}
	delete(s.authCodes, st.code) // single-use, after validating proof
	s.handshakeMu.Unlock()
	return s.CreateGrant(st.appID, st.name, st.grantedScopes, st.allowedWorkspaces)
}

// PollToken checks a poll-mode connect request and mints a token once approved.
func (s *Service) PollToken(requestID, codeVerifier string) (PollResult, error) {
	now := time.Now()
	s.handshakeMu.Lock()
	s.pruneHandshakesLocked(now)
	st, ok := s.connects[strings.TrimSpace(requestID)]
	if !ok {
		s.handshakeMu.Unlock()
		return PollResult{Status: "expired"}, nil
	}
	if st.req.Mode != ConnectModePoll {
		s.handshakeMu.Unlock()
		return PollResult{}, errors.New("request is not in poll mode")
	}
	if !VerifyPKCE(codeVerifier, st.codeChallenge) {
		s.handshakeMu.Unlock()
		return PollResult{}, errors.New("PKCE codeVerifier mismatch")
	}
	switch st.req.Status {
	case "pending":
		s.handshakeMu.Unlock()
		return PollResult{Status: "pending"}, nil
	case "denied":
		delete(s.connects, st.req.RequestID)
		s.handshakeMu.Unlock()
		return PollResult{Status: "denied"}, nil
	case "approved":
		if st.consumed {
			delete(s.connects, st.req.RequestID)
			s.handshakeMu.Unlock()
			return PollResult{Status: "expired"}, nil
		}
		st.consumed = true
		appID, name := st.req.AppID, st.req.Name
		scopes := append([]string(nil), st.req.GrantedScopes...)
		dirs := append([]string(nil), st.allowedWorkspaces...)
		delete(s.connects, st.req.RequestID)
		s.handshakeMu.Unlock()

		grant, token, err := s.CreateGrant(appID, name, scopes, dirs)
		if err != nil {
			return PollResult{}, err
		}
		return PollResult{Status: "approved", Grant: &grant, Token: token}, nil
	default:
		s.handshakeMu.Unlock()
		return PollResult{Status: "expired"}, nil
	}
}

func (s *Service) pruneHandshakesLocked(now time.Time) {
	nowMs := now.UnixMilli()
	for id, st := range s.connects {
		if st.req.ExpiresAt <= nowMs {
			delete(s.connects, id)
		}
	}
	for code, st := range s.authCodes {
		if !st.expiresAt.After(now) {
			delete(s.authCodes, code)
		}
	}
}

func appendQueryParams(rawURI string, params map[string]string) string {
	u, err := url.Parse(rawURI)
	if err != nil {
		return rawURI
	}
	q := u.Query()
	for _, k := range []string{"code", "error", "state", "baseURL"} {
		q.Del(k)
	}
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
