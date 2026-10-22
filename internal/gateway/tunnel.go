package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/tunnel"
)

// remoteSourceKey marks a request as coming from a paired remote controller and
// records which transport it arrived on. The GoPanel tunnel and the LAN pairing
// listener share the same route allow-list and file-root confinement; only the
// transport and the approval-source label differ.
type remoteSourceKey struct{}

const (
	remoteSourceTunnel = "tunnel"
	remoteSourceLAN    = "lan"
)

// tunnelRoute is one (method, path pattern) the mobile app may reach through
// the GoPanel tunnel. Patterns match segment by segment; "*" matches exactly
// one non-empty segment. Nothing is matched by prefix, so a route added to a
// domain package stays desktop-only until it is listed here (and in
// docs/gopanel-hoxi-remote-tunnel.md).
type tunnelRoute struct {
	method  string
	pattern string
}

// Everything not listed (tunnel and account management, provider config and
// keys, browser, clipboard, dialogs, updater, file open/validate, worktree
// command execution, raw OpenCode passthrough, legacy runner routes, runner
// hooks) is answered with 403.
var tunnelRoutes = []tunnelRoute{
	{http.MethodGet, "/healthz"},
	{http.MethodGet, "/api/hoxi/bootstrap"},
	{http.MethodGet, "/api/hoxi/system-info"},
	// Sanitized model ids/names only; models-config and the other provider
	// routes carry keys and stay desktop-only.
	{http.MethodGet, "/api/hoxi/provider/models"},
	{http.MethodGet, "/api/hoxi/runner/antigravity/models"},
	{http.MethodGet, "/api/hoxi/runner/claude-code/models"},
	{http.MethodGet, "/api/hoxi/runner/codebuddy/models"},
	{http.MethodGet, "/api/hoxi/runner/qoder/models"},

	// Session plane. Steering is a prompt sent while the session is busy.
	{http.MethodGet, "/api/hoxi/sessions"},
	{http.MethodPost, "/api/hoxi/sessions"},
	{http.MethodGet, "/api/hoxi/sessions/status"},
	{http.MethodGet, "/api/hoxi/sessions/*"},
	{http.MethodGet, "/api/hoxi/sessions/*/message"},
	{http.MethodGet, "/api/hoxi/sessions/*/diff"},
	{http.MethodPost, "/api/hoxi/sessions/*/prompt"},
	{http.MethodPost, "/api/hoxi/sessions/*/prompt_async"},
	{http.MethodPost, "/api/hoxi/sessions/*/abort"},
	{http.MethodPost, "/api/hoxi/sessions/*/stop-tool"},
	{http.MethodPost, "/api/hoxi/sessions/*/revert"},
	{http.MethodPost, "/api/hoxi/sessions/*/unrevert"},
	{http.MethodPost, "/api/hoxi/sessions/*/fork"},

	// Event and approval planes.
	{http.MethodGet, "/api/hoxi/event"},
	{http.MethodGet, "/api/hoxi/permission"},
	{http.MethodPost, "/api/hoxi/permission/*/reply"},

	// Worktree review. commit is accepted only with push=false (see
	// tunnelBodyAllowed); push, checkout, verify-*, setup-retry, prune and
	// delete stay desktop-only.
	{http.MethodGet, "/api/hoxi/worktree/status"},
	{http.MethodGet, "/api/hoxi/worktree/diff"},
	{http.MethodGet, "/api/hoxi/worktree/branches"},
	{http.MethodGet, "/api/hoxi/worktree/verify-status"},
	{http.MethodPost, "/api/hoxi/worktree/commit"},
	{http.MethodPost, "/api/hoxi/worktree/apply"},

	{http.MethodPost, "/api/hoxi/attachments"},
	{http.MethodGet, "/api/hoxi/attachments/*"},
	{http.MethodGet, "/api/hoxi/attachments/*/content"},
	{http.MethodGet, "/api/hoxi/skills/list"},
	// Marketplace catalog is published metadata only; installing writes
	// ~/.hoxi and stays desktop-only.
	{http.MethodGet, "/api/hoxi/skills/marketplace"},
	{http.MethodGet, "/api/hoxi/workspaces"},

	// Local file reads, confined to workspace folders and session worktrees
	// for tunnel requests (see tunnelFilePathAllowed). The content routes
	// also require ?path= so nothing falls through to the OpenCode proxy.
	{http.MethodGet, "/file/content"},
	{http.MethodGet, "/api/file/content"},
	{http.MethodGet, "/api/hoxi/file/content"},
	{http.MethodGet, "/api/hoxi/file/list"},
	{http.MethodGet, "/api/hoxi/file/search"},
}

var tunnelFileContentRoutes = map[string]bool{
	"/file/content":          true,
	"/api/file/content":      true,
	"/api/hoxi/file/content": true,
}

