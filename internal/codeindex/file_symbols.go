package codeindex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

var knownSourcePathExts = map[string]bool{
	"go": true, "ts": true, "tsx": true, "js": true, "jsx": true, "mjs": true, "cjs": true,
	"vue": true, "svelte": true, "py": true, "pyi": true, "rs": true, "dart": true,
	"java": true, "kt": true, "kts": true, "scala": true, "c": true, "h": true, "cc": true,
	"cpp": true, "cxx": true, "hpp": true, "m": true, "mm": true, "swift": true,
	"rb": true, "php": true, "cs": true, "sh": true, "bash": true, "zsh": true, "ps1": true,
	"sql": true, "html": true, "css": true, "scss": true, "less": true, "md": true, "mdx": true,
	"json": true, "jsonc": true, "yaml": true, "yml": true, "toml": true, "xml": true,
	"plist": true, "gradle": true, "arb": true, "lock": true, "env": true, "txt": true,
	"ini": true, "cfg": true, "conf": true, "proto": true, "graphql": true,
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "svg": true, "ico": true,
	"pdf": true, "xlsx": true, "docx": true, "csv": true, "tsv": true,
	"zip": true, "tar": true, "gz": true, "wasm": true, "bin": true, "db": true, "sqlite": true,
}

// looksLikeFilePathInput reports whether raw is a filesystem path or filename
// (for example "internal/remotehost/daemon.go", "checkout.go", or ".env")
// rather than a bare or qualified symbol identifier (such as "HandleRequest",
// "billing.ChargeCard", or "errgroup.Go").
func looksLikeFilePathInput(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, "/\\") || strings.HasPrefix(s, ".") {
		return true
	}
	if dot := strings.LastIndexByte(s, '.'); dot > 0 && dot+1 < len(s) {
		ext := s[dot+1:]
		if ext == strings.ToLower(ext) && knownSourcePathExts[ext] {
			return true
		}
	}
	return false
}

// normalizeSymbolAndPath promotes a file-path argument passed in symbol (when
// relPath is empty) into relPath, enforcing workspace boundary and secret-file
// guards so file paths are never split on '.' into extension tokens like "go".
func normalizeSymbolAndPath(directory, symbol, relPath string) (string, string, error) {
	symbol = strings.TrimSpace(symbol)
	relPath = strings.TrimSpace(relPath)
	if relPath == "" && symbol != "" {
		if looksLikeFilePathInput(symbol) {
			cleanRel, fullPath, err := resolveSafeWorktreeFile(directory, symbol)
			if err != nil {
				return "", "", err
			}
			st, statErr := os.Stat(fullPath)
			if statErr != nil || st.IsDir() {
				return "", "", fmt.Errorf("file %q does not exist", cleanRel)
			}
			return "", cleanRel, nil
		}
		if strings.Contains(symbol, ".") {
			if cleanRel, fullPath, err := resolveSafeWorktreeFile(directory, symbol); err == nil {
				if st, statErr := os.Stat(fullPath); statErr == nil && !st.IsDir() {
					return "", cleanRel, nil
				}
			}
		}
	}
	return symbol, relPath, nil
}

func declaredSymbolsInFile(directory, relPath string, maxFileBytes int64) (string, []string, error) {
	cleanRel, fullPath, err := resolveSafeWorktreeFile(directory, relPath)
	if err != nil {
		return "", nil, err
	}
	st, err := os.Stat(fullPath)
	if err != nil || st.IsDir() {
		return "", nil, errors.New("file does not exist")
	}
	if maxFileBytes > 0 && st.Size() > maxFileBytes {
		return "", nil, errors.New("file exceeds maximum indexable size")
	}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return "", nil, err
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", nil, errors.New("binary file cannot be inspected")
	}
	isGoFile := strings.HasSuffix(strings.ToLower(cleanRel), ".go")
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	seen := make(map[string]struct{})
	var syms []string
	for _, m := range typeScriptMethods(cleanRel, string(raw)) {
		name := m.Receiver + "." + m.Symbol
		if _, ok := seen[name]; !ok {
			syms = append(syms, name)
			seen[name] = struct{}{}
		}
	}
	for _, line := range lines {
		if isGoFile && leadingIndentWidth(line) > 0 {
			continue
		}
		sym := extractLineSymbolCandidate(line)
		if sym == "" || ignoredCallKeywords[sym] {
			continue
		}
		if _, ok := seen[sym]; ok {
			continue
		}
		seen[sym] = struct{}{}
		syms = append(syms, sym)
	}
	return cleanRel, syms, nil
}

