package codeindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

type Service struct {
	root  string
	cfg   Config
	now   func() int64
	mu    sync.Mutex
	cache map[string]*cachedIndex
}

type StatusInfo struct {
	Directory string `json:"directory"`
	Ready     bool   `json:"ready"`
	Files     int    `json:"files"`
	Chunks    int    `json:"chunks"`
	IndexedAt int64  `json:"indexedAt"`
	Root      string `json:"root"`
}

func NewService(root string) *Service {
	if strings.TrimSpace(root) == "" {
		root = DefaultRoot()
	}
	return &Service{
		root:  root,
		cfg:   DefaultConfig(),
		now:   func() int64 { return time.Now().UnixMilli() },
		cache: make(map[string]*cachedIndex),
	}
}

func DefaultRoot() string {
	if dir := hoxipaths.Join("code-index"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "hoxi-code-index")
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/code-index/search", s.handleSearch)
	mux.HandleFunc("/api/hoxi/code-index/inspect", s.handleInspect)
	mux.HandleFunc("/api/hoxi/code-index/callers", s.handleCallers)
	mux.HandleFunc("/api/hoxi/code-index/callees", s.handleCallees)
	mux.HandleFunc("/api/hoxi/code-index/impact", s.handleImpact)
	mux.HandleFunc("/api/hoxi/code-index/context", s.handleContext)
	mux.HandleFunc("/api/hoxi/code-index/status", s.handleStatus)
	mux.HandleFunc("/api/hoxi/code-index/refresh", s.handleRefresh)
}

func (s *Service) Search(directory, query string, limit int) (SearchResponse, error) {
	return s.SearchWithPath(directory, query, "", limit)
}

func (s *Service) SearchWithPath(directory, query, pathPrefix string, limit int) (SearchResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return SearchResponse{}, err
	}
	c, results := s.rankWithPath(directory, query, pathPrefix, limit)
	if c == nil {
		return SearchResponse{}, errors.New("code index unavailable")
	}
	return SearchResponse{
		Directory: directory,
		Query:     query,
		Results:   results,
		Files:     len(c.index.Files),
		Chunks:    c.chunkCount(),
		IndexedAt: c.index.BuiltAt,
	}, nil
}

func (s *Service) Status(directory string) (StatusInfo, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return StatusInfo{}, err
	}
	c, err := s.ensure(directory)
	if err != nil {
		return StatusInfo{}, err
	}
	return StatusInfo{
		Directory: directory,
		Ready:     true,
		Files:     len(c.index.Files),
		Chunks:    c.chunkCount(),
		IndexedAt: c.index.BuiltAt,
		Root:      s.root,
	}, nil
}

func (s *Service) Refresh(directory string) (StatusInfo, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return StatusInfo{}, err
	}
	s.mu.Lock()
	delete(s.cache, directory)
	s.mu.Unlock()
	return s.Status(directory)
}

func (s *Service) ensure(directory string) (*cachedIndex, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached := s.cache[directory]; cached != nil && s.now()-cached.index.BuiltAt < cacheTTL {
		return cached, nil
	}
	var previous *Index
	if cached := s.cache[directory]; cached != nil {
		previous = cached.index
	} else {
		previous = s.load(directory)
	}
	if previous == nil {
		previous = &Index{Files: map[string]*FileRecord{}}
	}
	index, err := s.build(directory, previous)
	if err != nil {
		return nil, err
	}
	if err := s.save(index); err != nil {
		return nil, err
	}
	cached := buildDocs(index)
	s.cache[directory] = cached
	return cached, nil
}

func (s *Service) indexPath(directory string) string {
	sum := sha256.Sum256([]byte(directory))
	return filepath.Join(s.root, hex.EncodeToString(sum[:])+".json")
}

func (s *Service) load(directory string) *Index {
	raw, err := os.ReadFile(s.indexPath(directory))
	if err != nil {
		return nil
	}
	var index Index
	if json.Unmarshal(raw, &index) != nil || index.Version != indexVersion || index.Files == nil {
		return nil
	}
	return &index
}

func (s *Service) save(index *Index) error {
	raw, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".index-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.indexPath(index.Directory))
}

func canonicalDir(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", errors.New("directory is required")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return "", errors.New("directory does not exist")
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if isForbiddenWorkspaceRoot(abs) {
		return "", errors.New("refusing to index or inspect protected directory")
	}
	return abs, nil
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (s *Service) directoryParam(r *http.Request) (string, error) {
	directory := strings.TrimSpace(r.URL.Query().Get("directory"))
	if directory == "" && r.Method == http.MethodPost {
		var body struct {
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			return "", errors.New("invalid request")
		}
		directory = strings.TrimSpace(body.Directory)
	}
	return canonicalDir(directory)
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	directory, err := s.directoryParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	if q.Get("refresh") == "1" || q.Get("refresh") == "true" {
		_, _ = s.Refresh(directory)
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	pathPrefix := q.Get("path")
	if pathPrefix == "" {
		pathPrefix = q.Get("pathPrefix")
	}
	result, err := s.SearchWithPath(directory, q.Get("q"), pathPrefix, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	directory, err := s.directoryParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := s.Status(directory)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	directory, err := s.directoryParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := s.Refresh(directory)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}
