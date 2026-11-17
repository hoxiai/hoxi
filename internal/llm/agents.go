package llm

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// AgentSpec describes a built-in, Markdown-defined, or JSON-configured primary/subagent role.
type AgentSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Mode        string   `json:"mode,omitempty"` // "primary" | "subagent" | "all"
	Model       string   `json:"model,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	Steps       int      `json:"steps,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	Hidden      bool     `json:"hidden,omitempty"`
	Disable     bool     `json:"disable,omitempty"`
	Native      bool     `json:"native,omitempty"`
	BuiltIn     bool     `json:"builtIn,omitempty"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
	TaskPolicy  any      `json:"-"`
}

const (
	builtinAskGuidance     = "Answer codebase and architecture questions accurately using read-only inspection tools, citing exact file paths and line numbers without modifying files."
	builtinDebugGuidance   = "Diagnose bugs systematically: trace the failure path, inspect callers/callees and recent changes, verify the root cause, apply a minimal targeted fix, and run verification tests."
	builtinExploreGuidance = "Fast, strictly read-only codebase and web researcher. Batch read-only search/inspect calls concurrently and return a concise report with exact file paths and line numbers."
	builtinGeneralGuidance = "General-purpose multi-step research and workspace execution subagent. Complete the delegated work, verify outcomes, and summarize findings and changes."
	builtinScoutGuidance   = "Lightweight read-only symbol and file locator. Quickly pinpoint exact files, definitions, and entry points using AST search, glob, and grep."
)

// BuiltinAgents returns Hoxi's default primary modes and subagent profiles.
func BuiltinAgents() []AgentSpec {
	return []AgentSpec{
		{
			Name:        "build",
			Description: "Full-capability coding agent with parallel search, atomic edits, diagnostics, and sandboxed shell",
			Mode:        "primary",
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    false,
		},
		{
			Name:        "plan",
			Description: "Read-only architectural analysis and planning agent",
			Mode:        "primary",
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    true,
		},
		{
			Name:        "ask",
			Description: "Read-only codebase Q&A and architectural explanation agent",
			Mode:        "primary",
			Prompt:      builtinAskGuidance,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    true,
		},
		{
			Name:        "debug",
			Description: "Systematic root-cause diagnosis and targeted bug-fixing agent",
			Mode:        "primary",
			Prompt:      builtinDebugGuidance,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    false,
		},
		{
			Name:        "general",
			Description: "General-purpose multi-step research and execution subagent with full tool access",
			Mode:        "subagent",
			Prompt:      builtinGeneralGuidance,
			Steps:       24,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    false,
		},
		{
			Name:        "explore",
			Description: "Fast read-only codebase and web exploration subagent",
			Mode:        "subagent",
			Prompt:      builtinExploreGuidance,
			Steps:       12,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    true,
		},
		{
			Name:        "scout",
			Description: "Lightweight read-only symbol and file locator subagent",
			Mode:        "subagent",
			Prompt:      builtinScoutGuidance,
			Steps:       8,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    true,
		},
	}
}

// LoadAgents loads and merges built-in, Markdown-file, and JSON-configured agents.
func LoadAgents(worktreeDir string, extraCfg map[string]any) ([]AgentSpec, map[string]AgentSpec) {
	byKey := make(map[string]AgentSpec)
	order := make([]string, 0, 12)
	put := func(spec AgentSpec) {
		k := strings.ToLower(strings.TrimSpace(spec.Name))
		if k == "" {
			return
		}
		if _, exists := byKey[k]; !exists {
			order = append(order, k)
		}
		byKey[k] = spec
	}
	for _, a := range BuiltinAgents() {
		put(a)
	}

	home, _ := os.UserHomeDir()
	hoxiHome := hoxipaths.Home()
	worktreeDir = strings.TrimSpace(worktreeDir)

	type mdSource struct {
		dir         string
		defaultMode string
	}
	var mdSources []mdSource
	if home != "" {
		mdSources = append(mdSources,
			mdSource{filepath.Join(home, ".claude", "agents"), "subagent"},
			mdSource{filepath.Join(home, ".config", "opencode", "agent"), "all"},
			mdSource{filepath.Join(home, ".config", "opencode", "agents"), "all"},
		)
	}
	if hoxiHome != "" {
		mdSources = append(mdSources, mdSource{filepath.Join(hoxiHome, "agents"), "all"})
	}
	if customDir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); customDir != "" {
		mdSources = append(mdSources,
			mdSource{filepath.Join(customDir, "agent"), "all"},
			mdSource{filepath.Join(customDir, "agents"), "all"},
		)
	}
	if worktreeDir != "" {
		mdSources = append(mdSources,
			mdSource{filepath.Join(worktreeDir, ".claude", "agents"), "subagent"},
			mdSource{filepath.Join(worktreeDir, ".opencode", "agent"), "all"},
			mdSource{filepath.Join(worktreeDir, ".opencode", "agents"), "all"},
			mdSource{filepath.Join(worktreeDir, ".hoxi", "agents"), "all"},
		)
	}
	for _, src := range mdSources {
		for _, spec := range scanMarkdownAgentDir(src.dir, src.defaultMode) {
			if prev, exists := byKey[strings.ToLower(spec.Name)]; exists && prev.BuiltIn {
				spec.BuiltIn = true
			}
			put(spec)
		}
	}

	var jsonPaths []string
	if home != "" {
		jsonPaths = append(jsonPaths, filepath.Join(home, ".config", "opencode", "opencode.json"))
	}
	if hoxiHome != "" {
		jsonPaths = append(jsonPaths, filepath.Join(hoxiHome, "models.json"))
	}
	if worktreeDir != "" {
		jsonPaths = append(jsonPaths,
			filepath.Join(worktreeDir, "opencode.json"),
			filepath.Join(worktreeDir, ".hoxi", "models.json"),
		)
	}
	for _, p := range jsonPaths {
		if doc := readJSONMapFile(p); doc != nil {
			mergeConfigAgents(byKey, &order, doc)
		}
	}
	if extraCfg != nil {
		mergeConfigAgents(byKey, &order, extraCfg)
	}

	active := make([]AgentSpec, 0, len(order))
	for _, k := range order {
		spec, ok := byKey[k]
		if !ok || spec.Disable {
			continue
		}
		active = append(active, spec)
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].BuiltIn != active[j].BuiltIn {
			return active[i].BuiltIn
		}
		if active[i].BuiltIn {
			return false
		}
		return active[i].Name < active[j].Name
	})
	return active, byKey
}

// IsSubagentAllowed evaluates global `permission.task` and primary-agent `permission.task` rules.
func IsSubagentAllowed(primaryMode, targetSubagent string, byKey map[string]AgentSpec, cfgDoc map[string]any) bool {
	targetLower := strings.ToLower(strings.TrimSpace(targetSubagent))
	if spec, ok := byKey[targetLower]; ok && spec.Disable {
		return false
	}
	if pSpec, ok := byKey[strings.ToLower(strings.TrimSpace(primaryMode))]; ok && pSpec.TaskPolicy != nil {
		switch pol := pSpec.TaskPolicy.(type) {
		case string:
			if strings.EqualFold(pol, "deny") {
				return false
			}
			if strings.EqualFold(pol, "allow") || strings.EqualFold(pol, "ask") {
				return true
			}
		case map[string]any:
			decision := matchWildcardPolicy(pol, targetSubagent)
			if decision != "" {
				return !strings.EqualFold(decision, "deny")
			}
		}
	}
	if cfgDoc != nil {
		if perm, ok := cfgDoc["permission"].(map[string]any); ok {
			if taskPol, ok := perm["task"].(string); ok && strings.EqualFold(taskPol, "deny") {
				return false
			}
		}
	}
	return true
}

// SplitModelRef splits "providerID/modelID" into provider and model (preserving slashes inside modelID).
func SplitModelRef(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if idx := strings.IndexByte(s, '#'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash >= len(s)-1 {
		return "", s
	}
	return strings.TrimSpace(s[:slash]), strings.TrimSpace(s[slash+1:])
}

func matchWildcardPolicy(rules map[string]any, name string) string {
	if action, ok := rules[name].(string); ok && action != "" {
		return action
	}
	for k, v := range rules {
		if strings.EqualFold(k, name) {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	for pattern, v := range rules {
		if pattern == "*" {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if matched, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(name)); matched {
			return s
		}
	}
	if star, ok := rules["*"].(string); ok {
		return star
	}
	return ""
}

func mergeConfigAgents(byKey map[string]AgentSpec, order *[]string, doc map[string]any) {
	rawAgents, ok := doc["agent"].(map[string]any)
	if !ok {
		return
	}
	for rawID, rawVal := range rawAgents {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if rawVal == nil {
			if prev, exists := byKey[key]; exists && !prev.BuiltIn {
				delete(byKey, key)
			}
			continue
		}
		m, ok := rawVal.(map[string]any)
		if !ok {
			continue
		}
		spec, exists := byKey[key]
		if !exists {
			spec = AgentSpec{
				Name:   id,
				Mode:   "all",
				Native: true,
			}
			*order = append(*order, key)
		}
		if desc := firstNonEmptyString(m["description"]); desc != "" {
			spec.Description = desc
		}
		if mode := normalizeAgentMode(firstNonEmptyString(m["mode"]), spec.Mode); mode != "" {
			spec.Mode = mode
		}
		if model := firstNonEmptyString(m["model"]); model != "" {
			spec.Model = model
		}
		if prompt := firstNonEmptyString(m["prompt"], m["system"]); prompt != "" {
			spec.Prompt = prompt
		}
		if steps := intVal(m["steps"]); steps > 0 {
			spec.Steps = steps
		} else if maxSteps := intVal(m["maxSteps"]); maxSteps > 0 {
			spec.Steps = maxSteps
		}
		if h, ok := m["hidden"].(bool); ok {
			spec.Hidden = h
		}
		if d, ok := m["disable"].(bool); ok {
			spec.Disable = d
		}
		if perm, ok := m["permission"].(map[string]any); ok {
			if editPol, ok := perm["edit"].(string); ok {
				if strings.EqualFold(editPol, "deny") {
					spec.ReadOnly = true
				} else if strings.EqualFold(editPol, "allow") || strings.EqualFold(editPol, "ask") {
					spec.ReadOnly = false
				}
			}
			if tp, hasTask := perm["task"]; hasTask {
				spec.TaskPolicy = tp
			}
		}
		if toolsMap, ok := m["tools"].(map[string]any); ok {
			if editOn, hasEdit := toolsMap["edit"].(bool); hasEdit && !editOn {
				if writeOn, hasWrite := toolsMap["write"].(bool); !hasWrite || !writeOn {
					spec.ReadOnly = true
				}
			}
		}
		byKey[key] = spec
	}
}

func scanMarkdownAgentDir(rootDir, defaultMode string) []AgentSpec {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return nil
	}
	var specs []AgentSpec
	_ = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if path != rootDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		stem := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		fm, body := parseSimpleFrontmatter(string(raw))
		name := strings.TrimSpace(fm["name"])
		if name == "" {
			name = stem
		}
		if name == "" {
			return nil
		}
		prompt := strings.TrimSpace(body)
		desc := strings.TrimSpace(fm["description"])
		if desc == "" {
			desc = firstMarkdownSummaryLine(prompt)
		}
		mode := normalizeAgentMode(fm["mode"], defaultMode)
		steps, _ := strconv.Atoi(firstNonEmptyStrVal(fm["steps"], fm["maxSteps"]))
		readOnly := isFrontmatterReadOnly(fm)
		var temp *float64
		if tf, err := strconv.ParseFloat(strings.TrimSpace(fm["temperature"]), 64); err == nil {
			temp = &tf
		}
		specs = append(specs, AgentSpec{
			Name:        name,
			Description: desc,
			Mode:        mode,
			Model:       strings.TrimSpace(fm["model"]),
			Prompt:      prompt,
			Steps:       steps,
			Temperature: temp,
			Hidden:      strings.EqualFold(strings.TrimSpace(fm["hidden"]), "true"),
			Disable:     strings.EqualFold(strings.TrimSpace(fm["disable"]), "true"),
			Native:      true,
			ReadOnly:    readOnly,
		})
		return nil
	})
	return specs
}

func normalizeAgentMode(raw, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "primary":
		return "primary"
	case "subagent":
		return "subagent"
	case "all":
		return "all"
	default:
		return fallback
	}
}

func isFrontmatterReadOnly(fm map[string]string) bool {
	if strings.EqualFold(strings.TrimSpace(fm["permission.edit"]), "deny") ||
		strings.EqualFold(strings.TrimSpace(fm["tools.edit"]), "false") {
		return true
	}
	if toolsLine := strings.ToLower(strings.TrimSpace(fm["tools"])); toolsLine != "" {
		if !strings.Contains(toolsLine, "write") &&
			!strings.Contains(toolsLine, "edit") &&
			!strings.Contains(toolsLine, "bash") &&
			!strings.Contains(toolsLine, "patch") {
			return true
		}
	}
	return false
}

func parseSimpleFrontmatter(content string) (map[string]string, string) {
	out := make(map[string]string)
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(norm, "---\n") {
		return out, content
	}
	rest := norm[4:]
	endIdx := strings.Index(rest, "\n---\n")
	sepLen := 5
	if endIdx < 0 && strings.HasSuffix(rest, "\n---") {
		endIdx = len(rest) - 4
		sepLen = 4
	}
	if endIdx < 0 {
		return out, content
	}
	header := rest[:endIdx]
	body := rest[endIdx+sepLen:]
	section := ""
	for _, rawLine := range strings.Split(header, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indented := len(rawLine) > 0 && (rawLine[0] == ' ' || rawLine[0] == '\t')
		colon := strings.IndexByte(trimmed, ':')
		if colon <= 0 {
			continue
		}
		k := strings.TrimSpace(trimmed[:colon])
		v := strings.Trim(strings.TrimSpace(trimmed[colon+1:]), `"'`)
		if !indented {
			if v == "" {
				section = strings.ToLower(k)
				continue
			}
			section = ""
			out[k] = v
		} else if section != "" {
			out[section+"."+strings.ToLower(k)] = v
		}
	}
	return out, body
}

func firstMarkdownSummaryLine(s string) string {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>-* "))
		if t == "" {
			continue
		}
		if len(t) > 120 {
			return t[:117] + "..."
		}
		return t
	}
	return ""
}

func readJSONMapFile(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	return doc
}

func firstNonEmptyString(vals ...any) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstNonEmptyStrVal(vals ...string) string {
	for _, s := range vals {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func boolVal(v any) bool {
	b, _ := v.(bool)
	return b
}

func intVal(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}
