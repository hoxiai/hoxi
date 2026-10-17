package main

import (
	"bufio"
	"bytes"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type desktopGateway struct {
	openCodePort          int
	password              string
	rootDir               string
	wsBaseURL             string
	proxy                 *httputil.ReverseProxy
	mux                   *http.ServeMux
	status                *desktopStatusTracker
	worktrees             *worktreeManager
	onOpenDirectoryDialog func() (string, error)
}

func newDesktopGateway(openCodePort int, password, rootDir string, distFS fs.FS, status *desktopStatusTracker) *desktopGateway {
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
			resp.Body = newSSETapReadCloser(resp.Body, status.inspectSSELine)
		}
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		writeJSONError(w, http.StatusBadGateway, "opencode service unavailable: "+err.Error())
	}

	g := &desktopGateway{
		openCodePort: openCodePort,
		password:     password,
		rootDir:      rootDir,
		proxy:        proxy,
		mux:          http.NewServeMux(),
		status:       status,
		worktrees:    newWorktreeManager(rootDir),
	}

	g.registerRoutes(distFS)
	return g
}

func (g *desktopGateway) setWSBaseURL(wsURL string) {
	g.wsBaseURL = wsURL
}

func (g *desktopGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Allow local CORS for WebSocket / loopback bridge
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	g.mux.ServeHTTP(w, r)
}

func (g *desktopGateway) registerRoutes(distFS fs.FS) {
	g.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	g.mux.HandleFunc("/api/hoxi/bootstrap", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"rootDir":   g.rootDir,
			"wsBaseURL": g.wsBaseURL,
			"status":    g.status.snapshot(),
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

	g.mux.HandleFunc("/api/hoxi/provider/fetch-models", g.handleFetchProviderModels)
	g.mux.HandleFunc("/api/hoxi/provider/test-model", g.handleTestProviderModel)

	g.worktrees.registerRoutes(g.mux)

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
		"/provider",
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
		cleanPath := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
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
			s := bufio.NewScanner(strings.NewReader(line))
			for s.Scan() {
				t.onLine(s.Text())
			}
		}
	}
	return n, err
}

func (t *sseTapReadCloser) Close() error {
	return t.rc.Close()
}
