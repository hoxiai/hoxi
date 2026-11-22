package codeindex

import (
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/textindex"
)

const (
	defaultRepoMapLimit      = 16
	maxRepoMapLimit          = 50
	maxRepoMapSymbolsPerFile = 6
	repoMapPageRankDamping   = 0.85
	repoMapPageRankSteps     = 20
)

// RepoMapSymbol describes a high-centrality symbol declaration inside a ranked file.
type RepoMapSymbol struct {
	Symbol     string `json:"symbol"`
	Kind       string `json:"kind,omitempty"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	References int    `json:"references"`
	Signature  string `json:"signature,omitempty"`
}

// RepoMapEntry is one ranked file in the workspace architectural backbone map.
type RepoMapEntry struct {
	Path    string          `json:"path"`
	Score   int             `json:"score"`
	Symbols []RepoMapSymbol `json:"symbols,omitempty"`
}

// RepoMapResponse summarizes the workspace backbone files ordered by dampened
// Personalized PageRank over cross-file symbol references and module imports.
type RepoMapResponse struct {
	Directory  string         `json:"directory"`
	PathPrefix string         `json:"pathPrefix,omitempty"`
	Query      string         `json:"query,omitempty"`
	Files      int            `json:"files"`
	Entries    []RepoMapEntry `json:"entries"`
}

type repoFileSymbol struct {
	symbol    string
	kind      string
	startLine int
	endLine   int
	signature string
}

var declKindKeywords = []string{
	"interface", "struct", "class", "function", "func", "type", "enum", "trait", "impl", "def", "fn", "const", "var", "let", "module", "namespace", "record",
}

func inferDeclarationKind(line string) string {
	cleaned := strings.TrimSpace(stripLineStringsAndComments(line))
	if goMethodRe.MatchString(line) {
		return "method"
	}
	for _, kw := range declKindKeywords {
		if containsIdentifier(cleaned, kw) {
			if kw == "fn" || kw == "def" || kw == "function" {
				return "func"
			}
			return kw
		}
	}
	if classMethodDeclRe.MatchString(line) {
		return "method"
	}
	return "symbol"
}

func compactSymbolSignature(line string) string {
	sig := strings.TrimSpace(line)
	sig = strings.TrimSuffix(sig, "{")
	sig = strings.TrimSpace(sig)
	if len(sig) <= 140 {
		return sig
	}
	cut := 140
	for cut > 0 && (sig[cut]&0xc0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(sig[:cut]) + "..."
}

func collectRepoFileSymbols(rec *FileRecord) []repoFileSymbol {
	if rec == nil || len(rec.Chunks) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []repoFileSymbol
	for _, m := range rec.Methods {
		seen[m.Symbol+":"+strconv.Itoa(m.StartLine)] = struct{}{}
		out = append(out, repoFileSymbol{symbol: m.Symbol, kind: "method", startLine: m.StartLine, endLine: m.EndLine, signature: m.Receiver + "." + m.Symbol})
	}
	for _, ch := range rec.Chunks {
		lines := strings.Split(ch.Text, "\n")
		for i, line := range lines {
			sym := extractLineSymbolCandidate(line)
			if sym == "" || ignoredCallKeywords[sym] {
				continue
			}
			declLine := ch.StartLine + i
			key := sym + ":" + strconv.Itoa(declLine)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			endLine := ch.EndLine
			if relEnd := estimateSymbolEndLine(lines, i); relEnd > i {
				if candEnd := ch.StartLine + relEnd - 1; candEnd > declLine {
					endLine = candEnd
				}
			}
			out = append(out, repoFileSymbol{
				symbol:    sym,
				kind:      inferDeclarationKind(line),
				startLine: declLine,
				endLine:   endLine,
				signature: compactSymbolSignature(line),
			})
		}
	}
	return out
}

func extractChunkIdentCounts(rec *FileRecord, knownLower map[string]struct{}) map[string]int {
	counts := make(map[string]int)
	if rec == nil || len(knownLower) == 0 {
		return counts
	}
	// Deduplicate overlapping chunk lines by 1-based line number.
	maxLineSeen := 0
	for _, ch := range rec.Chunks {
		lines := strings.Split(ch.Text, "\n")
		for idx, rawLine := range lines {
			lineNo := ch.StartLine + idx
			if lineNo <= maxLineSeen {
				continue
			}
			maxLineSeen = lineNo
			cleaned := stripLineStringsAndComments(rawLine)
			for i := 0; i < len(cleaned); {
				b := cleaned[i]
				if !isIdentChar(b) || (b >= '0' && b <= '9') {
					i++
					continue
				}
				j := i + 1
				for j < len(cleaned) && isIdentChar(cleaned[j]) {
					j++
				}
				ident := cleaned[i:j]
				i = j
				if len(ident) < 2 || ignoredCallKeywords[ident] {
					continue
				}
				lower := strings.ToLower(ident)
				if _, ok := knownLower[lower]; ok {
					counts[ident]++
				}
			}
		}
	}
	return counts
}

func resolveRepoMapTargets(cands []symbolDecl, fromPath, ident string, imports []string) []symbolDecl {
	if len(cands) == 0 {
		return nil
	}
	fromDir := filepath.Dir(fromPath)
	bestTier := -1
	var matched []symbolDecl
	for _, cand := range cands {
		if cand.Path == fromPath {
			continue
		}
		if !sameLanguageFamily(fromPath, cand.Path) {
			continue
		}
		tier := 1
		if cand.Symbol == ident {
			tier += 2
		}
		if filepath.Dir(cand.Path) == fromDir {
			tier += 4
		} else if matchesImportHint(cand.Path, imports, "") {
			tier += 3
		}
		if tier > bestTier {
			bestTier = tier
			matched = append(matched[:0], cand)
		} else if tier == bestTier {
			matched = append(matched, cand)
		}
	}
	return matched
}

type repoWeightedEdge struct {
	from   int
	to     int
	weight float64
}

func pagerankRepoFiles(n int, edges []repoWeightedEdge, pers []float64) []float64 {
	if n == 0 {
		return nil
	}
	ranks := make([]float64, n)
	next := make([]float64, n)
	outSum := make([]float64, n)
	for i := range ranks {
		ranks[i] = pers[i]
	}
	for _, e := range edges {
		outSum[e.from] += e.weight
	}
	for step := 0; step < repoMapPageRankSteps; step++ {
		dangling := 0.0
		for i := 0; i < n; i++ {
			if outSum[i] <= 0 {
				dangling += ranks[i]
			}
			next[i] = (1.0 - repoMapPageRankDamping) * pers[i]
		}
		if dangling > 0 {
			scale := repoMapPageRankDamping * dangling
			for i := 0; i < n; i++ {
				next[i] += scale * pers[i]
			}
		}
		for _, e := range edges {
			if outSum[e.from] > 0 {
				next[e.to] += repoMapPageRankDamping * ranks[e.from] * (e.weight / outSum[e.from])
			}
		}
		ranks, next = next, ranks
	}
	return ranks
}

func withinRepoPrefix(relPath, normPrefix string) bool {
	if normPrefix == "" {
		return true
	}
	lower := strings.ToLower(filepath.ToSlash(relPath))
	if lower == normPrefix {
		return true
	}
	return strings.HasPrefix(lower, strings.TrimSuffix(normPrefix, "/")+"/")
}

func validateSafeRepoPrefix(directory, rawPrefix string) (string, error) {
	trimmed := strings.TrimSpace(rawPrefix)
	if trimmed == "" || trimmed == "." {
		return "", nil
	}
	for _, seg := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", errors.New("path escapes workspace directory")
		}
		lowerSeg := strings.ToLower(seg)
		if protectedWorktreeSegments[lowerSeg] || ignoredFolders[seg] || ignoredFolders[lowerSeg] {
			return "", errors.New("refusing to inspect protected or secret directory")
		}
	}
	var full string
	if filepath.IsAbs(trimmed) {
		full = filepath.Clean(trimmed)
	} else {
		full = filepath.Clean(filepath.Join(directory, trimmed))
	}
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		full = resolved
	}
	rel, err := filepath.Rel(directory, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace directory")
	}
	if rel == "." {
		return "", nil
	}
	slash := filepath.ToSlash(rel)
	if secretFile(strings.ToLower(filepath.Base(slash))) {
		return "", errors.New("refusing to inspect protected or secret file")
	}
	return slash, nil
}

// Map computes a Personalized PageRank architectural map of the workspace (or a
// scoped subdirectory/query focus), ranking hub files and their top referenced symbols.
func (s *Service) Map(directory, pathPrefix, query string, limit int) (RepoMapResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return RepoMapResponse{}, err
	}
	cleanPrefix, err := validateSafeRepoPrefix(directory, pathPrefix)
	if err != nil {
		return RepoMapResponse{}, err
	}
	if limit <= 0 {
		limit = defaultRepoMapLimit
	} else if limit > maxRepoMapLimit {
		limit = maxRepoMapLimit
	}
	c, err := s.ensure(directory)
	if err != nil {
		return RepoMapResponse{}, err
	}

	allPaths := c.sortedPaths
	if len(allPaths) == 0 && len(c.index.Files) > 0 {
		allPaths = make([]string, 0, len(c.index.Files))
		for p := range c.index.Files {
			allPaths = append(allPaths, p)
		}
		sort.Strings(allPaths)
	}

	var prodPaths, testPaths []string
	for _, p := range allPaths {
		if isProtectedOrSecretRelPath(p) || isNonCodeCallersFile(p) {
			continue
		}
		if isTestFilePath(p) {
			testPaths = append(testPaths, p)
		} else {
			prodPaths = append(prodPaths, p)
		}
	}
	nodePaths := prodPaths
	if len(nodePaths) == 0 {
		nodePaths = testPaths
	}
	if len(nodePaths) == 0 {
		return RepoMapResponse{
			Directory:  directory,
			PathPrefix: cleanPrefix,
			Query:      strings.TrimSpace(query),
			Files:      len(c.index.Files),
			Entries:    []RepoMapEntry{},
		}, nil
	}

	nodeIdx := make(map[string]int, len(nodePaths))
	fileDecls := make([][]repoFileSymbol, len(nodePaths))
	for i, p := range nodePaths {
		nodeIdx[p] = i
		fileDecls[i] = collectRepoFileSymbols(c.index.Files[p])
	}

	symDir := c.symbols
	if len(symDir) == 0 {
		symDir = buildWorkspaceSymbolMap(c.index)
	}
	knownLower := make(map[string]struct{}, len(symDir))
	for lower := range symDir {
		knownLower[lower] = struct{}{}
	}

	type pairKey struct{ from, to int }
	type symRefKey struct {
		to     int
		symbol string
	}
	edgeWeights := make(map[pairKey]float64)
	inboundRefs := make(map[symRefKey]float64)

	// Index directory -> node indices for fast import edge resolution.
	byDir := make(map[string][]int)
	for i, p := range nodePaths {
		dirBase := strings.ToLower(filepath.Base(filepath.Dir(p)))
		byDir[dirBase] = append(byDir[dirBase], i)
	}

	for from, fromPath := range nodePaths {
		rec := c.index.Files[fromPath]
		if rec == nil {
			continue
		}
		for _, imp := range rec.Imports {
			normImp := strings.ToLower(strings.Trim(strings.ReplaceAll(imp, "::", "/"), "./"))
			if normImp == "" {
				continue
			}
			impBase := filepath.Base(normImp)
			var impTargets []int
			for _, candIdx := range byDir[impBase] {
				candPath := nodePaths[candIdx]
				if candIdx != from && sameLanguageFamily(fromPath, candPath) && matchesImportHint(candPath, []string{imp}, "") {
					impTargets = append(impTargets, candIdx)
				}
			}
			if len(impTargets) > 0 {
				w := 0.6 / float64(len(impTargets))
				for _, to := range impTargets {
					edgeWeights[pairKey{from: from, to: to}] += w
				}
			}
		}

		identCounts := extractChunkIdentCounts(rec, knownLower)
		for ident, count := range identCounts {
			cands := symDir[strings.ToLower(ident)]
			targets := resolveRepoMapTargets(cands, fromPath, ident, rec.Imports)
			if len(targets) == 0 {
				continue
			}
			// Dampened like Aider: repeated mentions of one helper in a single file do not dominate.
			w := math.Sqrt(float64(count)) / float64(len(targets))
			for _, decl := range targets {
				if to, ok := nodeIdx[decl.Path]; ok && to != from {
					edgeWeights[pairKey{from: from, to: to}] += w
					inboundRefs[symRefKey{to: to, symbol: decl.Symbol}] += w
				}
			}
		}
	}

	edges := make([]repoWeightedEdge, 0, len(edgeWeights))
	for k, w := range edgeWeights {
		edges = append(edges, repoWeightedEdge{from: k.from, to: k.to, weight: w})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		return edges[i].to < edges[j].to
	})

	normPrefix := strings.ToLower(cleanPrefix)
	trimmedQuery := strings.TrimSpace(query)
	lowerQuery := strings.ToLower(trimmedQuery)
	queryTokens := textindex.Tokenize(trimmedQuery)

	pers := make([]float64, len(nodePaths))
	persSum := 0.0
	for i, p := range nodePaths {
		w := 1.0
		lowerPath := strings.ToLower(p)
		if normPrefix != "" && withinRepoPrefix(p, normPrefix) {
			w += 15.0
		}
		if len(queryTokens) > 0 {
			for _, tok := range queryTokens {
				if strings.Contains(lowerPath, tok) {
					w += 6.0
				}
				for _, sym := range fileDecls[i] {
					ls := strings.ToLower(sym.symbol)
					if ls == lowerQuery || ls == tok {
						w += 20.0
					} else if strings.Contains(ls, tok) {
						w += 8.0
					}
				}
			}
		}
		pers[i] = w
		persSum += w
	}
	for i := range pers {
		pers[i] /= persSum
	}

	ranks := pagerankRepoFiles(len(nodePaths), edges, pers)
	var candidateIndices []int
	for i, p := range nodePaths {
		if normPrefix != "" && !withinRepoPrefix(p, normPrefix) {
			continue
		}
		candidateIndices = append(candidateIndices, i)
	}
	sort.SliceStable(candidateIndices, func(a, b int) bool {
		ia, ib := candidateIndices[a], candidateIndices[b]
		if math.Abs(ranks[ia]-ranks[ib]) > 1e-12 {
			return ranks[ia] > ranks[ib]
		}
		if len(fileDecls[ia]) != len(fileDecls[ib]) {
			return len(fileDecls[ia]) > len(fileDecls[ib])
		}
		return nodePaths[ia] < nodePaths[ib]
	})

	topRank := 0.0
	if len(candidateIndices) > 0 {
		topRank = ranks[candidateIndices[0]]
	}
	if len(candidateIndices) > limit {
		candidateIndices = candidateIndices[:limit]
	}

	entries := make([]RepoMapEntry, 0, len(candidateIndices))
	for _, idx := range candidateIndices {
		score := 0
		if topRank > 0 {
			score = int(math.Round(ranks[idx] / topRank * 1000))
		}
		decls := fileDecls[idx]
		type scoredSym struct {
			decl   repoFileSymbol
			weight float64
			order  int
		}
		scored := make([]scoredSym, len(decls))
		for k, d := range decls {
			scored[k] = scoredSym{
				decl:   d,
				weight: inboundRefs[symRefKey{to: idx, symbol: d.symbol}],
				order:  k,
			}
		}
		sort.SliceStable(scored, func(a, b int) bool {
			if math.Abs(scored[a].weight-scored[b].weight) > 1e-9 {
				return scored[a].weight > scored[b].weight
			}
			return scored[a].order < scored[b].order
		})
		maxSyms := minInt(len(scored), maxRepoMapSymbolsPerFile)
		syms := make([]RepoMapSymbol, 0, maxSyms)
		for k := 0; k < maxSyms; k++ {
			d := scored[k].decl
			syms = append(syms, RepoMapSymbol{
				Symbol:     d.symbol,
				Kind:       d.kind,
				StartLine:  d.startLine,
				EndLine:    d.endLine,
				References: int(math.Round(scored[k].weight)),
				Signature:  d.signature,
			})
		}
		entries = append(entries, RepoMapEntry{
			Path:    nodePaths[idx],
			Score:   score,
			Symbols: syms,
		})
	}

	return RepoMapResponse{
		Directory:  directory,
		PathPrefix: cleanPrefix,
		Query:      trimmedQuery,
		Files:      len(nodePaths),
		Entries:    entries,
	}, nil
}

func (s *Service) handleMap(w http.ResponseWriter, r *http.Request) {
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
	pathPrefix := q.Get("path")
	if pathPrefix == "" {
		pathPrefix = q.Get("pathPrefix")
	}
	query := q.Get("q")
	if query == "" {
		query = q.Get("query")
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	res, err := s.Map(directory, pathPrefix, query, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
