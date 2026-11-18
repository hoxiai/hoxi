package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/auth"
	"github.com/hoxiai/hoxi/internal/browser"
	"github.com/hoxiai/hoxi/internal/runner"
)

// Extension wires an optional desktop-only host module into Gateway without
// coupling open-source packages to closed-source domain code.
type Extension interface {
	HasSessionContext(sessionID string) bool
	ForgetSession(sessionID string)
	RegisterRoutes(mux *http.ServeMux)
	Close()
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

func (g *Gateway) AuthService() *auth.Service       { return g.auth }
func (g *Gateway) BrowserService() *browser.Service { return g.browser }
func (g *Gateway) RunnerService() *runner.Service   { return g.runner }
func (g *Gateway) Mux() *http.ServeMux              { return g.mux }

// NewExtensionTestGateway constructs a minimal Gateway suitable for host extension unit tests.
func NewExtensionTestGateway(rootDir string, runnerSvc *runner.Service, browserSvc *browser.Service) *Gateway {
	return &Gateway{
		rootDir: rootDir,
		runner:  runnerSvc,
		browser: browserSvc,
		mux:     http.NewServeMux(),
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

// WriteJSONError writes a standard JSON error response.
func WriteJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSONError(w, status, msg)
}
