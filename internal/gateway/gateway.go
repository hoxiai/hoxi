package gateway

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/auth"
	"github.com/hoxiai/hoxi/internal/browser"
	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/leaderboard"
	"github.com/hoxiai/hoxi/internal/privacy"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/updater"
	"github.com/hoxiai/hoxi/internal/workspace"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type Gateway struct {
	openCodePort          int
	password              string
	rootDir               string
	wsBaseURL             string
	proxy                 *httputil.ReverseProxy
	mux                   *http.ServeMux
	status                *statusbar.Tracker
	worktrees             *worktree.Manager
	workspaces            *workspace.Store
	providers             *provider.Service
	handoff               *handoff.Service
	runner                *runner.Service
	browser               *browser.Service
	auth                  *auth.Service
	updater               *updater.Checker
	leaderboard           *leaderboard.Service
	privacy               *privacy.Service
	onOpenDirectoryDialog func() (string, error)
	onFocusDesktop        func(directory, sessionID string)
	dialogMu              sync.RWMutex
	originMu              sync.RWMutex
	uiOrigins             map[string]bool
	gatewayToken          string
	updateMu              sync.Mutex
	updateCheckMu         sync.Mutex
	updateDraining        bool
	updateMutations       int
	updateDirectories     map[string]bool
}

func New(openCodePort int, password, rootDir string, distFS fs.FS, status *statusbar.Tracker) *Gateway {
	var runnerSvc *runner.Service
	targetURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(openCodePort))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Bypass system HTTP_PROXY / HTTPS_PROXY (e.g. Clash/Surge) so 127.0.0.1 never returns 502 Bad Gateway
	proxy.Transport = &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 0,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
	}
	// Instant flush for /event SSE streaming
	proxy.FlushInterval = -1

	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = targetURL.Host
		// OpenCode serve uses HTTP Basic Auth with username "opencode"
		req.SetBasicAuth("opencode", password)
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if runnerSvc != nil {
			if err := filterRunnerResponse(resp, runnerSvc); err != nil {
				return err
			}
		}
		if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Body = newSSETapReadCloser(resp.Body, func(line string) {
				if status != nil {
					status.InspectSSELine(line)
				}
				inspectOpenCodeError(line)
			})
		}
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
	}

	workspaces := workspace.NewStore(rootDir)
	rootDir = workspaces.PrimaryRootDir(rootDir)

	wtMgr := worktree.NewManager(rootDir)
	providers := provider.NewService(openCodePort, password, rootDir, proxy)
	if status != nil {
		providers.SetOnProviderDegraded(status.SetProviderDegraded)
	}
	handoffSvc := handoff.NewService(openCodePort, password, rootDir, providers)
	runnerSvc = runner.NewService(rootDir, wtMgr, status, "")
	runnerSvc.ConfigureOpenCode(targetURL.String(), password)
	authSvc := auth.NewService(nil)
	updaterSvc := updater.NewChecker(buildinfo.Version, nil)
	leaderboardSvc := leaderboard.NewService("", nil)

	g := &Gateway{
		openCodePort: openCodePort,
		password:     password,
		rootDir:      rootDir,
		proxy:        proxy,
		mux:          http.NewServeMux(),
		status:       status,
		worktrees:    wtMgr,
		workspaces:   workspaces,
		providers:    providers,
		handoff:      handoffSvc,
		runner:       runnerSvc,
		browser:      browser.New(),
		auth:         authSvc,
		updater:      updaterSvc,
		leaderboard:  leaderboardSvc,
		privacy:      privacy.New(""),
	}
	providers.SetBusyCheck(g.isAnySessionBusy)

	g.registerRoutes(distFS)
	return g
}

func (g *Gateway) SetBrowserAgentToken(token string) { g.browser.SetAgentToken(token) }

func (g *Gateway) HandleAuthCallback(ctx context.Context, rawURL string) error {
	return g.auth.HandleCallback(ctx, rawURL)
}

func (g *Gateway) CloseBrowser() {
	g.browser.Close()
	if g.runner != nil {
		g.runner.CloseAll()
	}
}

func (g *Gateway) SetWSBaseURL(wsURL string) {
	g.wsBaseURL = wsURL
	if g.handoff != nil {
		g.handoff.SetWSBaseURL(wsURL)
	}
	if g.runner != nil {
		// Runner approval hooks (agy, Claude Code) call back over the same loopback listener.
		httpBase := strings.Replace(strings.Replace(wsURL, "wss://", "https://", 1), "ws://", "http://", 1)
		g.runner.SetHookBaseURL(httpBase)
	}
}

// EnableAgyApprovalHook lets the runner keep Hoxi's PreToolUse hook installed
// in ~/.gemini/config/hooks.json before each Antigravity turn.
func (g *Gateway) EnableAgyApprovalHook() {
	if g.runner != nil {
		g.runner.EnableAgyHookAutoInstall()
	}
}

func (g *Gateway) SetOpenDirectoryDialog(fn func() (string, error)) {
	g.dialogMu.Lock()
	defer g.dialogMu.Unlock()
	g.onOpenDirectoryDialog = fn
}

