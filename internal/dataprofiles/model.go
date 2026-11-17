// Package dataprofiles implements Hoxi's credential-isolated API endpoint
// handle bridge so models invoke authenticated endpoints by handle ID without
// ever seeing bearer tokens or secret header values.
package dataprofiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	KindHTTPAPI = "http_api"

	maxHTTPResponseLen = 64 * 1024
	defaultHTTPTimeout = 20 * time.Second
)

var validProfileIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Profile defines a host-held endpoint connection record persisted in the workspace.
type Profile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"` // "http_api"
	Description string            `json:"description,omitempty"`
	ReadOnly    bool              `json:"readOnly,omitempty"`
	BaseURL     string            `json:"baseUrl,omitempty"`
	BearerToken string            `json:"bearerToken,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	UpdatedAt   int64             `json:"updatedAt"`
}

// PublicProfile is the sanitized view returned to UIs and agent tool listings.
type PublicProfile struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	ReadOnly    bool     `json:"readOnly"`
	TargetHint  string   `json:"targetHint,omitempty"`
	HasAuth     bool     `json:"hasAuth"`
	HeaderKeys  []string `json:"headerKeys,omitempty"`
	UpdatedAt   int64    `json:"updatedAt"`
}

// InvokeRequest describes an agent or host call against a named profile handle.
type InvokeRequest struct {
	Action   string         `json:"action"` // "list" | "http_request"
	Profile  string         `json:"profile,omitempty"`
	Method   string         `json:"method,omitempty"`
	Endpoint string         `json:"endpoint,omitempty"`
	Body     map[string]any `json:"body,omitempty"`
}

// Service manages credential-isolated profiles and executes bounded API calls.
type Service struct {
	storePath  string
	httpClient *http.Client

	mu       sync.RWMutex
	loaded   bool
	profiles map[string]Profile
}

// NewService constructs a profile handle service backed by storePath.
func NewService(storePath string, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Service{
		storePath:  strings.TrimSpace(storePath),
		httpClient: client,
		profiles:   make(map[string]Profile),
	}
}

func (s *Service) ensureLoadedLocked() error {
	if s.loaded {
		return nil
	}
	s.loaded = true
	if s.storePath == "" {
		return nil
	}
	raw, err := os.ReadFile(s.storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var doc struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	for _, p := range doc.Profiles {
		id := strings.TrimSpace(p.ID)
		if id != "" {
			p.ID = id
			if p.Kind == "" {
				p.Kind = KindHTTPAPI
			}
			s.profiles[id] = p
		}
	}
	return nil
}

func (s *Service) saveLocked() error {
	if s.storePath == "" {
		return nil
	}
	list := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return hoxipaths.WriteJSONAtomic(s.storePath, map[string]any{"profiles": list}, 0o600)
}

func toPublic(p Profile) PublicProfile {
	var keys []string
	for k := range p.Headers {
		if strings.TrimSpace(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	hint := ""
	if u, err := url.Parse(strings.TrimSpace(p.BaseURL)); err == nil && u.Host != "" {
		hint = u.Scheme + "://" + u.Host
	}
	return PublicProfile{
		ID:          p.ID,
		Name:        firstNonEmpty(p.Name, p.ID),
		Kind:        KindHTTPAPI,
		Description: p.Description,
		ReadOnly:    p.ReadOnly,
		TargetHint:  hint,
		HasAuth:     strings.TrimSpace(p.BearerToken) != "" || len(keys) > 0,
		HeaderKeys:  keys,
		UpdatedAt:   p.UpdatedAt,
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

// List returns sanitized metadata for all configured profiles without secrets.
func (s *Service) List() ([]PublicProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return nil, err
	}
	out := make([]PublicProfile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, toPublic(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Upsert validates and stores a profile, preserving existing secret fields when omitted.
func (s *Service) Upsert(in Profile) (PublicProfile, error) {
	id := strings.TrimSpace(in.ID)
	if !validProfileIDRe.MatchString(id) {
		return PublicProfile{}, fmt.Errorf("invalid profile id %q", id)
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = KindHTTPAPI
	}
	if kind != KindHTTPAPI {
		return PublicProfile{}, fmt.Errorf("unsupported profile kind %q (expected http_api)", in.Kind)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return PublicProfile{}, err
	}
	prev, exists := s.profiles[id]
	in.ID = id
	in.Kind = KindHTTPAPI
	in.Name = firstNonEmpty(in.Name, id)
	in.Description = strings.TrimSpace(in.Description)
	if exists {
		if strings.TrimSpace(in.BearerToken) == "" {
			in.BearerToken = prev.BearerToken
		}
		if len(in.Headers) == 0 && len(prev.Headers) > 0 {
			in.Headers = prev.Headers
		}
		if strings.TrimSpace(in.BaseURL) == "" {
			in.BaseURL = prev.BaseURL
		}
	}

	rawBase := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	parsed, err := url.Parse(rawBase)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return PublicProfile{}, errors.New("baseUrl must be a valid http:// or https:// origin")
	}
	in.BaseURL = rawBase

	in.UpdatedAt = time.Now().UTC().UnixMilli()
	s.profiles[id] = in
	if err := s.saveLocked(); err != nil {
		return PublicProfile{}, err
	}
	return toPublic(in), nil
}

// Delete removes a profile by handle ID.
func (s *Service) Delete(id string) (bool, error) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return false, errors.New("profile id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return false, err
	}
	if _, ok := s.profiles[cleanID]; !ok {
		return false, nil
	}
	delete(s.profiles, cleanID)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// Invoke executes a profile handle operation without exposing stored credentials.
func (s *Service) Invoke(ctx context.Context, req InvokeRequest) (map[string]any, error) {
	act := strings.ToLower(strings.TrimSpace(req.Action))
	if act == "list" {
		items, err := s.List()
		if err != nil {
			return nil, err
		}
		return map[string]any{"profiles": items, "count": len(items)}, nil
	}

	profID := strings.TrimSpace(req.Profile)
	if profID == "" {
		return nil, errors.New("profile handle id is required")
	}
	s.mu.Lock()
	err := s.ensureLoadedLocked()
	prof, ok := s.profiles[profID]
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("profile %q is not configured", profID)
	}

	switch act {
	case "http_request":
		return s.invokeHTTPRequest(ctx, prof, req)
	default:
		return nil, fmt.Errorf("unsupported action %q", req.Action)
	}
}

// InvokeJSON decodes raw JSON into InvokeRequest and executes Invoke.
func (s *Service) InvokeJSON(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var req InvokeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("invalid hoxi_data arguments: %w", err)
	}
	return s.Invoke(ctx, req)
}

// RegisterRoutes mounts desktop-only management endpoints on mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux, rejectRemote func(*http.Request) bool) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote != nil && rejectRemote(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":"dataprofiles management is desktop-only"}`)
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := s.List()
			if err != nil {
				writeProfileJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeProfileJSON(w, http.StatusOK, map[string]any{"profiles": items})
		case http.MethodPost, http.MethodPut:
			var in Profile
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
				writeProfileJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
				return
			}
			pub, err := s.Upsert(in)
			if err != nil {
				writeProfileJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeProfileJSON(w, http.StatusOK, map[string]any{"profile": pub})
		case http.MethodDelete:
			id := strings.TrimSpace(r.URL.Query().Get("id"))
			if id == "" && strings.HasPrefix(r.URL.Path, "/api/hoxi/dataprofiles/") {
				id = strings.TrimPrefix(r.URL.Path, "/api/hoxi/dataprofiles/")
			}
			removed, err := s.Delete(id)
			if err != nil {
				writeProfileJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeProfileJSON(w, http.StatusOK, map[string]any{"removed": removed})
		default:
			writeProfileJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	}
	mux.HandleFunc("/api/hoxi/dataprofiles", handler)
	mux.HandleFunc("/api/hoxi/dataprofiles/", handler)
}

func writeProfileJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
