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
	"time"

	"github.com/hoxiai/hoxi/internal/handoff"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/statusbar"
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
	onOpenDirectoryDialog func() (string, error)
}

type desktopGateway = Gateway

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

	providers := provider.NewService(openCodePort, password, rootDir, proxy)
	handoffSvc := handoff.NewService(openCodePort, password, rootDir, providers)

	g := &Gateway{
		openCodePort: openCodePort,
		password:     password,
		rootDir:      rootDir,
		proxy:        proxy,
		mux:          http.NewServeMux(),
		status:       status,
		worktrees:    worktree.NewManager(rootDir),
		workspaces:   workspace.NewStore(rootDir),
		providers:    providers,
		handoff:      handoffSvc,
	}

	g.registerRoutes(distFS)
	return g
}

func (g *Gateway) SetWSBaseURL(wsURL string) {
	g.wsBaseURL = wsURL
	if g.handoff != nil {
		g.handoff.SetWSBaseURL(wsURL)
	}
}

func (g *Gateway) SetOpenDirectoryDialog(fn func() (string, error)) {
	g.onOpenDirectoryDialog = fn
}

func isAllowedLocalOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	lower := strings.ToLower(origin)
	if strings.HasPrefix(lower, "wails://") {
		return true
	}
	parsed, err := url.Parse(lower)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" ||
		host == "localhost" ||
		host == "::1" ||
		host == "wails.localhost" ||
		strings.HasSuffix(host, ".wails.localhost")
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		if isAllowedLocalOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		} else if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusForbidden)
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
		if g.onOpenDirectoryDialog == nil {
			writeJSON(w, http.StatusOK, map[string]any{"path": ""})
			return
		}
		dirPath, err := g.onOpenDirectoryDialog()
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
	rc     io.ReadCloser
	onLine func(string)
	buf    bytes.Buffer
}

func newSSETapReadCloser(rc io.ReadCloser, onLine func(string)) io.ReadCloser {
	return &sseTapReadCloser{rc: rc, onLine: onLine}
}

func (t *sseTapReadCloser) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 && t.onLine != nil {
		t.buf.Write(p[:n])
		for {
			line, readErr := t.buf.ReadString('\n')
			if readErr != nil {
				// Put incomplete line back into buffer
				t.buf.WriteString(line)
				break
			}
			t.onLine(strings.TrimRight(line, "\r\n"))
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
