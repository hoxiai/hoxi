package codeindex

import (
	"sort"
	"strings"
)

// CallerTotals counts unique indexed locations before the response limit is applied.
type CallerTotals struct {
	TotalCallers           int `json:"totalCallers"`
	TotalTransitiveCallers int `json:"totalTransitiveCallers"`
	TotalTests             int `json:"totalTests"`
}

type callerNode struct {
	symbol string
	path   string
	chain  string
}

type callerLocation struct {
	path string
	line int
}

// Resolve each breadth level against the same index snapshot. Limit only the
// displayed rows: an omitted direct caller can still lead to a related test.
func (c *cachedIndex) callerGraph(symbols []string, path string, fileQuery bool, depth int, relatedTests bool, limit int) ImpactResponse {
	result := ImpactResponse{DirectCallers: []CallerHit{}, Tests: []CallerHit{}}
	frontier := make([]callerNode, 0, len(symbols))
	type symbolKey struct{ path, name string }
	visited := make(map[symbolKey]bool)
	for _, symbol := range symbols {
		frontier = append(frontier, callerNode{symbol, path, symbol})
		visited[symbolKey{path, symbol}] = true
	}
	seenCallers := make(map[callerLocation]bool)
	seenTests := make(map[callerLocation]bool)
	files := make(map[string]bool)
	if path != "" {
		files[path] = true
	}
	lastHop := depth
	if relatedTests {
		lastHop++ // Also find tests that reference a caller at the deepest level.
	}
	for hop := 1; hop <= lastHop && len(frontier) > 0; hop++ {
		candidates := c.callerCandidatePaths(frontier, hop > depth)
		var next []callerNode
		for _, node := range frontier {
			callers, tests := c.symbolCallerHits(node.symbol, node.path, candidates[shortSymbolName(node.symbol)])
			for _, hit := range tests {
				loc := callerLocation{hit.Path, hit.Line}
				if !seenTests[loc] {
					seenTests[loc] = true
					files[hit.Path] = true
					if len(result.Tests) < limit {
						result.Tests = append(result.Tests, hit)
					}
				}
			}
			for _, hit := range callers {
				loc := callerLocation{hit.Path, hit.Line}
				if seenCallers[loc] || (fileQuery && hit.Path == path) {
					continue
				}
				seenCallers[loc] = true
				files[hit.Path] = true
				if hop == 1 {
					result.TotalCallers++
					if len(result.DirectCallers) < limit {
						result.DirectCallers = append(result.DirectCallers, hit)
					}
				} else if len(result.TransitiveCallers) < limit {
					result.TransitiveCallers = append(result.TransitiveCallers, ImpactHit{
						Path: hit.Path, Line: hit.Line, Symbol: hit.Symbol, Snippet: hit.Snippet,
						Depth: hop, Via: node.chain, Possible: hit.Possible,
					})
				}
				key := symbolKey{hit.Path, hit.Symbol}
				if hop < lastHop && (fileQuery || !hit.Possible) && hit.Symbol != "" && !visited[key] {
					visited[key] = true
					next = append(next, callerNode{hit.Symbol, hit.Path, hit.Symbol + " -> " + node.chain})
				}
			}
		}
		frontier = next
	}
	result.TotalTests = len(seenTests)
	result.TotalTransitiveCallers = len(seenCallers) - result.TotalCallers
	result.AffectedFiles = make([]string, 0, len(files))
	for file := range files {
		result.AffectedFiles = append(result.AffectedFiles, file)
	}
	sort.Strings(result.AffectedFiles)
	return result
}

// For a wide frontier, shortlist files for all its identifiers in one pass
// rather than rescanning the workspace for every caller. The normal matcher
// still handles declarations, comments, receiver resolution and ranking.
func (c *cachedIndex) callerCandidatePaths(nodes []callerNode, testsOnly bool) map[string][]string {
	paths := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		paths[shortSymbolName(node.symbol)] = nil
	}
	if len(paths) == 1 && !testsOnly {
		for ident := range paths {
			paths[ident] = c.sortedPaths
		}
		return paths
	}
	// Unusual names outside the ASCII tokenizer use the regular matcher on all eligible files.
	var otherNames []string
	for ident := range paths {
		if strings.IndexFunc(ident, func(r rune) bool { return r > 127 || !isIdentChar(byte(r)) }) >= 0 {
			otherNames = append(otherNames, ident)
		}
	}
	for _, path := range c.sortedPaths {
		if (testsOnly && !isTestFilePath(path)) || isProtectedOrSecretRelPath(path) || isNonCodeCallersFile(path) {
			continue
		}
		for _, chunk := range c.index.Files[path].Chunks {
			text := chunk.Text
			for i := 0; i < len(text); {
				if !isIdentChar(text[i]) {
					i++
					continue
				}
				start := i
				for i < len(text) && isIdentChar(text[i]) {
					i++
				}
				ident := text[start:i]
				if matches, ok := paths[ident]; ok && (len(matches) == 0 || matches[len(matches)-1] != path) {
					paths[ident] = append(matches, path)
				}
			}
		}
		for _, ident := range otherNames {
			paths[ident] = append(paths[ident], path)
		}
	}
	return paths
}
