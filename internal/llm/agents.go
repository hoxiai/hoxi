package llm

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	Extension   bool     `json:"extension,omitempty"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
	TaskPolicy  any      `json:"-"`
}

var (
	extraBuiltinAgentsMu sync.RWMutex
	extraBuiltinAgents   []AgentSpec
)

// RegisterBuiltinAgent registers an additional host-extension primary or subagent
// profile. Duplicate names (case-insensitive) replace earlier registrations in place.
func RegisterBuiltinAgent(spec AgentSpec) {
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" {
		return
	}
	if strings.TrimSpace(spec.Mode) == "" {
		spec.Mode = "primary"
	}
	spec.Native = true
	spec.BuiltIn = true
	spec.Extension = true

	extraBuiltinAgentsMu.Lock()
	defer extraBuiltinAgentsMu.Unlock()
	for i := range extraBuiltinAgents {
		if strings.EqualFold(extraBuiltinAgents[i].Name, spec.Name) {
			extraBuiltinAgents[i] = spec
			return
		}
	}
	extraBuiltinAgents = append(extraBuiltinAgents, spec)
}

// UnregisterBuiltinAgent removes a previously registered host-extension agent by name.
func UnregisterBuiltinAgent(name string) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return
	}
	extraBuiltinAgentsMu.Lock()
	defer extraBuiltinAgentsMu.Unlock()
	filtered := extraBuiltinAgents[:0]
	for _, spec := range extraBuiltinAgents {
		if !strings.EqualFold(spec.Name, trimmed) {
			filtered = append(filtered, spec)
		}
	}
	extraBuiltinAgents = filtered
}

// ExtraBuiltinAgents returns a snapshot of host-extension built-in agents.
func ExtraBuiltinAgents() []AgentSpec {
	extraBuiltinAgentsMu.RLock()
	defer extraBuiltinAgentsMu.RUnlock()
	if len(extraBuiltinAgents) == 0 {
		return nil
	}
	out := make([]AgentSpec, len(extraBuiltinAgents))
	copy(out, extraBuiltinAgents)
	return out
}

const (
	builtinAskGuidance     = "Answer codebase and architecture questions accurately using read-only inspection tools, citing exact file paths and line numbers without modifying files."
	builtinDebugGuidance   = "Diagnose bugs systematically: trace the failure path, inspect callers/callees and recent changes, verify the root cause, apply a minimal targeted fix, and run verification tests."
	builtinExploreGuidance = "Broad, strictly read-only codebase and web researcher. Batch read-only search/inspect calls concurrently and return a concise report with exact file paths and line numbers."
	builtinGeneralGuidance = "General-purpose multi-step research and workspace execution subagent. Keep initial discovery focused, start implementing edits promptly rather than exhausting steps on broad reading, verify outcomes with targeted checks, and summarize findings and changes."
	builtinScoutGuidance   = "Lightweight read-only multi-area symbol and file locator. Quickly pinpoint relevant files, definitions, and entry points across unfamiliar directories using hoxi_code_index, glob, and grep."
)

// BuiltinAgents returns Hoxi's default primary modes and subagent profiles,
// merged with any optional host-extension built-in agents.
func BuiltinAgents() []AgentSpec {
	primaries := []AgentSpec{
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
	}
	subagents := []AgentSpec{
		{
			Name:        "general",
			Description: "General-purpose multi-step research and execution subagent with full tool access",
			Mode:        "subagent",
			Prompt:      builtinGeneralGuidance,
			Steps:       36,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    false,
		},
		{
			Name:        "explore",
			Description: "Broad read-only codebase and web exploration subagent",
			Mode:        "subagent",
			Prompt:      builtinExploreGuidance,
			Steps:       12,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    true,
		},
		{
			Name:        "scout",
			Description: "Lightweight read-only multi-area symbol and file locator subagent",
			Mode:        "subagent",
			Prompt:      builtinScoutGuidance,
			Steps:       8,
			Native:      true,
			BuiltIn:     true,
			ReadOnly:    true,
		},
	}

	extras := ExtraBuiltinAgents()
	out := make([]AgentSpec, 0, len(primaries)+len(subagents)+len(extras))
	out = append(out, primaries...)
	for _, ext := range extras {
		if ext.Mode != "subagent" {
			out = append(out, ext)
		}
	}
	out = append(out, subagents...)
	for _, ext := range extras {
		if ext.Mode == "subagent" {
			out = append(out, ext)
		}
	}
	return out
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
	return EvalTaskPolicyDecision(primaryMode, targetSubagent, byKey, cfgDoc) != "deny"
}

// HasAnyAllowedSubagent reports whether primaryMode can invoke at least one
// subagent ("allow" or "ask") under the loaded agent roster and permission.task
// policy. Returning false allows the engine to omit the `task` tool schema up
// front instead of letting the model waste a step on a denied delegation.
func HasAnyAllowedSubagent(primaryMode string, byKey map[string]AgentSpec, cfgDoc map[string]any) bool {
	primaryLower := strings.ToLower(strings.TrimSpace(primaryMode))
	var effectivePolicy any
	if pSpec, ok := byKey[primaryLower]; ok && pSpec.TaskPolicy != nil {
		effectivePolicy = pSpec.TaskPolicy
	} else if cfgDoc != nil {
		if perm, ok := cfgDoc["permission"].(map[string]any); ok {
			effectivePolicy = perm["task"]
		}
	}
	if rawStr, ok := effectivePolicy.(string); ok {
		if act := normalizePolicyAction(rawStr); act == "deny" {
			return false
		}
	}
	if len(byKey) == 0 {
		if polMap, ok := effectivePolicy.(map[string]any); ok {
			return taskPolicyMapAllowsAny(polMap, nil)
		}
		return true
	}
	for _, spec := range byKey {
		if spec.Disable || strings.EqualFold(strings.TrimSpace(spec.Mode), "primary") {
			continue
		}
		if EvalTaskPolicyDecision(primaryMode, spec.Name, byKey, cfgDoc) != "deny" {
			return true
		}
	}
	if polMap, ok := effectivePolicy.(map[string]any); ok {
		return taskPolicyMapAllowsAny(polMap, byKey)
	}
	return false
}

func taskPolicyMapAllowsAny(polMap map[string]any, byKey map[string]AgentSpec) bool {
	hasCatchAllDeny := false
	hasNonDenyEntry := false
	for rawPat, rawAct := range polMap {
		pat := strings.TrimSpace(rawPat)
		actStr, ok := rawAct.(string)
		if pat == "" || !ok {
			continue
		}
		act := normalizePolicyAction(actStr)
		if pat == "*" {
			if act == "deny" {
				hasCatchAllDeny = true
			} else if act == "allow" || act == "ask" {
				hasNonDenyEntry = true
			}
			continue
		}
		if act != "allow" && act != "ask" {
			continue
		}
		if !hasGlobMeta(pat) && byKey != nil {
			if spec, exists := byKey[strings.ToLower(pat)]; exists &&
				(spec.Disable || strings.EqualFold(strings.TrimSpace(spec.Mode), "primary")) {
				continue
			}
		}
		return true
	}
	if len(byKey) == 0 {
		return !hasCatchAllDeny || hasNonDenyEntry
	}
	return false
}

// EvalTaskPolicyDecision resolves "allow" | "ask" | "deny" for delegating to
// targetSubagent from primaryMode, checking disabled state, primary-agent
// TaskPolicy, and global permission.task rules deterministically.
func EvalTaskPolicyDecision(primaryMode, targetSubagent string, byKey map[string]AgentSpec, cfgDoc map[string]any) string {
	targetLower := strings.ToLower(strings.TrimSpace(targetSubagent))
	if spec, ok := byKey[targetLower]; ok && spec.Disable {
		return "deny"
	}
	if pSpec, ok := byKey[strings.ToLower(strings.TrimSpace(primaryMode))]; ok && pSpec.TaskPolicy != nil {
		if dec := EvalWildcardPolicy(pSpec.TaskPolicy, targetSubagent); dec != "" {
			return dec
		}
	}
	if cfgDoc != nil {
		if perm, ok := cfgDoc["permission"].(map[string]any); ok {
			if dec := EvalWildcardPolicy(perm["task"], targetSubagent); dec != "" {
				return dec
			}
		}
	}
	return "allow"
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

func normalizePolicyAction(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "deny":
		return "deny"
	case "ask":
		return "ask"
	case "allow":
		return "allow"
	default:
		return ""
	}
}

func policyActionRank(act string) int {
	switch act {
	case "deny":
		return 3
	case "ask":
		return 2
	case "allow":
		return 1
	default:
		return 0
	}
}

func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// EvalWildcardPolicy evaluates a string or map-valued task permission policy
// without depending on Go map iteration order:
//  1. Exact literal target name match (no glob metacharacters, case-insensitive).
//  2. Specific wildcard patterns (excluding catch-all "*").
//  3. Catch-all "*" fallback.
//
// Within any tier where multiple entries match (e.g. overlapping wildcards such
// as "explore-*": "allow" and "*-secret": "deny"), the most restrictive action
// wins deterministically (deny > ask > allow).
func EvalWildcardPolicy(raw any, name string) string {
	switch pol := raw.(type) {
	case string:
		return normalizePolicyAction(pol)
	case map[string]any:
		trimmedName := strings.TrimSpace(name)
		lowerName := strings.ToLower(trimmedName)
		bestExact := ""
		bestWildcard := ""
		bestStar := ""
		for rawPattern, v := range pol {
			s, ok := v.(string)
			if !ok {
				continue
			}
			act := normalizePolicyAction(s)
			if act == "" {
				continue
			}
			pat := strings.TrimSpace(rawPattern)
			if pat == "" {
				continue
			}
			if pat == "*" {
				if policyActionRank(act) > policyActionRank(bestStar) {
					bestStar = act
				}
				continue
			}
			if !hasGlobMeta(pat) {
				if strings.EqualFold(pat, trimmedName) && policyActionRank(act) > policyActionRank(bestExact) {
					bestExact = act
				}
				continue
			}
			if matched, err := filepath.Match(strings.ToLower(pat), lowerName); err == nil && matched {
				if policyActionRank(act) > policyActionRank(bestWildcard) {
					bestWildcard = act
				}
			}
		}
		if bestExact != "" {
			return bestExact
		}
		if bestWildcard != "" {
			return bestWildcard
		}
		return bestStar
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
