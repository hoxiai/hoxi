package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/appbridge"
	"github.com/hoxiai/hoxi/internal/auth"
	"github.com/hoxiai/hoxi/internal/browser"
	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/reclaim"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/skills"
	"github.com/hoxiai/hoxi/internal/worktree"
)

// Extension wires an optional desktop-only host module into Gateway without
// coupling open-source packages to closed-source domain code.
type Extension interface {
	HasSessionContext(sessionID string) bool
	ForgetSession(sessionID string)
	RegisterRoutes(mux *http.ServeMux)
	Close()
}

// SessionMCPExtension may be implemented by an Extension to contribute
// session-scoped MCP server definitions (keyed by server name) to native turns.
type SessionMCPExtension interface {
	SessionMCPServers(sessionID, directory string) map[string]any
}

// DesktopIframeRoutesExtension may be implemented by an Extension to declare
// exact read-only, zero-secret loopback HTML paths (protected by CSP
// frame-ancestors) that a native desktop WebView (e.g. macOS wails://wails)
// may navigate inside a cross-site iframe.
type DesktopIframeRoutesExtension interface {
	DesktopIframeRoutes() []string
}

// RelayEndpointExtension may be implemented by an Extension to supply public
// relay base URLs for isolated tester intake portals and/or paired remote
// controllers.
type RelayEndpointExtension interface {
	IntakeRelayBaseURL() string
	RemoteRelayBaseURL() string
}

// RelayDeviceIdentityExtension may be implemented by an Extension to supply the
// stable per-device identifier associated with its RemoteRelayBaseURL so mobile
// clients can target a specific desktop via X-Hoxi-Device-Id when multiple
// desktops share one relay hostname.
type RelayDeviceIdentityExtension interface {
	RemoteRelayDeviceID() string
}

// RelayLifecycleExtension may be implemented by an Extension that runs a
// background reverse relay bound to the desktop host lifecycle.
type RelayLifecycleExtension interface {
	StartRelay(variant string)
	StopRelay()
}

// TunnelRelayPolicyExtension may be implemented by an Extension to supply the
// default GoPanel-compatible tunnel relay controller URL and any retired
// auto-relay controller URLs whose saved auto-registered credentials should
// transparently re-register on startup.
type TunnelRelayPolicyExtension interface {
	DefaultTunnelRelayURL() string
	RetiredAutoRelayURLs() []string
}

// ShortCodeRelayExtension may be implemented by an Extension to supply the
// default 6-digit one-time pairing relay origin for commercial builds.
type ShortCodeRelayExtension interface {
	DefaultShortCodeRelayURL() string
}

// PublicPortalEndpoints reports loopback, LAN, and public relay base URLs
// available for isolated external portal links.
type PublicPortalEndpoints struct {
	LocalBaseURL string
	LANBaseURL   string
	RelayBaseURL string
}

// PublicPortalExtension may be implemented by an Extension to expose an
// isolated, token-authenticated external portal (SPA shell + scoped API) over
// loopback, LAN, reverse-proxy ingress, and relay transports.
type PublicPortalExtension interface {
	IsPublicPortalRequest(r *http.Request) bool
	IsPublicPortalAPIPath(requestPath string) bool
	ServePublicPortal(w http.ResponseWriter, r *http.Request)
	ActivePublicPortalTokenHashes() []string
	SetPublicPortalLinksChangedHook(fn func())
}

// ExtensionFactory constructs an Extension bound to a Gateway instance.
type ExtensionFactory func(g *Gateway) Extension

var (
	extensionMu        sync.RWMutex
	extensionFactories []ExtensionFactory
)

// RegisterExtension registers an optional host extension factory.
func RegisterExtension(fn ExtensionFactory) {
	if fn == nil {
		return
	}
	extensionMu.Lock()
	extensionFactories = append(extensionFactories, fn)
	extensionMu.Unlock()
}

func (g *Gateway) initExtensions() {
	extensionMu.RLock()
	factories := append([]ExtensionFactory(nil), extensionFactories...)
	extensionMu.RUnlock()
	for _, fn := range factories {
		if ext := fn(g); ext != nil {
			g.extensions = append(g.extensions, ext)
		}
	}
}

func (g *Gateway) hasExtensionSessionContext(sessionID string) bool {
	for _, ext := range g.extensions {
		if ext.HasSessionContext(sessionID) {
			return true
		}
	}
	return false
}

