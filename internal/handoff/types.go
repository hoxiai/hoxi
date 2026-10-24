package handoff

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ExternalToolID string

const (
	ExternalToolGrok      ExternalToolID = "grok-cli"
	ExternalToolClaude    ExternalToolID = "claude-code"
	ExternalToolCodex     ExternalToolID = "codex-cli"
	ExternalToolGemini    ExternalToolID = "gemini-antigravity"
	ExternalToolCodeBuddy ExternalToolID = "codebuddy"
	ExternalToolTrae      ExternalToolID = "trae"
	ExternalToolQoder     ExternalToolID = "qoder"
	ExternalToolOpenCode  ExternalToolID = "opencode-cli"
)

type ExternalSessionSummary struct {
	ID          string         `json:"id"`
	Source      ExternalToolID `json:"source"`
	SourceLabel string         `json:"sourceLabel"`
	CWD         string         `json:"cwd"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary,omitempty"`
	Model       string         `json:"model,omitempty"`
	GitBranch   string         `json:"gitBranch,omitempty"`
	GitCommit   string         `json:"gitCommit,omitempty"`
	CreatedAt   int64          `json:"createdAt"`
	UpdatedAt   int64          `json:"updatedAt"`
	TurnCount   int            `json:"turnCount,omitempty"`
	MatchesCWD  bool           `json:"matchesCwd"`
}

type NormalizedToolCall struct {
	CallID string                 `json:"callID"`
	Tool   string                 `json:"tool"`
	Title  string                 `json:"title"`
	Input  map[string]interface{} `json:"input"`
	Output string                 `json:"output"`
	Status string                 `json:"status"`
}

// TurnUsage is token usage reported by the source CLI for one assistant turn.
// Fields stay zero when the source recorded none.
type TurnUsage struct {
	Input      int64   `json:"input,omitempty"`
	Output     int64   `json:"output,omitempty"`
	Reasoning  int64   `json:"reasoning,omitempty"`
	CacheRead  int64   `json:"cacheRead,omitempty"`
	CacheWrite int64   `json:"cacheWrite,omitempty"`
	Cost       float64 `json:"cost,omitempty"`
}

func (u TurnUsage) empty() bool {
	return u.Input == 0 && u.Output == 0 && u.Reasoning == 0 && u.CacheRead == 0 && u.CacheWrite == 0 && u.Cost == 0
}

func (u TurnUsage) add(other TurnUsage) TurnUsage {
	u.Input += other.Input
	u.Output += other.Output
	u.Reasoning += other.Reasoning
	u.CacheRead += other.CacheRead
	u.CacheWrite += other.CacheWrite
	u.Cost += other.Cost
	return u
}

type NormalizedTurn struct {
	Role      string               `json:"role"`
	Timestamp int64                `json:"timestamp"`
	Text      string               `json:"text"`
	Reasoning string               `json:"reasoning,omitempty"`
	Tools     []NormalizedToolCall `json:"tools,omitempty"`
	ModelID   string               `json:"modelID,omitempty"`
	Usage     TurnUsage            `json:"usage,omitempty"`
}

type ExternalPlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

type HandoffPreviewFidelity struct {
	RetainedTurns       int      `json:"retainedTurns"`
	RetainedToolCalls   int      `json:"retainedToolCalls"`
	RetainedFiles       []string `json:"retainedFiles,omitempty"`
	UnsupportedFeatures []string `json:"unsupportedFeatures,omitempty"`
	TargetDirectory     string   `json:"targetDirectory"`
	TargetRuntime       string   `json:"targetRuntime"`
}

type ExternalSessionDetail struct {
	Summary         ExternalSessionSummary  `json:"summary"`
	Turns           []NormalizedTurn        `json:"turns"`
	PlanSteps       []ExternalPlanStep      `json:"planSteps,omitempty"`
	ChangedFiles    []string                `json:"changedFiles,omitempty"`
	LastTurnSummary string                  `json:"lastTurnSummary,omitempty"`
	LastRecap       string                  `json:"lastRecap,omitempty"`
	Fidelity        *HandoffPreviewFidelity `json:"fidelity,omitempty"`
}

