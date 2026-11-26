package codeindex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
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

func (s *Service) callersForFile(directory, relPath string, limit int, c *cachedIndex) (CallersResponse, error) {
	cleanRel, syms, err := declaredSymbolsInFile(directory, relPath, s.cfg.MaxFileBytes)
	if err != nil {
		return CallersResponse{}, err
	}
	matches := c.callerGraph(syms, cleanRel, true, 1, false, limit)
	return CallersResponse{
		Directory:    directory,
		Symbol:       cleanRel,
		Callers:      matches.DirectCallers,
		TotalCallers: matches.TotalCallers,
		Tests:        matches.Tests,
		TotalTests:   matches.TotalTests,
	}, nil
}
