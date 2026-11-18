package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var validHostIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// HostEntry stores a mounted remote headless development host on disk (0600).
type HostEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	Token         string `json:"token"`
	DefaultDir    string `json:"defaultDir,omitempty"`
	Platform      string `json:"platform,omitempty"`
	Arch          string `json:"arch,omitempty"`
	Version       string `json:"version,omitempty"`
	LastSeenAt    int64  `json:"lastSeenAt,omitempty"`
	LastLatencyMs int64  `json:"lastLatencyMs,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// PublicHost is the credential-free representation exposed to UI clients.
type PublicHost struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	HasToken      bool   `json:"hasToken"`
	DefaultDir    string `json:"defaultDir,omitempty"`
	Platform      string `json:"platform,omitempty"`
	Arch          string `json:"arch,omitempty"`
	Version       string `json:"version,omitempty"`
	LastSeenAt    int64  `json:"lastSeenAt,omitempty"`
	LastLatencyMs int64  `json:"lastLatencyMs,omitempty"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// UpsertInput creates or updates a mounted remote development host.
type UpsertInput struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Token      string `json:"token"`
	PairURI    string `json:"pairUri"`
	DefaultDir string `json:"defaultDir"`
}

// ProbeResult reports live connectivity, latency, and host metadata from a remote host.
type ProbeResult struct {
	OK        bool       `json:"ok"`
	LatencyMs int64      `json:"latencyMs"`
	Info      *HostInfo  `json:"info,omitempty"`
	Host      PublicHost `json:"host"`
	Error     string     `json:"error,omitempty"`
}

type storeDoc struct {
	Version int                  `json:"version"`
	Hosts   map[string]HostEntry `json:"hosts"`
}

// Service manages desktop-mounted remote development hosts and proxies requests.
type Service struct {
	mu        sync.RWMutex
	storePath string
	hosts     map[string]HostEntry
	loadErr   error
	transport http.RoundTripper
}