type ExternalSourceStats struct {
	Installed    bool `json:"installed"`
	SessionCount int  `json:"sessionCount"`
}

type HandoffSessionsResponse struct {
	Sessions []ExternalSessionSummary               `json:"sessions"`
	Sources  map[ExternalToolID]ExternalSourceStats `json:"sources"`
}

type HandoffImportRequest struct {
	Source              ExternalToolID `json:"source"`
	SessionID           string         `json:"sessionID"`
	SourceCWD           string         `json:"sourceCwd,omitempty"`
	TargetDirectory     string         `json:"targetDirectory,omitempty"`
	PreferredProviderID string         `json:"preferredProviderID,omitempty"`
	PreferredModelID    string         `json:"preferredModelID,omitempty"`
}

type HandoffImportResponse struct {
	OK                 bool                   `json:"ok"`
	Session            map[string]interface{} `json:"session,omitempty"`
	ImportedTurns      int                    `json:"importedTurns"`
	MemoryPath         string                 `json:"memoryPath,omitempty"`
	TargetDirectory    string                 `json:"targetDirectory,omitempty"`
	ResolvedProviderID string                 `json:"resolvedProviderID,omitempty"`
	ResolvedModelID    string                 `json:"resolvedModelID,omitempty"`
	Error              string                 `json:"error,omitempty"`
}

type LocalCLIStatusItem struct {
	ID            ExternalToolID `json:"id"`
	Name          string         `json:"name"`
	Installed     bool           `json:"installed"`
	Authenticated bool           `json:"authenticated"`
	Connected     bool           `json:"connected"`
	Version       string         `json:"version,omitempty"`
	DefaultModel  string         `json:"defaultModel,omitempty"`
	Models        []string       `json:"models,omitempty"`
	SessionCount  int            `json:"sessionCount"`
	AuthMode      string         `json:"authMode,omitempty"`
	ProviderID    string         `json:"providerID"`
}

type LocalCLIStatusResponse struct {
	Tools []LocalCLIStatusItem `json:"tools"`
}

type LocalCLIImportRequest struct {
	ToolID ExternalToolID `json:"toolID"`
}

