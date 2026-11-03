package codeindex

import (
	"math"
	"sort"
	"strings"

	"github.com/hoxiai/hoxi/internal/textindex"
)

const (
	indexVersion = 4
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
	length int
}

type cachedIndex struct {
	index    *Index
	docs     []document
	postings map[string][]uint32
	avgLen   float64
	symbols  map[string][]symbolDecl
	bytes    int64
	access   uint64
}

const (
	postingTFBits = 10
	postingTFMask = (1 << postingTFBits) - 1
)

func packPosting(docIdx, tf int) uint32 {
	if tf > postingTFMask {
		tf = postingTFMask
	}
	return (uint32(docIdx) << postingTFBits) | uint32(tf)
}

func unpackPosting(p uint32) (docIdx, tf int) {
	return int(p >> postingTFBits), int(p & postingTFMask)
}

func (c *cachedIndex) chunkCount() int { return len(c.docs) }

func buildDocs(index *Index) *cachedIndex {
	c := &cachedIndex{index: index}
	total := 0
	var approxBytes int64 = 1024
	var docTokens []map[string]int
	df := make(map[string]int)
	totalPostings := 0

	for _, file := range index.Files {
		approxBytes += int64(128 + len(file.Path) + len(file.Hash))
		for _, imp := range file.Imports {
			approxBytes += int64(16 + len(imp))
		}
		if isProtectedOrSecretRelPath(file.Path) {
			continue
		}
		for i := range file.Chunks {
			chunk := &file.Chunks[i]
			approxBytes += int64(64 + len(chunk.Path) + len(chunk.Symbol) + len(chunk.Text))
			if isProtectedOrSecretRelPath(chunk.Path) {
				continue
			}
			tokens := textindex.Counts(chunk.Text + " " + chunk.Symbol + " " + chunk.Path)
			if len(tokens) == 0 {
				continue
			}
			length := 0
			for token, count := range tokens {
				df[token]++
				length += count
				totalPostings++
			}
			c.docs = append(c.docs, document{chunk: chunk, length: length})
			docTokens = append(docTokens, tokens)
			total += length
		}
	}
	if len(c.docs) > 0 {
		c.avgLen = float64(total) / float64(len(c.docs))
	}
	if totalPostings > 0 {
		arena := make([]uint32, totalPostings)
		c.postings = make(map[string][]uint32, len(df))
		cursor := 0
		for token, count := range df {
			key := strings.Clone(token)
			c.postings[key] = arena[cursor : cursor : cursor+count]
			cursor += count
			approxBytes += int64(32 + len(key))
		}
		for docIdx, tokens := range docTokens {
			for token, tf := range tokens {
				c.postings[token] = append(c.postings[token], packPosting(docIdx, tf))
			}
		}
		approxBytes += int64(totalPostings * 4)
	}
	c.symbols = buildWorkspaceSymbolMap(index)
	for sym, decls := range c.symbols {
		approxBytes += int64(32 + len(sym) + len(decls)*64)
	}
	c.bytes = approxBytes
	return c
}

func (s *Service) rank(directory, query string, limit int) (*cachedIndex, []Result) {
	return s.rankWithPath(directory, query, "", limit)
}

func (s *Service) rankWithPath(directory, query, pathPrefix string, limit int) (*cachedIndex, []Result) {
	c, err := s.ensure(directory)
	if err != nil {
		return nil, nil
	}
	queryTokens := textindex.Tokenize(query)
	if len(queryTokens) == 0 || len(c.docs) == 0 {
		return c, []Result{}
	}
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	normPrefix := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(pathPrefix, "\\", "/")), "./"))

	totalDocs := float64(len(c.docs))
	avgLen := math.Max(c.avgLen, 1)
	docScores := make(map[int]float64)
	for _, token := range queryTokens {
		plist := c.postings[token]
		if len(plist) == 0 {
			continue
		}
		df := float64(len(plist))
		idf := math.Log(1 + (totalDocs-df+0.5)/(df+0.5))
		for _, packed := range plist {
			docIdx, tf := unpackPosting(packed)
			doc := c.docs[docIdx]
			norm := 1 - textindex.B + textindex.B*float64(doc.length)/avgLen
			docScores[docIdx] += idf * (float64(tf) * (textindex.K1 + 1)) / (float64(tf) + textindex.K1*norm)
		}
	}
	if len(docScores) == 0 {
		return c, []Result{}
	}

	type scored struct {
		docIdx int
		result Result
		score  float64
	}
	matches := make([]scored, 0, len(docScores))
	for docIdx, score := range docScores {
		if score <= 0 {
			continue
		}
		doc := c.docs[docIdx]
		lowerPath := strings.ToLower(doc.chunk.Path)
		if normPrefix != "" && !strings.HasPrefix(lowerPath, normPrefix) {
			continue
		}
		lowerSymbol := strings.ToLower(doc.chunk.Symbol)
		lowerText := strings.ToLower(doc.chunk.Text)
		if lowerSymbol != "" {
			if lowerSymbol == lowerQuery {
				score += 6.0
			} else if strings.HasPrefix(lowerSymbol, lowerQuery) {
				score += 3.0
			}
		}
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
			docIdx: docIdx,
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
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].docIdx < matches[j].docIdx
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	results := make([]Result, len(matches))
	for i, match := range matches {
		results[i] = match.result
	}
	return c, results
}
