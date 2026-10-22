package gateway

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
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
	"github.com/hoxiai/hoxi/internal/cloudflared"
	"github.com/hoxiai/hoxi/internal/codeindex"
	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/forge"
	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/leaderboard"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
	"github.com/hoxiai/hoxi/internal/orchestration"
	"github.com/hoxiai/hoxi/internal/pairing"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/recall"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/skills"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/tunnel"
	"github.com/hoxiai/hoxi/internal/updater"
	"github.com/hoxiai/hoxi/internal/workspace"
	"github.com/hoxiai/hoxi/internal/worktree"
)

type Gateway struct {
	openCodePort          int
	openCode              *ocapi.Client
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
	skills                *skills.Service
	browser               *browser.Service
	auth                  *auth.Service
	updater               *updater.Checker
	leaderboard           *leaderboard.Service
	sandbox               *sandbox.Manager
	codeIndex             *codeindex.Service
	recallSvc             *recall.Service
	orchestration         *orchestration.Service
	forge                 *forge.Service
	tunnel                *tunnel.Manager
	pairing               *pairing.Service
	cloudflared           *cloudflared.Manager
	onOpenDirectoryDialog func() (string, error)
	onFocusDesktop        func(directory, sessionID string)
	dialogMu              sync.RWMutex
	notifyMu              sync.RWMutex
	onDesktopNotify       func(DesktopNotification) error
	onDesktopNotifyStatus func() DesktopNotificationPermission
	onDesktopNotifyAsk    func() DesktopNotificationPermission
	intentMu              sync.Mutex
	pendingIntent         *DesktopIntent
	onDesktopIntent       func()
	originMu              sync.RWMutex
	uiOrigins             map[string]bool
	gatewayToken          string
	updateMu              sync.Mutex
	updateCheckMu         sync.Mutex
	updateDraining        bool
	updateMutations       int
	updateDirectories     map[string]bool
	pulseCache            devPulseCache
	engineMu              sync.Mutex
	engineInstaller       func(context.Context, bool) (string, error)
	engineReadyMu         sync.Mutex
	engineReadyAt         time.Time
	engineReadyOK         bool
}

func New(openCodePort int, password, rootDir string, distFS fs.FS, status *statusbar.Tracker) *Gateway {
	var runnerSvc *runner.Service
	openCode := ocapi.NewForPort(openCodePort, password)
	targetURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(openCodePort))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Share the engine client's loopback transport: it bypasses system
	// HTTP_PROXY / HTTPS_PROXY (e.g. Clash/Surge) so 127.0.0.1 never returns 502.
	proxy.Transport = ocapi.Transport()
	// Instant flush for /event SSE streaming
	proxy.FlushInterval = -1

	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = targetURL.Host
		// OpenCode rejects WebSocket upgrades whose Origin is not a loopback
		// HTTP origin. Present the upstream's own origin so native clients
		// (wails://wails) can attach to PTYs; the gateway already validated
		// the caller's Origin above.
		if req.Header.Get("Origin") != "" {
			req.Header.Set("Origin", targetURL.Scheme+"://"+targetURL.Host)
		}
		// OpenCode serve uses HTTP Basic Auth with username "opencode"
		openCode.Authorize(req)
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
	wireAssistRoutes(providers, wtMgr)
	handoffSvc := handoff.NewService(openCodePort, password, rootDir, providers)
	runnerSvc = runner.NewService(rootDir, wtMgr, status, "")
	runnerSvc.SetProjectResolver(workspaces)
	workspaces.SetFolderRelinkHandler(func(projectID, oldPath, newPath string) error {
		return runnerSvc.RelinkProject(projectID, oldPath, newPath)
	})
	runnerSvc.ConfigureOpenCode(targetURL.String(), password)
	runnerSvc.SetPermissionRulesSource(providers.PermissionRules)
	runnerSvc.SetApprovalPolicyHook(providers.EnforceAskPermissions)
	authSvc := auth.NewService(nil)
	updaterSvc := updater.NewChecker(buildinfo.Version, nil)
	leaderboardSvc := leaderboard.NewService("", nil)
	forgeSvc := forge.NewService()

	g := &Gateway{
		openCodePort: openCodePort,
		openCode:     openCode,
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
		sandbox:      sandbox.NewManager(""),
		codeIndex:    codeindex.NewService(""),
		forge:        forgeSvc,
	}
	g.recallSvc = recall.NewService(&gatewayRecallSource{g: g})
	if g.runner != nil {
		options := []orchestration.Option{orchestration.WithLLM(providers)}
		if dir := hoxipaths.Join("orchestration"); dir != "" {
			options = append(options, orchestration.WithStore(orchestration.NewStore(dir)))
		}
		g.orchestration = orchestration.NewService(convoySessions{runner: g.runner}, options...)
	}
	g.skills = skills.NewService(targetURL.String(), password, g.resolveSkillContext)
	g.skills.SetMarketplace(skills.NewMarketplaceService("", nil))
	runnerSvc.SetSkillResolver(func(ctx context.Context, directory, sessionID string, sel runner.SkillSelection) (runner.ResolvedSkill, error) {
		detail, err := g.skills.ResolveSelection(ctx, directory, sessionID, skills.SkillSelection{
			Key:  sel.Key,
			Name: sel.Name,
		})
		if err != nil {
			return runner.ResolvedSkill{}, err
		}
		return runner.ResolvedSkill{Name: detail.Name, Content: detail.Content}, nil
	})
	providers.SetBusyCheck(g.isAnySessionBusy)
	g.tunnel = tunnel.NewManager(tunnel.Options{Handler: http.HandlerFunc(g.serveTunnelRequest), Version: buildinfo.Version})
	g.pairing = pairing.NewService(hoxipaths.PairingTokenPath(), hoxipaths.DefaultLANPairingPort)
	g.cloudflared = cloudflared.NewManager(cloudflared.Options{IngressURL: "http://" + hoxipaths.DefaultRemoteIngressAddr})
	g.pairing.SetRelayURL(func() string {
		host := strings.TrimSpace(g.cloudflared.Hostname())
		if host == "" {
			return ""
		}
		return "https://" + host
	})

	g.registerRoutes(distFS)
	return g
}