type LocalCLIImportResponse struct {
	OK         bool     `json:"ok"`
	ToolID     string   `json:"toolID"`
	ProviderID string   `json:"providerID"`
	Models     []string `json:"models,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func normalizeExternalToolName(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "read", "read_file", "view_file", "cat", "view":
		return "read"
	case "edit", "multiedit", "replace_file_content", "replace_in_file", "edit_file", "apply_patch", "patch", "str_replace_editor":
		return "edit"
	case "write", "write_file", "create_file", "write_to_file":
		return "write"
	case "bash", "exec_command", "execute_command", "shell", "run_command", "terminal", "command":
		return "bash"
	case "grep", "search", "search_content", "ripgrep", "rg", "grep_search":
		return "grep"
	case "glob", "list_dir", "list_files", "ls", "find", "find_by_name", "file_search", "search_file":
		return "glob"
	default:
		if lower == "" {
			return "bash"
		}
		return lower
	}
}

func cleanDirPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func sameOrSubPath(candidate, target string) bool {
	c := comparableFolderPath(candidate)
	t := comparableFolderPath(target)
	if c == "" || t == "" || c == "." || t == "." {
		return false
	}
	if c == t {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(c, t+sep) || strings.HasPrefix(t, c+sep)
}

func decodeHyphenatedProjectPath(encoded, targetDir string) string {
	s := strings.TrimSpace(encoded)
	if s == "" || s == "-" {
		return ""
	}
	if idx := strings.Index(s, "--p"); idx > 0 {
		s = s[:idx]
	}
	s = strings.Trim(s, "-")
	if s == "" {
		return ""
	}
	if targetDir != "" {
		tClean := cleanDirPath(targetDir)
		tHint := strings.Trim(encodeClaudeProjectHint(tClean), "-")
		if tHint != "" && strings.EqualFold(s, tHint) {
			return tClean
		}
	}
	tokens := strings.Split(s, "-")
	if resolved := walkHyphenTokens(string(os.PathSeparator), tokens); resolved != "" {
		return resolved
	}
	return string(os.PathSeparator) + strings.Join(tokens, string(os.PathSeparator))
}

func walkHyphenTokens(curDir string, tokens []string) string {
	if len(tokens) == 0 {
		return cleanDirPath(curDir)
	}
	entries, err := os.ReadDir(curDir)
	if err != nil {
		return ""
	}
	// Prefer longer multi-token directory matches first (e.g., "gopanel_v2" or "qingpu-ai")
	for span := len(tokens); span >= 1; span-- {
		wantHint := strings.ToLower(strings.Join(tokens[:span], "-"))
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			entryHint := strings.ToLower(strings.Trim(encodeClaudeProjectHint(e.Name()), "-"))
			if entryHint == wantHint {
				nextDir := filepath.Join(curDir, e.Name())
				if span == len(tokens) {
					return cleanDirPath(nextDir)
				}
				if sub := walkHyphenTokens(nextDir, tokens[span:]); sub != "" {
					return sub
				}
			}
		}
	}
	return ""
}

func extractChangedFilesFromTurns(turns []NormalizedTurn) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, turn := range turns {
		for _, tc := range turn.Tools {
			if tc.Tool != "edit" && tc.Tool != "write" {
				continue
			}
			for _, key := range []string{"filePath", "file_path", "path", "target_file", "file"} {
				if val, ok := tc.Input[key].(string); ok && strings.TrimSpace(val) != "" {
					p := strings.TrimSpace(val)
					if _, exists := seen[p]; !exists {
						seen[p] = struct{}{}
						out = append(out, p)
					}
				}
			}
		}
	}
	return out
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt64(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
	default:
		return 0
	}
}

func asFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f
	default:
		return 0
	}
}

// parseTurnUsage reads a CLI usage object. Claude reports input_tokens plus
// cache_creation_input_tokens; Codex and Antigravity report input_tokens,
// output_tokens, cache_read_tokens and thinking_tokens.
func parseTurnUsage(raw interface{}) TurnUsage {
	m, ok := raw.(map[string]interface{})
	if !ok || m == nil {
		return TurnUsage{}
	}
	rawInput := asInt64(m["input_tokens"])
	if rawInput == 0 {
		rawInput = asInt64(m["prompt_tokens"])
	}
	if rawInput == 0 {
		rawInput = asInt64(m["inputTokens"])
	}
	if rawInput == 0 {
		rawInput = asInt64(m["input"])
	}
	input := rawInput
	rawOutput := asInt64(m["output_tokens"])
	if rawOutput == 0 {
		rawOutput = asInt64(m["completion_tokens"])
	}
	if rawOutput == 0 {
		rawOutput = asInt64(m["outputTokens"])
	}
	if rawOutput == 0 {
		rawOutput = asInt64(m["output"])
	}
	output := rawOutput
	reasoning := asInt64(m["thinking_tokens"])
	if reasoning == 0 {
		reasoning = asInt64(m["reasoning_tokens"])
	}
	if reasoning == 0 {
		reasoning = asInt64(m["reasoning_output_tokens"])
	}
	if reasoning == 0 {
		reasoning = asInt64(m["completion_thinking_tokens"])
	}
	var detailsThinking int64
	if details, ok := m["output_tokens_details"].(map[string]interface{}); ok && details != nil {
		detailsThinking = asInt64(details["thinking_tokens"])
		if detailsThinking == 0 {
			detailsThinking = asInt64(details["reasoning_tokens"])
		}
		if reasoning == 0 {
			reasoning = detailsThinking
		}
	}
	if details, ok := m["completion_tokens_details"].(map[string]interface{}); ok && details != nil {
		if r := asInt64(details["reasoning_tokens"]); r > 0 {
			detailsThinking = r
			if reasoning == 0 {
				reasoning = r
			}
		}
	}
	if (asInt64(m["reasoning_output_tokens"]) > 0 || asInt64(m["completion_thinking_tokens"]) > 0 || detailsThinking > 0) && output >= reasoning {
		output -= reasoning
	}
	cacheRead := asInt64(m["cache_read_input_tokens"])
	if cacheRead == 0 {
		cacheRead = asInt64(m["cache_read_tokens"])
	}
	if cacheRead == 0 {
		cacheRead = asInt64(m["prompt_cache_hit_tokens"])
	}
	// Codex reports cached input as a subset of input_tokens; normalize it to
	// the same disjoint categories used by the handoff usage ledger.
	codexCachedInput := asInt64(m["cached_input_tokens"])
	if codexCachedInput > 0 {
		cacheRead = codexCachedInput
	}
	cacheWrite := asInt64(m["cache_creation_input_tokens"])
	if cacheWrite == 0 {
		cacheWrite = asInt64(m["cache_write_tokens"])
	}
	if cacheWrite == 0 {
		cacheWrite = asInt64(m["cache_write_input_tokens"])
	}
	// CodeBuddy maps prompt_tokens -> input_tokens and prompt_cache_miss_tokens -> cache_creation_input_tokens.
	openAIStylePrompt := asInt64(m["prompt_tokens"]) > 0 || asInt64(m["prompt_cache_hit_tokens"]) > 0 ||
		(asInt64(m["total_tokens"]) > 0 && asInt64(m["total_tokens"]) == rawInput+rawOutput && cacheRead > 0)
	if rawInput > 0 && cacheWrite > 0 && rawInput == cacheRead+cacheWrite {
		input = cacheWrite
		cacheWrite = 0
	} else if codexCachedInput > 0 || asInt64(m["cache_write_input_tokens"]) > 0 || openAIStylePrompt {
		input -= cacheRead + cacheWrite
		if input < 0 {
			input = 0
		}
	}
	if input == 0 && output == 0 && cacheRead == 0 && cacheWrite == 0 {
		if ratio := asFloat64(m["context_usage_ratio"]); ratio > 0 {
			input = int64(math.Round(ratio * 180000))
		}
	}
	cost := asFloat64(m["cost"])
	if cost == 0 {
		if costMap, ok := m["cost"].(map[string]interface{}); ok {
			cost = asFloat64(costMap["total"])
		}
	}
	return TurnUsage{
		Input:      input,
		Output:     output,
		Reasoning:  reasoning,
		CacheRead:  cacheRead,
		CacheWrite: cacheWrite,
		Cost:       cost,
	}
}

// ImportedSessionTitlePrefixes returns the SQL LIKE prefix patterns used to tag
// sessions imported via external CLI handoff, so OpenCode scanners and Daily Pulse
// metrics can exclude them from native session counts.
func ImportedSessionTitlePrefixes() []string {
	return []string{
		"[Grok CLI]%",
		"[Claude Code]%",
		"[Codex%",
		"[OpenAI Codex]%",
		"[Gemini%",
		"[Antigravity]%",
		"[Antigravity (Gemini)]%",
		"[CodeBuddy]%",
		"[Trae]%",
		"[Qoder]%",
		"[OpenCode%",
	}
}

// SQLNotImportedTitleClause builds a conjunction of `<column> NOT LIKE '<prefix>'` clauses.
func SQLNotImportedTitleClause(column string) string {
	prefixes := ImportedSessionTitlePrefixes()
	clauses := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		clauses = append(clauses, column+" NOT LIKE '"+p+"'")
	}
	return strings.Join(clauses, " AND ")
}

// SQLNotImportedTurnClause matches rows from native Hoxi sessions as well as new
// turns executed inside Hoxi after an external session was imported, while
// excluding the historical turns materialized during handoff import.
func SQLNotImportedTurnClause(titleCol, dataCol, rowTimeCol, sessTimeCol string) string {
	return "(" + SQLNotImportedTitleClause(titleCol) +
		" OR (" + rowTimeCol + " > COALESCE(" + sessTimeCol + ", 0) AND COALESCE(json_extract(" + dataCol + ", '$.imported'), 0) = 0))"
}
