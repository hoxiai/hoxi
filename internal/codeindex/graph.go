package codeindex

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type CalleeHit struct {
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	CallLine  int    `json:"callLine"`
	Snippet   string `json:"snippet,omitempty"`
}

type CalleesResponse struct {
	Directory string      `json:"directory"`
	Path      string      `json:"path"`
	Symbol    string      `json:"symbol"`
	Imports   []string    `json:"imports,omitempty"`
	Callees   []CalleeHit `json:"callees"`
}

type ImpactHit struct {
	Path    string `json:"path"`
	Symbol  string `json:"symbol,omitempty"`
	Line    int    `json:"line"`
	Depth   int    `json:"depth"`
	Via     string `json:"via"`
	Snippet string `json:"snippet"`
}

type ImpactResponse struct {
	Directory         string      `json:"directory"`
	Path              string      `json:"path,omitempty"`
	Symbol            string      `json:"symbol"`
	DirectCallers     []CallerHit `json:"directCallers"`
	TransitiveCallers []ImpactHit `json:"transitiveCallers,omitempty"`
	Tests             []CallerHit `json:"tests"`
	AffectedFiles     []string    `json:"affectedFiles"`
}

type ContextResponse struct {
	Directory         string          `json:"directory"`
	Inspect           InspectResponse `json:"inspect"`
	Imports           []string        `json:"imports,omitempty"`
	Callees           []CalleeHit     `json:"callees"`
	Callers           []CallerHit     `json:"callers"`
	TransitiveCallers []ImpactHit     `json:"transitiveCallers,omitempty"`
	Tests             []CallerHit     `json:"tests"`
	AffectedFiles     []string        `json:"affectedFiles,omitempty"`
}

var (
	quotedImportRe = regexp.MustCompile(`["']([^"'\s]+)["']`)
	pyFromImportRe = regexp.MustCompile(`^\s*from\s+([A-Za-z0-9_.]+)\s+import\b`)
	pyImportRe     = regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.]+)`)
	rustUseRe      = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([A-Za-z0-9_:]+)`)
	callExprRe     = regexp.MustCompile(`(?:([A-Za-z_$][\w$]*)\s*\.\s*)?([A-Za-z_$][\w$]*)\s*\(`)
)

var ignoredCallKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "func": true, "function": true, "def": true, "class": true,
	"else": true, "select": true, "case": true, "default": true, "go": true,
	"defer": true, "make": true, "new": true, "len": true, "cap": true,
	"append": true, "copy": true, "delete": true, "close": true, "panic": true,
	"recover": true, "print": true, "println": true, "complex": true, "real": true,
	"imag": true, "clear": true, "min": true, "max": true, "super": true,
	"this": true, "self": true, "typeof": true, "sizeof": true, "alignof": true,
	"offsetof": true, "await": true, "yield": true, "throw": true, "raise": true,
	"assert": true, "require": true, "import": true, "export": true, "from": true,
	"where": true, "match": true, "loop": true, "pub": true, "impl": true,
	"fn": true, "let": true, "const": true, "var": true, "type": true,
	"struct": true, "interface": true, "enum": true, "trait": true,
	"String": true, "Number": true, "Boolean": true, "Object": true, "Array": true,
	"Map": true, "Set": true, "Promise": true, "Error": true, "RegExp": true,
	"int": true, "int64": true, "int32": true, "uint": true, "uint64": true,
	"uint32": true, "float64": true, "float32": true, "byte": true, "rune": true,
	"string": true, "bool": true, "error": true, "any": true,
}

