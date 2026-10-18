package browser

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Action struct {
	Action   string  `json:"action"`
	URL      string  `json:"url,omitempty"`
	Selector string  `json:"selector,omitempty"`
	Text     string  `json:"text,omitempty"`
	Key      string  `json:"key,omitempty"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	DeltaX   float64 `json:"deltaX,omitempty"`
	DeltaY   float64 `json:"deltaY,omitempty"`
	TabID    string  `json:"tabID,omitempty"`
	Manual   *bool   `json:"manual,omitempty"`
}

type Tab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type State struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Tabs   []Tab  `json:"tabs"`
	TabID  string `json:"tabID"`
	Manual bool   `json:"manual"`
}

func emptyState() State { return State{Tabs: []Tab{}} }

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	for _, path := range []string{"state", "frame", "action"} {
		mux.HandleFunc("/api/hoxi/browser/"+path, s.serveHTTP)
	}
}

func localHost(host string) bool {
	return host == "localhost" || wailsHost(host) || host == "127.0.0.1" || host == "::1"
}

func wailsHost(host string) bool {
	return host == "wails" || host == "wails.localhost" || host == "app.wails.localhost"
}

func trustedAuthority(authority string) bool {
	u, err := url.Parse("http://" + authority)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !localHost(u.Hostname()) {
		return false
	}
	if strings.Contains(authority, ":") && !strings.HasPrefix(authority, "[") {
		if _, _, err := net.SplitHostPort(authority); err != nil {
			return false
		}
	}
	return true
}

func validOrigin(u *url.URL) bool {
	return u.User == nil && u.Host != "" && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == ""
}

func (s *Service) trustedUI(origin, authority string) (bool, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	configuredHost := false
	for allowed := range s.uiOrigins {
		u, _ := url.Parse(allowed)
		if u.Host == authority {
			configuredHost = true
		}
	}
	u, err := url.Parse(origin)
	nativeOrigin := err == nil && validOrigin(u) && u.Scheme == "wails" && wailsHost(u.Host)
	nativeHost := wailsHost(authority) || s.uiOrigins["wails://"+authority]
	return nativeOrigin || s.uiOrigins[origin], configuredHost, nativeHost
}

func (s *Service) authenticate(r *http.Request) (bool, error) {
	if !trustedAuthority(r.Host) {
		return false, errors.New("untrusted Host")
	}
	origin := r.Header.Get("Origin")
	allowedOrigin, configuredHost, native := s.trustedUI(origin, r.Host)
	if origin != "" && !allowedOrigin {
		return false, errors.New("untrusted Origin")
	}
	agentHeader, auth := r.Header.Get("X-Hoxi-Agent"), r.Header.Get("Authorization")
	if agentHeader != "" || auth != "" {
		// Browser-originated requests can never acquire agent privileges.
		if origin != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("X-Hoxi-Browser") != "" {
			return false, errors.New("agent requests must not originate in a browser")
		}
		s.mu.Lock()
		token := s.token
		s.mu.Unlock()
		if agentHeader != "true" || token == "" || subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+token)) != 1 {
			return false, errors.New("invalid agent credentials")
		}
		return true, nil
	}
	if origin == "" {
		site := r.Header.Get("Sec-Fetch-Site")
		if !native && site == "" {
			if ref, err := url.Parse(r.Header.Get("Referer")); err == nil && ref.Scheme == "wails" {
				allowedRef, _, refNative := s.trustedUI(ref.Scheme+"://"+ref.Host, ref.Host)
				native = allowedRef && refNative
			}
		}
		if r.Header.Get("X-Hoxi-Browser") != "true" || (!native && !configuredHost) || (site != "same-origin" && !(site == "" && native)) {
			return false, errors.New("UI Origin or trusted browser request required")
		}
	}
	return false, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	agent, err := s.authenticate(r)
	if err != nil {
		fail(w, 403, err)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/hoxi/browser/")
	method := http.MethodGet
	if path == "action" {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	key, err := requestScope(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	var action Action
	if path == "action" {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			fail(w, 415, errors.New("application/json required"))
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&action); err != nil {
			fail(w, 400, err)
			return
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			fail(w, 400, errors.New("one JSON object required"))
			return
		}
		if err = validateAction(action, agent); err != nil {
			fail(w, 400, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sc, err := s.acquire(ctx, key, path == "action" && action.Action != "close")
	if err != nil {
		fail(w, 503, err)
		return
	}
	if sc == nil {
		if path == "frame" {
			fail(w, 404, errors.New("browser not started"))
			return
		}
		writeJSON(w, 200, emptyState())
		return
	}
	defer func() { <-sc.gate }()
	if agent && sc.manual {
		fail(w, 403, errors.New("manual control is active"))
		return
	}
	if path == "action" && action.Action == "close" {
		s.remove(key, sc)
		writeJSON(w, 200, emptyState())
		return
	}
	if path == "action" && action.Action == "control" {
		sc.manual = *action.Manual
	} else if path == "action" {
		if err = sc.start(ctx); err != nil {
			s.remove(key, sc)
			fail(w, 502, err)
			return
		}
	}
	if sc.browser == nil {
		if path == "frame" {
			fail(w, 404, errors.New("browser not started"))
			return
		}
		state := emptyState()
		state.Manual = sc.manual
		writeJSON(w, 200, state)
		return
	}
	opctx, opcancel := context.WithCancel(sc.browser)
	stop := context.AfterFunc(ctx, opcancel)
	defer stop()
	defer opcancel()
	if path == "frame" {
		if sc.tabID == "" {
			if _, err := sc.state(opctx); err != nil {
				fail(w, 502, err)
				return
			}
		}
		image, err := sc.capture(opctx)
		if err != nil {
			if _, stateErr := sc.state(opctx); stateErr == nil && sc.tabID != "" {
				image, err = sc.capture(opctx)
			}
		}
		if err != nil {
			fail(w, 502, err)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(image)
		return
	}
	var extra map[string]any
	if path == "action" && action.Action != "control" {
		if sc.tabID == "" && action.Action != "tab_new" && action.Action != "tab_select" && action.Action != "tab_close" {
			if _, err = sc.state(opctx); err != nil {
				fail(w, 502, err)
				return
			}
		}
		prevTab := sc.tabID
		extra, err = sc.perform(opctx, action)
		if err != nil && action.Action != "tab_new" && action.Action != "tab_select" && action.Action != "tab_close" {
			if _, stateErr := sc.state(opctx); stateErr == nil && sc.tabID != "" && sc.tabID != prevTab {
				extra, err = sc.perform(opctx, action)
			}
		}
		if err != nil {
			fail(w, 502, err)
			return
		}
	}
	state, err := sc.state(opctx)
	if err != nil {
		fail(w, 502, err)
		return
	}
	result := map[string]any{"url": state.URL, "title": state.Title, "tabs": state.Tabs, "tabID": state.TabID, "manual": state.Manual}
	for k, v := range extra {
		if b, ok := v.([]byte); ok {
			result[k] = base64.StdEncoding.EncodeToString(b)
		} else {
			result[k] = v
		}
	}
	writeJSON(w, 200, result)
}
