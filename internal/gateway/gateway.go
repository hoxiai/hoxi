package gateway

import (
	"bytes"
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
	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/leaderboard"
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
	onOpenDirectoryDialog func() (string, error)
	dialogMu              sync.RWMutex
	originMu              sync.RWMutex
	uiOrigins             map[string]bool
}

func New(openCodePort int, password, rootDir string, distFS fs.FS, status *statusbar.Tracker) *Gateway {
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
		if status != nil && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Body = newSSETapReadCloser(resp.Body, status.InspectSSELine)
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
	handoffSvc := handoff.NewService(openCodePort, password, rootDir, providers)
	runnerSvc := runner.NewService(rootDir, wtMgr, status, "")
	authSvc := auth.NewService(nil)
	updaterSvc := updater.NewChecker("0.1.0", nil)
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
	}

	g.registerRoutes(distFS)
	return g
}

func (g *Gateway) SetBrowserAgentToken(token string) { g.browser.SetAgentToken(token) }

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
}

func (g *Gateway) SetOpenDirectoryDialog(fn func() (string, error)) {
	g.dialogMu.Lock()
	defer g.dialogMu.Unlock()
	g.onOpenDirectoryDialog = fn
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.allowRequest(w, r) {
		return
	}
	if !g.guardSessionScope(w, r) {
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Hoxi-Browser, X-Hoxi-Directory")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	g.mux.ServeHTTP(w, r)
}

func (g *Gateway) registerRoutes(distFS fs.FS) {
	g.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	g.mux.HandleFunc("/api/hoxi/bootstrap", func(w http.ResponseWriter, _ *http.Request) {
		var st statusbar.Summary
		if g.status != nil {
			st = g.status.Snapshot()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"rootDir":   g.rootDir,
			"wsBaseURL": g.wsBaseURL,
			"status":    st,
		})
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
		g.mux.Handle("/api/hoxi/updater", updater.NewHTTPHandler(g.updater))
		g.mux.Handle("/api/hoxi/updater/", updater.NewHTTPHandler(g.updater))
	}
	if g.leaderboard != nil {
		h := leaderboard.NewHTTPHandler(g.leaderboard)
		g.mux.Handle("/api/hoxi/models/leaderboard", h)
		g.mux.Handle("/api/hoxi/models/leaderboard/", h)
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
				_, _ = io.Copy(w, data)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

type sseTapReadCloser struct {
	rc       io.ReadCloser
	onLine   func(string)
	buf      []byte
	dropping bool
}

const maxSSETapLineSize = 64 * 1024

func newSSETapReadCloser(rc io.ReadCloser, onLine func(string)) io.ReadCloser {
	return &sseTapReadCloser{rc: rc, onLine: onLine}
}

func (t *sseTapReadCloser) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 && t.onLine != nil {
		for remaining := p[:n]; len(remaining) > 0; {
			end := bytes.IndexByte(remaining, '\n')
			fragment := remaining
			if end >= 0 {
				fragment = remaining[:end]
			}
			if !t.dropping {
				if len(t.buf)+len(fragment) > maxSSETapLineSize {
					t.dropping = true
					t.buf = t.buf[:0]
				} else {
					t.buf = append(t.buf, fragment...)
				}
			}
			if end < 0 {
				break
			}
			if !t.dropping {
				t.onLine(strings.TrimRight(string(t.buf), "\r"))
			}
			t.buf = t.buf[:0]
			t.dropping = false
			remaining = remaining[end+1:]
		}
	}
	return n, err
}

func (t *sseTapReadCloser) Close() error {
	return t.rc.Close()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
