// Package recall searches historical session transcripts so the composer can
// pull prior decisions and tool failures into a new turn.
package recall

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/textindex"
)

const (
	cacheTTL            = 30 * 1000
	maxCachedRecallDirs = 16
)

type Turn struct {
	Role    string
	Text    string
	Created int64
}

type Session struct {
	ID        string
	Title     string
	Directory string
	Runtime   string
	UpdatedAt int64
	Turns     []Turn
}

type sessionMeta struct {
	ID        string
	Title     string
	Directory string
	Runtime   string
	UpdatedAt int64
}

type Source interface {
	Sessions(ctx context.Context, directory string) ([]Session, error)
}

type Result struct {
	SessionID string  `json:"sessionID"`
	Title     string  `json:"title"`
	Runtime   string  `json:"runtime,omitempty"`
	Role      string  `json:"role,omitempty"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	UpdatedAt int64   `json:"updatedAt"`
	CreatedAt int64   `json:"createdAt,omitempty"`
}

type Response struct {
	Directory string   `json:"directory"`
	Query     string   `json:"query"`
	Results   []Result `json:"results"`
	Sessions  int      `json:"sessions"`
	IndexedAt int64    `json:"indexedAt"`
}

type document struct {
	session sessionMeta
	turn    *Turn
	tokens  map[string]int
	length  int
}

type cached struct {
	docs      []document
	df        map[string]int
	avgLen    float64
	sessions  int
	indexedAt int64
	access    uint64
}

type recallFlight struct {
	done   chan struct{}
	cached *cached
	err    error
}

type Service struct {
	source   Source
	mu       sync.Mutex
	clock    uint64
	cache    map[string]*cached
	inflight map[string]*recallFlight
}

func NewService(source Source) *Service {
	return &Service{
		source:   source,
		cache:    make(map[string]*cached),
		inflight: make(map[string]*recallFlight),
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/recall/search", s.handleSearch)
}

func (s *Service) Search(ctx context.Context, directory, query string, limit int) (Response, error) {
	if s.source == nil {
		return Response{}, errors.New("session recall is unavailable")
	}
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return Response{}, errors.New("directory is required")
	}
	c, err := s.index(ctx, directory)
	if err != nil {
		return Response{}, err
	}
	queryTokens := textindex.Tokenize(query)
	if len(queryTokens) == 0 {
		return Response{Directory: directory, Query: query, Results: []Result{}, Sessions: c.sessions, IndexedAt: c.indexedAt}, nil
	}
	if limit <= 0 || limit > 30 {
		limit = 8
	}
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	best := make(map[string]Result)
	for _, doc := range c.docs {
		score := textindex.Score(queryTokens, doc.tokens, c.df, len(c.docs), doc.length, c.avgLen)
		if score <= 0 {
			continue
		}
		lowerTitle := strings.ToLower(doc.session.Title)
		for _, token := range queryTokens {
			if strings.Contains(lowerTitle, token) {
				score += 1
			}
		}
		turnText := doc.session.Title
		role, created := "", int64(0)
		if doc.turn != nil {
			turnText = doc.turn.Text
			role, created = doc.turn.Role, doc.turn.Created
		}
		if len(lowerQuery) > 3 && strings.Contains(strings.ToLower(turnText), lowerQuery) {
			score += 1.5
		}
		current, exists := best[doc.session.ID]
		if exists && current.Score >= score {
			continue
		}
		best[doc.session.ID] = Result{
			SessionID: doc.session.ID,
			Title:     doc.session.Title,
			Runtime:   doc.session.Runtime,
			Role:      role,
			Snippet:   textindex.Snippet(turnText, 5, 360),
			Score:     math.Round(score*100) / 100,
			UpdatedAt: doc.session.UpdatedAt,
			CreatedAt: created,
		}
	}
	results := make([]Result, 0, len(best))
	for _, result := range best {
		results = append(results, result)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > limit {
		results = results[:limit]
	}
	return Response{Directory: directory, Query: query, Results: results, Sessions: c.sessions, IndexedAt: c.indexedAt}, nil
}

func (s *Service) index(ctx context.Context, directory string) (*cached, error) {
	now := time.Now().UnixMilli()
	s.mu.Lock()
	if c := s.cache[directory]; c != nil && now-c.indexedAt < cacheTTL {
		s.clock++
		c.access = s.clock
		s.mu.Unlock()
		return c, nil
	}
	if s.inflight == nil {
		s.inflight = make(map[string]*recallFlight)
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
		return flight.cached, flight.err
	}
	flight := &recallFlight{done: make(chan struct{})}
	s.inflight[directory] = flight
	s.mu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	sessions, err := s.source.Sessions(reqCtx, directory)
	var result *cached
	if err == nil {
		builtAt := time.Now().UnixMilli()
		result = &cached{df: make(map[string]int), sessions: len(sessions), indexedAt: builtAt}
		total := 0
		for _, session := range sessions {
			meta := sessionMeta{
				ID:        session.ID,
				Title:     session.Title,
				Directory: session.Directory,
				Runtime:   session.Runtime,
				UpdatedAt: session.UpdatedAt,
			}
			if len(session.Turns) == 0 {
				tokens := textindex.Counts(session.Title)
				if len(tokens) > 0 {
					result.docs = append(result.docs, document{session: meta, tokens: tokens, length: tokenLength(tokens)})
				}
			}
			for i := range session.Turns {
				turn := session.Turns[i]
				if strings.TrimSpace(turn.Text) == "" {
					continue
				}
				tokens := textindex.Counts(turn.Text + " " + session.Title)
				if len(tokens) == 0 {
					continue
				}
				length := tokenLength(tokens)
				for token := range tokens {
					result.df[token]++
				}
				turnCopy := turn
				result.docs = append(result.docs, document{session: meta, turn: &turnCopy, tokens: tokens, length: length})
				total += length
			}
		}
		if len(result.docs) > 0 {
			result.avgLen = float64(total) / float64(len(result.docs))
		}
	}

	s.mu.Lock()
	delete(s.inflight, directory)
	if err == nil && result != nil {
		s.clock++
		result.access = s.clock
		s.cache[directory] = result
		s.trimCacheLocked(directory, result.indexedAt)
	}
	flight.cached = result
	flight.err = err
	close(flight.done)
	s.mu.Unlock()
	return result, err
}

func (s *Service) trimCacheLocked(keepDir string, now int64) {
	for dir, entry := range s.cache {
		if dir == keepDir {
			continue
		}
		if entry == nil || now-entry.indexedAt >= cacheTTL {
			delete(s.cache, dir)
		}
	}
	for len(s.cache) > maxCachedRecallDirs {
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
		delete(s.cache, oldestDir)
	}
}

func tokenLength(tokens map[string]int) int {
	length := 0
	for _, count := range tokens {
		length += count
	}
	return length
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	response, err := s.Search(r.Context(), r.URL.Query().Get("directory"), r.URL.Query().Get("q"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
