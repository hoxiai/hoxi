package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/appbridge"
	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	appCapabilityPath = "/api/hoxi/cap/v1"
	hostMCPPath       = "/api/hoxi/mcp"
	appGrantPath      = "/api/hoxi/app-grants"
	appIDHeader       = "X-Hoxi-App-Id"
	appRequestLimit   = 64 << 10
)

func isAppProtocolPath(path string) bool {
	return path == hostMCPPath || strings.HasPrefix(path, hostMCPPath+"/") || path == appCapabilityPath || strings.HasPrefix(path, appCapabilityPath+"/")
}

func isAppGrantPath(path string) bool {
	return path == appGrantPath || strings.HasPrefix(path, appGrantPath+"/")
}

func denyRemoteAppProtocol(w http.ResponseWriter, requestPath string) bool {
	clean := path.Clean(requestPath)
	if !isAppProtocolPath(requestPath) && !isAppGrantPath(requestPath) && !isAppProtocolPath(clean) && !isAppGrantPath(clean) {
		return false
	}
	writeAppError(w, http.StatusForbidden, "REMOTE_FORBIDDEN", "application protocol is local only")
	return true
}

// GuardAppProtocolFromRemote also protects the dedicated remote development
// host listener, which intentionally rewrites remote Host to loopback.
func GuardAppProtocolFromRemote(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !denyRemoteAppProtocol(w, r.URL.Path) {
			next.ServeHTTP(w, r)
		}
	})
}

func (g *Gateway) AppBridge() *appbridge.Service { return g.appBridge }

func (g *Gateway) BeginAppConnect(in appbridge.ConnectInput) (appbridge.ConnectRequest, error) {
	if g.appBridge == nil {
		return appbridge.ConnectRequest{}, appbridge.ErrStorage
	}
	req, err := g.appBridge.BeginConnect(in)
	if err == nil {
		g.QueueDesktopIntent(DesktopIntent{Action: DesktopIntentActionConnectApp, RequestID: req.RequestID})
	}
	return req, err
}

func writeAppError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeAppServiceError(w http.ResponseWriter, err error, status int, code string) {
	switch {
	case errors.Is(err, appbridge.ErrStorage):
		writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "application grant storage unavailable")
	case errors.Is(err, appbridge.ErrQueueFull):
		writeAppError(w, http.StatusTooManyRequests, "RATE_LIMITED", "application request queue is full")
	case errors.Is(err, appbridge.ErrGrantNotFound):
		writeAppError(w, http.StatusNotFound, "NOT_FOUND", "grant not found")
	default:
		writeAppError(w, status, code, err.Error())
	}
}

func (g *Gateway) admitAppUpdateRequest(w http.ResponseWriter, r *http.Request) bool {
	if g.reserveUpdateRequest(r) {
		return true
	}
	writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "update in progress")
	return false
}

func appMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeAppError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", method+" required")
	return false
}

func decodeAppJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeAppError(w, http.StatusUnsupportedMediaType, "INVALID_REQUEST", "Content-Type must be application/json")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err == nil && decoder.Decode(new(any)) == io.EOF {
		return true
	}
	writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON payload")
	return false
}

func appLoopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback()
}

func appLoopbackOrigin(origin string) bool {
	if !validUIOrigin(origin) {
		return false
	}
	u, _ := url.Parse(origin)
	host := u.Hostname()
	return (u.Scheme == "http" || u.Scheme == "https") && (host == "localhost" || net.ParseIP(host).IsLoopback())
}

func (g *Gateway) appBaseURL() string {
	if !strings.HasPrefix(g.wsBaseURL, "ws://") && !strings.HasPrefix(g.wsBaseURL, "wss://") {
		return ""
	}
	return "http" + strings.TrimPrefix(g.wsBaseURL, "ws")
}

type appCapability struct {
	path, method, scope       string
	tool, description, schema string
}