func extractFileImports(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	maxLines := len(lines)
	if maxLines > 160 {
		maxLines = 160
	}
	var imports []string
	seen := make(map[string]struct{})
	addImport := func(imp string) {
		imp = strings.TrimSpace(imp)
		if imp == "" || len(imports) >= 32 {
			return
		}
		if _, ok := seen[imp]; ok {
			return
		}
		seen[imp] = struct{}{}
		imports = append(imports, imp)
	}

	inGoImportBlock := false
	for i := 0; i < maxLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if inGoImportBlock {
			if strings.HasPrefix(trimmed, ")") {
				inGoImportBlock = false
				continue
			}
			if m := quotedImportRe.FindStringSubmatch(trimmed); len(m) == 2 {
				addImport(m[1])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "import (") || trimmed == "import(" {
			inGoImportBlock = true
			continue
		}
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "export ") || strings.Contains(trimmed, "require(") {
			if matches := quotedImportRe.FindAllStringSubmatch(trimmed, -1); len(matches) > 0 {
				for _, m := range matches {
					if len(m) == 2 {
						addImport(m[1])
					}
				}
				continue
			}
		}
		if m := pyFromImportRe.FindStringSubmatch(trimmed); len(m) == 2 {
			addImport(m[1])
			continue
		}
		if m := pyImportRe.FindStringSubmatch(trimmed); len(m) == 2 {
			addImport(m[1])
			continue
		}
		if m := rustUseRe.FindStringSubmatch(trimmed); len(m) == 2 {
			addImport(m[1])
			continue
		}
	}
	return imports
}

type symbolDecl struct {
	Path      string
	Symbol    string
	StartLine int
	EndLine   int
}

func buildWorkspaceSymbolMap(index *Index) map[string][]symbolDecl {
	byLower := make(map[string][]symbolDecl)
	if index == nil || len(index.Files) == 0 {
		return byLower
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0, len(index.Files))
	for p := range index.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, relPath := range paths {
		if isProtectedOrSecretRelPath(relPath) || isTestFilePath(relPath) || isNonCodeCallersFile(relPath) {
			continue
		}
		rec := index.Files[relPath]
		for _, ch := range rec.Chunks {
			lines := strings.Split(ch.Text, "\n")
			for i, line := range lines {
				sym := extractLineSymbolCandidate(line)
				if sym == "" || ignoredCallKeywords[sym] {
					continue
				}
				declLine := ch.StartLine + i
				key := fmt.Sprintf("%s:%s:%d", relPath, sym, declLine)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				endLine := ch.EndLine
				if relIdx := estimateSymbolEndLine(lines, i); relIdx > i {
					candEnd := ch.StartLine + relIdx - 1
					if candEnd > declLine {
						endLine = candEnd
					}
				}
				lower := strings.ToLower(sym)
				byLower[lower] = append(byLower[lower], symbolDecl{
					Path:      relPath,
					Symbol:    sym,
					StartLine: declLine,
					EndLine:   endLine,
				})
			}
		}
	}
	return byLower
}

