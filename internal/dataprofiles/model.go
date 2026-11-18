// Package dataprofiles implements Hoxi's credential-isolated connection handle
// bridge so models invoke HTTP APIs, SQL databases (SQLite, PostgreSQL, MySQL),
// and SSH hosts by handle ID without ever seeing tokens, passwords, or keys.
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
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	KindHTTPAPI  = "http_api"
	KindSQLite   = "sqlite"
	KindPostgres = "postgres"
	KindMySQL    = "mysql"
	KindSSH      = "ssh"

	maxHTTPResponseLen = 64 * 1024
	defaultHTTPTimeout = 20 * time.Second
)

var validProfileIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Profile defines a host-held connection record persisted in ~/.hoxi.
type Profile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"` // "http_api" | "sqlite" | "postgres" | "mysql" | "ssh"
	Description string            `json:"description,omitempty"`
	ReadOnly    bool              `json:"readOnly,omitempty"`
	BaseURL     string            `json:"baseUrl,omitempty"`
	BearerToken string            `json:"bearerToken,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Host        string            `json:"host,omitempty"`
	Port        int               `json:"port,omitempty"`
	Username    string            `json:"username,omitempty"`
	Password    string            `json:"password,omitempty"`
	Database    string            `json:"database,omitempty"`
	DBPath      string            `json:"dbPath,omitempty"`
	SSLMode     string            `json:"sslMode,omitempty"`
	KeyPath     string            `json:"keyPath,omitempty"`
	PrivateKey  string            `json:"privateKey,omitempty"`
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
	Action   string         `json:"action"` // "list" | "http_request" | "sql_query" | "ssh_exec" | "test"
	Profile  string         `json:"profile,omitempty"`
	Method   string         `json:"method,omitempty"`
	Endpoint string         `json:"endpoint,omitempty"`
	Body     map[string]any `json:"body,omitempty"`
	Query    string         `json:"query,omitempty"`
	MaxRows  int            `json:"maxRows,omitempty"`
	Command  string         `json:"command,omitempty"`
}

// Service manages credential-isolated profiles and executes bounded calls.
type Service struct {
	storePath  string
	httpClient *http.Client
	sshBin     string
	psqlBin    string
	mysqlBin   string

	mu         sync.RWMutex
	loaded     bool
	profiles   map[string]Profile
	writeSeq   uint64
	writeMu    sync.Mutex
	writtenSeq uint64
}

// NewService constructs a profile handle service backed by storePath.
func NewService(storePath string, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{
			Timeout: defaultHTTPTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				if len(via) > 0 {
					orig := via[0].URL
					if req.URL.Scheme != orig.Scheme || !strings.EqualFold(req.URL.Host, orig.Host) {
						return errors.New("refusing cross-origin redirect on credentialed HTTP profile")
					}
				}
				return nil
			},
		}
	}
	return &Service{
		storePath:  strings.TrimSpace(storePath),
		httpClient: client,
		profiles:   make(map[string]Profile),
	}
}

// SetHelperBinariesForTest overrides external CLI paths for deterministic unit tests.
func (s *Service) SetHelperBinariesForTest(sshBin, psqlBin, mysqlBin string) {
	if s == nil {
		return
	}
	s.sshBin = sshBin
	s.psqlBin = psqlBin
	s.mysqlBin = mysqlBin
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
		s.loaded = false
		backup := fmt.Sprintf("%s.corrupt-%d", s.storePath, time.Now().UTC().UnixMilli())
		_ = os.Rename(s.storePath, backup)
		return fmt.Errorf("corrupt dataprofiles store backed up to %s: %w", filepath.Base(backup), err)
	}
	for _, p := range doc.Profiles {
		id := strings.TrimSpace(p.ID)
		if id != "" {
			p.ID = id
			if p.Kind == "" {
				p.Kind = KindHTTPAPI
			}
			s.profiles[id] = cloneProfile(p)
		}
	}
	return nil
}

func cloneProfile(p Profile) Profile {
	if len(p.Headers) > 0 {
		hdrs := make(map[string]string, len(p.Headers))
		for k, v := range p.Headers {
			hdrs[k] = v
		}
		p.Headers = hdrs
	}
	return p
}

func (s *Service) snapshotLocked() (uint64, []Profile) {
	s.writeSeq++
	list := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		list = append(list, cloneProfile(p))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return s.writeSeq, list
}

func (s *Service) persistSnapshot(seq uint64, list []Profile) error {
	if s.storePath == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if seq <= s.writtenSeq {
		return nil
	}
	if dir := filepath.Dir(s.storePath); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	fl := flock.New(s.storePath + ".lock")
	if err := fl.Lock(); err != nil {
		return err
	}
	defer func() { _ = fl.Unlock() }()
	if err := hoxipaths.WriteJSONAtomic(s.storePath, map[string]any{"profiles": list}, 0o600); err != nil {
		return err
	}
	s.writtenSeq = seq
	return nil
}

func toPublic(p Profile) PublicProfile {
	var keys []string
	for k := range p.Headers {
		if strings.TrimSpace(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	hint := buildTargetHint(p)
	hasAuth := strings.TrimSpace(p.BearerToken) != "" ||
		len(keys) > 0 ||
		strings.TrimSpace(p.Password) != "" ||
		strings.TrimSpace(p.PrivateKey) != "" ||
		strings.TrimSpace(p.KeyPath) != ""
	return PublicProfile{
		ID:          p.ID,
		Name:        firstNonEmpty(p.Name, p.ID),
		Kind:        p.Kind,
		Description: p.Description,
		ReadOnly:    p.ReadOnly,
		TargetHint:  hint,
		HasAuth:     hasAuth,
		HeaderKeys:  keys,
		UpdatedAt:   p.UpdatedAt,
	}
}

func buildTargetHint(p Profile) string {
	switch p.Kind {
	case KindSQLite:
		if strings.TrimSpace(p.DBPath) != "" {
			return "sqlite:" + filepath.Base(strings.TrimSpace(p.DBPath))
		}
		return "sqlite"
	case KindPostgres, KindMySQL:
		host := firstNonEmpty(p.Host, "127.0.0.1")
		port := p.Port
		if port <= 0 {
			if p.Kind == KindPostgres {
				port = 5432
			} else {
				port = 3306
			}
		}
		db := strings.TrimSpace(p.Database)
		if db != "" {
			return fmt.Sprintf("%s://%s:%d/%s", p.Kind, host, port, db)
		}
		return fmt.Sprintf("%s://%s:%d", p.Kind, host, port)
	case KindSSH:
		host := strings.TrimSpace(p.Host)
		port := p.Port
		if port <= 0 {
			port = 22
		}
		if u := strings.TrimSpace(p.Username); u != "" {
			return fmt.Sprintf("ssh://%s@%s:%d", u, host, port)
		}
		return fmt.Sprintf("ssh://%s:%d", host, port)
	default:
		if u, err := url.Parse(strings.TrimSpace(p.BaseURL)); err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
		return ""
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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

// GetPublic returns the sanitized public metadata for a single profile handle.
func (s *Service) GetPublic(id string) (PublicProfile, bool) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return PublicProfile{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ensureLoadedLocked() != nil {
		return PublicProfile{}, false
	}
	p, ok := s.profiles[cleanID]
	if !ok {
		return PublicProfile{}, false
	}
	return toPublic(p), true
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
	switch kind {
	case KindHTTPAPI, KindSQLite, KindPostgres, KindMySQL, KindSSH:
	default:
		return PublicProfile{}, fmt.Errorf("unsupported profile kind %q (expected http_api|sqlite|postgres|mysql|ssh)", in.Kind)
	}

	s.mu.Lock()
	if err := s.ensureLoadedLocked(); err != nil {
		s.mu.Unlock()
		return PublicProfile{}, err
	}
	prev, exists := s.profiles[id]
	in.ID = id
	in.Kind = kind
	in.Name = firstNonEmpty(in.Name, id)
	in.Description = strings.TrimSpace(in.Description)
	if exists && prev.Kind == kind {
		if strings.TrimSpace(in.BearerToken) == "" {
			in.BearerToken = prev.BearerToken
		}
		if len(in.Headers) == 0 && len(prev.Headers) > 0 {
			in.Headers = prev.Headers
		}
		if strings.TrimSpace(in.BaseURL) == "" {
			in.BaseURL = prev.BaseURL
		}
		if strings.TrimSpace(in.Password) == "" {
			in.Password = prev.Password
		}
		if strings.TrimSpace(in.PrivateKey) == "" {
			in.PrivateKey = prev.PrivateKey
		}
		if strings.TrimSpace(in.KeyPath) == "" {
			in.KeyPath = prev.KeyPath
		}
		if strings.TrimSpace(in.Host) == "" {
			in.Host = prev.Host
		}
		if in.Port <= 0 {
			in.Port = prev.Port
		}
		if strings.TrimSpace(in.Username) == "" {
			in.Username = prev.Username
		}
		if strings.TrimSpace(in.Database) == "" {
			in.Database = prev.Database
		}
		if strings.TrimSpace(in.DBPath) == "" {
			in.DBPath = prev.DBPath
		}
	}

	if err := validateProfileFields(&in); err != nil {
		s.mu.Unlock()
		return PublicProfile{}, err
	}

	in.UpdatedAt = time.Now().UTC().UnixMilli()
	stored := cloneProfile(in)
	s.profiles[id] = stored
	seq, snap := s.snapshotLocked()
	pub := toPublic(stored)
	s.mu.Unlock()
	if err := s.persistSnapshot(seq, snap); err != nil {
		return PublicProfile{}, err
	}
	return pub, nil
}

func validateProfileFields(in *Profile) error {
	switch in.Kind {
	case KindHTTPAPI:
		rawBase := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
		parsed, err := url.Parse(rawBase)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("baseUrl must be a valid http:// or https:// origin")
		}
		in.BaseURL = rawBase
	case KindSQLite:
		dbPath := strings.TrimSpace(in.DBPath)
		if dbPath == "" || strings.HasPrefix(dbPath, "-") {
			return errors.New("valid dbPath is required for sqlite profile")
		}
		in.DBPath = filepath.Clean(dbPath)
	case KindPostgres, KindMySQL:
		in.Host = firstNonEmpty(in.Host, "127.0.0.1")
		if strings.HasPrefix(in.Host, "-") {
			return errors.New("invalid database host")
		}
		if strings.HasPrefix(strings.TrimSpace(in.Username), "-") || strings.HasPrefix(strings.TrimSpace(in.Database), "-") {
			return errors.New("invalid database username or name")
		}
		if in.Port < 0 || in.Port > 65535 {
			return errors.New("port must be 1..65535")
		}
	case KindSSH:
		in.Host = strings.TrimSpace(in.Host)
		if in.Host == "" || strings.HasPrefix(in.Host, "-") {
			return errors.New("valid host is required for ssh profile")
		}
		if strings.HasPrefix(strings.TrimSpace(in.Username), "-") || strings.HasPrefix(strings.TrimSpace(in.KeyPath), "-") {
			return errors.New("invalid ssh username or keyPath")
		}
		if in.Port < 0 || in.Port > 65535 {
			return errors.New("port must be 1..65535")
		}
	}
	return nil
}

// Delete removes a profile by handle ID.
func (s *Service) Delete(id string) (bool, error) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return false, errors.New("profile id is required")
	}
	s.mu.Lock()
	if err := s.ensureLoadedLocked(); err != nil {
		s.mu.Unlock()
		return false, err
	}
	if _, ok := s.profiles[cleanID]; !ok {
		s.mu.Unlock()
		return false, nil
	}
	delete(s.profiles, cleanID)
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()
	if err := s.persistSnapshot(seq, snap); err != nil {
		return false, err
	}
	return true, nil
}

// IsSafeReadCall reports whether raw JSON describes a strictly read-only,
// side-effect-free operation against a configured profile so the runner
// approval gate can distinguish safe queries from mutations.
func (s *Service) IsSafeReadCall(raw json.RawMessage) bool {
	var req InvokeRequest
	if json.Unmarshal(raw, &req) != nil {
		return false
	}
	act := strings.ToLower(strings.TrimSpace(req.Action))
	if act == "list" || act == "test" {
		return true
	}
	pub, ok := s.GetPublic(req.Profile)
	if !ok {
		return false
	}
	switch act {
	case "http_request":
		m := strings.ToUpper(strings.TrimSpace(req.Method))
		return (m == "" || m == http.MethodGet || m == http.MethodHead) && pub.Kind == KindHTTPAPI
	case "sql_query":
		ro, _ := IsReadOnlySQL(req.Query)
		return ro && (pub.Kind == KindSQLite || pub.Kind == KindPostgres || pub.Kind == KindMySQL)
	case "ssh_exec":
		ro, _ := IsReadOnlySSHCommand(req.Command)
		return ro && pub.ReadOnly && pub.Kind == KindSSH
	default:
		return false
	}
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
	case "sql_query":
		return s.invokeSQLQuery(ctx, prof, req)
	case "ssh_exec":
		return s.invokeSSHExec(ctx, prof, req)
	case "test":
		return s.invokeTestConnection(ctx, prof)
	default:
		return nil, fmt.Errorf("unsupported action %q (expected list|http_request|sql_query|ssh_exec|test)", req.Action)
	}
}

func (s *Service) invokeSQLQuery(ctx context.Context, prof Profile, req InvokeRequest) (map[string]any, error) {
	switch prof.Kind {
	case KindSQLite, KindPostgres, KindMySQL:
	default:
		return nil, fmt.Errorf("profile %q is kind %q, not a database profile", prof.ID, prof.Kind)
	}
	stmt := strings.TrimSpace(req.Query)
	if stmt == "" {
		return nil, errors.New("query is required for sql_query")
	}
	if err := ValidateSingleSQLStatement(stmt); err != nil {
		return nil, err
	}
	isRO, roReason := IsReadOnlySQL(stmt)
	if prof.ReadOnly && !isRO {
		return nil, fmt.Errorf("profile %q is configured as read-only: %s", prof.ID, roReason)
	}
	qCtx, cancel := withDefaultTimeout(ctx, defaultSQLTimeout)
	defer cancel()
	maxRows := effectiveMaxRows(req.MaxRows)
	switch prof.Kind {
	case KindSQLite:
		cols, rows, trunc, err := querySQLiteRows(qCtx, prof.DBPath, prof.ReadOnly, maxRows, stmt)
		if err != nil {
			return nil, fmt.Errorf("sqlite query on %s failed: %s", prof.ID, redactProfileSecrets(err.Error(), prof))
		}
		return map[string]any{
			"profile":   prof.ID,
			"kind":      KindSQLite,
			"columns":   cols,
			"rows":      rows,
			"rowCount":  len(rows),
			"truncated": trunc,
		}, nil
	case KindPostgres:
		return s.execPostgresDriver(qCtx, prof, stmt, maxRows)
	case KindMySQL:
		return s.execMySQLDriver(qCtx, prof, stmt, maxRows)
	default:
		return nil, fmt.Errorf("unsupported database profile kind %q", prof.Kind)
	}
}

func (s *Service) invokeTestConnection(ctx context.Context, prof Profile) (map[string]any, error) {
	started := time.Now()
	var err error
	switch prof.Kind {
	case KindHTTPAPI:
		_, err = s.invokeHTTPRequest(ctx, prof, InvokeRequest{Action: "http_request", Method: http.MethodHead, Endpoint: "/"})
		if err != nil {
			_, err = s.invokeHTTPRequest(ctx, prof, InvokeRequest{Action: "http_request", Method: http.MethodGet, Endpoint: "/"})
		}
	case KindSQLite, KindPostgres, KindMySQL:
		_, err = s.invokeSQLQuery(ctx, prof, InvokeRequest{Action: "sql_query", Query: "SELECT 1 AS ok", MaxRows: 1})
	case KindSSH:
		_, err = s.invokeSSHExec(ctx, prof, InvokeRequest{Action: "ssh_exec", Command: "uname -s"})
	default:
		err = fmt.Errorf("unsupported kind %q", prof.Kind)
	}
	lat := time.Since(started).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "profile": prof.ID, "latencyMs": lat, "error": err.Error()}, err
	}
	return map[string]any{"ok": true, "profile": prof.ID, "kind": prof.Kind, "latencyMs": strconv.FormatInt(lat, 10) + "ms"}, nil
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
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/test") {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
			id := firstNonEmpty(body.ID, r.URL.Query().Get("id"))
			res, err := s.Invoke(r.Context(), InvokeRequest{Action: "test", Profile: id})
			if err != nil {
				writeProfileJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeProfileJSON(w, http.StatusOK, res)
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
