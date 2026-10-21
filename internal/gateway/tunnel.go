package gateway

import (
	"context"
	"net/http"
	"path"
	"strings"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/tunnel"
)

type tunnelRequestKey struct{}

// Routes the mobile app may reach through the GoPanel tunnel. Everything else
// (tunnel and account management, provider keys, browser, clipboard, dialogs,
// updater, raw OpenCode passthrough) stays desktop-only.
var tunnelRoutePrefixes = []string{
	"/api/hoxi/sessions",
	"/api/hoxi/event",
	"/api/hoxi/permission",
	"/api/hoxi/worktree",
	"/api/hoxi/runner",
	"/api/hoxi/workspaces",
	"/api/hoxi/attachments",
	"/api/hoxi/skills",
	"/api/hoxi/file",
}

var tunnelExactRoutes = map[string]bool{
	"/healthz":              true,
	"/api/hoxi/bootstrap":   true,
	"/api/hoxi/system-info": true,
	"/file":                 true,
	"/file/content":         true,
	"/api/file/content":     true,
}

func tunnelRouteAllowed(requestPath string) bool {
	if requestPath == "" || path.Clean(requestPath) != requestPath {
		return false
	}
	if tunnelExactRoutes[requestPath] {
		return true
	}
	for _, prefix := range tunnelRoutePrefixes {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}

func isTunnelRequest(r *http.Request) bool {
	remote, _ := r.Context().Value(tunnelRequestKey{}).(bool)
	return remote
}

// serveTunnelRequest is the handler the tunnel manager calls in-process. The
// tunnel package already rewrote Host to loopback and dropped Origin, cookies
// and credentials; this layer applies the route allowlist and lets /file reach
// the token-guarded OpenCode passthrough.
func (g *Gateway) serveTunnelRequest(w http.ResponseWriter, r *http.Request) {
	if !tunnelRouteAllowed(r.URL.Path) {
		writeJSONError(w, http.StatusForbidden, "route is not available over the remote tunnel")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), tunnelRequestKey{}, true))
	if tok := g.GatewayToken(); tok != "" {
		r.Header.Set(gatewayTokenHeader, tok)
	}
	g.ServeHTTP(w, r)
}

// StartRemoteTunnel enables GoPanel pairing with the credential at configPath
// and reconnects if this desktop is already paired. targetPort is the loopback
// bridge port reported to GoPanel for display.
func (g *Gateway) StartRemoteTunnel(configPath string, targetPort int) {
	g.tunnel.Configure(tunnel.NewStore(configPath), targetPort)
	if g.status != nil {
		// Pending approvals and agent questions become phone pushes via GoPanel.
		// The notice holds ids only, never command text (see statusbar.ApprovalNotice).
		g.status.SetApprovalListener(func(n statusbar.ApprovalNotice) {
			_ = g.tunnel.Notify(n)
		})
	}
	g.tunnel.Start()
}

func (g *Gateway) StopRemoteTunnel() { g.tunnel.Stop() }
