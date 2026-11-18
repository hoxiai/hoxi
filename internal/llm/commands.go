package llm

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	maxTemplateInlineFileBytes  = 64 * 1024
	maxTemplateInlineTotalBytes = 128 * 1024
)

// SlashCommandSpec describes a built-in, JSON-configured, or Markdown-defined slash command.
type SlashCommandSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Model       string   `json:"model,omitempty"`
	Source      string   `json:"source,omitempty"`
	Template    string   `json:"template,omitempty"`
	Subtask     bool     `json:"subtask,omitempty"`
	Hints       []string `json:"hints,omitempty"`
	BuiltIn     bool     `json:"builtIn,omitempty"`
}

// ExpandedCommand holds the rendered prompt and optional agent/model overrides for a slash command.
type ExpandedCommand struct {
	Name           string
	RenderedPrompt string
	Agent          string
	Model          string
	Subtask        bool
}

const builtinInitTemplate = `Inspect this repository's directory structure, package manifests, build/lint/test scripts, architecture boundaries, and coding conventions, then create or update AGENTS.md in the workspace root.

Requirements:
1. Keep AGENTS.md concise, high-signal, and actionable for AI coding agents.
2. Document exact build, lint, typecheck, and unit-test commands verified from actual config files (package.json, go.mod, Makefile, Cargo.toml, pyproject.toml, etc.).
3. Summarize key directory responsibilities, architectural boundaries, and critical non-obvious gotchas.
4. Preserve any accurate project-specific rules already present if AGENTS.md exists.`

const builtinReviewTemplate = `Perform a thorough, read-only code review of the target changes in this workspace.

Instructions:
1. If specific files, commits, or branches are provided in "$ARGUMENTS", inspect those targets; otherwise inspect current uncommitted changes via git status and git diff (staged and unstaged), falling back to HEAD~1..HEAD if the working tree is clean.
2. Focus on real defects: logic bugs, race conditions, security/injection risks, resource leaks, broken edge cases, error handling gaps, and architectural/test regressions.
3. Cite exact file paths and line numbers for every finding, ordered by severity (P0/P1/P2).
4. Do NOT modify, stage, or commit any files unless explicitly asked.`

// BuiltinCommands returns the built-in slash commands exposed by the native engine.
func BuiltinCommands() []SlashCommandSpec {
	return []SlashCommandSpec{
		{
			Name:        "compact",
			Description: "Summarize and compact conversation context",
			Source:      "command",
			BuiltIn:     true,
		},
		{
			Name:        "init",
			Description: "Inspect repository and generate or update AGENTS.md guidelines",
			Agent:       "build",
			Source:      "command",
			Template:    builtinInitTemplate,
			Hints:       []string{"[notes]"},
			BuiltIn:     true,
		},
		{
			Name:        "review",
			Description: "Review uncommitted workspace changes or a target ref for bugs and regressions",
			Agent:       "plan",
			Source:      "command",
			Template:    builtinReviewTemplate,
			Hints:       []string{"[target]"},
			BuiltIn:     true,
		},
	}
}

