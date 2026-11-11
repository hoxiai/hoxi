package codeindex

import (
	"context"
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
	"github.com/hoxiai/hoxi/internal/llm"
)

type indexFlight struct {
	done         chan struct{}
	forceRebuild bool
	cached       *cachedIndex
	err          error
}

type Service struct {
	root     string
	cfg      Config
	now      func() int64
	mu       sync.Mutex
	clock    uint64
	cache    map[string]*cachedIndex
	inflight map[string]*indexFlight
	embedder llm.EmbeddingClient
	semCache *semanticCache
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
		root:     root,
		cfg:      DefaultConfig(),
		now:      func() int64 { return time.Now().UnixMilli() },
		cache:    make(map[string]*cachedIndex),
		semCache: newSemanticCache(),
	}
}

func (s *Service) SetEmbedder(embedder llm.EmbeddingClient) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.embedder = embedder
	s.mu.Unlock()
}

func (s *Service) getEmbedder() llm.EmbeddingClient {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.embedder
}

func DefaultRoot() string {
	if dir := hoxipaths.Join("code-index"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "hoxi-code-index")
}

func (s *Service) Root() string {
	return s.root
}

// StorageBytes returns the total on-disk footprint of cached index files under s.root.
func (s *Service) StorageBytes() int64 {
	if s == nil {
		return 0
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// ClearCache drops in-memory and on-disk cached indexes under s.root.
func (s *Service) ClearCache() int64 {
	s.mu.Lock()
	s.cache = make(map[string]*cachedIndex)
	s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0
	}
	var freed int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		if info, err := entry.Info(); err == nil {
			if os.Remove(path) == nil {
				freed += info.Size()
			}
		}
	}
	return freed
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
	return s.StatusContext(context.Background(), directory)
}

func (s *Service) StatusContext(ctx context.Context, directory string) (StatusInfo, error) {
	if ctx != nil && ctx.Err() != nil {
		return StatusInfo{}, ctx.Err()
	}
	directory, err := canonicalDir(directory)
	if err != nil {
		return StatusInfo{}, err
	}
	c, err := s.ensureIndexContext(ctx, directory, false)
	if err != nil {
		return StatusInfo{}, err
	}
	return statusFromCached(directory, s.root, c), nil
}

func (s *Service) Refresh(directory string) (StatusInfo, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return StatusInfo{}, err
	}
	c, err := s.ensureIndexContext(context.Background(), directory, true)
	if err != nil {
		return StatusInfo{}, err
	}
	return statusFromCached(directory, s.root, c), nil
}

func statusFromCached(directory, root string, c *cachedIndex) StatusInfo {
	return StatusInfo{
		Directory: directory,
		Ready:     true,
		Files:     len(c.index.Files),
		Chunks:    c.chunkCount(),
		IndexedAt: c.index.BuiltAt,
		Root:      root,
	}
}

// Evict drops the in-memory cached index for directory.
func (s *Service) Evict(directory string) {
	if s == nil {
		return
	}
	if clean, err := canonicalDir(directory); err == nil {
		directory = clean
	} else {
		directory = filepath.Clean(strings.TrimSpace(directory))
	}
	s.mu.Lock()
	delete(s.cache, directory)
	s.mu.Unlock()
}

func (s *Service) ensure(directory string) (*cachedIndex, error) {
	return s.ensureIndexContext(context.Background(), directory, false)
}

func (s *Service) ensureIndexContext(ctx context.Context, directory string, forceRebuild bool) (*cachedIndex, error) {
	for {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.mu.Lock()
		now := s.now()
		if !forceRebuild {
			if cached := s.cache[directory]; cached != nil && now-cached.index.BuiltAt < cacheTTL {
				s.clock++
				cached.access = s.clock
				s.trimCacheLocked(directory, now)
				s.mu.Unlock()
				return cached, nil
			}
		}
		if s.inflight == nil {
			s.inflight = make(map[string]*indexFlight)
		}
		if flight := s.inflight[directory]; flight != nil {
			s.mu.Unlock()
			if ctx != nil {
				select {
				case <-flight.done:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			} else {
				<-flight.done
			}
			if !forceRebuild || flight.forceRebuild {
				return flight.cached, flight.err
			}
			continue
		}
		prevCached := s.cache[directory]
		var previous *Index
		if prevCached != nil {
			previous = prevCached.index
		}
		flight := &indexFlight{
			done:         make(chan struct{}),
			forceRebuild: forceRebuild,
		}
		s.inflight[directory] = flight
		s.mu.Unlock()

		var cached *cachedIndex
		var err error
		if previous == nil {
			previous = s.load(directory)
			if !forceRebuild && previous != nil && now-previous.BuiltAt < cacheTTL {
				cached = buildDocs(previous)
			}
		}
		if cached == nil {
			hadPrevious := previous != nil
			if previous == nil {
				previous = &Index{Files: map[string]*FileRecord{}}
			}
			var index *Index
			var changed bool
			index, changed, err = s.build(ctx, directory, previous)
			if err == nil && (forceRebuild || !hadPrevious || changed) {
				err = s.save(index)
			}
			if err == nil {
				if !forceRebuild && !changed && prevCached != nil {
					reused := *prevCached
					reused.index = index
					cached = &reused
				} else {
					cached = buildDocs(index)
				}
			}
		}

		s.mu.Lock()
		delete(s.inflight, directory)
		if err == nil && cached != nil {
			s.clock++
			cached.access = s.clock
			s.cache[directory] = cached
			s.trimCacheLocked(directory, cached.index.BuiltAt)
		}
		flight.cached = cached
		flight.err = err
		close(flight.done)
		s.mu.Unlock()
		return cached, err
	}
}

func (s *Service) trimCacheLocked(keepDir string, now int64) {
	for dir, entry := range s.cache {
		if dir == keepDir {
			continue
		}
		if entry == nil || entry.index == nil || now-entry.index.BuiltAt >= cacheTTL {
			delete(s.cache, dir)
		}
	}
	maxDirs := s.cfg.MaxCachedDirs
	if maxDirs <= 0 {
		maxDirs = 8
	}
	maxBytes := s.cfg.MaxCachedBytes
	if maxBytes <= 0 {
		maxBytes = 12 * 1024 * 1024
	}
	var totalBytes int64
	for _, entry := range s.cache {
		totalBytes += entry.bytes
	}
	for len(s.cache) > maxDirs || (len(s.cache) > 1 && totalBytes > maxBytes) {
		oldestDir := ""
		var oldestAccess uint64
		for dir, entry := range s.cache {
			if dir == keepDir {
				continue
			}
			if oldestDir == "" || entry.access < oldestAccess {
				oldestDir = dir
				oldestAccess = entry.access
			}
		}
		if oldestDir == "" {
			break
		}
		totalBytes -= s.cache[oldestDir].bytes
		delete(s.cache, oldestDir)
	}
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
	return hoxipaths.WriteFileAtomic(s.indexPath(index.Directory), raw, 0o600)
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
