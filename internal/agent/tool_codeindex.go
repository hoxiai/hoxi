package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type hoxiCodeIndexTool struct{}

func (t *hoxiCodeIndexTool) Name() string     { return "hoxi_code_index" }
func (t *hoxiCodeIndexTool) IsReadOnly() bool { return true }
func (t *hoxiCodeIndexTool) Description() string {
	return "Navigate workspace code efficiently via the worktree-scoped knowledge graph. Prefer action='context' to fetch a symbol's source, callees, callers, blast radius, and tests in 1 call; or use search -> inspect -> callers/callees/impact -> refresh. Avoids full-repo grep and whole-file reads."
}
func (t *hoxiCodeIndexTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"context", "search", "inspect", "callers", "callees", "impact", "refresh"},
				"description": "context | search | inspect | callers | callees | impact | refresh",
			},
			"query":        map[string]any{"type": "string", "description": "Symbol or keyword query for search or context"},
			"symbol":       map[string]any{"type": "string", "description": "Symbol name for context, inspect, callers, callees, or impact"},
			"path":         map[string]any{"type": "string", "description": "Relative file path (context/inspect/callers/callees/impact) or path prefix (search)"},
			"startLine":    map[string]any{"type": "integer", "description": "1-based start line for inspect"},
			"endLine":      map[string]any{"type": "integer", "description": "1-based end line for inspect"},
			"contextLines": map[string]any{"type": "integer", "description": "Extra context lines for inspect or context (default 5, max 30)"},
			"depth":        map[string]any{"type": "integer", "description": "Call graph depth for impact (default 2, max 3)"},
			"limit":        map[string]any{"type": "integer", "description": "Max hits for search, callers, callees, or impact (default 12)"},
			"refresh":      map[string]any{"type": "boolean", "description": "Refresh worktree index before search"},
		},
		"required": []string{"action"},
	}
}

func (t *hoxiCodeIndexTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	if tc.HostInvoke != nil {
		return execHostTool(ctx, tc, t.Name(), input)
	}
	var args struct {
		Action string `json:"action"`
		Query  string `json:"query"`
		Symbol string `json:"symbol"`
		Path   string `json:"path"`
		Depth  int    `json:"depth"`
		Limit  int    `json:"limit"`
	}
	_ = json.Unmarshal(input, &args)
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "inspect", "callers", "context":
		fb, _ := json.Marshal(map[string]any{"symbol": firstNonEmptyCodeStr(args.Symbol, args.Query), "filePath": args.Path, "includeCallers": true})
		return (&symbolInspectTool{}).Execute(ctx, tc, fb)
	case "impact", "callees":
		fb, _ := json.Marshal(map[string]any{"symbol": firstNonEmptyCodeStr(args.Symbol, args.Query), "filePath": args.Path, "depth": args.Depth, "limit": args.Limit})
		return (&impactAnalysisTool{}).Execute(ctx, tc, fb)
	case "refresh":
		return ToolOutput{Title: "CodeIndex: refresh", Output: "Index refresh is unavailable without host gateway."}, nil
	default:
		fb, _ := json.Marshal(map[string]any{"query": firstNonEmptyCodeStr(args.Query, args.Symbol), "pathPrefix": args.Path, "limit": args.Limit})
		return (&codeSearchTool{}).Execute(ctx, tc, fb)
	}
}

func firstNonEmptyCodeStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

type codeSearchTool struct{}

func (t *codeSearchTool) Name() string     { return "code_search" }
func (t *codeSearchTool) IsReadOnly() bool { return true }
func (t *codeSearchTool) Description() string {
	return "Search the workspace hybrid AST/BM25/semantic code index for symbols, concepts, or implementation chunks."
}
func (t *codeSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":      map[string]any{"type": "string", "description": "Identifier, symbol name, or natural-language code query"},
			"pathPrefix": map[string]any{"type": "string", "description": "Optional relative directory/file prefix filter"},
			"limit":      map[string]any{"type": "integer", "description": "Max results (default 10, max 25)"},
		},
		"required": []string{"query"},
	}
}