// LoadCommands merges built-in, global/workspace JSON, and global/workspace Markdown commands.
func LoadCommands(worktreeDir string, extraCfg map[string]any) []SlashCommandSpec {
	byKey := make(map[string]SlashCommandSpec)
	for _, cmd := range BuiltinCommands() {
		byKey[strings.ToLower(cmd.Name)] = cmd
	}

	home, _ := os.UserHomeDir()
	hoxiHome := hoxipaths.Home()
	worktreeDir = strings.TrimSpace(worktreeDir)

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
			mergeConfigCommands(byKey, doc)
		}
	}
	if extraCfg != nil {
		mergeConfigCommands(byKey, extraCfg)
	}

	var mdDirs []string
	if home != "" {
		mdDirs = append(mdDirs,
			filepath.Join(home, ".claude", "commands"),
			filepath.Join(home, ".config", "opencode", "command"),
			filepath.Join(home, ".config", "opencode", "commands"),
		)
	}
	if hoxiHome != "" {
		mdDirs = append(mdDirs, filepath.Join(hoxiHome, "commands"))
	}
	if customDir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); customDir != "" {
		mdDirs = append(mdDirs,
			filepath.Join(customDir, "command"),
			filepath.Join(customDir, "commands"),
		)
	}
	if worktreeDir != "" {
		mdDirs = append(mdDirs,
			filepath.Join(worktreeDir, ".claude", "commands"),
			filepath.Join(worktreeDir, ".opencode", "command"),
			filepath.Join(worktreeDir, ".opencode", "commands"),
			filepath.Join(worktreeDir, ".hoxi", "commands"),
		)
	}
	for _, dir := range mdDirs {
		scanMarkdownCommandDir(byKey, dir)
	}

	out := make([]SlashCommandSpec, 0, len(byKey))
	for _, spec := range byKey {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BuiltIn != out[j].BuiltIn {
			return out[i].BuiltIn
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ExpandSlashCommand checks whether rawPrompt invokes a known slash command (excluding /compact)
// and expands its template with positional ($1..$9) and full ($ARGUMENTS) parameters.
func ExpandSlashCommand(rawPrompt, worktreeDir string, extraCfg map[string]any) (ExpandedCommand, bool) {
	trimmed := strings.TrimSpace(rawPrompt)
	if !strings.HasPrefix(trimmed, "/") || len(trimmed) < 2 {
		return ExpandedCommand{}, false
	}
	rest := trimmed[1:]
	cmdToken := rest
	rawArgs := ""
	if idx := strings.IndexAny(rest, " \t\r\n"); idx >= 0 {
		cmdToken = rest[:idx]
		rawArgs = strings.TrimSpace(rest[idx:])
	}
	cmdLower := strings.ToLower(strings.TrimSpace(cmdToken))
	if cmdLower == "" || cmdLower == "compact" || cmdLower == "goal" || cmdLower == "subtask" || cmdLower == "subtasks" {
		return ExpandedCommand{}, false
	}
	altColon := strings.ReplaceAll(cmdLower, "/", ":")

	cmds := LoadCommands(worktreeDir, extraCfg)
	var matched *SlashCommandSpec
	for i := range cmds {
		candidate := strings.ToLower(cmds[i].Name)
		if candidate == cmdLower || candidate == altColon {
			matched = &cmds[i]
			break
		}
	}
	if matched == nil || strings.TrimSpace(matched.Template) == "" {
		return ExpandedCommand{}, false
	}

	tmpl := matched.Template
	// Resolve static @<workspace-file> references from the command template BEFORE
	// interpolating user-supplied $ARGUMENTS / $1..$9 so arguments cannot inject file reads.
	inlinedBlocks := collectTemplateFileBlocks(tmpl, worktreeDir)

	posArgs := splitCommandArgs(rawArgs)
	usedPlaceholder := false

	if strings.Contains(tmpl, "$ARGUMENTS") {
		usedPlaceholder = true
		tmpl = strings.ReplaceAll(tmpl, "$ARGUMENTS", rawArgs)
	}
	for i := 9; i >= 1; i-- {
		tok := "$" + strconv.Itoa(i)
		if strings.Contains(tmpl, tok) {
			usedPlaceholder = true
			val := ""
			if i-1 < len(posArgs) {
				val = posArgs[i-1]
			}
			tmpl = strings.ReplaceAll(tmpl, tok, val)
		}
	}
	rendered := strings.TrimSpace(tmpl)
	if len(inlinedBlocks) > 0 {
		rendered = rendered + "\n\n" + strings.Join(inlinedBlocks, "\n\n")
	}
	if rawArgs != "" && !usedPlaceholder {
		rendered = rendered + "\n\nArguments: " + rawArgs
	}
	return ExpandedCommand{
		Name:           matched.Name,
		RenderedPrompt: rendered,
		Agent:          strings.TrimSpace(matched.Agent),
		Model:          strings.TrimSpace(matched.Model),
		Subtask:        matched.Subtask,
	}, true
}

func mergeConfigCommands(dst map[string]SlashCommandSpec, doc map[string]any) {
	rawCmds, ok := doc["command"].(map[string]any)
	if !ok {
		return
	}
	for rawName, val := range rawCmds {
		name := strings.TrimPrefix(strings.TrimSpace(rawName), "/")
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if val == nil {
			if prev, exists := dst[key]; exists && !prev.BuiltIn {
				delete(dst, key)
			}
			continue
		}
		m, ok := val.(map[string]any)
		if !ok {
			continue
		}
		tmpl := firstNonEmptyString(m["template"], m["prompt"])
		if tmpl == "" {
			continue
		}
		spec := SlashCommandSpec{
			Name:        name,
			Description: firstNonEmptyString(m["description"]),
			Agent:       firstNonEmptyString(m["agent"]),
			Model:       firstNonEmptyString(m["model"]),
			Source:      "command",
			Template:    tmpl,
			Subtask:     boolVal(m["subtask"]),
			Hints:       inferCommandHints(tmpl, extractStringSlice(m["hints"])),
		}
		dst[key] = spec
	}
}

func scanMarkdownCommandDir(dst map[string]SlashCommandSpec, rootDir string) {
	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return
	}
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
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		noExt := strings.TrimSuffix(filepath.ToSlash(rel), filepath.Ext(rel))
		cmdName := strings.ReplaceAll(strings.Trim(noExt, "/"), "/", ":")
		if cmdName == "" {
			return nil
		}
		fm, body := parseSimpleFrontmatter(string(raw))
		tmpl := strings.TrimSpace(body)
		if tmpl == "" {
			return nil
		}
		if explicitName := strings.TrimPrefix(strings.TrimSpace(fm["name"]), "/"); explicitName != "" {
			cmdName = explicitName
		}
		desc := strings.TrimSpace(fm["description"])
		if desc == "" {
			desc = firstMarkdownSummaryLine(tmpl)
		}
		var explicitHints []string
		if hint := firstNonEmptyStrVal(fm["argument-hint"], fm["hints"]); hint != "" {
			explicitHints = []string{hint}
		}
		dst[strings.ToLower(cmdName)] = SlashCommandSpec{
			Name:        cmdName,
			Description: desc,
			Agent:       strings.TrimSpace(fm["agent"]),
			Model:       strings.TrimSpace(fm["model"]),
			Source:      "command",
			Template:    tmpl,
			Subtask:     strings.EqualFold(strings.TrimSpace(fm["subtask"]), "true"),
			Hints:       inferCommandHints(tmpl, explicitHints),
		}
		return nil
	})
}