func (g *Gateway) forgetExtensionSession(sessionID string) {
	for _, ext := range g.extensions {
		ext.ForgetSession(sessionID)
	}
}

func (g *Gateway) extensionSessionMCPServers(sessionID, directory string) map[string]any {
	if g == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if g.runner != nil && g.runner.IsAppDevSession(sessionID) {
		return nil
	}
	var merged map[string]any
	for _, ext := range g.extensions {
		provider, ok := ext.(SessionMCPExtension)
		if !ok {
			continue
		}
		entries := provider.SessionMCPServers(sessionID, directory)
		if len(entries) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string]any, len(entries))
		}
		for k, v := range entries {
			merged[k] = v
		}
	}
	return merged
}

// LoopbackBaseURL returns the local HTTP origin of this Gateway listener, or "" when unset.
func (g *Gateway) LoopbackBaseURL() string {
	if g == nil {
		return ""
	}
	ws := strings.TrimRight(strings.TrimSpace(g.wsBaseURL), "/")
	if ws == "" {
		return ""
	}
	return strings.Replace(strings.Replace(ws, "wss://", "https://", 1), "ws://", "http://", 1)
}

func (g *Gateway) isDesktopIframeRoute(requestPath string) bool {
	if g == nil || requestPath == "" {
		return false
	}
	for _, ext := range g.extensions {
		provider, ok := ext.(DesktopIframeRoutesExtension)
		if !ok {
			continue
		}
		for _, route := range provider.DesktopIframeRoutes() {
			if route != "" && route == requestPath {
				return true
			}
		}
	}
	return false
}

func (g *Gateway) registerExtensionRoutes() {
	if g == nil || g.mux == nil {
		return
	}
	g.registerDeclarativeExtensionRoutes()
	for _, ext := range g.extensions {
		ext.RegisterRoutes(g.mux)
	}
}

func (g *Gateway) closeExtensions() {
	for _, ext := range g.extensions {
		ext.Close()
	}
}

// StartExtensionRelays starts any background reverse relays registered by
// optional host extensions. Only desktop/headless host processes call this.
func (g *Gateway) StartExtensionRelays(variant string) {
	if g == nil {
		return
	}
	for _, ext := range g.extensions {
		if relayExt, ok := ext.(RelayLifecycleExtension); ok {
			relayExt.StartRelay(variant)
		}
	}
}

// StopExtensionRelays stops background reverse relays registered by extensions.
func (g *Gateway) StopExtensionRelays() {
	if g == nil {
		return
	}
	for _, ext := range g.extensions {
		if relayExt, ok := ext.(RelayLifecycleExtension); ok {
			relayExt.StopRelay()
		}
	}
}

func (g *Gateway) extensionIntakeRelayBaseURL() string {
	if g == nil {
		return ""
	}
	for _, ext := range g.extensions {
		if provider, ok := ext.(RelayEndpointExtension); ok {
			if base := strings.TrimRight(strings.TrimSpace(provider.IntakeRelayBaseURL()), "/"); base != "" {
				return base
			}
		}
	}
	return ""
}

func (g *Gateway) extensionRemoteRelayBaseURL() string {
	if g == nil {
		return ""
	}
	for _, ext := range g.extensions {
		if provider, ok := ext.(RelayEndpointExtension); ok {
			if base := strings.TrimRight(strings.TrimSpace(provider.RemoteRelayBaseURL()), "/"); base != "" {
				return base
			}
		}
	}
	return ""
}

func (g *Gateway) extensionRemoteRelayDeviceID() string {
	if g == nil {
		return ""
	}
	for _, ext := range g.extensions {
		if provider, ok := ext.(RelayDeviceIdentityExtension); ok {
			if id := strings.TrimSpace(provider.RemoteRelayDeviceID()); id != "" {
				return id
			}
		}
	}
	return ""
}

func (g *Gateway) extensionDefaultTunnelRelayURL() string {
	if g == nil {
		return ""
	}
	for _, ext := range g.extensions {
		if provider, ok := ext.(TunnelRelayPolicyExtension); ok {
			if base := strings.TrimRight(strings.TrimSpace(provider.DefaultTunnelRelayURL()), "/"); base != "" {
				return base
			}
		}
	}
	return ""
}