func (t *codeSearchTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Query      string `json:"query"`
		PathPrefix string `json:"pathPrefix"`
		Path       string `json:"path"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "code_search", IsError: true}, err
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return ToolOutput{Title: "code_search", IsError: true}, fmt.Errorf("query is required")
	}
	prefix := strings.TrimSpace(args.PathPrefix)
	if prefix == "" {
		prefix = strings.TrimSpace(args.Path)
	}
	if prefix != "" {
		if _, err := resolveWorkspacePath(tc.WorktreeDir, prefix, true); err != nil {
			return ToolOutput{Title: q, IsError: true}, err
		}
	}
	limit := args.Limit
	if limit <= 0 || limit > 25 {
		limit = 10
	}

	if tc.CodeSearch == nil {
		fallbackIn, _ := json.Marshal(map[string]any{"pattern": q, "path": prefix})
		return (&grepTool{}).Execute(ctx, tc, fallbackIn)
	}

	hits, err := tc.CodeSearch(ctx, tc.WorktreeDir, q, prefix, limit)
	if err != nil {
		return ToolOutput{Title: q, IsError: true}, err
	}
	if len(hits) == 0 {
		return ToolOutput{Title: q, Output: "no indexed code chunks matched query: " + q}, nil
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		header := fmt.Sprintf("%s:%d-%d", h.Path, h.StartLine, h.EndLine)
		if h.Symbol != "" {
			header += fmt.Sprintf(" [%s]", h.Symbol)
		}
		fmt.Fprintf(&b, "%s (score %.2f)\n%s", header, h.Score, strings.TrimSpace(h.Snippet))
	}
	return ToolOutput{Title: q, Output: b.String()}, nil
}

type symbolInspectTool struct{}

func (t *symbolInspectTool) Name() string     { return "symbol_inspect" }
func (t *symbolInspectTool) IsReadOnly() bool { return true }
func (t *symbolInspectTool) Description() string {
	return "Jump to a function, type, class, or method definition in the workspace index and optionally list its callers and tests."
}
func (t *symbolInspectTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"symbol":         map[string]any{"type": "string", "description": "Symbol name to inspect (e.g. RunTurn or Service.CreateSession)"},
			"filePath":       map[string]any{"type": "string", "description": "Optional relative file path if known"},
			"includeCallers": map[string]any{"type": "boolean", "description": "Include production and test call sites (default true)"},
		},
		"required": []string{"symbol"},
	}
}

func (t *symbolInspectTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Symbol         string `json:"symbol"`
		FilePath       string `json:"filePath"`
		Path           string `json:"path"`
		IncludeCallers *bool  `json:"includeCallers"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "symbol_inspect", IsError: true}, err
	}
	sym := strings.TrimSpace(args.Symbol)
	if sym == "" {
		return ToolOutput{Title: "symbol_inspect", IsError: true}, fmt.Errorf("symbol is required")
	}
	rel := strings.TrimSpace(args.FilePath)
	if rel == "" {
		rel = strings.TrimSpace(args.Path)
	}
	if rel != "" {
		if _, err := resolveWorkspacePath(tc.WorktreeDir, rel, true); err != nil {
			return ToolOutput{Title: sym, IsError: true}, err
		}
	}
	includeCallers := true
	if args.IncludeCallers != nil {
		includeCallers = *args.IncludeCallers
	}
	if tc.SymbolInspect == nil {
		fallbackIn, _ := json.Marshal(map[string]any{"pattern": sym, "path": rel})
		return (&grepTool{}).Execute(ctx, tc, fallbackIn)
	}
	report, err := tc.SymbolInspect(ctx, tc.WorktreeDir, rel, sym, includeCallers)
	if err != nil {
		return ToolOutput{Title: sym, IsError: true}, err
	}
	return ToolOutput{Title: sym, Output: report}, nil
}

type impactAnalysisTool struct{}

func (t *impactAnalysisTool) Name() string     { return "impact_analysis" }
func (t *impactAnalysisTool) IsReadOnly() bool { return true }
func (t *impactAnalysisTool) Description() string {
	return "Analyze refactoring blast radius for a symbol: outgoing callees, direct and transitive callers (up to 3 hops), affected files, and test coverage."
}
func (t *impactAnalysisTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"symbol":   map[string]any{"type": "string", "description": "Function, method, or type name to analyze"},
			"filePath": map[string]any{"type": "string", "description": "Optional relative file path disambiguating the symbol"},
			"depth":    map[string]any{"type": "integer", "description": "Transitive caller hop depth (1-3, default 2)"},
			"limit":    map[string]any{"type": "integer", "description": "Max callers/tests per category (default 16, max 40)"},
		},
		"required": []string{"symbol"},
	}
}

func (t *impactAnalysisTool) Execute(ctx context.Context, tc ToolContext, input json.RawMessage) (ToolOutput, error) {
	var args struct {
		Symbol   string `json:"symbol"`
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
		Depth    int    `json:"depth"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ToolOutput{Title: "impact_analysis", IsError: true}, err
	}
	sym := strings.TrimSpace(args.Symbol)
	if sym == "" {
		return ToolOutput{Title: "impact_analysis", IsError: true}, fmt.Errorf("symbol is required")
	}
	rel := strings.TrimSpace(args.FilePath)
	if rel == "" {
		rel = strings.TrimSpace(args.Path)
	}
	if rel != "" {
		if _, err := resolveWorkspacePath(tc.WorktreeDir, rel, true); err != nil {
			return ToolOutput{Title: sym, IsError: true}, err
		}
	}
	if tc.ImpactInspect == nil {
		return (&symbolInspectTool{}).Execute(ctx, tc, input)
	}
	report, err := tc.ImpactInspect(ctx, tc.WorktreeDir, rel, sym, args.Depth, args.Limit)
	if err != nil {
		return ToolOutput{Title: sym, IsError: true}, err
	}
	return ToolOutput{Title: sym, Output: report}, nil
}