func tunnelPatternMatch(pattern, requestPath string) bool {
	want := strings.Split(pattern, "/")
	got := strings.Split(requestPath, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] == "*" {
			if got[i] == "" {
				return false
			}
			continue
		}
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

// tunnelRouteAllowed reports whether method + path is on the remote allow-list.
// The path must already be in path.Clean form so "..", "//" and trailing
// slashes cannot reach a different handler than the one that was checked.
func tunnelRouteAllowed(method, requestPath string) bool {
	if requestPath == "" || path.Clean(requestPath) != requestPath {
		return false
	}
	for _, route := range tunnelRoutes {
		if route.method == method && tunnelPatternMatch(route.pattern, requestPath) {
			return true
		}
	}
	return false
}

// tunnelRequestAllowed applies the route table plus the per-route query rules.
func tunnelRequestAllowed(r *http.Request) bool {
	// Encoded separators ("%2F") would let the mux and the allow-list see
	// different paths; no legitimate mobile route needs them.
	if r.URL.RawPath != "" {
		return false
	}
	if !tunnelRouteAllowed(r.Method, r.URL.Path) {
		return false
	}
	if tunnelFileContentRoutes[r.URL.Path] && strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		return false
	}
	return true
}

const maxTunnelInspectedBody = 16 << 20

// tunnelBodyAllowed rejects request bodies that would turn an allowed route
// into a desktop-only operation. The body is restored for the real handler.
func tunnelBodyAllowed(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return true
	}
	isCommit := r.URL.Path == "/api/hoxi/worktree/commit"
	isPermReply := tunnelPatternMatch("/api/hoxi/permission/*/reply", r.URL.Path)
	if !isCommit && !isPermReply {
		return true
	}
	if r.Body == nil || r.Body == http.NoBody {
		return isCommit
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxTunnelInspectedBody+1))
	_ = r.Body.Close()
	if err != nil || len(raw) > maxTunnelInspectedBody {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	if isPermReply {
		var body struct {
			Reply string `json:"reply"`
		}
		if json.Unmarshal(raw, &body) != nil {
			return false
		}
		return body.Reply == "once" || body.Reply == "reject"
	}
	var body struct {
		Push bool `json:"push"`
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	return !body.Push
}

func remoteSource(r *http.Request) string {
	source, _ := r.Context().Value(remoteSourceKey{}).(string)
	return source
}

// isRemoteRequest reports whether the request arrived from a paired remote
// controller (GoPanel tunnel or LAN pairing), not a local desktop caller.
func isRemoteRequest(r *http.Request) bool { return remoteSource(r) != "" }

// serveTunnelRequest is the handler the tunnel manager calls in-process. The
// tunnel package already rewrote Host to loopback and dropped Origin, cookies
// and credentials; this layer applies the route allow-list. The loopback
// gateway token is never attached to tunnel requests, so the token-guarded
// raw OpenCode passthrough stays unreachable even if a route slipped through.
func (g *Gateway) serveTunnelRequest(w http.ResponseWriter, r *http.Request) {
	g.serveRemoteRequest(w, r, remoteSourceTunnel)
}

// serveRemoteRequest applies the paired-controller route allow-list and file
// confinement, tags the request with its transport source for the approval
// audit, and serves it in-process.
func (g *Gateway) serveRemoteRequest(w http.ResponseWriter, r *http.Request, source string) {
	if !tunnelRequestAllowed(r) || !tunnelBodyAllowed(r) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), remoteSourceKey{}, source))
	r.Header.Del(gatewayTokenHeader)
	if r.URL.Path == "/api/hoxi/file/search" && !g.tunnelSearchRootAllowed(r) {
		writeJSONError(w, http.StatusForbidden, "path is outside the workspace")
		return
	}
	g.ServeHTTP(w, r)
}

// tunnelAllowedRoots lists the directories a tunnel request may read: the
// gateway root, every registered workspace folder and, when known, the
// session's bound worktree. Request-supplied directories are deliberately not
// trusted here.
func (g *Gateway) tunnelAllowedRoots(sessionID string) []string {
	var roots []string
	if g.rootDir != "" {
		roots = append(roots, g.rootDir)
	}
	if g.workspaces != nil {
		for _, ws := range g.workspaces.List() {
			for _, folder := range ws.Folders {
				if strings.TrimSpace(folder.Path) != "" {
					roots = append(roots, folder.Path)
				}
			}
		}
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" && g.worktrees != nil {
		if binding, known, err := g.worktrees.BindingForSession(sessionID); err == nil && known && binding.Directory != "" {
			roots = append(roots, binding.Directory)
		}
	}
	return roots
}

// tunnelFilePathAllowed reports whether target lies inside a workspace folder
// or session worktree. Symlinks are resolved by isPathWithinDir.
func (g *Gateway) tunnelFilePathAllowed(target, sessionID string) bool {
	if strings.TrimSpace(target) == "" {
		return false
	}
	for _, root := range g.tunnelAllowedRoots(sessionID) {
		if isPathWithinDir(root, target) {
			return true
		}
	}
	return false
}

// tunnelSearchRootAllowed mirrors handleFileSearch's base directory choice.
func (g *Gateway) tunnelSearchRootAllowed(r *http.Request) bool {
	q := r.URL.Query()
	sessionID := strings.TrimSpace(q.Get("sessionID"))
	baseDir := g.rootDir
	if dirs := g.candidateBaseDirs(strings.TrimSpace(q.Get("directory")), sessionID); len(dirs) > 0 {
		baseDir = dirs[0]
	}
	if strings.TrimSpace(baseDir) == "" {
		return true // handler returns an empty result without touching disk
	}
	return g.tunnelFilePathAllowed(baseDir, sessionID)
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
