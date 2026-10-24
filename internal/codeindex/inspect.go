package codeindex

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
		matched := findSymbolChunk(c, symbol)
		if matched == nil {
			return InspectResponse{}, fmt.Errorf("symbol %q not found in index", symbol)
		}
		relPath = matched.Path
		if startLine <= 0 {
			startLine = matched.StartLine
		}
		if endLine <= 0 {
			endLine = matched.EndLine
		}
		if symbol == "" {
			symbol = matched.Symbol
		}
	}
	cleanRel, fullPath, err := resolveSafeWorktreeFile(directory, relPath)
	if err != nil {
		return InspectResponse{}, err
	}
	if secretFile(strings.ToLower(filepath.Base(cleanRel))) {
		return InspectResponse{}, errors.New("refusing to inspect credential or secret file")
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
	if startLine <= 0 && symbol != "" {
		chunks := chunkFile(cleanRel, content, s.cfg)
		for _, ch := range chunks {
			if strings.EqualFold(ch.Symbol, symbol) || strings.Contains(strings.ToLower(ch.Symbol), strings.ToLower(symbol)) {
				startLine = ch.StartLine
				if endLine <= 0 {
					endLine = ch.EndLine
				}
				resolvedSymbol = ch.Symbol
				break
			}
		}
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

func (s *Service) Callers(directory, symbol, excludePath string, limit int) (CallersResponse, error) {
	directory, err := canonicalDir(directory)
	if err != nil {
		return CallersResponse{}, err
	}
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
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
		rec := c.index.Files[relPath]
		isTest := isTestFilePath(relPath)
		for _, ch := range rec.Chunks {
			lines := strings.Split(ch.Text, "\n")
			for i, line := range lines {
				lineNo := ch.StartLine + i
				if !containsIdentifier(line, symbol) {
					continue
				}
				// Skip declaration lines of the symbol itself.
				if isDeclarationOf(line, symbol) && (normExclude == "" || relPath == normExclude) {
					continue
				}
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
					Symbol:  ch.Symbol,
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

func findSymbolChunk(c *cachedIndex, symbol string) *Chunk {
	lower := strings.ToLower(strings.TrimSpace(symbol))
	var partial *Chunk
	for _, doc := range c.docs {
		sym := strings.ToLower(doc.chunk.Symbol)
		if sym == "" {
			continue
		}
		if sym == lower {
			return doc.chunk
		}
		if partial == nil && strings.Contains(sym, lower) {
			partial = doc.chunk
		}
	}
	return partial
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
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(directory, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path escapes workspace directory")
	}
	return filepath.ToSlash(rel), abs, nil
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
	if m := declarationRe.FindStringSubmatch(line); len(m) == 2 && strings.EqualFold(m[1], symbol) {
		return true
	}
	if m := assignRe.FindStringSubmatch(line); len(m) == 2 && strings.EqualFold(m[1], symbol) {
		return true
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "func (") {
		if closeIdx := strings.IndexByte(trimmed, ')'); closeIdx > 0 {
			rest := strings.TrimSpace(trimmed[closeIdx+1:])
			if strings.HasPrefix(rest, symbol) && (len(rest) == len(symbol) || !isIdentChar(rest[len(symbol)])) {
				return true
			}
		}
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
