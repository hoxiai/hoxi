package codeindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/hoxiai/hoxi/internal/llm"
	"github.com/hoxiai/hoxi/internal/textindex"
)

const (
	maxSemanticCachedVectors = 2048
	maxSemanticBatchChunks   = 24
	maxSemanticRecallPool    = 48
	semanticTimeout          = 2500 * time.Millisecond
	minSemanticRecallCosine  = 0.28
)

type semanticCache struct {
	mu      sync.Mutex
	order   []string
	vectors map[string][]float32
}

func newSemanticCache() *semanticCache {
	return &semanticCache{
		vectors: make(map[string][]float32),
	}
}

func (sc *semanticCache) get(key string) ([]float32, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	vec, ok := sc.vectors[key]
	return vec, ok
}

func (sc *semanticCache) put(key string, vec []float32) {
	if len(vec) == 0 {
		return
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if _, exists := sc.vectors[key]; !exists {
		sc.order = append(sc.order, key)
	}
	sc.vectors[key] = vec
	for len(sc.order) > maxSemanticCachedVectors {
		evict := sc.order[0]
		sc.order = sc.order[1:]
		delete(sc.vectors, evict)
	}
}

func semanticHashKey(prefix, text string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + text))
	return hex.EncodeToString(sum[:16])
}

func formatChunkEmbeddingInput(ch *Chunk) string {
	var b strings.Builder
	b.WriteString("file: ")
	b.WriteString(ch.Path)
	if strings.TrimSpace(ch.Symbol) != "" {
		b.WriteString("\nsymbol: ")
		b.WriteString(ch.Symbol)
	}
	b.WriteString("\n")
	snippet := strings.TrimSpace(ch.Text)
	if len(snippet) > 1200 {
		snippet = strings.ToValidUTF8(snippet[:1200], "")
	}
	b.WriteString(snippet)
	return b.String()
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		va := float64(a[i])
		vb := float64(b[i])
		dot += va * vb
		normA += va * va
		normB += vb * vb
	}
	if normA <= 0 || normB <= 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func shouldApplySemanticRerank(query string, lexicalCount int) bool {
	trimmed := strings.TrimSpace(query)
	if len(trimmed) < 3 {
		return false
	}
	if lexicalCount < 3 {
		return true
	}
	for _, r := range trimmed {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return strings.Contains(trimmed, " ")
}

func (s *Service) embedDocsAndQuery(query string, chunks []*Chunk) ([]float32, [][]float32, bool) {
	embedder := s.getEmbedder()
	if embedder == nil || !embedder.HasEmbeddingModel() || len(chunks) == 0 {
		return nil, nil, false
	}
	if s.semCache == nil {
		return nil, nil, false
	}

	qKey := semanticHashKey("q", strings.TrimSpace(query))
	var missingInputs []string
	var missingKeys []string
	missingIdxMap := make(map[string][]int)

	qVec, hasQ := s.semCache.get(qKey)
	if !hasQ {
		missingInputs = append(missingInputs, strings.TrimSpace(query))
		missingKeys = append(missingKeys, qKey)
	}

	chunkVecs := make([][]float32, len(chunks))
	for i, ch := range chunks {
		text := formatChunkEmbeddingInput(ch)
		cKey := semanticHashKey("c", text)
		if vec, ok := s.semCache.get(cKey); ok {
			chunkVecs[i] = vec
			continue
		}
		if _, queued := missingIdxMap[cKey]; !queued {
			missingInputs = append(missingInputs, text)
			missingKeys = append(missingKeys, cKey)
		}
		missingIdxMap[cKey] = append(missingIdxMap[cKey], i)
	}

	if len(missingInputs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), semanticTimeout)
		resp, err := embedder.EmbedTexts(ctx, llm.EmbeddingRequest{Inputs: missingInputs})
		cancel()
		if err != nil || len(resp.Vectors) != len(missingInputs) {
			return nil, nil, false
		}
		ns := resp.ProviderID + "/" + resp.ModelID
		_ = ns
		for idx, key := range missingKeys {
			vec := resp.Vectors[idx]
			s.semCache.put(key, vec)
			if key == qKey {
				qVec = vec
			}
			for _, targetIdx := range missingIdxMap[key] {
				chunkVecs[targetIdx] = vec
			}
		}
	}
	if len(qVec) == 0 {
		return nil, nil, false
	}
	return qVec, chunkVecs, true
}