func stripLineStringsAndComments(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	var quote byte
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quote != 0 {
			if ch == '\\' && quote != '`' {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '/' && i+1 < len(line) {
			if line[i+1] == '/' {
				break
			}
			if line[i+1] == '*' {
				end := strings.Index(line[i+2:], "*/")
				if end < 0 {
					break
				}
				i += 3 + end
				continue
			}
		}
		if ch == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			break
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func matchesImportHint(candPath string, imports []string, receiver string) bool {
	lowerCand := strings.ToLower(filepath.ToSlash(candPath))
	candDir := filepath.Base(filepath.Dir(lowerCand))
	candStem := strings.TrimSuffix(filepath.Base(lowerCand), filepath.Ext(lowerCand))
	if receiver != "" {
		lowerRecv := strings.ToLower(receiver)
		if lowerRecv == candDir || lowerRecv == candStem {
			return true
		}
	}
	for _, imp := range imports {
		normImp := strings.ToLower(strings.Trim(strings.ReplaceAll(imp, "::", "/"), "./"))
		if normImp == "" {
			continue
		}
		impBase := filepath.Base(normImp)
		if strings.HasSuffix(filepath.Dir(lowerCand), normImp) || candDir == impBase || candStem == impBase {
			return true
		}
	}
	return false
}

func pickBestSymbolDecl(cands []symbolDecl, callerPath, calledIdent, receiver string, imports []string) (symbolDecl, bool) {
	if len(cands) == 0 {
		return symbolDecl{}, false
	}
	callerDir := filepath.Dir(callerPath)
	bestIdx := -1
	bestScore := -1
	for i, cand := range cands {
		score := 0
		if cand.Symbol == calledIdent {
			score += 4
		}
		if cand.Path == callerPath {
			score += 8
		} else if filepath.Dir(cand.Path) == callerDir {
			score += 6
		}
		if matchesImportHint(cand.Path, imports, receiver) {
			score += 5
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return symbolDecl{}, false
	}
	return cands[bestIdx], true
}

func (s *Service) locateSymbolSpan(directory string, c *cachedIndex, relPath, symbol string) (string, []string, int, int, string, error) {
	relPath = strings.TrimSpace(relPath)
	symbol = strings.TrimSpace(symbol)
	if relPath == "" && symbol == "" {
		return "", nil, 0, 0, "", errors.New("path or symbol is required")
	}
	if relPath == "" {
		foundPath, _, _, resolvedSym, ok := s.locateSymbolInIndex(directory, c, symbol)
		if !ok {
			return "", nil, 0, 0, "", fmt.Errorf("symbol %q not found in index", symbol)
		}
		relPath = foundPath
		if resolvedSym != "" {
			symbol = resolvedSym
		}
	}
	cleanRel, fullPath, err := resolveSafeWorktreeFile(directory, relPath)
	if err != nil {
		return "", nil, 0, 0, "", err
	}
	if isProtectedOrSecretRelPath(cleanRel) {
		return "", nil, 0, 0, "", errors.New("refusing to inspect protected or secret file")
	}
	st, err := os.Stat(fullPath)
	if err != nil || st.IsDir() {
		return "", nil, 0, 0, "", errors.New("file does not exist")
	}
	if st.Size() > s.cfg.MaxFileBytes {
		return "", nil, 0, 0, "", errors.New("file exceeds maximum indexable size")
	}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return "", nil, 0, 0, "", err
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", nil, 0, 0, "", errors.New("binary file cannot be inspected")
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if symbol == "" {
		for _, line := range lines {
			if cand := extractLineSymbolCandidate(line); cand != "" {
				symbol = cand
				break
			}
		}
	}
	if symbol == "" {
		return cleanRel, lines, 1, minInt(len(lines), 40), "", nil
	}
	symStart, symEnd, matchedSym, ok := findSymbolInLines(lines, symbol)
	if !ok {
		return "", nil, 0, 0, "", fmt.Errorf("symbol %q not found in %s", symbol, cleanRel)
	}
	return cleanRel, lines, symStart, symEnd, matchedSym, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Service) Callees(directory, symbol, relPath string, limit int) (CalleesResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return CalleesResponse{}, err
	}
	if limit <= 0 || limit > 40 {
		limit = 16
	}
	c, err := s.ensure(directory)
	if err != nil {
		return CalleesResponse{}, err
	}
	cleanRel, lines, symStart, symEnd, resolvedSym, err := s.locateSymbolSpan(directory, c, relPath, symbol)
	if err != nil {
		return CalleesResponse{}, err
	}
	var imports []string
	if rec := c.index.Files[cleanRel]; rec != nil && len(rec.Imports) > 0 {
		imports = rec.Imports
	} else {
		imports = extractFileImports(strings.Join(lines, "\n"))
	}

	symDir := c.symbols
	if len(symDir) == 0 {
		symDir = buildWorkspaceSymbolMap(c.index)
	}
	callees := []CalleeHit{}
	seenTarget := make(map[string]struct{})

	for lineNo := symStart; lineNo <= symEnd && lineNo <= len(lines); lineNo++ {
		rawLine := lines[lineNo-1]
		cleaned := stripLineStringsAndComments(rawLine)
		if strings.TrimSpace(cleaned) == "" {
			continue
		}
		matches := callExprRe.FindAllStringSubmatch(cleaned, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			receiver := m[1]
			ident := m[2]
			if ident == "" || ignoredCallKeywords[ident] {
				continue
			}
			if lineNo == symStart && strings.EqualFold(ident, resolvedSym) {
				continue
			}
			if strings.EqualFold(ident, resolvedSym) && receiver == "" {
				continue
			}
			cands := symDir[strings.ToLower(ident)]
			decl, ok := pickBestSymbolDecl(cands, cleanRel, ident, receiver, imports)
			if !ok {
				continue
			}
			if decl.Path == cleanRel && decl.StartLine == symStart {
				continue
			}
			targetKey := fmt.Sprintf("%s:%s:%d", decl.Path, decl.Symbol, decl.StartLine)
			if _, exists := seenTarget[targetKey]; exists {
				continue
			}
			seenTarget[targetKey] = struct{}{}
			snippet := strings.TrimSpace(rawLine)
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
			callees = append(callees, CalleeHit{
				Path:      decl.Path,
				Symbol:    decl.Symbol,
				StartLine: decl.StartLine,
				EndLine:   decl.EndLine,
				CallLine:  lineNo,
				Snippet:   snippet,
			})
			if len(callees) >= limit {
				break
			}
		}
		if len(callees) >= limit {
			break
		}
	}

	return CalleesResponse{
		Directory: directory,
		Path:      cleanRel,
		Symbol:    resolvedSym,
		Imports:   imports,
		Callees:   callees,
	}, nil
}

func (s *Service) Impact(directory, symbol, relPath string, depth, limit int) (ImpactResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return ImpactResponse{}, err
	}
	symbol = strings.TrimSpace(symbol)
	relPath = strings.TrimSpace(relPath)
	if relPath != "" {
		cleanRel, _, err := resolveSafeWorktreeFile(directory, relPath)
		if err != nil {
			return ImpactResponse{}, err
		}
		relPath = cleanRel
	}
	if symbol == "" {
		return ImpactResponse{}, errors.New("symbol is required")
	}
	if depth <= 0 {
		depth = 2
	} else if depth > 3 {
		depth = 3
	}
	if limit <= 0 || limit > 40 {
		limit = 16
	}
	c, err := s.ensure(directory)
	if err != nil {
		return ImpactResponse{}, err
	}
	if relPath == "" {
		if foundPath, _, _, resolvedSym, ok := s.locateSymbolInIndex(directory, c, symbol); ok {
			relPath = foundPath
			if resolvedSym != "" {
				symbol = resolvedSym
			}
		}
	}

	hop1, err := s.Callers(directory, symbol, relPath, limit)
	if err != nil {
		return ImpactResponse{}, err
	}

	type frontierNode struct {
		symbol string
		path   string
		chain  string
	}

	visitedSymbols := map[string]bool{strings.ToLower(symbol): true}
	seenProdKey := make(map[string]bool)
	seenTestKey := make(map[string]bool)
	affectedSet := make(map[string]struct{})
	if relPath != "" {
		affectedSet[relPath] = struct{}{}
	}

	var frontier []frontierNode
	for _, hit := range hop1.Callers {
		key := fmt.Sprintf("%s:%d", hit.Path, hit.Line)
		seenProdKey[key] = true
		affectedSet[hit.Path] = struct{}{}
		sym := strings.TrimSpace(hit.Symbol)
		if sym != "" && !visitedSymbols[strings.ToLower(sym)] {
			visitedSymbols[strings.ToLower(sym)] = true
			frontier = append(frontier, frontierNode{
				symbol: sym,
				path:   hit.Path,
				chain:  sym + " -> " + symbol,
			})
		}
	}
	tests := make([]CallerHit, 0, len(hop1.Tests))
	for _, hit := range hop1.Tests {
		key := fmt.Sprintf("%s:%d", hit.Path, hit.Line)
		seenTestKey[key] = true
		affectedSet[hit.Path] = struct{}{}
		tests = append(tests, hit)
	}

	var transitive []ImpactHit
	for d := 2; d <= depth+1 && len(frontier) > 0; d++ {
		collectCallers := d <= depth && len(transitive) < limit
		var nextFrontier []frontierNode
		for _, node := range frontier {
			res, err := s.Callers(directory, node.symbol, node.path, limit)
			if err != nil {
				continue
			}
			if collectCallers {
				for _, hit := range res.Callers {
					key := fmt.Sprintf("%s:%d", hit.Path, hit.Line)
					if seenProdKey[key] {
						continue
					}
					seenProdKey[key] = true
					affectedSet[hit.Path] = struct{}{}
					if len(transitive) < limit {
						transitive = append(transitive, ImpactHit{
							Path:    hit.Path,
							Symbol:  hit.Symbol,
							Line:    hit.Line,
							Depth:   d,
							Via:     node.chain,
							Snippet: hit.Snippet,
						})
					}
					sym := strings.TrimSpace(hit.Symbol)
					if sym != "" && !visitedSymbols[strings.ToLower(sym)] {
						visitedSymbols[strings.ToLower(sym)] = true
						nextFrontier = append(nextFrontier, frontierNode{
							symbol: sym,
							path:   hit.Path,
							chain:  sym + " -> " + node.chain,
						})
					}
				}
			}
			for _, tHit := range res.Tests {
				key := fmt.Sprintf("%s:%d", tHit.Path, tHit.Line)
				if seenTestKey[key] {
					continue
				}
				seenTestKey[key] = true
				affectedSet[tHit.Path] = struct{}{}
				if len(tests) < limit {
					tests = append(tests, tHit)
				}
			}
		}
		frontier = nextFrontier
	}

	affectedFiles := make([]string, 0, len(affectedSet))
	for f := range affectedSet {
		affectedFiles = append(affectedFiles, f)
	}
	sort.Strings(affectedFiles)

	return ImpactResponse{
		Directory:         directory,
		Path:              relPath,
		Symbol:            symbol,
		DirectCallers:     hop1.Callers,
		TransitiveCallers: transitive,
		Tests:             tests,
		AffectedFiles:     affectedFiles,
	}, nil
}

func (s *Service) Context(directory, relPath, symbol, query string, contextLines, limit int) (ContextResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return ContextResponse{}, err
	}
	relPath = strings.TrimSpace(relPath)
	symbol = strings.TrimSpace(symbol)
	query = strings.TrimSpace(query)
	if relPath != "" {
		cleanRel, _, err := resolveSafeWorktreeFile(directory, relPath)
		if err != nil {
			return ContextResponse{}, err
		}
		relPath = cleanRel
	}
	if symbol == "" && query != "" {
		symbol = query
	}
	if relPath == "" && symbol == "" {
		return ContextResponse{}, errors.New("symbol, query, or path is required")
	}
	if limit <= 0 || limit > 40 {
		limit = 12
	}

	inspected, err := s.Inspect(directory, relPath, symbol, 0, 0, contextLines)
	if err != nil && relPath == "" {
		searchTerm := query
		if searchTerm == "" {
			searchTerm = symbol
		}
		if searchRes, sErr := s.Search(directory, searchTerm, 5); sErr == nil && len(searchRes.Results) > 0 {
			top := searchRes.Results[0]
			for _, cand := range searchRes.Results {
				if cand.Symbol != "" {
					top = cand
					break
				}
			}
			inspected, err = s.Inspect(directory, top.Path, top.Symbol, top.StartLine, top.EndLine, contextLines)
		}
	}
	if err != nil {
		return ContextResponse{}, err
	}

	var imports []string
	if c, cErr := s.ensure(directory); cErr == nil {
		if rec := c.index.Files[inspected.Path]; rec != nil {
			imports = rec.Imports
		}
	}

	targetSym := inspected.Symbol
	if targetSym == "" {
		targetSym = symbol
	}
	callees := []CalleeHit{}
	callers := []CallerHit{}
	var transitive []ImpactHit
	tests := []CallerHit{}
	var affectedFiles []string

	if targetSym != "" {
		if calleesRes, cErr := s.Callees(directory, targetSym, inspected.Path, limit); cErr == nil {
			callees = calleesRes.Callees
			if len(imports) == 0 {
				imports = calleesRes.Imports
			}
		}
		if impactRes, iErr := s.Impact(directory, targetSym, inspected.Path, 2, limit); iErr == nil {
			callers = impactRes.DirectCallers
			transitive = impactRes.TransitiveCallers
			tests = impactRes.Tests
			affectedFiles = impactRes.AffectedFiles
		}
	}

	return ContextResponse{
		Directory:         directory,
		Inspect:           inspected,
		Imports:           imports,
		Callees:           callees,
		Callers:           callers,
		TransitiveCallers: transitive,
		Tests:             tests,
		AffectedFiles:     affectedFiles,
	}, nil
}

func (s *Service) handleCallees(w http.ResponseWriter, r *http.Request) {
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
	limit, _ := strconv.Atoi(q.Get("limit"))
	res, err := s.Callees(directory, q.Get("symbol"), q.Get("path"), limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Service) handleImpact(w http.ResponseWriter, r *http.Request) {
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
	depth, _ := strconv.Atoi(q.Get("depth"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	res, err := s.Impact(directory, q.Get("symbol"), q.Get("path"), depth, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Service) handleContext(w http.ResponseWriter, r *http.Request) {
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
	contextLines, _ := strconv.Atoi(q.Get("contextLines"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	res, err := s.Context(directory, q.Get("path"), q.Get("symbol"), q.Get("q"), contextLines, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
