package gateway

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/hoxiai/hoxi/internal/appbridge"
)

type appDesktopRequestKey struct{}

// SetAppConnectRedirect installs the desktop-only OS callback opener. The
// gateway never makes an HTTP request to a callback destination itself.
func (g *Gateway) SetAppConnectRedirect(open func(context.Context, string) error) {
	g.dialogMu.Lock()
	g.onAppConnectRedirect = open
	g.dialogMu.Unlock()
}

// WithAppProtocolDesktop is called only by the in-process desktop asset server.
// A network client cannot impersonate that channel using a synthetic Wails Host.
func WithAppProtocolDesktop(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), appDesktopRequestKey{}, true))
}

func (g *Gateway) allowAppGrantAdmin(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if isRemoteRequest(r) {
		denyRemoteAppProtocol(w, r.URL.Path)
		return false
	}
	if !g.allowRequest(w, r) {
		return false
	}
	desktop, _ := r.Context().Value(appDesktopRequestKey{}).(bool)
	token := g.GatewayToken()
	credentials := r.Header.Values(gatewayTokenHeader)
	if !desktop || token == "" || len(credentials) != 1 || subtle.ConstantTimeCompare([]byte(credentials[0]), []byte(token)) != 1 {
		writeAppError(w, http.StatusForbidden, "DESKTOP_REQUIRED", "application grant management requires the desktop workspace")
		return false
	}
	if r.URL.RawPath != "" {
		writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "encoded admin paths are not accepted")
		return false
	}
	if g.appBridge == nil {
		writeAppError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "application bridge unavailable")
		return false
	}
	return true
}

func (g *Gateway) registerAppGrantAdminRoutes() {
	register := g.mux.HandleFunc
	register("GET "+appGrantPath, func(w http.ResponseWriter, r *http.Request) {
		grants, err := g.appBridge.ListGrants()
		if err != nil {
			writeAppServiceError(w, err, http.StatusServiceUnavailable, "UNAVAILABLE")
			return
		}
		writeJSON(w, http.StatusOK, grants)
	})
	register("PATCH "+appGrantPath+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		var patch appbridge.GrantUpdate
		if !decodeAppJSON(w, r, &patch, appRequestLimit) {
			return
		}
		grant, err := g.appBridge.UpdateGrant(r.PathValue("id"), patch)
		if err != nil {
			writeAppServiceError(w, err, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		writeJSON(w, http.StatusOK, grant)
	})
	register("DELETE "+appGrantPath+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.appBridge.RevokeGrant(r.PathValue("id")); err != nil {
			writeAppServiceError(w, err, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	register("GET "+appGrantPath+"/pending", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, g.appBridge.ListPendingConnects())
	})
	register("POST "+appGrantPath+"/pending/{requestId}/decision", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Approve           *bool    `json:"approve"`
			Scopes            []string `json:"scopes"`
			AllowedWorkspaces []string `json:"allowedWorkspaces"`
		}
		if !decodeAppJSON(w, r, &input, appRequestLimit) {
			return
		}
		if input.Approve == nil {
			writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "approve is required")
			return
		}
		res, err := g.appBridge.DecideConnect(r.PathValue("requestId"), *input.Approve, input.Scopes, input.AllowedWorkspaces, g.appBaseURL())
		if err != nil {
			writeAppServiceError(w, err, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		g.dialogMu.RLock()
		open := g.onAppConnectRedirect
		g.dialogMu.RUnlock()
		if res.RedirectTarget != "" && open != nil {
			if err := open(r.Context(), res.RedirectTarget); err != nil {
				writeAppError(w, http.StatusServiceUnavailable, "CALLBACK_FAILED", "desktop could not open the callback; start a new connection request")
				return
			}
			res.RedirectTarget = ""
		}
		writeJSON(w, http.StatusOK, res)
	})
	register("GET "+appGrantPath+"/approvals", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, g.appBridge.ListPendingApprovals())
	})
	register("POST "+appGrantPath+"/approvals/{approvalId}/decision", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Approve *bool `json:"approve"`
		}
		if !decodeAppJSON(w, r, &input, appRequestLimit) {
			return
		}
		if input.Approve == nil {
			writeAppError(w, http.StatusBadRequest, "INVALID_REQUEST", "approve is required")
			return
		}
		res, err := g.appBridge.DecideApproval(r.PathValue("approvalId"), *input.Approve)
		if err != nil {
			writeAppServiceError(w, err, http.StatusNotFound, "NOT_FOUND")
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}