var appCapabilities = []appCapability{
	{"/llm/models", "GET", appbridge.ScopeLLMModels, "llm_models", "List configured chat models", `{"type":"object","additionalProperties":false}`},
	{"/llm/chat/completions", "POST", appbridge.ScopeLLMChat, "llm_chat", "Call a configured chat model (non-streaming over MCP)", `{"type":"object","properties":{"model":{"type":"string"},"messages":{"type":"array","items":{"type":"object"}}},"required":["model","messages"]}`},
	{"/media/capabilities", "GET", appbridge.ScopeMediaCapabilities, "media_capabilities", "List configured media capabilities", `{"type":"object","additionalProperties":false}`},
	{"/media/image", "POST", appbridge.ScopeMediaImage, "media_image", "Generate images and return inline data", `{"type":"object","properties":{"prompt":{"type":"string"},"aspectRatio":{"type":"string"},"imageSize":{"type":"string"}},"required":["prompt"]}`},
	{"/media/video", "POST", appbridge.ScopeMediaVideo, "media_video", "Generate video and return inline data", `{"type":"object","properties":{"prompt":{"type":"string"},"aspectRatio":{"type":"string"}},"required":["prompt"]}`},
	{"/media/speech", "POST", appbridge.ScopeMediaSpeech, "media_speech", "Synthesize speech and return inline data", `{"type":"object","properties":{"text":{"type":"string"},"voice":{"type":"string"},"format":{"type":"string"}},"required":["text"]}`},
	{"/media/transcribe", "POST", appbridge.ScopeMediaTranscribe, "media_transcribe", "Transcribe an audio data URL", `{"type":"object","properties":{"dataUrl":{"type":"string"},"language":{"type":"string"}},"required":["dataUrl"]}`},
	{"/notify", "POST", appbridge.ScopeNotifySend, "notify", "Show an app-labelled desktop notification", `{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"}},"required":["title"]}`},
	{"/approval/ask", "POST", appbridge.ScopeApprovalRequest, "approval_ask", "Ask the desktop user for a decision", `{"type":"object","properties":{"title":{"type":"string"},"summary":{"type":"string"},"riskLevel":{"enum":["low","medium","high"]},"details":{"type":"object","additionalProperties":{"type":"string"}},"async":{"type":"boolean"}},"required":["title"]}`},
	{"/approval/result", "GET", appbridge.ScopeApprovalRequest, "", "Read an approval result owned by the calling grant", ""},
}

func (g *Gateway) appDiscovery() map[string]any {
	routes := make(map[string]string, len(appCapabilities))
	for _, cap := range appCapabilities {
		routes[cap.path] = cap.scope
	}
	return map[string]any{"protocol": appbridge.ProtocolVersion, "version": buildinfo.Version,
		"baseURL": g.appBaseURL(), "capabilityPath": appCapabilityPath, "mcpPath": hostMCPPath,
		"mcpVersion": hostMCPVersion, "scopes": appbridge.AllScopes(), "routes": routes}
}

func (g *Gateway) serveAppProtocol(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if isRemoteRequest(r) {
		denyRemoteAppProtocol(w, r.URL.Path)
		return
	}
	if !trustedHost(r.Host) || !appLoopbackPeer(r) {
		writeAppError(w, http.StatusForbidden, "UNTRUSTED_HOST", "a loopback connection and trusted Host are required")
		return
	}
	w.Header().Add("Vary", "Origin")
	if origins := r.Header.Values("Origin"); len(origins) > 0 {
		if len(origins) != 1 || !appLoopbackOrigin(origins[0]) {
			writeAppError(w, http.StatusForbidden, "UNTRUSTED_ORIGIN", "only loopback HTTP(S) origins are allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origins[0])
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Hoxi-App-Id, MCP-Protocol-Version")
	} else {
		for _, site := range r.Header.Values("Sec-Fetch-Site") {
			if site != "" && site != "same-origin" && site != "same-site" && site != "none" {
				writeAppError(w, http.StatusForbidden, "UNTRUSTED_ORIGIN", "untrusted browser request")
				return
			}
		}
	}
	if r.URL.RawPath != "" {
		writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "encoded route paths are not accepted")
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if updateMutation(r) {
		if !g.admitAppUpdateRequest(w, r) {
			return
		}
		defer g.finishUpdateRequest()
	}
	switch r.URL.Path {
	case appCapabilityPath + "/discovery":
		if appMethod(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, g.appDiscovery())
		}
		return
	case appCapabilityPath + "/auth/poll-init", appCapabilityPath + "/auth/poll-token", appCapabilityPath + "/auth/token":
		if !appMethod(w, r, http.MethodPost) {
			return
		}
		if g.appBridge == nil {
			writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "application bridge unavailable")
			return
		}
		g.serveAppAuth(w, r)
		return
	case hostMCPPath:
		if !appMethod(w, r, http.MethodPost) {
			return
		}
		if grant, ok := g.authenticateApp(w, r); ok {
			g.serveHostMCP(w, r, grant)
		}
		return
	}
	for _, cap := range appCapabilities {
		if r.URL.Path != appCapabilityPath+cap.path {
			continue
		}
		if !appMethod(w, r, cap.method) {
			return
		}
		if grant, ok := g.authenticateApp(w, r); ok {
			g.serveAppCapability(w, r, cap, grant)
		}
		return
	}
	writeAppError(w, http.StatusNotFound, "NOT_FOUND", "unknown application protocol route")
}

