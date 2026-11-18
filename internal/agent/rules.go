package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// DefaultProjectRulesBudgetBytes is the UTF-8-safe total character budget for
	// project, global, ancestor, explicit instruction, and @file rule injection.
	// Sized well above Hoxi's own AGENTS.md (>12 KB) and typical monorepo rule sets.
	DefaultProjectRulesBudgetBytes = 96 * 1024

	// DefaultSubdirRuleBudgetBytes bounds newly discovered subdirectory rule files
	// attached to file tool outputs during a turn.
	DefaultSubdirRuleBudgetBytes = 32 * 1024

	maxRuleIncludeDepth = 3
)

var (
	ruleAtFileRefPattern = regexp.MustCompile(`(?:^|[\s(])@([A-Za-z0-9_./~-]+\.(?:md|mdc|txt))\b`)
	projectRuleFilenames = []string{"AGENTS.md", "CLAUDE.md", ".claude/CLAUDE.md", "CONTEXT.md"}
	subdirRuleFilenames  = []string{"AGENTS.md", "CLAUDE.md", ".claude/CLAUDE.md"}
)

// RulesLoadResult captures the rendered XML block and budget diagnostics for
// loaded global, ancestor, root, config-specified, and @referenced rules.
type RulesLoadResult struct {
	Rendered       string
	LoadedFiles    []string
	TruncatedFiles []string
	TotalBytes     int
	BudgetBytes    int
	OverBudget     bool
}

// SubdirRuleTracker deduplicates loaded rule file real paths across a turn so
// root rules and previously injected package rules are never repeated.
type SubdirRuleTracker struct {
	mu          sync.Mutex
	seenReal    map[string]bool
	usedBytes   int
	budgetBytes int
}

// NewSubdirRuleTracker creates a turn-scoped rule tracker seeded with already-loaded paths.
func NewSubdirRuleTracker(seedPaths []string, budgetBytes int) *SubdirRuleTracker {
	if budgetBytes <= 0 {
		budgetBytes = DefaultSubdirRuleBudgetBytes
	}
	seen := make(map[string]bool, len(seedPaths))
	for _, p := range seedPaths {
		if key := canonicalRuleKey(p); key != "" {
			seen[key] = true
		}
	}
	return &SubdirRuleTracker{
		seenReal:    seen,
		budgetBytes: budgetBytes,
	}
}

