package handoff

import (
	"os"
	"path/filepath"
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

type NormalizedTurn struct {
	Role      string               `json:"role"`
	Timestamp int64                `json:"timestamp"`
	Text      string               `json:"text"`
	Reasoning string               `json:"reasoning,omitempty"`
	Tools     []NormalizedToolCall `json:"tools,omitempty"`
	ModelID   string               `json:"modelID,omitempty"`
}

type ExternalPlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

type ExternalSessionDetail struct {
	Summary         ExternalSessionSummary `json:"summary"`
	Turns           []NormalizedTurn       `json:"turns"`
	PlanSteps       []ExternalPlanStep     `json:"planSteps,omitempty"`
	ChangedFiles    []string               `json:"changedFiles,omitempty"`
	LastTurnSummary string                 `json:"lastTurnSummary,omitempty"`
	LastRecap       string                 `json:"lastRecap,omitempty"`
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