func inferCommandHints(tmpl string, explicit []string) []string {
	if len(explicit) > 0 {
		return explicit
	}
	if strings.Contains(tmpl, "$ARGUMENTS") || strings.Contains(tmpl, "$1") {
		return []string{"<args>"}
	}
	return nil
}

func splitCommandArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	escaped := false
	inToken := false
	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			inToken = true
			continue
		}
		if r == '\\' {
			escaped = true
			inToken = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inToken = true
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			inToken = true
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if inToken {
				out = append(out, cur.String())
				cur.Reset()
				inToken = false
			}
			continue
		}
		cur.WriteRune(r)
		inToken = true
	}
	if inToken {
		out = append(out, cur.String())
	}
	return out
}

func extractStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		if s, okStr := v.([]string); okStr {
			return s
		}
		return nil
	}
	var out []string
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func collectTemplateFileBlocks(tmpl, worktreeDir string) []string {
	worktreeDir = strings.TrimSpace(worktreeDir)
	if worktreeDir == "" || !strings.Contains(tmpl, "@") {
		return nil
	}
	realRoot, err := filepath.EvalSymlinks(worktreeDir)
	if err != nil || !filepath.IsAbs(realRoot) {
		return nil
	}
	realRoot = filepath.Clean(realRoot)

	var blocks []string
	seen := make(map[string]bool)
	remaining := maxTemplateInlineTotalBytes

	for i := 0; i < len(tmpl) && remaining > 0; i++ {
		if tmpl[i] != '@' {
			continue
		}
		if i > 0 {
			prev, _ := utf8.DecodeLastRuneInString(tmpl[:i])
			if !isTemplateRefBoundaryBefore(prev) {
				continue
			}
		}
		end := i + 1
		for end < len(tmpl) {
			r, w := utf8.DecodeRuneInString(tmpl[end:])
			if unicode.IsSpace(r) || strings.ContainsRune(",;<>\"'`()[]{}，；、。）)】》", r) {
				break
			}
			end += w
		}
		rawRef := strings.TrimRight(tmpl[i+1:end], ".:!?：！？")
		if end > i+1 {
			i = end - 1
		}
		if rawRef == "" {
			continue
		}
		cleanRel, ok := sanitizeTemplateRelativePath(rawRef)
		if !ok || seen[cleanRel] {
			continue
		}
		content, n, ok := readSafeTemplateWorkspaceFile(worktreeDir, realRoot, cleanRel, remaining)
		if !ok {
			continue
		}
		seen[cleanRel] = true
		remaining -= n
		blocks = append(blocks, "<file path=\""+cleanRel+"\">\n"+content+"\n</file>")
	}
	return blocks
}