func (g *Gateway) SetBrowserAgentToken(token string) { g.browser.SetAgentToken(token) }

// SetEngineInstaller lets the desktop host resolve, verify and restart the
// supervised OpenCode daemon when the user asks to install or retry. The
// installer reports the binary path it actually resolved; restart forces a
// fresh process even when the current one is healthy.
func (g *Gateway) SetEngineInstaller(fn func(context.Context, bool) (string, error)) {
	g.engineMu.Lock()
	g.engineInstaller = fn
	g.engineMu.Unlock()
}

func (g *Gateway) HandleAuthCallback(ctx context.Context, rawURL string) error {
	return g.auth.HandleCallback(ctx, rawURL)
}

func (g *Gateway) CloseBrowser() {
	g.browser.Close()
	if g.runner != nil {
		g.runner.CloseAll()
	}
	if g.handoff != nil {
		g.handoff.StopCredentialSync()
	}
}

func (g *Gateway) SetWSBaseURL(wsURL string) {
	g.wsBaseURL = wsURL
	if g.providers != nil {
		httpBase := strings.Replace(strings.Replace(wsURL, "wss://", "https://", 1), "ws://", "http://", 1)
		g.providers.SetRelayBaseURL(httpBase)
	}
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
	if source := remoteSource(r); source != "" {
		r.Header.Set(runner.ApprovalSourceHeader, source)
	} else {
		r.Header.Set(runner.ApprovalSourceHeader, "desktop")
	}
	// The local OpenAI-compatible relay authenticates with its own hk-* keys
	// instead of the gateway token, and desktop AI clients (VS Code
	// extensions, Electron apps, curl) send Origins and Sec-Fetch-Site values
	// the browser guard rejects. The Host check still applies, so a
	// DNS-rebinding page cannot reach it.
	if provider.IsRelayPath(r.URL.Path) {
		// Defense in depth: even if a future edit added /v1/* to the tunnel
		// allow-list, a remote caller still cannot spend the user's provider
		// credits with a relay key.
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, "the local model relay is not available to remote controllers")
			return
		}
		if !trustedHost(r.Host) {
			http.Error(w, "untrusted Host", http.StatusForbidden)
			return
		}
		if g.providers == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider service is not available")
			return
		}
		g.providers.ServeRelay(w, r)
		return
	}
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
	g.observeUpdateDirectory(r)
	g.mux.ServeHTTP(w, r)
}

