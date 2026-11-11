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
	maxSemanticCachedVectors     = 8192
	maxPersistedWorkspaceVectors = 8192
	maxSemanticBatchChunks       = 24
	maxSemanticRecallPool        = 48
	semanticTimeout              = 2500 * time.Millisecond
	minSemanticRecallCosine      = 0.28
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

func (sc *semanticCache) reset() {
	if sc == nil {
		return
	}
	sc.mu.Lock()
	sc.order = nil
	sc.vectors = make(map[string][]float32)
	sc.mu.Unlock()
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

func (s *Service) hydrateCachedVectors(c *cachedIndex) map[string][]float32 {
	if c == nil || c.index == nil {
		return nil
	}
	s.mu.Lock()
	if len(c.index.Vectors) == 0 {
		s.mu.Unlock()
		return nil
	}
	snap := make(map[string][]float32, len(c.index.Vectors))
	for k, v := range c.index.Vectors {
		if len(v) > 0 {
			snap[k] = v
		}
	}
	s.mu.Unlock()
	if s.semCache != nil {
		for k, v := range snap {
			s.semCache.put(k, v)
		}
	}
	return snap
}

func (s *Service) persistChunkVectors(c *cachedIndex, modelNS string, added map[string][]float32) {
	if c == nil || c.index == nil || len(added) == 0 {
		return
	}
	s.mu.Lock()
	if modelNS != "" && c.index.VectorModel != "" && c.index.VectorModel != modelNS {
		c.index.Vectors = make(map[string][]float32, len(added))
	}
	if modelNS != "" {
		c.index.VectorModel = modelNS
	}
	if c.index.Vectors == nil {
		c.index.Vectors = make(map[string][]float32, len(added))
	}
	changed := false
	for k, vec := range added {
		if len(vec) == 0 {
			continue
		}
		if _, exists := c.index.Vectors[k]; !exists {
			if len(c.index.Vectors) >= maxPersistedWorkspaceVectors {
				continue
			}
			changed = true
		}
		c.index.Vectors[k] = vec
	}
	var snapshot Index
	if changed {
		snapshot = *c.index
		copiedVecs := make(map[string][]float32, len(c.index.Vectors))
		for k, v := range c.index.Vectors {
			copiedVecs[k] = v
		}
		snapshot.Vectors = copiedVecs
	}
	s.mu.Unlock()

	if changed {
		_ = s.save(&snapshot)
	}
}

// WarmVectorsContext incrementally embeds unindexed workspace chunks under ctx
// and persists their vectors atomically into the workspace index file.
func (s *Service) WarmVectorsContext(ctx context.Context, directory string, maxChunks int) (int, error) {
	embedder := s.getEmbedder()
	if embedder == nil || !embedder.HasEmbeddingModel() {
		return 0, nil
	}
	directory, err := canonicalDir(directory)
	if err != nil {
		return 0, err
	}
	c, err := s.ensureIndexContext(ctx, directory, false)
	if err != nil || c == nil {
		return 0, err
	}
	if maxChunks <= 0 {
		maxChunks = 64
	}
	persisted := s.hydrateCachedVectors(c)
	candidates := selectSemanticCandidateDocs(c, "", nil, len(c.docs))
	var batchTexts []string
	var batchKeys []string
	seenKey := make(map[string]bool)
	warmed := 0

	flushBatch := func() error {
		if len(batchTexts) == 0 {
			return nil
		}
		callCtx := ctx
		var cancel context.CancelFunc
		if callCtx == nil {
			callCtx, cancel = context.WithTimeout(context.Background(), semanticTimeout*2)
			defer cancel()
		}
		resp, embErr := embedder.EmbedTexts(callCtx, llm.EmbeddingRequest{Inputs: batchTexts})
		if embErr != nil || len(resp.Vectors) != len(batchTexts) {
			return embErr
		}
		added := make(map[string][]float32, len(batchKeys))
		for i, k := range batchKeys {
			vec := resp.Vectors[i]
			if s.semCache != nil {
				s.semCache.put(k, vec)
			}
			added[k] = vec
		}
		s.persistChunkVectors(c, resp.ProviderID+"/"+resp.ModelID, added)
		warmed += len(added)
		batchTexts = batchTexts[:0]
		batchKeys = batchKeys[:0]
		return nil
	}

	for _, docIdx := range candidates {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		if warmed+len(batchTexts) >= maxChunks {
			break
		}
		text := formatChunkEmbeddingInput(c.docs[docIdx].chunk)
		key := semanticHashKey("c", text)
		if seenKey[key] {
			continue
		}
		if persisted != nil {
			if _, ok := persisted[key]; ok {
				continue
			}
		}
		if s.semCache != nil {
			if _, ok := s.semCache.get(key); ok {
				continue
			}
		}
		seenKey[key] = true
		batchTexts = append(batchTexts, text)
		batchKeys = append(batchKeys, key)
		if len(batchTexts) >= maxSemanticBatchChunks {
			if err := flushBatch(); err != nil {
				return warmed, err
			}
		}
	}
	if err := flushBatch(); err != nil {
		return warmed, err
	}
	return warmed, nil
}

func (s *Service) embedDocsAndQuery(c *cachedIndex, query string, chunks []*Chunk) ([]float32, [][]float32, bool) {
	embedder := s.getEmbedder()
	if embedder == nil || !embedder.HasEmbeddingModel() || s.semCache == nil {
		return nil, nil, false
	}

	persisted := s.hydrateCachedVectors(c)
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
		if persisted != nil {
			if vec, ok := persisted[cKey]; ok && len(vec) > 0 {
				chunkVecs[i] = vec
				continue
			}
		}
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
		addedChunks := make(map[string][]float32, len(missingKeys))
		for idx, key := range missingKeys {
			vec := resp.Vectors[idx]
			s.semCache.put(key, vec)
			if key == qKey {
				qVec = vec
				continue
			}
			addedChunks[key] = vec
			for _, targetIdx := range missingIdxMap[key] {
				chunkVecs[targetIdx] = vec
			}
		}
		if len(addedChunks) > 0 {
			s.persistChunkVectors(c, ns, addedChunks)
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
		if exclude != nil && exclude[i] {
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

	persisted := s.hydrateCachedVectors(c)
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
		alreadyCached := make(map[int]bool, len(seen))
		for k, v := range seen {
			alreadyCached[k] = v
		}
		for docIdx, doc := range c.docs {
			if alreadyCached[docIdx] {
				continue
			}
			key := semanticHashKey("c", formatChunkEmbeddingInput(doc.chunk))
			if persisted != nil {
				if _, ok := persisted[key]; ok {
					alreadyCached[docIdx] = true
					continue
				}
			}
			if s.semCache != nil {
				if _, ok := s.semCache.get(key); ok {
					alreadyCached[docIdx] = true
				}
			}
		}
		extra := selectSemanticCandidateDocs(c, normPrefix, alreadyCached, budget)
		for _, idx := range extra {
			seen[idx] = true
			evalIdxs = append(evalIdxs, idx)
		}
	}

	chunks := make([]*Chunk, len(evalIdxs))
	for i, docIdx := range evalIdxs {
		chunks[i] = c.docs[docIdx].chunk
	}

	qVec, chunkVecs, ok := s.embedDocsAndQuery(c, query, chunks)
	if !ok {
		return matches
	}

	simByDoc := make(map[int]float64, len(evalIdxs)+len(persisted))
	for i, docIdx := range evalIdxs {
		if len(chunkVecs[i]) > 0 {
			simByDoc[docIdx] = cosineSimilarity(qVec, chunkVecs[i])
		}
	}

	// Score ALL remaining workspace chunks that already have cached/persisted vectors
	// so semantic recall covers the entire indexed repository at zero extra API cost.
	for docIdx, doc := range c.docs {
		if _, done := simByDoc[docIdx]; done {
			continue
		}
		if normPrefix != "" && !strings.HasPrefix(strings.ToLower(doc.chunk.Path), normPrefix) {
			continue
		}
		key := semanticHashKey("c", formatChunkEmbeddingInput(doc.chunk))
		var vec []float32
		if persisted != nil {
			vec = persisted[key]
		}
		if len(vec) == 0 && s.semCache != nil {
			vec, _ = s.semCache.get(key)
		}
		if len(vec) > 0 {
			simByDoc[docIdx] = cosineSimilarity(qVec, vec)
		}
	}

	updated := make([]scoredDocMatch, 0, len(matches)+len(simByDoc))
	existing := make(map[int]bool, len(matches))
	for _, m := range matches {
		existing[m.docIdx] = true
		if sim, has := simByDoc[m.docIdx]; has && sim > 0 {
			m.score += sim * 6.0
			m.result.Score = math.Round(m.score*100) / 100
		}
		updated = append(updated, m)
	}

	for docIdx, sim := range simByDoc {
		if existing[docIdx] || sim < minSemanticRecallCosine {
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