func isTemplateRefBoundaryBefore(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("([{\"'`:：、，。；", r)
}

func sanitizeTemplateRelativePath(raw string) (string, bool) {
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "\\") || strings.HasPrefix(raw, "~") ||
		strings.ContainsAny(raw, "*?[\x00") {
		return "", false
	}
	slash := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if slash == "." || slash == "" || slash == ".." || strings.HasPrefix(slash, "../") || strings.Contains(slash, "/../") {
		return "", false
	}
	parts := strings.Split(slash, "/")
	startIdx := 0
	// Exempt workspace skill docs (.hoxi/skills/<slug>/..., .agents/skills/<slug>/..., .opencode/skills/<slug>/...) while blocking all other hidden/dot paths.
	if len(parts) >= 3 && (parts[0] == ".hoxi" || parts[0] == ".agents" || parts[0] == ".opencode") && parts[1] == "skills" && !strings.HasPrefix(parts[2], ".") {
		startIdx = 2
	}
	for _, seg := range parts[startIdx:] {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return "", false
		}
	}
	if isSecretTemplateFileName(parts[len(parts)-1]) {
		return "", false
	}
	return slash, true
}

func isSecretTemplateFileName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kdbx", ".asc", ".gpg", ".crt", ".cer", ".der", ".tfstate"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	for _, prefix := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, sub := range []string{"secret", "credential", "password", "private_key", "apikey", "api_key", "token", "auth.json", "kubeconfig"} {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

func readSafeTemplateWorkspaceFile(worktreeDir, realRoot, cleanRel string, remaining int) (string, int, bool) {
	joined := filepath.Join(worktreeDir, filepath.FromSlash(cleanRel))
	realFile, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", 0, false
	}
	realFile = filepath.Clean(realFile)
	if !strings.HasPrefix(realFile, realRoot+string(filepath.Separator)) {
		return "", 0, false
	}
	// Re-check resolved basename against secret rules so symlinks to secret files inside workspace are also refused.
	if isSecretTemplateFileName(filepath.Base(realFile)) || strings.HasPrefix(filepath.Base(realFile), ".") {
		return "", 0, false
	}
	relFromRealRoot, err := filepath.Rel(realRoot, realFile)
	if err != nil {
		return "", 0, false
	}
	if _, ok := sanitizeTemplateRelativePath(filepath.ToSlash(relFromRealRoot)); !ok {
		return "", 0, false
	}
	st, err := os.Stat(realFile)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return "", 0, false
	}
	capBytes := maxTemplateInlineFileBytes
	if remaining < capBytes {
		capBytes = remaining
	}
	f, err := os.Open(realFile)
	if err != nil {
		return "", 0, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(capBytes)+1))
	if err != nil || len(data) == 0 {
		return "", 0, false
	}
	truncated := false
	if len(data) > capBytes {
		data = data[:capBytes]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		truncated = true
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", 0, false
	}
	text := strings.TrimRight(string(data), "\r\n")
	if truncated {
		text += "\n... [truncated]"
	}
	return text, len(data), true
}