func (g *Gateway) SetDesktopFocusHandler(fn func(directory, sessionID string)) {
	g.dialogMu.Lock()
	defer g.dialogMu.Unlock()
	g.onFocusDesktop = fn
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.allowRequest(w, r) {
		return
	}
	if !g.routeConversation(w, r) {
		return
	}
	if !g.admitUpdateRequest(w, r) {
		return
	}
	if updateMutation(r) {
		defer g.finishUpdateRequest()
	}
	if !g.guardSessionScope(w, r) {
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Hoxi-Browser, X-Hoxi-Directory, X-Hoxi-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if !g.guardUpstreamPassthrough(w, r) {
		return
	}
	if !g.guardPrivacy(w, r) {
		return
	}
	g.observeUpdateDirectory(r)
	g.mux.ServeHTTP(w, r)
}

func (g *Gateway) registerRoutes(distFS fs.FS) {
	diagnosticHandler := diagnostics.Handler()
	g.mux.Handle("/api/hoxi/diagnostics", diagnosticHandler)
	g.mux.Handle("/api/hoxi/diagnostics/", diagnosticHandler)
	g.mux.HandleFunc("/api/hoxi/privacy", g.handlePrivacy)
	g.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	g.mux.HandleFunc("/api/hoxi/bootstrap", func(w http.ResponseWriter, _ *http.Request) {
		var st statusbar.Summary
		if g.status != nil {
			st = g.status.Snapshot()
		}
		tok := g.GatewayToken()
		if tok != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     gatewayTokenCookieName,
				Value:    tok,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
		}
		resp := map[string]any{
			"appVersion":   buildinfo.Version,
			"rootDir":      g.rootDir,
			"wsBaseURL":    g.wsBaseURL,
			"status":       st,
			"privacyScope": privacy.Scope,
		}
		if tok != "" {
			resp["gatewayToken"] = tok
		}
		writeJSON(w, http.StatusOK, resp)
	})

	g.mux.HandleFunc("/api/hoxi/dialog/open-directory", func(w http.ResponseWriter, _ *http.Request) {
		g.dialogMu.RLock()
		openDialog := g.onOpenDirectoryDialog
		g.dialogMu.RUnlock()
		if openDialog == nil {
			writeJSON(w, http.StatusOK, map[string]any{"path": ""})
			return
		}
		dirPath, err := openDialog()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"path": "", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": dirPath})
	})

	g.mux.HandleFunc("/api/hoxi/desktop/focus", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		var body struct {
			Directory string `json:"directory"`
			SessionID string `json:"sessionID"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
		g.dialogMu.RLock()
		focusFn := g.onFocusDesktop
		g.dialogMu.RUnlock()
		if focusFn == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "desktop window not attached")
			return
		}
		focusFn(strings.TrimSpace(body.Directory), strings.TrimSpace(body.SessionID))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	g.mux.HandleFunc("/api/hoxi/system-info", g.handleSystemRuntimeInfo)
	g.mux.HandleFunc("/api/hoxi/dev-pulse", g.handleDevPulse)
	g.mux.Handle("/provider/", g.proxy)

	g.providers.RegisterRoutes(g.mux)
	g.handoff.RegisterRoutes(g.mux)
	g.worktrees.RegisterRoutes(g.mux)
	g.workspaces.RegisterRoutes(g.mux)
	g.browser.RegisterRoutes(g.mux)
	if g.runner != nil {
		g.runner.RegisterRoutes(g.mux)
	}

	if g.auth != nil {
		g.mux.Handle("/api/hoxi/auth", auth.NewHTTPHandler(g.auth))
		g.mux.Handle("/api/hoxi/auth/", auth.NewHTTPHandler(g.auth))
	}
	if g.updater != nil {
		h := updater.NewHTTPHandler(g.updater)
		h.SetDraining(g.UpdateDraining)
		g.mux.Handle("/api/hoxi/updater", h)
		g.mux.Handle("/api/hoxi/updater/", h)
	}
	if g.leaderboard != nil {
		h := leaderboard.NewHTTPHandler(g.leaderboard)
		g.mux.Handle("/api/hoxi/models/leaderboard", h)
		g.mux.Handle("/api/hoxi/models/leaderboard/", h)
		g.mux.Handle("/api/hoxi/models/pricing", h)
		g.mux.Handle("/api/hoxi/models/pricing/", h)
		g.mux.Handle("/api/hoxi/models/free-tiers", h)
		g.mux.Handle("/api/hoxi/models/free-tiers/", h)
		g.mux.Handle("/api/hoxi/gateways/connectable", h)
		g.mux.Handle("/api/hoxi/gateways/connectable/", h)
	}

	// OpenCode API prefixes
	openCodePrefixes := []string{
		"/session",
		"/event",
		"/global",
		"/permission",
		"/question",
		"/pty",
		"/vcs",
		"/file",
		"/find",
		"/config",
		"/command",
		"/agent",
		"/lsp",
		"/mcp",
		"/formatter",
		"/skill",
		"/experimental",
		"/path",
		"/project",
		"/auth",
	}
	for _, prefix := range openCodePrefixes {
		g.mux.Handle(prefix, g.proxy)
		g.mux.Handle(prefix+"/", g.proxy)
	}
	g.mux.HandleFunc("/file/content", g.handleFileContent)
	g.mux.HandleFunc("/api/file/content", g.handleFileContent)
	g.mux.HandleFunc("/api/hoxi/file/content", g.handleFileContent)
	g.mux.HandleFunc("/api/hoxi/file/validate", g.handleFileValidate)
	g.mux.HandleFunc("/api/hoxi/file/list", g.handleFileList)
	g.mux.HandleFunc("/api/hoxi/file/open", g.handleFileOpen)
	g.mux.Handle("/api/", http.StripPrefix("/api", g.proxy))

	if distFS != nil {
		g.mux.Handle("/", spaStaticHandler(distFS))
	}
}

func spaStaticHandler(distFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(distFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			cleanPath = "index.html"
		}
		if f, err := distFS.Open(cleanPath); err != nil {
			r.URL.Path = "/"
			cleanPath = "index.html"
		} else {
			_ = f.Close()
		}
		if cleanPath == "index.html" {
			if data, err := distFS.Open("index.html"); err == nil {
				defer data.Close()
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = io.Copy(w, data)
				return
			}
		}
		if strings.HasPrefix(cleanPath, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