func canonicalRuleKey(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(trimmed); err == nil && real != "" {
		return filepath.Clean(real)
	}
	if abs, err := filepath.Abs(trimmed); err == nil && abs != "" {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

func resolveRulesBudgetBytes(cfgDoc map[string]any) int {
	if cfgDoc != nil {
		if v := positiveIntField(cfgDoc["rules_max_bytes"]); v > 0 {
			return v
		}
		if exp, ok := cfgDoc["experimental"].(map[string]any); ok {
			if v := positiveIntField(exp["rules_max_bytes"]); v > 0 {
				return v
			}
		}
	}
	return DefaultProjectRulesBudgetBytes
}

func positiveIntField(raw any) int {
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func truncateUTF8Safe(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", len(s) > 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " \t\r\n"), true
}

type ruleCandidate struct {
	label   string
	absPath string
	scope   string // "global", "ancestor", "project", "instructions", "include"
}

// loadProjectRules resolves rules in deterministic precedence order:
//  1. Global user rules (~/.hoxi/AGENTS.md, ~/.config/opencode/AGENTS.md, ~/.claude/CLAUDE.md)
//  2. Ancestor directory rules from repository root down to worktreeDir
//  3. Workspace root rules (AGENTS.md, CLAUDE.md, .claude/CLAUDE.md, CONTEXT.md)
//  4. Explicit config `instructions` paths and globs
//  5. Explicit `@path/to/file.md` includes inside loaded rule documents
//
// Higher-specificity project rules appear after global rules so model recency
// favors workspace-local instructions while preserving global defaults.
func loadProjectRules(worktreeDir string, cfgDoc map[string]any) RulesLoadResult {
	budget := resolveRulesBudgetBytes(cfgDoc)
	res := RulesLoadResult{BudgetBytes: budget}

	var candidates []ruleCandidate
	seenKey := make(map[string]bool)

	addCandidate := func(absPath, label, scope string) {
		key := canonicalRuleKey(absPath)
		if key == "" || seenKey[key] {
			return
		}
		st, err := os.Stat(key)
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			return
		}
		if isSensitiveRuleTarget(key) {
			return
		}
		seenKey[key] = true
		candidates = append(candidates, ruleCandidate{
			label:   label,
			absPath: key,
			scope:   scope,
		})
	}

	// 1. Global user rule files (skip when HOME points inside worktreeDir during isolated tests unless distinct).
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		globalPaths := []struct {
			rel   string
			label string
		}{
			{filepath.Join(".hoxi", "AGENTS.md"), "~/.hoxi/AGENTS.md"},
			{filepath.Join(".config", "opencode", "AGENTS.md"), "~/.config/opencode/AGENTS.md"},
			{filepath.Join(".claude", "CLAUDE.md"), "~/.claude/CLAUDE.md"},
		}
		for _, gp := range globalPaths {
			addCandidate(filepath.Join(home, gp.rel), gp.label, "global")
		}
	}

	// 2 & 3. Ancestor chain from enclosing VCS root down to worktreeDir.
	cleanWT := strings.TrimSpace(worktreeDir)
	if cleanWT != "" {
		if absWT, err := filepath.Abs(cleanWT); err == nil {
			cleanWT = absWT
		}
		chain := ancestorRuleDirs(cleanWT)
		for idx, dir := range chain {
			scope := "ancestor"
			if idx == len(chain)-1 {
				scope = "project"
			}
			for _, name := range projectRuleFilenames {
				full := filepath.Join(dir, filepath.FromSlash(name))
				lbl := name
				if scope == "ancestor" {
					if rel, rErr := filepath.Rel(dir, cleanWT); rErr == nil && rel != "." {
						lbl = filepath.ToSlash(filepath.Join(filepath.Base(dir), name))
					}
				}
				addCandidate(full, lbl, scope)
			}
		}
	}

	// 4. Explicit config `instructions` entries (paths & globs).
	for _, pat := range extractConfigInstructions(cfgDoc) {
		for _, resolved := range expandInstructionPattern(cleanWT, pat) {
			lbl := displayRuleLabel(cleanWT, resolved)
			addCandidate(resolved, lbl, "instructions")
		}
	}

	// Expand @file references depth-first right after each referencing file.
	expanded := expandRuleIncludes(cleanWT, candidates, seenKey)

	// Measure total bytes and prioritize project/instructions/include if global+all exceeds budget,
	// while still rendering in clean global -> ancestor -> project -> instructions order.
	type loadedDoc struct {
		cand ruleCandidate
		text string
	}
	docs := make([]loadedDoc, 0, len(expanded))
	for _, c := range expanded {
		raw, err := os.ReadFile(c.absPath)
		if err != nil {
			continue
		}
		txt := strings.TrimSpace(string(raw))
		if txt == "" {
			continue
		}
		res.TotalBytes += len(txt)
		docs = append(docs, loadedDoc{cand: c, text: txt})
	}
	if len(docs) == 0 {
		return res
	}

	// Reserve budget in priority order (project & instructions first, then ancestor, then global)
	// so critical project-root rules (including the tail of large AGENTS.md files) are never squeezed
	// out by global boilerplate, while keeping output ordering natural.
	alloc := make([]string, len(docs))
	truncated := make([]bool, len(docs))
	priorityOrder := make([]int, len(docs))
	for i := range docs {
		priorityOrder[i] = i
	}
	scopeRank := func(scope string) int {
		switch scope {
		case "project":
			return 0
		case "instructions", "include":
			return 1
		case "ancestor":
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(priorityOrder, func(i, j int) bool {
		return scopeRank(docs[priorityOrder[i]].cand.scope) < scopeRank(docs[priorityOrder[j]].cand.scope)
	})

	remaining := budget
	for _, idx := range priorityOrder {
		res.LoadedFiles = append(res.LoadedFiles, docs[idx].cand.absPath)
		if remaining <= 0 {
			res.OverBudget = true
			truncated[idx] = true
			res.TruncatedFiles = append(res.TruncatedFiles, docs[idx].cand.label)
			continue
		}
		sliced, wasTrunc := truncateUTF8Safe(docs[idx].text, remaining)
		alloc[idx] = sliced
		remaining -= len(sliced)
		if wasTrunc {
			res.OverBudget = true
			truncated[idx] = true
			res.TruncatedFiles = append(res.TruncatedFiles, docs[idx].cand.label)
		}
	}

	var b strings.Builder
	for idx, d := range docs {
		if alloc[idx] == "" {
			continue
		}
		fmt.Fprintf(&b, "<project_instructions file=%q scope=%q>\n", d.cand.label, d.cand.scope)
		b.WriteString(alloc[idx])
		if truncated[idx] {
			b.WriteString("\n...[rule truncated at context budget]")
		}
		b.WriteString("\n</project_instructions>\n\n")
	}
	if res.OverBudget {
		fmt.Fprintf(&b, "<rules_budget_notice budget_bytes=\"%d\" total_bytes=\"%d\" truncated_files=%q>\n",
			res.BudgetBytes, res.TotalBytes, strings.Join(res.TruncatedFiles, ", "))
		fmt.Fprintf(&b, "Project instruction files (%d bytes) exceeded the %d-byte system prompt rule budget; truncated: %s. Use the `read` tool if you need the omitted sections.",
			res.TotalBytes, res.BudgetBytes, strings.Join(res.TruncatedFiles, ", "))
		b.WriteString("\n</rules_budget_notice>\n\n")
	}
	res.Rendered = strings.TrimSpace(b.String())
	return res
}

func ancestorRuleDirs(worktreeDir string) []string {
	clean := filepath.Clean(worktreeDir)
	stopAt := clean
	// Walk upward only to the nearest enclosing VCS/workspace root (.git or .hq)
	// so temp directories or unrelated parent folders above the repo are not swept.
	cur := clean
	for {
		if hasRepoBoundaryMarker(cur) {
			stopAt = cur
			// Continue checking if this package sits inside a monorepo parent with .git/.hq.
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		// Stop climbing above user home directory.
		if home, err := os.UserHomeDir(); err == nil && home != "" && filepath.Clean(parent) == filepath.Clean(home) {
			break
		}
		cur = parent
	}
	var rev []string
	for d := clean; ; d = filepath.Dir(d) {
		rev = append(rev, d)
		if d == stopAt {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func hasRepoBoundaryMarker(dir string) bool {
	for _, marker := range []string{".git", ".hq"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func extractConfigInstructions(cfgDoc map[string]any) []string {
	if cfgDoc == nil {
		return nil
	}
	raw, ok := cfgDoc["instructions"]
	if !ok || raw == nil {
		return nil
	}
	var out []string
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	case []string:
		for _, item := range v {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

func expandInstructionPattern(worktreeDir, pat string) []string {
	pat = strings.TrimSpace(pat)
	if pat == "" || strings.HasPrefix(pat, "http://") || strings.HasPrefix(pat, "https://") {
		return nil
	}
	if strings.HasPrefix(pat, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			pat = filepath.Join(home, pat[2:])
		}
	}
	hasGlob := strings.ContainsAny(pat, "*?[")
	if !hasGlob {
		target := pat
		if !filepath.IsAbs(target) {
			if worktreeDir == "" {
				return nil
			}
			target = filepath.Join(worktreeDir, filepath.FromSlash(target))
		}
		if st, err := os.Stat(target); err == nil && st.Mode().IsRegular() {
			return []string{target}
		}
		return nil
	}
	if filepath.IsAbs(pat) && !strings.Contains(pat, "**") {
		matches, _ := filepath.Glob(pat)
		sort.Strings(matches)
		return matches
	}
	if worktreeDir == "" {
		return nil
	}
	relPat := filepath.ToSlash(pat)
	if filepath.IsAbs(pat) {
		if rel, err := filepath.Rel(worktreeDir, pat); err == nil && !strings.HasPrefix(rel, "..") {
			relPat = filepath.ToSlash(rel)
		} else {
			return nil
		}
	}
	var matches []string
	_ = filepath.WalkDir(worktreeDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipSearchDir(d.Name()) && p != worktreeDir {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(worktreeDir, p)
		if err != nil {
			return nil
		}
		if matchGlob(relPat, filepath.ToSlash(rel)) {
			matches = append(matches, p)
		}
		return nil
	})
	sort.Strings(matches)
	return matches
}

func expandRuleIncludes(worktreeDir string, base []ruleCandidate, seenKey map[string]bool) []ruleCandidate {
	var out []ruleCandidate
	var visit func(c ruleCandidate, depth int)
	visit = func(c ruleCandidate, depth int) {
		out = append(out, c)
		if depth >= maxRuleIncludeDepth {
			return
		}
		raw, err := os.ReadFile(c.absPath)
		if err != nil {
			return
		}
		matches := ruleAtFileRefPattern.FindAllStringSubmatch(string(raw), -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			ref := strings.TrimSpace(m[1])
			resolved := resolveAtFileInclude(worktreeDir, filepath.Dir(c.absPath), ref)
			if resolved == "" || isSensitiveRuleTarget(resolved) {
				continue
			}
			key := canonicalRuleKey(resolved)
			if key == "" || seenKey[key] {
				continue
			}
			st, sErr := os.Stat(key)
			if sErr != nil || !st.Mode().IsRegular() || st.Size() == 0 {
				continue
			}
			seenKey[key] = true
			visit(ruleCandidate{
				label:   displayRuleLabel(worktreeDir, key),
				absPath: key,
				scope:   "include",
			}, depth+1)
		}
	}
	for _, c := range base {
		visit(c, 0)
	}
	return out
}

func resolveAtFileInclude(worktreeDir, baseDir, ref string) string {
	if strings.HasPrefix(ref, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cand := filepath.Join(home, filepath.FromSlash(ref[2:]))
			if st, sErr := os.Stat(cand); sErr == nil && st.Mode().IsRegular() {
				return cand
			}
		}
		return ""
	}
	if filepath.IsAbs(ref) {
		return ref
	}
	for _, root := range []string{baseDir, worktreeDir} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		cand := filepath.Join(root, filepath.FromSlash(ref))
		if worktreeDir != "" && !isRealPathWithin(worktreeDir, cand) && !isLexicalPathWithin(baseDir, cand) {
			continue
		}
		if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
			return cand
		}
	}
	return ""
}

func displayRuleLabel(worktreeDir, absPath string) string {
	if worktreeDir != "" {
		if rel, err := filepath.Rel(worktreeDir, absPath); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, rErr := filepath.Rel(home, absPath); rErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return filepath.Base(absPath)
}

func isSensitiveRuleTarget(absPath string) bool {
	base := strings.ToLower(filepath.Base(absPath))
	if base == "agents.md" || base == "claude.md" || base == "context.md" {
		return false
	}
	return isSensitiveAgentPathLexical(absPath)
}

// appendSubdirectoryRules discovers any nested AGENTS.md / CLAUDE.md / .claude/CLAUDE.md
// files along the directory path between tc.WorktreeDir and targetFilePath that have
// not yet been injected during the current turn, appending them cleanly to tool output.
func appendSubdirectoryRules(tc ToolContext, targetFilePath, baseOutput string) string {
	if tc.RulesTracker == nil || strings.TrimSpace(tc.WorktreeDir) == "" || strings.TrimSpace(targetFilePath) == "" {
		return baseOutput
	}
	wtClean := canonicalRuleKey(tc.WorktreeDir)
	targetDir := filepath.Dir(targetFilePath)
	if st, err := os.Stat(targetFilePath); err == nil && st.IsDir() {
		targetDir = targetFilePath
	}
	dirClean := canonicalRuleKey(targetDir)
	if wtClean == "" || dirClean == "" || dirClean == wtClean || !isLexicalPathWithin(wtClean, dirClean) {
		return baseOutput
	}

	var relDirs []string
	for cur := dirClean; cur != wtClean && isLexicalPathWithin(wtClean, cur); cur = filepath.Dir(cur) {
		relDirs = append(relDirs, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
	}
	for i, j := 0, len(relDirs)-1; i < j; i, j = i+1, j-1 {
		relDirs[i], relDirs[j] = relDirs[j], relDirs[i]
	}

	var blocks []string
	for _, dir := range relDirs {
		for _, name := range subdirRuleFilenames {
			cand := filepath.Join(dir, filepath.FromSlash(name))
			key := canonicalRuleKey(cand)
			if key == "" {
				continue
			}
			st, err := os.Stat(key)
			if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
				continue
			}

			tc.RulesTracker.mu.Lock()
			if tc.RulesTracker.seenReal[key] {
				tc.RulesTracker.mu.Unlock()
				continue
			}
			tc.RulesTracker.seenReal[key] = true
			tc.RulesTracker.mu.Unlock()

			raw, rErr := os.ReadFile(key)
			if rErr != nil {
				continue
			}
			txt := strings.TrimSpace(string(raw))
			if txt == "" {
				continue
			}
			lbl := displayRuleLabel(wtClean, key)

			tc.RulesTracker.mu.Lock()
			avail := tc.RulesTracker.budgetBytes - tc.RulesTracker.usedBytes
			sliced, wasTrunc := truncateUTF8Safe(txt, avail)
			tc.RulesTracker.usedBytes += len(sliced)
			tc.RulesTracker.mu.Unlock()

			if sliced == "" {
				blocks = append(blocks, fmt.Sprintf("<rules_budget_notice file=%q>\nSubdirectory instructions (%d bytes) omitted because the %d-byte subdirectory rule budget was reached. Use `read` on %s if needed.\n</rules_budget_notice>", lbl, len(txt), tc.RulesTracker.budgetBytes, lbl))
				continue
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "<subdirectory_instructions file=%q>\n%s", lbl, sliced)
			if wasTrunc {
				sb.WriteString("\n...[rule truncated at subdirectory context budget]")
			}
			sb.WriteString("\n</subdirectory_instructions>")
			blocks = append(blocks, sb.String())
		}
	}

	if len(blocks) == 0 {
		return baseOutput
	}
	return baseOutput + "\n\n" + strings.Join(blocks, "\n\n")
}

func drainSteerMessages(steer SteerSource) []Message {
	if steer == nil {
		return nil
	}
	if bs, ok := steer.(SteerBlockSource); ok {
		return bs.DrainMessages()
	}
	steers := steer.Drain()
	if len(steers) == 0 {
		return nil
	}
	var out []Message
	for _, s := range steers {
		if strings.TrimSpace(s) != "" {
			out = append(out, Message{
				Role:    RoleUser,
				Content: []ContentBlock{{Type: ContentText, Text: s}},
			})
		}
	}
	return out
}
