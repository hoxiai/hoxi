package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/computer"
)

func (g *Gateway) SetComputerAgentToken(token string) {
	g.originMu.Lock()
	g.computerAgentToken = token
	g.originMu.Unlock()
}

// ComputerAppConfirmation describes one app grant shown in the native
// confirmation dialog. RequestedByAgent is true when the model, not the
// Computer panel, asked for the app.
type ComputerAppConfirmation struct {
	App              computer.App
	Scope            computer.Scope
	Locale           string
	RequestedByAgent bool
}

func (g *Gateway) SetComputerAppConfirmation(confirm func(context.Context, ComputerAppConfirmation) (bool, error)) {
	g.originMu.Lock()
	g.confirmComputerApp = confirm
	g.originMu.Unlock()
}

// grantComputerApp grants an app only after the user accepts the native
// dialog, whoever asked. Without a dialog (headless mode) nothing is granted.
// Agent requests show one dialog at a time so a model cannot stack prompts.
func (g *Gateway) grantComputerApp(ctx context.Context, scope computer.Scope, app computer.App, locale string, byAgent bool) (*computer.Grant, int, error) {
	if byAgent {
		if !g.computerConfirming.CompareAndSwap(false, true) {
			return nil, http.StatusConflict, errors.New("app_confirmation_pending")
		}
		defer g.computerConfirming.Store(false)
	}
	g.originMu.Lock()
	confirm := g.confirmComputerApp
	if !byAgent && (locale == "zh-CN" || locale == "en-US") {
		g.computerLocale = locale
	}
	if byAgent || locale == "" {
		locale = g.computerLocale
	}
	g.originMu.Unlock()
	if confirm == nil {
		return nil, http.StatusServiceUnavailable, errors.New("native_desktop_app_confirmation_required")
	}
	version := g.computer.AuthorizationVersion()
	accepted, err := confirm(ctx, ComputerAppConfirmation{App: app, Scope: scope, Locale: locale, RequestedByAgent: byAgent})
	if err != nil || !accepted || ctx.Err() != nil || version != g.computer.AuthorizationVersion() {
		return nil, http.StatusForbidden, errors.New("app_permission_not_granted")
	}
	grant, err := g.computer.AuthorizeConfirmed(ctx, scope, app.ID, version)
	if err != nil {
		return nil, http.StatusConflict, err
	}
	return grant, http.StatusOK, nil
}
func (g *Gateway) computerScope(r *http.Request) (computer.Scope, error) {
	q := r.URL.Query()
	if len(q["sessionID"]) != 1 || len(q["directory"]) != 1 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(q.Get("sessionID")) {
		return computer.Scope{}, errors.New("session_and_directory_required")
	}
	dir := q.Get("directory")
	if !filepath.IsAbs(dir) {
		return computer.Scope{}, errors.New("directory_must_be_absolute")
	}
	dir, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return computer.Scope{}, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return computer.Scope{}, errors.New("directory_not_found")
	}
	session, err := g.runner.ResolveComputerSession(q.Get("sessionID"), dir)
	if err != nil {
		return computer.Scope{}, err
	}
	return computer.Scope{SessionID: session, Directory: dir}, nil
}
func (g *Gateway) trustedComputerUI(origin, authority string) (bool, bool, bool) {
	g.originMu.RLock()
	defer g.originMu.RUnlock()
	configuredHost := false
	for allowed := range g.uiOrigins {
		if u, err := url.Parse(allowed); err == nil && u.Host == authority {
			configuredHost = true
		}
	}
	nativeOrigin := origin == "wails://wails" || origin == "wails://wails.localhost" || origin == "wails://app.wails.localhost"
	native := nativeHost(authority) || g.uiOrigins["wails://"+authority]
	return nativeOrigin || g.uiOrigins[origin], configuredHost, native
}
func (g *Gateway) computerCaller(r *http.Request) (bool, error) {
	if isRemoteRequest(r) || !trustedHost(r.Host) {
		return false, errors.New("computer_use_is_desktop_only")
	}
	agent, auth := r.Header.Get("X-Hoxi-Agent"), r.Header.Get("Authorization")
	if agent != "" || auth != "" {
		g.originMu.RLock()
		token := g.computerAgentToken
		g.originMu.RUnlock()
		if agent != "true" || token == "" || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("X-Hoxi-Computer") != "" || subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+token)) != 1 {
			return false, errors.New("computer_agent_authentication_required")
		}
		return true, nil
	}
	origin := r.Header.Get("Origin")
	allowedOrigin, configuredHost, native := g.trustedComputerUI(origin, r.Host)
	if origin != "" {
		if len(r.Header.Values("Origin")) == 1 && allowedOrigin {
			return false, nil
		}
		return false, errors.New("computer_ui_origin_required")
	}
	site := r.Header.Get("Sec-Fetch-Site")
	if !native && site == "" {
		if ref, err := url.Parse(r.Header.Get("Referer")); err == nil && ref.Scheme == "wails" {
			allowedRef, _, refNative := g.trustedComputerUI(ref.Scheme+"://"+ref.Host, ref.Host)
			native = allowedRef && refNative
		}
	}
	if r.Header.Get("X-Hoxi-Computer") == "true" && (native || configuredHost) && (site == "same-origin" || (site == "" && native)) {
		return false, nil
	}
	return false, errors.New("computer_ui_origin_required")
}
func decodeComputerJSON(w http.ResponseWriter, r *http.Request, body any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("one_json_object_required")
	}
	return nil
}
func (g *Gateway) handleComputer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	agent, err := g.computerCaller(r)
	if err != nil {
		writeJSONError(w, 403, err.Error())
		return
	}
	operation := strings.TrimPrefix(r.URL.Path, "/api/hoxi/computer/")
	if agent && operation != "action" {
		writeJSONError(w, 403, "only_computer_agent_actions_allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if operation == "permissions" || operation == "apps" {
		if r.Method != "GET" && !(operation == "permissions" && r.Method == "POST") {
			writeJSONError(w, 405, "method_not_allowed")
			return
		}
		if operation == "apps" {
			apps, err := g.computer.Apps(ctx)
			if err != nil {
				writeJSONError(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"apps": apps})
			return
		}
		prompt := ""
		if r.Method == "POST" {
			var req struct {
				Permission string `json:"permission"`
			}
			if decodeComputerJSON(w, r, &req) != nil || (req.Permission != "accessibility" && req.Permission != "screen" && req.Permission != "reset" && req.Permission != "reveal") {
				writeJSONError(w, 400, "permission_required")
				return
			}
			prompt = req.Permission
		}
		permission, err := g.computer.Permissions(ctx, prompt)
		if err != nil {
			writeJSONError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, permission)
		return
	}
	if operation == "stop" {
		if r.Method != "POST" {
			writeJSONError(w, 405, "POST_required")
			return
		}
		g.computer.Stop()
		g.runner.CancelComputerApprovals("")
		writeJSON(w, 200, map[string]bool{"stopped": true})
		return
	}
	scope, err := g.computerScope(r)
	if err != nil {
		writeJSONError(w, 400, err.Error())
		return
	}
	if operation == "state" {
		if r.Method != "GET" {
			writeJSONError(w, 405, "GET_required")
			return
		}
		writeJSON(w, 200, map[string]any{"grant": g.computer.Current(scope)})
		return
	}
	if r.Method != "POST" {
		writeJSONError(w, 405, "POST_required")
		return
	}
	switch operation {
	case "grant":
		var req struct {
			AppID  string `json:"appID"`
			Locale string `json:"locale,omitempty"`
		}
		if decodeComputerJSON(w, r, &req) != nil || strings.TrimSpace(req.AppID) == "" {
			writeJSONError(w, 400, "app_id_required")
			return
		}
		app, err := g.computer.ResolveApp(ctx, req.AppID)
		if err != nil {
			status := 403
			if err.Error() != "target_app_not_allowed" {
				status = 409
			}
			writeJSONError(w, status, err.Error())
			return
		}
		grant, status, err := g.grantComputerApp(ctx, scope, *app, req.Locale, false)
		if err != nil {
			writeJSONError(w, status, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"grant": grant})
	case "revoke":
		g.computer.Revoke(scope)
		g.runner.CancelComputerApprovals(scope.SessionID)
		writeJSON(w, 200, map[string]any{"grant": nil})
	case "pause":
		var req struct {
			Paused *bool `json:"paused"`
		}
		if decodeComputerJSON(w, r, &req) != nil || req.Paused == nil {
			writeJSONError(w, 400, "paused_required")
			return
		}
		g.computer.Pause(scope, *req.Paused)
		if *req.Paused {
			g.runner.CancelComputerApprovals(scope.SessionID)
		}
		writeJSON(w, 200, map[string]any{"grant": g.computer.Current(scope)})
	case "action":
		var req struct {
			Action        string  `json:"action"`
			App           string  `json:"app,omitempty"`
			WindowID      string  `json:"windowID,omitempty"`
			ObservationID string  `json:"observationID,omitempty"`
			ElementID     int     `json:"elementID,omitempty"`
			X             float64 `json:"x,omitempty"`
			Y             float64 `json:"y,omitempty"`
			EndX          float64 `json:"endX,omitempty"`
			EndY          float64 `json:"endY,omitempty"`
			DeltaX        int     `json:"deltaX,omitempty"`
			DeltaY        int     `json:"deltaY,omitempty"`
			Button        string  `json:"button,omitempty"`
			ClickCount    int     `json:"clickCount,omitempty"`
			Text          string  `json:"text,omitempty"`
			Key           string  `json:"key,omitempty"`
		}
		if decodeComputerJSON(w, r, &req) != nil {
			writeJSONError(w, 400, "invalid_computer_action")
			return
		}
		if req.Action == "status" {
			writeJSON(w, 200, map[string]any{"grant": g.computer.Current(scope)})
			return
		}
		if req.Action == "apps" {
			apps, err := g.computer.Apps(ctx)
			if err != nil {
				writeJSONError(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"apps": apps})
			return
		}
		if targetQuery := strings.TrimSpace(req.App); targetQuery != "" {
			target, err := g.computer.ResolveApp(ctx, targetQuery)
			if err != nil {
				writeJSONError(w, 403, err.Error())
				return
			}
			if current := g.computer.Current(scope); current == nil || current.App.ID != target.ID {
				// Switching apps only happens on a read, and only after the
				// user confirms the exact app in the native dialog.
				if req.Action != "state" && req.Action != "screenshot" {
					writeJSONError(w, 409, "inspect_app_before_input")
					return
				}
				if _, status, err := g.grantComputerApp(ctx, scope, *target, "", agent); err != nil {
					writeJSONError(w, status, err.Error())
					return
				}
			}
		}
		if req.Action == "state" || req.Action == "screenshot" {
			snapshot, err := g.computer.Observe(ctx, scope, req.WindowID, req.Action == "screenshot")
			if err != nil {
				writeJSONError(w, 409, err.Error())
				return
			}
			writeJSON(w, 200, snapshot)
			return
		}
		action := computer.Action{Action: req.Action, WindowID: req.WindowID, ObservationID: req.ObservationID, ElementID: req.ElementID, X: req.X, Y: req.Y, EndX: req.EndX, EndY: req.EndY, DeltaX: req.DeltaX, DeltaY: req.DeltaY, Button: req.Button, ClickCount: req.ClickCount, Text: req.Text, Key: req.Key}
		if !agent {
			writeJSONError(w, 403, "native_input_requires_agent_approval")
			return
		}
		if err := computer.ValidateAction(action); err != nil {
			writeJSONError(w, 400, err.Error())
			return
		}
		grant := g.computer.Current(scope)
		if grant == nil || grant.Paused {
			writeJSONError(w, 403, "app_permission_required_or_paused")
			return
		}
		approved, err := g.runner.RequestComputerApproval(ctx, scope.SessionID, scope.Directory, grant.App.Name, action)
		if err != nil || !approved {
			writeJSONError(w, 403, "computer_action_not_approved")
			return
		}
		if err := g.computer.Execute(ctx, scope, action); err != nil {
			writeJSONError(w, 409, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"completed": true, "next": "Capture a fresh screenshot before the next input"})
	default:
		writeJSONError(w, 404, "computer_route_not_found")
	}
}