// NewService opens the remote host registry at storePath.
func NewService(storePath string, transport http.RoundTripper) *Service {
	if transport == nil {
		transport = &http.Transport{
			Proxy: nil, // Never route LAN/Tailscale/SSH-forwarded dev hosts through system HTTP_PROXY
			DialContext: (&net.Dialer{
				Timeout:   8 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		}
	}
	s := &Service{
		storePath: strings.TrimSpace(storePath),
		hosts:     make(map[string]HostEntry),
		transport: transport,
	}
	s.loadFromDisk()
	return s
}

func toPublicHost(e HostEntry) PublicHost {
	u, err := url.Parse(strings.TrimSpace(e.URL))
	cleanURL := strings.TrimSpace(e.URL)
	if err == nil && u.Scheme != "" && u.Host != "" {
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		cleanURL = strings.TrimRight(u.String(), "/")
	}
	return PublicHost{
		ID:            e.ID,
		Name:          e.Name,
		URL:           cleanURL,
		HasToken:      strings.TrimSpace(e.Token) != "",
		DefaultDir:    e.DefaultDir,
		Platform:      e.Platform,
		Arch:          e.Arch,
		Version:       e.Version,
		LastSeenAt:    e.LastSeenAt,
		LastLatencyMs: e.LastLatencyMs,
		UpdatedAt:     e.UpdatedAt,
	}
}

func (s *Service) loadFromDisk() {
	if s.storePath == "" {
		return
	}
	raw, err := os.ReadFile(s.storePath)
	if err != nil {
		if !os.IsNotExist(err) {
			s.loadErr = err
		}
		return
	}
	var doc storeDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", s.storePath, time.Now().UnixNano())
		_ = writeAtomic0600(backup, raw)
		s.loadErr = fmt.Errorf("corrupt remote-hosts store backed up: %w", err)
		return
	}
	for id, h := range doc.Hosts {
		if strings.TrimSpace(id) != "" {
			h.ID = id
			s.hosts[id] = h
		}
	}
}

func (s *Service) saveLocked() error {
	if s.loadErr != nil {
		return s.loadErr
	}
	if s.storePath == "" {
		return nil
	}
	raw, err := json.MarshalIndent(storeDoc{
		Version: 1,
		Hosts:   s.hosts,
	}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic0600(s.storePath, append(raw, '\n'))
}

// ListPublic returns sorted credential-free remote host descriptors.
func (s *Service) ListPublic() []PublicHost {
	s.mu.RLock()
	out := make([]PublicHost, 0, len(s.hosts))
	for _, h := range s.hosts {
		out = append(out, toPublicHost(h))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sanitizeSlug(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
			b.WriteRune(r)
		case r == '.' || r == ' ' || r == ':':
			b.WriteByte('-')
		}
	}
	res := strings.Trim(b.String(), "-_")
	if len(res) > 48 {
		res = res[:48]
	}
	return res
}

// Upsert adds or updates a remote development host entry (supporting `pairUri` or explicit fields).
func (s *Service) Upsert(in UpsertInput) (PublicHost, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(in.URL), "/")
	name := strings.TrimSpace(in.Name)
	token := strings.TrimSpace(in.Token)
	if strings.TrimSpace(in.PairURI) != "" {
		pURL, pName, pTok, err := ParsePairURI(in.PairURI)
		if err != nil {
			return PublicHost{}, err
		}
		endpoint = pURL
		if name == "" {
			name = pName
		}
		if token == "" {
			token = pTok
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return PublicHost{}, errors.New("valid http:// or https:// host URL is required")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = sanitizeSlug(firstNonEmptyString(name, u.Hostname(), "remote-host"))
	}
	if !validHostIDRe.MatchString(id) {
		return PublicHost{}, errors.New("host id must match [a-zA-Z0-9._-]{1,64}")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return PublicHost{}, s.loadErr
	}
	now := time.Now().UnixMilli()
	prev, exists := s.hosts[id]
	if !exists {
		prev = HostEntry{ID: id, CreatedAt: now}
	}
	if token == "" {
		token = prev.Token
	}
	if strings.TrimSpace(token) == "" {
		return PublicHost{}, errors.New("host bearer token is required")
	}
	if name == "" {
		name = firstNonEmptyString(prev.Name, u.Hostname(), id)
	}
	prev.Name = name
	prev.URL = endpoint
	prev.Token = token
	if strings.TrimSpace(in.DefaultDir) != "" || !exists {
		prev.DefaultDir = strings.TrimSpace(in.DefaultDir)
	}
	prev.UpdatedAt = now
	s.hosts[id] = prev
	if err := s.saveLocked(); err != nil {
		return PublicHost{}, err
	}
	return toPublicHost(prev), nil
}

// Delete removes a remote host entry by ID.
func (s *Service) Delete(id string) (bool, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return false, s.loadErr
	}
	if _, ok := s.hosts[id]; !ok {
		return false, nil
	}
	delete(s.hosts, id)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// Probe performs an authenticated `/api/hoxi/host-info` health & RTT check against host ID.
func (s *Service) Probe(ctx context.Context, id string) (ProbeResult, error) {
	s.mu.RLock()
	entry, ok := s.hosts[strings.TrimSpace(id)]
	s.mu.RUnlock()
	if !ok {
		return ProbeResult{}, fmt.Errorf("remote host %q not found", id)
	}

	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	target := strings.TrimRight(entry.URL, "/") + "/api/hoxi/host-info"
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+entry.Token)
	req.Header.Set("Accept", "application/json")

	started := time.Now()
	client := &http.Client{Transport: s.transport, Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return ProbeResult{
			OK:        false,
			LatencyMs: latency,
			Host:      toPublicHost(entry),
			Error:     scrubToken(err.Error(), entry.Token),
		}, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProbeResult{
			OK:        false,
			LatencyMs: latency,
			Host:      toPublicHost(entry),
			Error:     fmt.Sprintf("HTTP %d: %s", resp.StatusCode, scrubToken(strings.TrimSpace(string(raw)), entry.Token)),
		}, nil
	}
	var info HostInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return ProbeResult{
			OK:        false,
			LatencyMs: latency,
			Host:      toPublicHost(entry),
			Error:     "invalid host-info JSON response",
		}, nil
	}

	now := time.Now().UnixMilli()
	s.mu.Lock()
	if cur, exists := s.hosts[entry.ID]; exists {
		cur.Platform = info.Platform
		cur.Arch = info.Arch
		cur.Version = info.Version
		if cur.DefaultDir == "" && strings.TrimSpace(info.RootDir) != "" {
			cur.DefaultDir = strings.TrimSpace(info.RootDir)
		}
		cur.LastSeenAt = now
		cur.LastLatencyMs = latency
		cur.UpdatedAt = now
		s.hosts[entry.ID] = cur
		entry = cur
		_ = s.saveLocked()
	}
	s.mu.Unlock()

	return ProbeResult{
		OK:        true,
		LatencyMs: latency,
		Info:      &info,
		Host:      toPublicHost(entry),
	}, nil
}