func selectSemanticCandidateDocs(c *cachedIndex, normPrefix string, exclude map[int]bool, maxCount int) []int {
	if c == nil || len(c.docs) == 0 || maxCount <= 0 {
		return nil
	}
	type candidate struct {
		docIdx   int
		priority int
	}
	pool := make([]candidate, 0, len(c.docs))
	for i, doc := range c.docs {
		if exclude[i] {
			continue
		}
		if normPrefix != "" && !strings.HasPrefix(strings.ToLower(doc.chunk.Path), normPrefix) {
			continue
		}
		prio := 0
		if strings.TrimSpace(doc.chunk.Symbol) != "" {
			prio += 2
		}
		if doc.chunk.StartLine <= 80 {
			prio++
		}
		pool = append(pool, candidate{docIdx: i, priority: prio})
	}
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].priority != pool[j].priority {
			return pool[i].priority > pool[j].priority
		}
		return pool[i].docIdx < pool[j].docIdx
	})
	if len(pool) <= maxCount {
		out := make([]int, len(pool))
		for i, it := range pool {
			out[i] = it.docIdx
		}
		return out
	}
	out := make([]int, 0, maxCount)
	step := float64(len(pool)) / float64(maxCount)
	for i := 0; i < maxCount; i++ {
		idx := int(float64(i) * step)
		if idx >= len(pool) {
			idx = len(pool) - 1
		}
		out = append(out, pool[idx].docIdx)
	}
	return out
}

func (s *Service) applySemanticHybridSearch(
	c *cachedIndex,
	query string,
	normPrefix string,
	matches []scoredDocMatch,
	limit int,
) []scoredDocMatch {
	embedder := s.getEmbedder()
	if embedder == nil || !embedder.HasEmbeddingModel() || !shouldApplySemanticRerank(query, len(matches)) {
		return matches
	}

	topN := len(matches)
	if topN > maxSemanticBatchChunks {
		topN = maxSemanticBatchChunks
	}

	seen := make(map[int]bool, topN)
	var evalIdxs []int
	for i := 0; i < topN; i++ {
		seen[matches[i].docIdx] = true
		evalIdxs = append(evalIdxs, matches[i].docIdx)
	}

	if len(matches) < 3 {
		budget := maxSemanticRecallPool - len(evalIdxs)
		if budget > maxSemanticBatchChunks {
			budget = maxSemanticBatchChunks
		}
		extra := selectSemanticCandidateDocs(c, normPrefix, seen, budget)
		evalIdxs = append(evalIdxs, extra...)
	}
	if len(evalIdxs) == 0 {
		return matches
	}

	chunks := make([]*Chunk, len(evalIdxs))
	for i, docIdx := range evalIdxs {
		chunks[i] = c.docs[docIdx].chunk
	}

	qVec, chunkVecs, ok := s.embedDocsAndQuery(query, chunks)
	if !ok {
		return matches
	}

	simByDoc := make(map[int]float64, len(evalIdxs))
	for i, docIdx := range evalIdxs {
		if len(chunkVecs[i]) > 0 {
			simByDoc[docIdx] = cosineSimilarity(qVec, chunkVecs[i])
		}
	}

	updated := make([]scoredDocMatch, 0, len(matches)+len(evalIdxs))
	existing := make(map[int]bool, len(matches))
	for _, m := range matches {
		existing[m.docIdx] = true
		if sim, has := simByDoc[m.docIdx]; has && sim > 0 {
			m.score += sim * 6.0
			m.result.Score = math.Round(m.score*100) / 100
		}
		updated = append(updated, m)
	}

	for _, docIdx := range evalIdxs {
		if existing[docIdx] {
			continue
		}
		sim := simByDoc[docIdx]
		if sim < minSemanticRecallCosine {
			continue
		}
		doc := c.docs[docIdx]
		semScore := sim * 8.0
		updated = append(updated, scoredDocMatch{
			docIdx: docIdx,
			score:  semScore,
			result: Result{
				Path:      doc.chunk.Path,
				Symbol:    doc.chunk.Symbol,
				StartLine: doc.chunk.StartLine,
				EndLine:   doc.chunk.EndLine,
				Score:     math.Round(semScore*100) / 100,
				Snippet:   textindex.Snippet(doc.chunk.Text, 6, 400),
			},
		})
	}

	sort.SliceStable(updated, func(i, j int) bool {
		if updated[i].score != updated[j].score {
			return updated[i].score > updated[j].score
		}
		return updated[i].docIdx < updated[j].docIdx
	})
	if limit > 0 && len(updated) > limit {
		updated = updated[:limit]
	}
	return updated
}
