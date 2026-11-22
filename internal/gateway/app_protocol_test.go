package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/appbridge"
	"github.com/hoxiai/hoxi/internal/provider"
)

const (
	appTestVerifier     = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	appTestChallenge    = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	appTestID           = "demo.app"
	appTestDesktopToken = "test-desktop-token"
)

func newAppProtocolGateway(t *testing.T) *Gateway {
	t.Helper()
	t.Setenv("HOXI_HOME", t.TempDir())
	g := &Gateway{mux: http.NewServeMux(), appBridge: appbridge.NewService(), providers: &provider.Service{}}
	g.SetGatewayToken(appTestDesktopToken)
	g.wsBaseURL = "ws://127.0.0.1:5410"
	g.registerAppGrantAdminRoutes()
	return g
}

func appTestRequest(method, path, body, token string) *http.Request {
	r := localRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set(appIDHeader, appTestID)
	}
	return r
}

func appTestServe(g *Gateway, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func appTestAdmin(g *Gateway, method, path, body string) *httptest.ResponseRecorder {
	r := appTestRequest(method, appGrantPath+path, body, "")
	r.Header.Set(gatewayTokenHeader, appTestDesktopToken)
	r.Header.Set("Origin", "wails://wails")
	return appTestServe(g, WithAppProtocolDesktop(r))
}

func appTestStatus(t *testing.T, w *httptest.ResponseRecorder, status int, text string) {
	t.Helper()
	if w.Code != status || !strings.Contains(w.Body.String(), text) {
		t.Fatalf("want %d %q; got %d: %s", status, text, w.Code, w.Body.String())
	}
}

func appTestDecode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid response (%d): %v", w.Code, err)
	}
	return out
}

func appTestIssue(t *testing.T, g *Gateway, scopes ...string) string {
	t.Helper()
	_, token, err := g.AppBridge().CreateGrant(appTestID, "Demo", scopes, nil)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestAppProtocolDiscoveryAndBoundaries(t *testing.T) {
	g := newAppProtocolGateway(t)
	path := appCapabilityPath + "/discovery"
	r := appTestRequest(http.MethodGet, path, "", "")
	r.Header.Set("Origin", "http://localhost:5421")
	w := appTestServe(g, r)
	appTestStatus(t, w, http.StatusOK, appbridge.ProtocolVersion)
	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5421" || !strings.Contains(w.Body.String(), g.appBaseURL()) {
		t.Fatal("discovery did not return actual endpoint / allowed origin")
	}
	r.Method = http.MethodOptions
	appTestStatus(t, appTestServe(g, r), http.StatusNoContent, "")
	for _, origin := range []string{"https://example.com", "null", "wails://wails", "http://127.0.0.1:5410/"} {
		r := appTestRequest(http.MethodGet, path, "", "")
		r.Header.Set("Origin", origin)
		w := appTestServe(g, r)
		appTestStatus(t, w, http.StatusForbidden, "UNTRUSTED_ORIGIN")
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("reflected an untrusted origin")
		}
	}
	for _, host := range []string{"example.com", "127.0.0.1.example.com"} {
		r := appTestRequest(http.MethodGet, path, "", "")
		r.Host = host
		appTestStatus(t, appTestServe(g, r), http.StatusForbidden, "UNTRUSTED_HOST")
	}
	r = appTestRequest(http.MethodGet, path, "", "")
	r.RemoteAddr = "192.0.2.1:3456"
	appTestStatus(t, appTestServe(g, r), http.StatusForbidden, "UNTRUSTED_HOST")
	for _, path := range []string{path, hostMCPPath, appGrantPath, appGrantPath + "/pending/con_x/decision"} {
		w := httptest.NewRecorder()
		g.serveTunnelRequest(w, appTestRequest(http.MethodGet, path, "", ""))
		appTestStatus(t, w, http.StatusForbidden, "REMOTE_FORBIDDEN")
		for _, source := range []string{remoteSourceTunnel, remoteSourceLAN, remoteSourceRelay} {
			r := appTestRequest(http.MethodGet, path, "", "")
			w := httptest.NewRecorder()
			g.serveRemoteRequest(w, r, source)
			appTestStatus(t, w, http.StatusForbidden, "REMOTE_FORBIDDEN")
			r = WithAppProtocolDesktop(r.WithContext(context.WithValue(r.Context(), remoteSourceKey{}, source)))
			appTestStatus(t, appTestServe(g, r), http.StatusForbidden, "REMOTE_FORBIDDEN")
		}
	}
}