func (g *Gateway) registerRoutes(distFS fs.FS) {
	diagnosticHandler := diagnostics.Handler()
	g.mux.Handle("/api/hoxi/diagnostics", diagnosticHandler)
	g.mux.Handle("/api/hoxi/diagnostics/", diagnosticHandler)
	g.mux.HandleFunc("/api/hoxi/telemetry", g.handleTelemetry)
	loadGitAttribution()
	g.mux.HandleFunc("/api/hoxi/git/attribution", g.handleGitAttribution)
	g.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	g.mux.HandleFunc("/api/hoxi/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		var st statusbar.Summary
		if g.status != nil {
			st = g.status.Snapshot()
		}
		// The loopback gateway token never leaves the machine to a remote caller.
		tok := g.GatewayToken()
		if isRemoteRequest(r) {
			tok = ""
		}
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
			"appVersion": buildinfo.Version,
			"rootDir":    g.rootDir,
			"wsBaseURL":  g.wsBaseURL,
			"status":     st,
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
		g.QueueDesktopIntent(DesktopIntent{
			SessionID: body.SessionID,
			Directory: body.Directory,
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	g.mux.HandleFunc("/api/hoxi/desktop/intent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		intent, ok := g.ConsumeDesktopIntent()
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"intent": nil})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"intent": intent})
	})
	g.registerDesktopNotifyRoute(g.mux)

	g.mux.HandleFunc("/api/hoxi/system-info", g.handleSystemRuntimeInfo)
	g.mux.HandleFunc("/api/hoxi/engine/install", g.handleEngineInstall)
	g.mux.HandleFunc("/api/hoxi/keep-awake", g.handleKeepAwake)
	g.mux.HandleFunc("/api/hoxi/sandbox/session", g.handleSandboxSession)
	g.mux.HandleFunc("/api/hoxi/clipboard/images", g.handleClipboardImages)
	g.mux.HandleFunc("/api/hoxi/dev-pulse", g.handleDevPulse)
	g.mux.HandleFunc("/api/hoxi/dev-pulse/history", g.handleDevPulseHistory)
	g.mux.HandleFunc("/api/hoxi/request-log", g.handleRequestLog)
	g.mux.HandleFunc("/api/hoxi/request-log/content", g.handleRequestLogContent)
	g.mux.Handle("/provider/", g.proxy)

	g.providers.RegisterRoutes(g.mux)
	g.handoff.RegisterRoutes(g.mux)
	g.worktrees.RegisterRoutes(g.mux)
	g.tunnel.RegisterRoutes(g.mux)
	g.pairing.RegisterRoutes(g.mux)
	g.cloudflared.RegisterRoutes(g.mux)
	g.workspaces.RegisterRoutes(g.mux)
	g.browser.RegisterRoutes(g.mux)
	if g.runner != nil {
		g.runner.RegisterRoutes(g.mux)
	}
	if g.skills != nil {
		g.skills.RegisterRoutes(g.mux)
	}
	if g.codeIndex != nil {
		g.codeIndex.RegisterRoutes(g.mux)
	}
	if g.recallSvc != nil {
		g.recallSvc.RegisterRoutes(g.mux)
	}
	if g.orchestration != nil {
		g.orchestration.RegisterRoutes(g.mux)
	}
	if g.forge != nil {
		g.forge.RegisterRoutes(g.mux)
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
	for _, prefix := range openCodeProxyPrefixes {
		g.mux.Handle(prefix, g.proxy)
		g.mux.Handle(prefix+"/", g.proxy)
	}
	g.mux.HandleFunc("/file/content", g.handleFileContent)
	g.mux.HandleFunc("/api/file/content", g.handleFileContent)
	g.mux.HandleFunc("/api/hoxi/file/content", g.handleFileContent)
	g.mux.HandleFunc("/api/hoxi/file/validate", g.handleFileValidate)
	g.mux.HandleFunc("/api/hoxi/file/list", g.handleFileList)
	g.mux.HandleFunc("/api/hoxi/file/search", g.handleFileSearch)
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
