package codeindex

import (
	"math"
	"sort"
	"strings"

	"github.com/hoxiai/hoxi/internal/textindex"
)

const (
	indexVersion = 2
	cacheTTL     = 60 * 1000
)

type Result struct {
	Path      string  `json:"path"`
	Symbol    string  `json:"symbol,omitempty"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
}

type SearchResponse struct {
	Directory string   `json:"directory"`
	Query     string   `json:"query"`
	Results   []Result `json:"results"`
	Files     int      `json:"files"`
	Chunks    int      `json:"chunks"`
	IndexedAt int64    `json:"indexedAt"`
}

type document struct {
	chunk  *Chunk
	tokens map[string]int
	length int
}

type cachedIndex struct {
	index  *Index
	docs   []document
	df     map[string]int
	avgLen float64
}

func (c *cachedIndex) chunkCount() int { return len(c.docs) }

func buildDocs(index *Index) *cachedIndex {
	c := &cachedIndex{index: index, df: make(map[string]int)}
	total := 0
	for _, file := range index.Files {
		for i := range file.Chunks {
			chunk := &file.Chunks[i]
			tokens := textindex.Counts(chunk.Text + " " + chunk.Symbol + " " + chunk.Path)
			if len(tokens) == 0 {
				continue
			}
			length := 0
			for token := range tokens {
				c.df[token]++
				length += tokens[token]
			}
			c.docs = append(c.docs, document{chunk: chunk, tokens: tokens, length: length})
			total += length
		}
	}
	if len(c.docs) > 0 {
		c.avgLen = float64(total) / float64(len(c.docs))
	}
	return c
}

func (s *Service) rank(directory, query string, limit int) (*cachedIndex, []Result) {
	c, err := s.ensure(directory)
	if err != nil {
		return nil, nil
	}
	queryTokens := textindex.Tokenize(query)
	if len(queryTokens) == 0 {
		return c, []Result{}
	}
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		result Result
		score  float64
	}
	var matches []scored
	for _, doc := range c.docs {
		score := textindex.Score(queryTokens, doc.tokens, c.df, len(c.docs), doc.length, c.avgLen)
		if score <= 0 {
			continue
		}
		lowerSymbol := strings.ToLower(doc.chunk.Symbol)
		lowerPath := strings.ToLower(doc.chunk.Path)
		lowerText := strings.ToLower(doc.chunk.Text)
		for _, token := range queryTokens {
			if strings.Contains(lowerSymbol, token) {
				score += 1.5
			}
			if strings.Contains(lowerPath, token) {
				score += 0.8
			}
		}
		if len(lowerQuery) > 3 && strings.Contains(lowerText, lowerQuery) {
			score += 2.5
		}
		matches = append(matches, scored{
			result: Result{
				Path:      doc.chunk.Path,
				Symbol:    doc.chunk.Symbol,
				StartLine: doc.chunk.StartLine,
				EndLine:   doc.chunk.EndLine,
				Score:     math.Round(score*100) / 100,
				Snippet:   textindex.Snippet(doc.chunk.Text, 6, 400),
			},
			score: score,
		})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	results := make([]Result, len(matches))
	for i, match := range matches {
		results[i] = match.result
	}
	return c, results
}
