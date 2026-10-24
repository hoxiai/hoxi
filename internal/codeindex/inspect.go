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

const (
	defaultInspectContextLines = 5
	maxInspectContextLines     = 30
	maxInspectWindowLines      = 120
)

type InspectResponse struct {
	Directory  string `json:"directory"`
	Path       string `json:"path"`
	Symbol     string `json:"symbol,omitempty"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	TotalLines int    `json:"totalLines"`
	Truncated  bool   `json:"truncated,omitempty"`
	Content    string `json:"content"`
}

type CallerHit struct {
	Path    string `json:"path"`
	Symbol  string `json:"symbol,omitempty"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

type CallersResponse struct {
	Directory string      `json:"directory"`
	Symbol    string      `json:"symbol"`
	Callers   []CallerHit `json:"callers"`
	Tests     []CallerHit `json:"tests"`
}

var classMethodDeclRe = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|async|override|readonly|abstract|virtual|get|set)\s+)*([A-Za-z_$][\w$]*)\s*(?:<[^>]*>)?\s*\([^()]*\)\s*(?::\s*[^{;=>]+|\s+throws\s+[^{;=>]+)?\{\s*$`)

var nonMethodKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "func": true, "function": true, "else": true, "select": true,
	"defer": true, "go": true, "try": true, "with": true, "match": true,
	"loop": true, "synchronized": true,
}

func (s *Service) Inspect(directory, relPath, symbol string, startLine, endLine, contextLines int) (InspectResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return InspectResponse{}, err
	}
	relPath = strings.TrimSpace(relPath)
	symbol = strings.TrimSpace(symbol)
	if relPath == "" && symbol == "" {
		return InspectResponse{}, errors.New("path or symbol is required")
	}
	if relPath == "" {
		c, err := s.ensure(directory)
		if err != nil {
			return InspectResponse{}, err
		}
		foundPath, symStart, symEnd, resolvedSym, ok := s.locateSymbolInIndex(directory, c, symbol)
		if !ok {
			return InspectResponse{}, fmt.Errorf("symbol %q not found in index", symbol)
		}
		relPath = foundPath
		if startLine <= 0 {
			startLine = symStart
		}
		if endLine <= 0 {
			endLine = symEnd
		}
		if resolvedSym != "" {
			symbol = resolvedSym
		}
	}
	cleanRel, fullPath, err := resolveSafeWorktreeFile(directory, relPath)
	if err != nil {
		return InspectResponse{}, err
	}
	if isProtectedOrSecretRelPath(cleanRel) {
		return InspectResponse{}, errors.New("refusing to inspect protected or secret file")
	}
	st, err := os.Stat(fullPath)
	if err != nil || st.IsDir() {
		return InspectResponse{}, errors.New("file does not exist")
	}
	if st.Size() > s.cfg.MaxFileBytes {
		return InspectResponse{}, errors.New("file exceeds maximum indexable size")
	}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return InspectResponse{}, err
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return InspectResponse{}, errors.New("binary file cannot be inspected")
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	resolvedSymbol := symbol
	if symbol != "" {
		symStart, symEnd, matchedSym, ok := findSymbolInLines(lines, symbol)
		if !ok {
			return InspectResponse{}, fmt.Errorf("symbol %q not found in %s", symbol, cleanRel)
		}
		if startLine <= 1 || startLine < symStart || startLine > symEnd {
			startLine = symStart
		}
		if endLine <= 0 || endLine < startLine || endLine > symEnd+maxInspectWindowLines {
			endLine = symEnd
		}
		resolvedSymbol = matchedSym
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 {
		endLine = startLine + 40
	}
	if contextLines < 0 {
		contextLines = 0
	} else if contextLines == 0 {
		contextLines = defaultInspectContextLines
	} else if contextLines > maxInspectContextLines {
		contextLines = maxInspectContextLines
	}

	winStart := startLine - contextLines
	if winStart < 1 {
		winStart = 1
	}
	winEnd := endLine + contextLines
	if winEnd > totalLines {
		winEnd = totalLines
	}
	if winStart > winEnd {
		winStart = winEnd
	}
	truncated := false
	if winEnd-winStart+1 > maxInspectWindowLines {
		winEnd = winStart + maxInspectWindowLines - 1
		truncated = true
	}

	var b strings.Builder
	for lineNo := winStart; lineNo <= winEnd && lineNo <= totalLines; lineNo++ {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d: %s", lineNo, lines[lineNo-1])
	}
	return InspectResponse{
		Directory:  directory,
		Path:       cleanRel,
		Symbol:     resolvedSymbol,
		StartLine:  winStart,
		EndLine:    winEnd,
		TotalLines: totalLines,
		Truncated:  truncated,
		Content:    b.String(),
	}, nil
}

func shortSymbolName(symbol string) string {
	target := strings.TrimSpace(symbol)
	if dot := strings.LastIndexByte(target, '.'); dot >= 0 && dot+1 < len(target) {
		return target[dot+1:]
	}
	return target
}

func (s *Service) Callers(directory, symbol, excludePath string, limit int) (CallersResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return CallersResponse{}, err
	}
	symbol = strings.TrimSpace(symbol)
	callIdent := shortSymbolName(symbol)
	if callIdent == "" {
		return CallersResponse{}, errors.New("symbol is required")
	}
	if limit <= 0 || limit > 40 {
		limit = 16
	}
	c, err := s.ensure(directory)
	if err != nil {
		return CallersResponse{}, err
	}
	normExclude := filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(excludePath), "./"))
	callers := []CallerHit{}
	tests := []CallerHit{}
	seen := make(map[string]struct{})

	paths := make([]string, 0, len(c.index.Files))
	for p := range c.index.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, relPath := range paths {
		if isProtectedOrSecretRelPath(relPath) || isNonCodeCallersFile(relPath) {
			continue
		}
		rec := c.index.Files[relPath]
		isTest := isTestFilePath(relPath)
		for _, ch := range rec.Chunks {
			if !strings.Contains(ch.Text, callIdent) {
				continue
			}
			lines := strings.Split(ch.Text, "\n")
			for i, line := range lines {
				if !containsIdentifier(line, callIdent) {
					continue
				}
				cleaned := stripLineStringsAndComments(line)
				if !containsIdentifier(cleaned, callIdent) {
					continue
				}
				if isDeclarationOf(line, callIdent) && (normExclude == "" || relPath == normExclude) {
					continue
				}
				lineNo := ch.StartLine + i
				key := fmt.Sprintf("%s:%d", relPath, lineNo)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				snippet := strings.TrimSpace(line)
				if len(snippet) > 200 {
					snippet = snippet[:200] + "..."
				}
				hit := CallerHit{
					Path:    relPath,
					Symbol:  enclosingSymbolAt(lines, i, ch.Symbol),
					Line:    lineNo,
					Snippet: snippet,
				}
				if isTest {
					if len(tests) < limit {
						tests = append(tests, hit)
					}
				} else if len(callers) < limit {
					callers = append(callers, hit)
				}
			}
		}
	}
	return CallersResponse{
		Directory: directory,
		Symbol:    symbol,
		Callers:   callers,
		Tests:     tests,
	}, nil
}

func (s *Service) locateSymbolInIndex(directory string, c *cachedIndex, symbol string) (path string, startLine, endLine int, resolvedSymbol string, ok bool) {
	target := strings.TrimSpace(symbol)
	if dot := strings.LastIndexByte(target, '.'); dot >= 0 && dot+1 < len(target) {
		target = target[dot+1:]
	}
	if target != "" && len(c.symbols) > 0 {
		if cands := c.symbols[strings.ToLower(target)]; len(cands) > 0 {
			best := cands[0]
			for _, cand := range cands {
				if cand.Symbol == target {
					best = cand
					break
				}
			}
			raw, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(best.Path)))
			if err == nil {
				lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
				if st, en, matched, found := findSymbolInLines(lines, symbol); found {
					return best.Path, st, en, matched, true
				}
			}
			return best.Path, best.StartLine, best.EndLine, best.Symbol, true
		}
	}
	paths := make([]string, 0, len(c.index.Files))
	for p := range c.index.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// Fallback pass: check indexed files for exact/case-insensitive symbol declaration.
	for _, relPath := range paths {
		if isProtectedOrSecretRelPath(relPath) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(relPath)))
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		if st, en, matched, found := findSymbolInLines(lines, symbol); found {
			return relPath, st, en, matched, true
		}
	}
	// Fallback: check chunk symbols.
	lower := strings.ToLower(strings.TrimSpace(symbol))
	for _, doc := range c.docs {
		if isProtectedOrSecretRelPath(doc.chunk.Path) {
			continue
		}
		sym := strings.ToLower(doc.chunk.Symbol)
		if sym != "" && sym == lower {
			return doc.chunk.Path, doc.chunk.StartLine, doc.chunk.EndLine, doc.chunk.Symbol, true
		}
	}
	return "", 0, 0, "", false
}

func enclosingSymbolAt(lines []string, idx int, fallback string) string {
	for i := idx; i >= 0 && i < len(lines); i-- {
		if sym := extractLineSymbolCandidate(lines[i]); sym != "" {
			return sym
		}
	}
	return fallback
}

func isNonCodeCallersFile(relPath string) bool {
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".md", ".mdx", ".json", ".jsonc", ".yaml", ".yml", ".toml", ".lock", ".ini", ".cfg", ".conf", ".txt":
		return true
	}
	return false
}

func extractLineSymbolCandidate(line string) string {
	if sym := declaredSymbolOnLine(line); sym != "" {
		return sym
	}
	if m := classMethodDeclRe.FindStringSubmatch(line); len(m) == 2 {
		if !nonMethodKeywords[m[1]] {
			return m[1]
		}
	}
	return ""
}

func findSymbolInLines(lines []string, symbol string) (startLine, endLine int, resolvedSymbol string, ok bool) {
	target := strings.TrimSpace(symbol)
	if target == "" {
		return 0, 0, "", false
	}
	shortTarget := ""
	if dot := strings.LastIndexByte(target, '.'); dot >= 0 && dot+1 < len(target) {
		shortTarget = target[dot+1:]
	}
	declIdx := -1
	matchedSym := ""
	// Pass 1: exact case match on declared symbol.
	for i, line := range lines {
		sym := extractLineSymbolCandidate(line)
		if sym == "" {
			continue
		}
		if sym == target || (shortTarget != "" && sym == shortTarget) {
			declIdx = i
			matchedSym = sym
			break
		}
	}
	// Pass 2: case-insensitive match on declared symbol.
	if declIdx < 0 {
		for i, line := range lines {
			sym := extractLineSymbolCandidate(line)
			if sym == "" {
				continue
			}
			if strings.EqualFold(sym, target) || (shortTarget != "" && strings.EqualFold(sym, shortTarget)) {
				declIdx = i
				matchedSym = sym
				break
			}
		}
	}
	if declIdx < 0 {
		return 0, 0, "", false
	}
	startLine = declIdx + 1
	endLine = estimateSymbolEndLine(lines, declIdx)
	return startLine, endLine, matchedSym, true
}

var pythonBlockDeclRe = regexp.MustCompile(`^\s*(?:async\s+)?(?:def|class)\s+[A-Za-z_]\w*`)

func leadingIndentWidth(line string) int {
	width := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}
	return width
}

func estimatePythonBlockEndLine(lines []string, declIdx, maxIdx int) int {
	baseIndent := leadingIndentWidth(lines[declIdx])
	headerEnd := declIdx
	parenDepth := 0
	for i := declIdx; i <= maxIdx && i < declIdx+12; i++ {
		line := lines[i]
		for j := 0; j < len(line); j++ {
			if line[j] == '#' {
				break
			}
			switch line[j] {
			case '(':
				parenDepth++
			case ')':
				if parenDepth > 0 {
					parenDepth--
				}
			}
		}
		headerEnd = i
		trimmed := strings.TrimSpace(line)
		if idx := strings.IndexByte(trimmed, '#'); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
		if parenDepth == 0 && strings.HasSuffix(trimmed, ":") {
			break
		}
	}
	lastBodyLine := headerEnd + 1
	sawBody := false
	for i := headerEnd + 1; i <= maxIdx; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingIndentWidth(lines[i])
		if indent <= baseIndent {
			if sawBody {
				return lastBodyLine
			}
			return headerEnd + 1
		}
		sawBody = true
		lastBodyLine = i + 1
	}
	if sawBody {
		return lastBodyLine
	}
	return headerEnd + 1
}

func estimateSymbolEndLine(lines []string, declIdx int) int {
	maxIdx := declIdx + maxInspectWindowLines - 1
	if maxIdx >= len(lines) {
		maxIdx = len(lines) - 1
	}
	if pythonBlockDeclRe.MatchString(lines[declIdx]) && !strings.Contains(lines[declIdx], "{") {
		return estimatePythonBlockEndLine(lines, declIdx, maxIdx)
	}
	braceDepth := 0
	sawBrace := false
	inBlockComment := false
	var stringQuote byte
	for i := declIdx; i <= maxIdx; i++ {
		line := lines[i]
		if i > declIdx+8 && !sawBrace && !inBlockComment && stringQuote == 0 && extractLineSymbolCandidate(line) != "" {
			return i
		}
		for j := 0; j < len(line); j++ {
			ch := line[j]
			if inBlockComment {
				if ch == '*' && j+1 < len(line) && line[j+1] == '/' {
					inBlockComment = false
					j++
				}
				continue
			}
			if stringQuote != 0 {
				if ch == '\\' && stringQuote != '`' {
					j++
					continue
				}
				if ch == stringQuote {
					stringQuote = 0
				}
				continue
			}
			if ch == '/' && j+1 < len(line) {
				if line[j+1] == '/' {
					break
				}
				if line[j+1] == '*' {
					inBlockComment = true
					j++
					continue
				}
			}
			switch ch {
			case '"', '\'', '`':
				stringQuote = ch
			case '{':
				braceDepth++
				sawBrace = true
			case '}':
				braceDepth--
			}
		}
		if sawBrace && braceDepth <= 0 {
			return i + 1
		}
	}
	end := declIdx + 40
	if end > len(lines) {
		end = len(lines)
	}
	return end
}