func TestAppProtocolPKCEAndGrantAdministration(t *testing.T) {
	g := newAppProtocolGateway(t)
	in := appbridge.ConnectInput{AppID: appTestID, Name: "Demo", Scopes: []string{appbridge.ScopeLLMModels}, RedirectURI: "http://127.0.0.1:2468/callback", CodeChallenge: appTestChallenge, CodeChallengeMethod: "S256", State: "opaque-state"}
	req, err := g.BeginAppConnect(in)
	if err != nil {
		t.Fatal(err)
	}
	decisionPath := "/pending/" + req.RequestID + "/decision"
	appTestStatus(t, appTestAdmin(g, http.MethodGet, "/pending", ""), http.StatusOK, req.RequestID)
	r := appTestRequest(http.MethodPost, appGrantPath+decisionPath, `{"approve":true}`, "")
	r.Host = "wails"
	r.Header.Set("Origin", "wails://wails")
	r.Header.Set(gatewayTokenHeader, appTestDesktopToken)
	appTestStatus(t, appTestServe(g, r), http.StatusForbidden, "DESKTOP_REQUIRED")
	r.Header.Del(gatewayTokenHeader)
	appTestStatus(t, appTestServe(g, WithAppProtocolDesktop(r)), http.StatusForbidden, "DESKTOP_REQUIRED")
	var callback string
	g.SetAppConnectRedirect(func(_ context.Context, target string) error { callback = target; return nil })
	appTestStatus(t, appTestAdmin(g, http.MethodPost, decisionPath, `{"approve":true,"scopes":[]}`), http.StatusBadRequest, "INVALID_REQUEST")
	w := appTestAdmin(g, http.MethodPost, decisionPath, `{"approve":true}`)
	appTestStatus(t, w, http.StatusOK, "approved")
	dec := appTestDecode[appbridge.ConnectDecisionResult](t, w)
	if dec.RedirectTarget != "" {
		t.Fatal("callback was sent to the UI after native delivery")
	}
	target, err := url.Parse(callback)
	if err != nil || target.Query().Get("state") != in.State || target.Query().Get("baseURL") != g.appBaseURL() {
		t.Fatal("invalid callback")
	}
	code := target.Query().Get("code")
	exchange := func(verifier, uri string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]string{"code": code, "codeVerifier": verifier, "redirectUri": uri})
		return appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/auth/token", string(payload), ""))
	}
	appTestStatus(t, exchange(strings.Repeat("x", 43), in.RedirectURI), http.StatusBadRequest, "INVALID_PROOF")
	w = exchange(appTestVerifier, in.RedirectURI)
	appTestStatus(t, w, http.StatusOK, "approved")
	grant := appTestDecode[appbridge.PollResult](t, w)
	appTestStatus(t, exchange(appTestVerifier, in.RedirectURI), http.StatusBadRequest, "INVALID_PROOF")
	r = appTestRequest(http.MethodGet, appCapabilityPath+"/llm/models", "", grant.Token)
	appTestStatus(t, appTestServe(g, r), http.StatusOK, `"object":"list"`)
	r.Header.Set(appIDHeader, "other.app")
	appTestStatus(t, appTestServe(g, r), http.StatusUnauthorized, "INVALID_TOKEN")
	appTestStatus(t, appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/llm/chat/completions", "{}", grant.Token)), http.StatusForbidden, "SCOPE_MISSING")
	appTestStatus(t, appTestAdmin(g, http.MethodPatch, "/"+grant.Grant.ID, `{"scopes":["browser:control"]}`), http.StatusBadRequest, "new consent")
	appTestStatus(t, appTestAdmin(g, http.MethodDelete, "/"+grant.Grant.ID, ""), http.StatusNoContent, "")
	appTestStatus(t, appTestServe(g, appTestRequest(http.MethodGet, appCapabilityPath+"/llm/models", "", grant.Token)), http.StatusUnauthorized, "INVALID_TOKEN")
}