func (g *Gateway) authenticateApp(w http.ResponseWriter, r *http.Request) (appbridge.GrantInfo, bool) {
	if g.appBridge == nil {
		writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "application bridge unavailable")
		return appbridge.GrantInfo{}, false
	}
	auth, ids := r.Header.Values("Authorization"), r.Header.Values(appIDHeader)
	if len(auth) == 1 && len(ids) == 1 {
		kind, token, found := strings.Cut(auth[0], " ")
		if found && strings.EqualFold(kind, "Bearer") {
			grant, err := g.appBridge.VerifyToken(token, ids[0])
			if err == nil {
				return grant, true
			}
			if !errors.Is(err, appbridge.ErrInvalidToken) {
				writeAppServiceError(w, err, http.StatusServiceUnavailable, "UNAVAILABLE")
				return appbridge.GrantInfo{}, false
			}
		}
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeAppError(w, http.StatusUnauthorized, "INVALID_TOKEN", "a valid bearer token and matching X-Hoxi-App-Id are required")
	return appbridge.GrantInfo{}, false
}

func (g *Gateway) serveAppAuth(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case appCapabilityPath + "/auth/poll-init":
		var input struct {
			AppID               string   `json:"appId"`
			Name                string   `json:"name"`
			Scopes              []string `json:"scopes"`
			CodeChallenge       string   `json:"codeChallenge"`
			CodeChallengeMethod string   `json:"codeChallengeMethod"`
			State               string   `json:"state"`
		}
		if !decodeAppJSON(w, r, &input, appRequestLimit) {
			return
		}
		req, err := g.BeginAppConnect(appbridge.ConnectInput{AppID: input.AppID, Name: input.Name, Scopes: input.Scopes,
			CodeChallenge: input.CodeChallenge, CodeChallengeMethod: input.CodeChallengeMethod, State: input.State, Mode: appbridge.ConnectModePoll})
		if err != nil {
			writeAppServiceError(w, err, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"requestId": req.RequestID, "status": req.Status, "expiresAt": req.ExpiresAt, "openURL": "hoxi://connect?requestId=" + req.RequestID})
	case appCapabilityPath + "/auth/poll-token":
		var input struct {
			RequestID    string `json:"requestId"`
			CodeVerifier string `json:"codeVerifier"`
		}
		if !decodeAppJSON(w, r, &input, appRequestLimit) {
			return
		}
		res, err := g.appBridge.PollToken(input.RequestID, input.CodeVerifier)
		if err != nil {
			writeAppServiceError(w, err, http.StatusBadRequest, "INVALID_PROOF")
			return
		}
		writeJSON(w, http.StatusOK, res)
	case appCapabilityPath + "/auth/token":
		var input struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"codeVerifier"`
			RedirectURI  string `json:"redirectUri"`
		}
		if !decodeAppJSON(w, r, &input, appRequestLimit) {
			return
		}
		grant, token, err := g.appBridge.ExchangeAuthCode(input.Code, input.CodeVerifier, input.RedirectURI)
		if err != nil {
			writeAppServiceError(w, err, http.StatusBadRequest, "INVALID_PROOF")
			return
		}
		writeJSON(w, http.StatusOK, appbridge.PollResult{Status: "approved", Grant: &grant, Token: token})
	}
}

type appMediaRequestKey struct{}

type appMediaOutput struct {
	Mime string `json:"mime"`
	Data []byte `json:"data"` // base64 on the wire
}

func writeAppMediaFailure(w http.ResponseWriter, r *http.Request, err error) bool {
	if !isAppMediaRequest(r) {
		return false
	}
	if errors.Is(err, llm.ErrUnavailable) {
		writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "no configured model supports this capability")
	} else {
		writeAppError(w, http.StatusBadGateway, "UPSTREAM_ERROR", "configured media provider failed")
	}
	return true
}

func isAppMediaRequest(r *http.Request) bool {
	isApp, _ := r.Context().Value(appMediaRequestKey{}).(bool)
	return isApp
}

func (g *Gateway) serveAppCapability(w http.ResponseWriter, r *http.Request, cap appCapability, grant appbridge.GrantInfo) {
	if !appbridge.HasScope(grant.Scopes, cap.scope) {
		writeAppError(w, http.StatusForbidden, "SCOPE_MISSING", "required scope: "+cap.scope)
		return
	}
	if cap.path == "/llm/models" || cap.path == "/llm/chat/completions" {
		if g.providers == nil {
			writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "provider service unavailable")
			return
		}
		if cap.path == "/llm/models" {
			g.providers.ServeAuthorizedRelayModels(w)
			return
		}
		g.providers.ServeAuthorizedRelayChat(w, r, grant.ID)
		return
	}
	// This context marker is never derived from a header. The shared media
	// handlers use it for inline output and to disable filesystem/session access.
	media := func() *http.Request { return r.WithContext(context.WithValue(r.Context(), appMediaRequestKey{}, true)) }
	switch cap.path {
	case "/media/capabilities":
		g.handleMediaCapabilities(w, r)
	case "/media/image":
		g.handleMediaImage(w, media())
	case "/media/video":
		g.handleMediaVideo(w, media())
	case "/media/speech":
		g.handleMediaSpeech(w, media())
	case "/media/transcribe":
		r.Body = http.MaxBytesReader(w, r.Body, 2*maxLocalMediaUploadBytes)
		g.handleMediaTranscribe(w, media())
	case "/notify":
		g.handleAppNotify(w, r, grant)
	case "/approval/ask":
		g.handleAppApproval(w, r, grant)
	case "/approval/result":
		res, found := g.appBridge.ApprovalResult(grant.ID, r.URL.Query().Get("approvalId"))
		if !found {
			writeAppError(w, http.StatusNotFound, "NOT_FOUND", "approval not found or expired")
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func (g *Gateway) handleAppNotify(w http.ResponseWriter, r *http.Request, grant appbridge.GrantInfo) {
	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if !decodeAppJSON(w, r, &input, appRequestLimit) {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len(input.Title) > 160 || len(input.Body) > 1000 || strings.ContainsAny(input.Title, "\r\n") {
		writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid notification title or body")
		return
	}
	g.notifyMu.RLock()
	send := g.onDesktopNotify
	g.notifyMu.RUnlock()
	if send == nil {
		writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "desktop notifications unavailable")
		return
	}
	if !g.appBridge.AllowNotify(grant.AppID) {
		w.Header().Set("Retry-After", "60")
		writeAppError(w, http.StatusTooManyRequests, "RATE_LIMITED", "at most ten notifications per minute per app")
		return
	}
	if err := send(DesktopNotification{Title: "[" + grant.Name + "] " + input.Title, Body: strings.TrimSpace(input.Body)}); err != nil {
		writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "desktop notification could not be delivered")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (g *Gateway) handleAppApproval(w http.ResponseWriter, r *http.Request, grant appbridge.GrantInfo) {
	var input struct {
		Title     string            `json:"title"`
		Summary   string            `json:"summary"`
		RiskLevel string            `json:"riskLevel"`
		Details   map[string]string `json:"details"`
		Async     bool              `json:"async"`
	}
	if !decodeAppJSON(w, r, &input, appRequestLimit) {
		return
	}
	req, wait, err := g.appBridge.EnqueueApproval(grant, input.Title, input.Summary, input.RiskLevel, input.Details)
	if err != nil {
		writeAppServiceError(w, err, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	g.QueueDesktopIntent(DesktopIntent{Action: DesktopIntentActionSettings, Params: map[string]string{"section": "app-grants"}})
	if input.Async {
		writeJSON(w, http.StatusAccepted, req)
		return
	}
	timer := time.NewTimer(time.Until(time.UnixMilli(req.ExpiresAt)))
	defer timer.Stop()
	defer g.appBridge.CancelApproval(grant.ID, req.ApprovalID)
	select {
	case res, ok := <-wait:
		if !ok {
			writeAppError(w, http.StatusForbidden, "APPROVAL_CANCELLED", "desktop approval is no longer active")
			return
		}
		if res.Status != "expired" {
			if res.Status == "approved" {
				current, valid := g.authenticateApp(w, r)
				if !valid {
					return
				}
				if current.ID != grant.ID || !appbridge.HasScope(current.Scopes, appbridge.ScopeApprovalRequest) {
					writeAppError(w, http.StatusForbidden, "APPROVAL_CANCELLED", "desktop approval is no longer active")
					return
				}
			}
			writeJSON(w, http.StatusOK, res)
			return
		}
		writeAppError(w, http.StatusGatewayTimeout, "APPROVAL_EXPIRED", "desktop approval expired")
	case <-timer.C:
		writeAppError(w, http.StatusGatewayTimeout, "APPROVAL_EXPIRED", "desktop approval expired")
	case <-r.Context().Done():
	}
}