func (s *Service) callersForFile(directory, relPath string, limit int) (CallersResponse, error) {
	if limit <= 0 || limit > 40 {
		limit = 16
	}
	cleanRel, syms, err := declaredSymbolsInFile(directory, relPath, s.cfg.MaxFileBytes)
	if err != nil {
		return CallersResponse{}, err
	}
	callers := []CallerHit{}
	tests := []CallerHit{}
	seenProd := make(map[string]bool)
	seenTest := make(map[string]bool)
	for _, sym := range syms {
		res, cErr := s.Callers(directory, sym, cleanRel, limit)
		if cErr != nil {
			continue
		}
		for _, h := range res.Callers {
			if h.Path == cleanRel {
				continue
			}
			key := fmt.Sprintf("%s:%d", h.Path, h.Line)
			if !seenProd[key] && len(callers) < limit {
				seenProd[key] = true
				callers = append(callers, h)
			}
		}
		for _, th := range res.Tests {
			key := fmt.Sprintf("%s:%d", th.Path, th.Line)
			if !seenTest[key] && len(tests) < limit {
				seenTest[key] = true
				tests = append(tests, th)
			}
		}
	}
	return CallersResponse{
		Directory: directory,
		Symbol:    cleanRel,
		Callers:   callers,
		Tests:     tests,
	}, nil
}

func (s *Service) impactForFile(directory, relPath string, depth, limit int) (ImpactResponse, error) {
	if depth <= 0 {
		depth = 2
	} else if depth > 3 {
		depth = 3
	}
	if limit <= 0 || limit > 40 {
		limit = 16
	}
	cleanRel, syms, err := declaredSymbolsInFile(directory, relPath, s.cfg.MaxFileBytes)
	if err != nil {
		return ImpactResponse{}, err
	}

	type frontierNode struct {
		symbol string
		path   string
		chain  string
	}
	visitedSymbols := make(map[string]bool, len(syms))
	for _, sym := range syms {
		visitedSymbols[strings.ToLower(sym)] = true
	}
	seenProdKey := make(map[string]bool)
	seenTestKey := make(map[string]bool)
	affectedSet := map[string]struct{}{cleanRel: {}}

	direct := []CallerHit{}
	tests := []CallerHit{}
	var frontier []frontierNode

	for _, declSym := range syms {
		hop1, cErr := s.Callers(directory, declSym, cleanRel, limit)
		if cErr != nil {
			continue
		}
		for _, hit := range hop1.Callers {
			if hit.Path == cleanRel {
				continue
			}
			key := fmt.Sprintf("%s:%d", hit.Path, hit.Line)
			if !seenProdKey[key] {
				seenProdKey[key] = true
				affectedSet[hit.Path] = struct{}{}
				if len(direct) < limit {
					direct = append(direct, hit)
				}
			}
			callerSym := strings.TrimSpace(hit.Symbol)
			if callerSym != "" && !visitedSymbols[strings.ToLower(callerSym)] {
				visitedSymbols[strings.ToLower(callerSym)] = true
				frontier = append(frontier, frontierNode{
					symbol: callerSym,
					path:   hit.Path,
					chain:  callerSym + " -> " + declSym,
				})
			}
		}
		for _, th := range hop1.Tests {
			key := fmt.Sprintf("%s:%d", th.Path, th.Line)
			if !seenTestKey[key] {
				seenTestKey[key] = true
				affectedSet[th.Path] = struct{}{}
				if len(tests) < limit {
					tests = append(tests, th)
				}
			}
		}
	}

	var transitive []ImpactHit
	for d := 2; d <= depth+1 && len(frontier) > 0 && (d <= depth || len(tests) < limit); d++ {
		collectCallers := d <= depth && len(transitive) < limit
		var nextFrontier []frontierNode
		for _, node := range frontier {
			res, cErr := s.Callers(directory, node.symbol, node.path, limit)
			if cErr != nil {
				continue
			}
			if collectCallers {
				for _, hit := range res.Callers {
					if hit.Path == cleanRel {
						continue
					}
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
					callerSym := strings.TrimSpace(hit.Symbol)
					if callerSym != "" && !visitedSymbols[strings.ToLower(callerSym)] {
						visitedSymbols[strings.ToLower(callerSym)] = true
						nextFrontier = append(nextFrontier, frontierNode{
							symbol: callerSym,
							path:   hit.Path,
							chain:  callerSym + " -> " + node.chain,
						})
					}
				}
			}
			for _, th := range res.Tests {
				key := fmt.Sprintf("%s:%d", th.Path, th.Line)
				if !seenTestKey[key] {
					seenTestKey[key] = true
					affectedSet[th.Path] = struct{}{}
					if len(tests) < limit {
						tests = append(tests, th)
					}
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
		Path:              cleanRel,
		Symbol:            cleanRel,
		DirectCallers:     direct,
		TransitiveCallers: transitive,
		Tests:             tests,
		AffectedFiles:     affectedFiles,
	}, nil
}