func TestAppProtocolPollAndFileConfinement(t *testing.T) {
	g := newAppProtocolGateway(t)
	payload := `{"appId":"demo.app","name":"Demo","scopes":["media:image"],"codeChallenge":"` + appTestChallenge + `","codeChallengeMethod":"S256"}`
	w := appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/auth/poll-init", payload, ""))
	appTestStatus(t, w, http.StatusAccepted, "hoxi://connect?requestId=")
	req := appTestDecode[appbridge.ConnectRequest](t, w)
	poll := func() *httptest.ResponseRecorder {
		return appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/auth/poll-token", `{"requestId":"`+req.RequestID+`","codeVerifier":"`+appTestVerifier+`"}`, ""))
	}
	appTestStatus(t, poll(), http.StatusOK, "pending")
	appTestStatus(t, appTestAdmin(g, http.MethodPost, "/pending/"+req.RequestID+"/decision", `{"approve":true}`), http.StatusOK, "approved")
	w = poll()
	appTestStatus(t, w, http.StatusOK, "approved")
	grant := appTestDecode[appbridge.PollResult](t, w)
	appTestStatus(t, poll(), http.StatusOK, "expired")
	for _, body := range []string{`{"inputPaths":["notes.png"]}`, `{"outputPath":"notes.png"}`, `{"sessionID":"ses_other"}`} {
		appTestStatus(t, appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/media/image", body, grant.Token)), http.StatusForbidden, "FILES_FORBIDDEN")
	}
}

func TestHostMCPScopedCalls(t *testing.T) {
	g := newAppProtocolGateway(t)
	token := appTestIssue(t, g, appbridge.ScopeLLMModels)
	call := func(body string) *httptest.ResponseRecorder {
		r := appTestRequest(http.MethodPost, hostMCPPath, body, token)
		r.Header.Set("Accept", "application/json, text/event-stream")
		return appTestServe(g, r)
	}
	appTestStatus(t, call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`), http.StatusOK, hostMCPVersion)
	appTestStatus(t, call(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), http.StatusAccepted, "")
	w := call(`{"jsonrpc":"2.0","id":"list","method":"tools/list"}`)
	appTestStatus(t, w, http.StatusOK, `"name":"llm_models"`)
	if strings.Contains(w.Body.String(), `"name":"media_image"`) {
		t.Fatal("listed an ungranted tool")
	}
	appTestStatus(t, call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"llm_models"}}`), http.StatusOK, `"isError":false`)
	appTestStatus(t, call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"media_image"}}`), http.StatusOK, "SCOPE_MISSING")
	appTestStatus(t, call(`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`), http.StatusOK, hostMCPDiscoveryURI)
	appTestStatus(t, call(`{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"file:///private/file"}}`), http.StatusOK, "Resource not found")
	appTestStatus(t, call(`{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"`+hostMCPDiscoveryURI+`"}}`), http.StatusOK, appbridge.ProtocolVersion)
	r := appTestRequest(http.MethodPost, hostMCPPath, `{"jsonrpc":"2.0","id":8,"method":"ping"}`, token)
	r.Header.Set("Accept", "text/event-stream")
	appTestStatus(t, appTestServe(g, r), http.StatusOK, "event: message\ndata:")
	w = call(`[{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":9,"method":"ping"},{"jsonrpc":"2.0","id":10,"method":"tools/list"}]`)
	appTestStatus(t, w, http.StatusOK, `"id":10`)
	if strings.Count(w.Body.String(), "event: message") != 2 {
		t.Fatal("wrong number of batch replies")
	}
	appTestStatus(t, call(`{`), http.StatusOK, "-32700")
}

func TestAppProtocolDesktopApprovalAndNotifications(t *testing.T) {
	g := newAppProtocolGateway(t)
	token := appTestIssue(t, g, appbridge.ScopeApprovalRequest, appbridge.ScopeNotifySend)
	seen := make(chan struct{}, 1)
	g.SetDesktopIntentNotifier(func() { seen <- struct{}{} })
	ask := func(body string) *httptest.ResponseRecorder {
		return appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/approval/ask", body, token))
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		done <- appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/approval/ask", `{"title":"Confirm"}`, token).WithContext(ctx))
	}()
	select {
	case <-seen:
	case <-ctx.Done():
		t.Fatal("approval never reached desktop queue")
	}
	items := appTestDecode[[]appbridge.ApprovalRequest](t, appTestAdmin(g, http.MethodGet, "/approvals", ""))
	if len(items) != 1 || items[0].Status != "pending" {
		t.Fatal("missing pending approval")
	}
	select {
	case <-done:
		t.Fatal("returned before desktop decision")
	default:
	}
	appTestStatus(t, appTestAdmin(g, http.MethodPost, "/approvals/"+items[0].ApprovalID+"/decision", `{"approve":true}`), http.StatusOK, "approved")
	select {
	case w := <-done:
		appTestStatus(t, w, http.StatusOK, "approved")
	case <-ctx.Done():
		t.Fatal("approval did not complete")
	}

	w := ask(`{"title":"Reject me","async":true}`)
	appTestStatus(t, w, http.StatusAccepted, "pending")
	id := appTestDecode[appbridge.ApprovalRequest](t, w).ApprovalID
	appTestStatus(t, appTestAdmin(g, http.MethodPost, "/approvals/"+id+"/decision", `{"approve":false}`), http.StatusOK, "denied")
	result := appCapabilityPath + "/approval/result?approvalId=" + id
	appTestStatus(t, appTestServe(g, appTestRequest(http.MethodGet, result, "", token)), http.StatusOK, "denied")
	other := appTestIssue(t, g, appbridge.ScopeApprovalRequest)
	appTestStatus(t, appTestServe(g, appTestRequest(http.MethodGet, result, "", other)), http.StatusNotFound, "NOT_FOUND")

	calls := 0
	g.SetDesktopNotifier(func(n DesktopNotification) error {
		calls++
		if n.Title != "[Demo] Hello" || n.SessionID != "" {
			t.Errorf("bad notification: %+v", n)
		}
		return nil
	})
	for i := range 11 {
		w := appTestServe(g, appTestRequest(http.MethodPost, appCapabilityPath+"/notify", `{"title":"Hello"}`, token))
		if i < 10 {
			appTestStatus(t, w, http.StatusOK, "ok")
		} else {
			appTestStatus(t, w, http.StatusTooManyRequests, "RATE_LIMITED")
		}
	}
	if calls != 10 {
		t.Fatalf("notification count %d", calls)
	}
}