func (g *Gateway) extensionRetiredAutoRelayURLs() []string {
	if g == nil {
		return nil
	}
	var out []string
	for _, ext := range g.extensions {
		if provider, ok := ext.(TunnelRelayPolicyExtension); ok {
			for _, raw := range provider.RetiredAutoRelayURLs() {
				if trimmed := strings.TrimRight(strings.TrimSpace(raw), "/"); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
	}
	return out
}

func (g *Gateway) extensionDefaultShortCodeRelayURL() string {
	if g == nil {
		return ""
	}
	for _, ext := range g.extensions {
		if provider, ok := ext.(ShortCodeRelayExtension); ok {
			if base := strings.TrimRight(strings.TrimSpace(provider.DefaultShortCodeRelayURL()), "/"); base != "" {
				return base
			}
		}
	}
	return ""
}

// ActiveIntakeTokenHashes returns sorted SHA-256 hex digests of active intake
// and extension public portal tokens so relay extensions can route multi-device
// collaboration links without exposing raw portal tokens.
func (g *Gateway) ActiveIntakeTokenHashes() []string {
	if g == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	appendHashes := func(hashes []string) {
		for _, h := range hashes {
			if h != "" && !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	if g.intake != nil {
		appendHashes(g.intake.ActiveTokenHashes())
	}
	for _, ext := range g.extensions {
		if portal, ok := ext.(PublicPortalExtension); ok {
			appendHashes(portal.ActivePublicPortalTokenHashes())
		}
	}
	sort.Strings(out)
	return out
}

// SetIntakeLinksChangedHook notifies a relay extension whenever intake or
// extension public portal links are created, updated, or deleted.
func (g *Gateway) SetIntakeLinksChangedHook(fn func()) {
	if g == nil {
		return
	}
	if g.intake != nil {
		g.intake.SetOnLinksChanged(fn)
	}
	for _, ext := range g.extensions {
		if portal, ok := ext.(PublicPortalExtension); ok {
			portal.SetPublicPortalLinksChangedHook(fn)
		}
	}
}

func (g *Gateway) isExtensionPublicPortalRequest(r *http.Request) bool {
	if g == nil || r == nil || r.URL == nil || r.URL.RawPath != "" {
		return false
	}
	for _, ext := range g.extensions {
		if portal, ok := ext.(PublicPortalExtension); ok && portal.IsPublicPortalRequest(r) {
			return true
		}
	}
	return false
}

func (g *Gateway) isExtensionPublicPortalAPIPath(requestPath string) bool {
	if g == nil || requestPath == "" {
		return false
	}
	for _, ext := range g.extensions {
		if portal, ok := ext.(PublicPortalExtension); ok && portal.IsPublicPortalAPIPath(requestPath) {
			return true
		}
	}
	return false
}

func (g *Gateway) serveExtensionPublicPortal(w http.ResponseWriter, r *http.Request) {
	if g == nil || r == nil || r.URL == nil {
		writeJSONError(w, http.StatusForbidden, "route is not available")
		return
	}
	for _, ext := range g.extensions {
		portal, ok := ext.(PublicPortalExtension)
		if !ok {
			continue
		}
		if portal.IsPublicPortalRequest(r) || portal.IsPublicPortalAPIPath(r.URL.Path) {
			portal.ServePublicPortal(w, r)
			return
		}
	}
	writeJSONError(w, http.StatusForbidden, "route is not available")
}

// IsIntakePublicSurfaceRequest reports whether r targets an isolated public
// portal SPA shell, static bundle assets, or token-authenticated portal API.
func (g *Gateway) IsIntakePublicSurfaceRequest(r *http.Request) bool {
	return isIntakePublicSurfaceRequest(r) || g.isExtensionPublicPortalRequest(r)
}

// ServeIntakeRelayRequest serves ONLY isolated public portal SPA shells,
// bundle assets, and token-authenticated portal APIs over an extension reverse
// relay. Untrusted proxy/browser headers are stripped; non-portal paths are
// always rejected with 403.
func (g *Gateway) ServeIntakeRelayRequest(w http.ResponseWriter, r *http.Request) {
	if g == nil || r == nil || r.URL == nil || denyRemoteAppProtocol(w, r.URL.Path) {
		return
	}
	isIntake := isIntakePublicSurfaceRequest(r)
	isExtPortal := g.isExtensionPublicPortalRequest(r)
	if !isIntake && !isExtPortal {
		writeJSONError(w, http.StatusForbidden, "route is not available on this relay")
		return
	}
	r.Host = "127.0.0.1"
	isExtPortalAPI := g.isExtensionPublicPortalAPIPath(r.URL.Path)
	for key := range r.Header {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "authorization" &&
			(strings.HasPrefix(r.URL.Path, "/api/hoxi/intake/portal/") || isExtPortalAPI) {
			continue
		}
		if droppedRemoteHeader(key) {
			delete(r.Header, key)
		}
	}
	stripGatewayTokenFromRequest(r)
	if isExtPortal && !isIntake {
		g.serveExtensionPublicPortal(w, r)
		return
	}
	g.serveIntakePublicSurface(w, r)
}

// ResolvePublicPortalEndpoints returns the loopback, LAN, and public relay base
// URLs currently available on this Gateway for isolated public portal links.
func (g *Gateway) ResolvePublicPortalEndpoints() PublicPortalEndpoints {
	if g == nil {
		return PublicPortalEndpoints{}
	}
	ep := g.resolveIntakeEndpoints()
	return PublicPortalEndpoints{
		LocalBaseURL: ep.LocalBaseURL,
		LANBaseURL:   ep.LANBaseURL,
		RelayBaseURL: ep.RelayBaseURL,
	}
}

// IsPublicPortalStaticAssetPath reports whether requestPath is a whitelisted
// frontend bundle asset path safe to serve to isolated public portals.
func IsPublicPortalStaticAssetPath(requestPath string) bool {
	return isIntakeStaticAssetPath(requestPath)
}

// ServePublicPortalShell serves either index.html (when isPagePath is true) or
// a whitelisted static bundle asset from the embedded frontend bundle for an
// isolated extension public portal.
func (g *Gateway) ServePublicPortalShell(w http.ResponseWriter, r *http.Request, isPagePath bool) {
	if g == nil || g.distFS == nil || r == nil || r.URL == nil {
		http.NotFound(w, r)
		return
	}
	if isPagePath {
		data, err := g.distFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer data.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.Copy(w, data)
		return
	}
	if !isIntakeStaticAssetPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	cleanAsset := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	f, err := g.distFS.Open(cleanAsset)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, statErr := f.Stat()
	_ = f.Close()
	if statErr != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(cleanAsset, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.FileServer(http.FS(g.distFS)).ServeHTTP(w, r)
}

// ServePairedRelayRequest serves a paired remote controller request arriving
// over an extension reverse relay, enforcing the desktop relay pairing token,
// optional E2EE envelope, and explicit remote route allow-list.
func (g *Gateway) ServePairedRelayRequest(w http.ResponseWriter, r *http.Request) {
	if g == nil || r == nil {
		return
	}
	g.servePairedRequest(w, r, remoteSourceRelay)
}

// AttachExtension attaches an extension directly to an initialized or test Gateway.
func (g *Gateway) AttachExtension(ext Extension) {
	if g == nil || ext == nil {
		return
	}
	g.extensions = append(g.extensions, ext)
	if g.mux != nil {
		ext.RegisterRoutes(g.mux)
	}
}

func (g *Gateway) AuthService() *auth.Service         { return g.auth }
func (g *Gateway) BrowserService() *browser.Service   { return g.browser }
func (g *Gateway) RunnerService() *runner.Service     { return g.runner }
func (g *Gateway) SkillsService() *skills.Service     { return g.skills }
func (g *Gateway) WorktreeManager() *worktree.Manager { return g.worktrees }
func (g *Gateway) Mux() *http.ServeMux                { return g.mux }
func (g *Gateway) RootDir() string                    { return g.rootDir }
func (g *Gateway) LLMClient() llm.Client {
	if g == nil || g.providers == nil {
		return nil
	}
	return g.providers
}
func (g *Gateway) ReclaimLedger() *reclaim.Ledger {
	if g == nil {
		return nil
	}
	g.originMu.RLock()
	defer g.originMu.RUnlock()
	return g.ledger
}

// NewExtensionTestGateway constructs a minimal Gateway suitable for host extension unit tests.
func NewExtensionTestGateway(rootDir string, runnerSvc *runner.Service, browserSvc *browser.Service) *Gateway {
	return &Gateway{
		rootDir:   rootDir,
		runner:    runnerSvc,
		browser:   browserSvc,
		appBridge: appbridge.NewService(),
		mux:       http.NewServeMux(),
	}
}

// WalkSessionAncestors traverses sessionID and up to 8 parent sessions in the runner store.
func (g *Gateway) WalkSessionAncestors(sessionID string, visit func(cur string) bool) {
	cur := strings.TrimSpace(sessionID)
	for depth := 0; cur != "" && depth < 8; depth++ {
		if visit(cur) {
			return
		}
		if g.runner == nil || g.runner.Store() == nil {
			return
		}
		rec, ok := g.runner.Store().Meta(cur)
		if !ok || strings.TrimSpace(rec.ParentID) == "" || rec.ParentID == cur {
			return
		}
		cur = strings.TrimSpace(rec.ParentID)
	}
}

// IsRemoteRequest reports whether r arrived over a remote controller plane.
func (g *Gateway) IsRemoteRequest(r *http.Request) bool {
	return isRemoteRequest(r)
}

// WithRemoteSource tags a test request as arriving from a remote controller source.
func WithRemoteSource(r *http.Request, source string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), remoteSourceKey{}, source))
}

// ServeRemoteTestRequest invokes the remote-controller ingress guard for extension security tests.
func (g *Gateway) ServeRemoteTestRequest(w http.ResponseWriter, r *http.Request, source string) {
	g.serveRemoteRequest(w, r, source)
}

// DesktopOnlyHandler blocks remote-plane requests with HTTP 403.
func DesktopOnlyHandler(forbiddenMessage string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, forbiddenMessage)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// VerifyDesktopUIHeaderRequest validates that r comes from a trusted local desktop UI context
// carrying the per-boot gateway bearer token and the required header flag.
func (g *Gateway) VerifyDesktopUIHeaderRequest(r *http.Request, requiredHeader, surfaceName string) error {
	if isRemoteRequest(r) || !trustedHost(r.Host) || r.Header.Get(requiredHeader) != "true" {
		return errors.New(surfaceName + " requires the desktop UI")
	}
	token := g.GatewayToken()
	if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		return errors.New(surfaceName + " requires the local gateway token")
	}
	origin := r.Header.Get("Origin")
	allowed, configuredHost, native := g.trustedComputerUI(origin, r.Host)
	trustedReferer := false
	wailsReferer := false
	if ref, err := url.Parse(r.Header.Get("Referer")); err == nil && ref.User == nil && ref.Host != "" {
		allowedRef, _, refNative := g.trustedComputerUI(ref.Scheme+"://"+ref.Host, ref.Host)
		trustedReferer = allowedRef
		wailsReferer = ref.Scheme == "wails" && allowedRef && refNative
	}
	if origin != "" {
		if len(r.Header.Values("Origin")) != 1 || !allowed {
			return errors.New("untrusted " + surfaceName + " origin")
		}
		return nil
	}
	site := r.Header.Get("Sec-Fetch-Site")
	if !native && site == "" && wailsReferer {
		native = true
	}
	nativeAllowed := native && (site == "" || site == "same-origin" || site == "none")
	httpAllowed := site == "same-origin" && (configuredHost || trustedReferer)
	if !nativeAllowed && !httpAllowed {
		return errors.New(surfaceName + " requires a desktop UI context")
	}
	return nil
}

// VerifyDesktopPassiveTokenRequest validates that a passive desktop read (such
// as an <img> attachment load or EventSource SSE stream that cannot send custom
// headers) comes from a non-remote trusted loopback/native host and presents the
// per-boot gateway token (via header, HttpOnly cookie, or ?hoxiToken query param).
func (g *Gateway) VerifyDesktopPassiveTokenRequest(r *http.Request, surfaceName string) error {
	if isRemoteRequest(r) || !trustedHost(r.Host) {
		return errors.New(surfaceName + " requires the desktop UI")
	}
	token := g.GatewayToken()
	if token == "" {
		return errors.New(surfaceName + " requires the local gateway token")
	}
	if hasVerifiedGatewayToken(r) {
		return nil
	}
	got := requestGatewayToken(r)
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		return errors.New(surfaceName + " requires the local gateway token")
	}
	return nil
}

// WriteJSONError writes a standard JSON error response.
func WriteJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSONError(w, status, msg)
}