// ProxyHTTP forwards a desktop request to `/api/hoxi/hosts/proxy/{id}/{rest...}`
// with streaming flush enabled (`FlushInterval: -1`) and host credentials injected
// strictly in-memory inside the Go backend.
func (s *Service) ProxyHTTP(w http.ResponseWriter, r *http.Request, hostID, subPath string) {
	s.mu.RLock()
	entry, ok := s.hosts[strings.TrimSpace(hostID)]
	s.mu.RUnlock()
	if !ok {
		writeHostJSONError(w, http.StatusNotFound, fmt.Sprintf("remote host %q not found", hostID))
		return
	}
	upstreamURL, err := url.Parse(strings.TrimRight(entry.URL, "/"))
	if err != nil {
		writeHostJSONError(w, http.StatusBadGateway, "invalid upstream host URL")
		return
	}
	if !strings.HasPrefix(subPath, "/") {
		subPath = "/" + subPath
	}

	rp := &httputil.ReverseProxy{
		Transport:     s.transport,
		FlushInterval: -1,
		Director: func(out *http.Request) {
			out.URL.Scheme = upstreamURL.Scheme
			out.URL.Host = upstreamURL.Host
			out.URL.Path = strings.TrimRight(upstreamURL.Path, "/") + subPath
			out.URL.RawPath = ""
			if strings.Contains(out.URL.RawQuery, "hoxiToken") {
				if q, qErr := url.ParseQuery(out.URL.RawQuery); qErr == nil {
					q.Del("hoxiToken")
					out.URL.RawQuery = q.Encode()
				}
			}
			out.Host = upstreamURL.Host
			out.Header.Del("Cookie")
			out.Header.Del("Origin")
			out.Header.Del("Referer")
			out.Header.Del("X-Hoxi-Token")
			out.Header.Set("Authorization", "Bearer "+entry.Token)
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, pErr error) {
			writeHostJSONError(rw, http.StatusBadGateway, scrubToken(pErr.Error(), entry.Token))
		},
	}
	rp.ServeHTTP(w, r)
}

// RegisterRoutes mounts desktop-only `/api/hoxi/hosts` management, probe, and proxy routes.
func (s *Service) RegisterRoutes(mux *http.ServeMux, isRemote func(*http.Request) bool) {
	if s == nil || mux == nil {
		return
	}
	rejectRemote := func(w http.ResponseWriter, r *http.Request) bool {
		if isRemote != nil && isRemote(r) {
			writeHostJSONError(w, http.StatusForbidden, "remote host registry is only accessible on desktop")
			return true
		}
		return false
	}

	mux.HandleFunc("/api/hoxi/hosts", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeHostJSON(w, http.StatusOK, map[string]any{"hosts": s.ListPublic()})
		case http.MethodPost:
			var in UpsertInput
			if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&in); err != nil {
				writeHostJSONError(w, http.StatusBadRequest, "invalid JSON payload")
				return
			}
			pub, err := s.Upsert(in)
			if err != nil {
				writeHostJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeHostJSON(w, http.StatusOK, pub)
		case http.MethodDelete:
			id := strings.TrimSpace(r.URL.Query().Get("id"))
			removed, err := s.Delete(id)
			if err != nil {
				writeHostJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !removed {
				writeHostJSONError(w, http.StatusNotFound, "remote host not found")
				return
			}
			writeHostJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeHostJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/api/hoxi/hosts/probe", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			writeHostJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
			writeHostJSONError(w, http.StatusBadRequest, "host id is required")
			return
		}
		res, err := s.Probe(r.Context(), body.ID)
		if err != nil {
			writeHostJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeHostJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("/api/hoxi/hosts/proxy/", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/hoxi/hosts/proxy/")
		slashIdx := strings.IndexByte(rest, '/')
		var hostID, subPath string
		if slashIdx < 0 {
			hostID = rest
			subPath = "/"
		} else {
			hostID = rest[:slashIdx]
			subPath = rest[slashIdx:]
		}
		if strings.TrimSpace(hostID) == "" {
			writeHostJSONError(w, http.StatusBadRequest, "host id is required in proxy path")
			return
		}
		s.ProxyHTTP(w, r, hostID, subPath)
	})
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func scrubToken(msg, tok string) string {
	if len(strings.TrimSpace(tok)) >= 6 {
		return strings.ReplaceAll(msg, strings.TrimSpace(tok), "[REDACTED]")
	}
	return msg
}

func writeHostJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeHostJSONError(w http.ResponseWriter, status int, msg string) {
	writeHostJSON(w, status, map[string]any{"error": msg})
}