func resolveSafeWorktreeFile(directory, relPath string) (string, string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(relPath))
	if cleaned == "." || cleaned == "" {
		return "", "", errors.New("invalid file path")
	}
	var full string
	if filepath.IsAbs(cleaned) {
		full = cleaned
	} else {
		full = filepath.Join(directory, cleaned)
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", "", err
	}
	lexRel, err := filepath.Rel(directory, abs)
	if err != nil || lexRel == ".." || strings.HasPrefix(lexRel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path escapes workspace directory")
	}
	if isProtectedOrSecretRelPath(filepath.ToSlash(lexRel)) {
		return "", "", errors.New("refusing to inspect protected or secret file")
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(directory, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path escapes workspace directory")
	}
	cleanRel := filepath.ToSlash(rel)
	if isProtectedOrSecretRelPath(cleanRel) {
		return "", "", errors.New("refusing to inspect protected or secret file")
	}
	return cleanRel, abs, nil
}

func containsIdentifier(line, ident string) bool {
	if ident == "" || len(line) < len(ident) {
		return false
	}
	offset := 0
	for {
		idx := strings.Index(line[offset:], ident)
		if idx < 0 {
			return false
		}
		pos := offset + idx
		end := pos + len(ident)
		prevOK := pos == 0 || !isIdentChar(line[pos-1])
		nextOK := end == len(line) || !isIdentChar(line[end])
		if prevOK && nextOK {
			return true
		}
		offset = pos + 1
	}
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}

func isDeclarationOf(line, symbol string) bool {
	if sym := extractLineSymbolCandidate(line); sym != "" && strings.EqualFold(sym, symbol) {
		return true
	}
	return false
}

func isTestFilePath(relPath string) bool {
	lower := strings.ToLower(filepath.ToSlash(relPath))
	base := filepath.Base(lower)
	return strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") ||
		strings.Contains(lower, "/__tests__/") ||
		strings.Contains(lower, "/tests/") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.HasPrefix(lower, "test/")
}

func (s *Service) handleInspect(w http.ResponseWriter, r *http.Request) {
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
	startLine, _ := strconv.Atoi(q.Get("startLine"))
	endLine, _ := strconv.Atoi(q.Get("endLine"))
	contextLines, _ := strconv.Atoi(q.Get("contextLines"))
	res, err := s.Inspect(directory, q.Get("path"), q.Get("symbol"), startLine, endLine, contextLines)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Service) handleCallers(w http.ResponseWriter, r *http.Request) {
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
	res, err := s.Callers(directory, q.Get("symbol"), q.Get("path"), limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
