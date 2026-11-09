package runner

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	MaxMemoryDocumentBytes   = 256 << 10
	maxCustomLinesPerSection = 64
	autoSessionMemoryHeading = "# Hoxi Session Memory (8-Dimension Compaction)"
)

var numberedListItemRe = regexp.MustCompile(`^\d+\.\s+`)

// ErrNoPromotableRules indicates that session memory contained no promotable rules or notes.
var ErrNoPromotableRules = errors.New("no promotable rules or notes found in session memory")

// ErrInvalidMemoryPath indicates that the memory file path could not be resolved safely within the project root.
var ErrInvalidMemoryPath = errors.New("could not resolve memory file path")

func SanitizeMemorySessionID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 128 {
		return ""
	}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return ""
	}
	return trimmed
}

func resolveMemoryPathWithSymlinks(p string, depth int) (string, bool) {
	if depth > 40 {
		return "", false
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		if abs, err := filepath.Abs(real); err == nil {
			return filepath.Clean(abs), true
		}
		return filepath.Clean(real), true
	}
	suffix := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if abs, err := filepath.Abs(real); err == nil {
				real = abs
			}
			return filepath.Clean(filepath.Join(real, suffix)), true
		}
		info, err := os.Lstat(cur)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(cur)
				if err != nil {
					return "", false
				}
				if !filepath.IsAbs(link) {
					link = filepath.Join(filepath.Dir(cur), link)
				}
				return resolveMemoryPathWithSymlinks(filepath.Join(link, suffix), depth+1)
			}
		} else if !os.IsNotExist(err) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, true
		}
		suffix = filepath.Join(filepath.Base(cur), suffix)
		cur = parent
	}
}

func isMemoryPathWithinRoot(root, target string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(target) == "" {
		return false
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	cleanRoot, rootOK := resolveMemoryPathWithSymlinks(root, 0)
	if !rootOK {
		return false
	}
	cleanTarget, targetOK := resolveMemoryPathWithSymlinks(target, 0)
	if !targetOK {
		return false
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return false
	}
	slash := filepath.ToSlash(rel)
	return slash != "." && slash != ".." && !strings.HasPrefix(slash, "../") && !filepath.IsAbs(rel)
}

func sessionMemoryBaselineFile(projectRoot, sessionID string) string {
	sid := SanitizeMemorySessionID(sessionID)
	base := hoxipaths.ProjectMemoryDir(projectRoot)
	if base == "" || sid == "" {
		return ""
	}
	return filepath.Join(base, ".baseline", sid+".md")
}

// SaveMemoryDocument atomically persists a project or session memory document
// under <projectRoot>/.hoxi/memory, coordinated by the shared runner memory mutex.
func SaveMemoryDocument(projectRoot, scope, sessionID, content string) (string, string, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return "", "", errors.New("workspace directory not found")
	}
	if len(content) > MaxMemoryDocumentBytes {
		return "", "", errors.New("memory document exceeds maximum size")
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "project"
	}
	if scope != "project" && scope != "session" {
		return "", "", errors.New("scope must be 'project' or 'session'")
	}
	sid := SanitizeMemorySessionID(sessionID)
	if scope == "session" && sid == "" {
		return "", "", errors.New("valid sessionID is required for session memory")
	}

	var targetFile string
	if scope == "project" {
		targetFile = hoxipaths.ProjectMemoryFile(projectRoot)
	} else {
		targetFile = hoxipaths.ProjectSessionMemoryFile(projectRoot, sid)
	}
	if targetFile == "" || !isMemoryPathWithinRoot(projectRoot, targetFile) {
		return "", "", ErrInvalidMemoryPath
	}

	memoryWriteMu.Lock()
	defer memoryWriteMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		return "", "", err
	}
	if !isMemoryPathWithinRoot(projectRoot, targetFile) {
		return "", "", ErrInvalidMemoryPath
	}
	worktree.EnsureGitExclude(projectRoot, ".hoxi/")
	normalized := strings.TrimRight(normalizeMemoryLF(content), " \t\n")
	if normalized != "" {
		normalized += "\n"
	}
	if scope == "session" {
		if baselineFile := sessionMemoryBaselineFile(projectRoot, sid); baselineFile != "" && isMemoryPathWithinRoot(projectRoot, baselineFile) {
			if _, err := os.Stat(baselineFile); os.IsNotExist(err) {
				if err := os.MkdirAll(filepath.Dir(baselineFile), 0o755); err == nil && isMemoryPathWithinRoot(projectRoot, baselineFile) {
					var seedBaseline []byte
					if existingRaw, readErr := os.ReadFile(targetFile); readErr == nil && len(strings.TrimSpace(string(existingRaw))) > 0 {
						seedBaseline = existingRaw
					} else {
						seedBaseline = []byte("# Hoxi Session Memory Baseline\n")
					}
					_ = hoxipaths.WriteFileAtomic(baselineFile, seedBaseline, 0o600)
				}
			}
		}
	}
	if err := hoxipaths.WriteFileAtomic(targetFile, []byte(normalized), 0o600); err != nil {
		return "", "", err
	}
	return targetFile, normalized, nil
}

// ExtractPromotableRulesFromSessionMemory extracts reusable constraints and notes
// from a session memory markdown document.
func ExtractPromotableRulesFromSessionMemory(md string) []string {
	var rules []string
	seen := make(map[string]bool)
	addRule := func(raw string) {
		clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "-"))
		clean = strings.TrimSpace(strings.TrimPrefix(clean, "User Constraint:"))
		if clean == "" || seen[clean] {
			return
		}
		lower := strings.ToLower(clean)
		if strings.HasPrefix(lower, "initial request:") ||
			strings.HasPrefix(lower, "latest request:") ||
			strings.HasPrefix(lower, "active engineering session") ||
			strings.HasPrefix(lower, "carried in quoted") ||
			strings.HasPrefix(lower, "no explicit file") ||
			strings.HasPrefix(lower, "no unresolved tool") ||
			strings.HasPrefix(lower, "executed ") ||
			strings.HasPrefix(lower, "all tracked tasks") ||
			strings.HasPrefix(lower, "ready for next") ||
			lower == "none" {
			return
		}
		seen[clean] = true
		rules = append(rules, clean)
	}

	inSection := ""
	isStandardSection := false
	for _, rawLine := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "# ") {
			inSection = ""
			isStandardSection = false
			continue
		}
		if strings.HasPrefix(line, "## ") {
			inSection = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			isStandardSection = isEightDimHeading(line)
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		if strings.HasPrefix(line, "- User Constraint:") {
			addRule(line)
			continue
		}
		if !isStandardSection {
			addRule(line)
			continue
		}
		if strings.Contains(inSection, "constraint") ||
			strings.Contains(inSection, "primary request") ||
			strings.Contains(inSection, "problem solving") ||
			strings.Contains(inSection, "errors and fixes") {
			if strings.HasPrefix(line, "- Verification:") || strings.HasPrefix(line, "- Latest Summary:") {
				addRule(strings.TrimPrefix(line, "- Latest Summary:"))
				continue
			}
			addRule(line)
		}
	}
	return rules
}

func normalizeProjectRuleKey(line string) string {
	clean := strings.TrimSpace(line)
	clean = strings.TrimSpace(strings.TrimPrefix(clean, "-"))
	clean = strings.TrimSpace(strings.TrimPrefix(clean, "User Constraint:"))
	return clean
}

// PromoteSessionMemoryToProject extracts rules from session memory and appends
// deduplicated entries to <projectRoot>/.hoxi/memory/project.md.
func PromoteSessionMemoryToProject(projectRoot, sessionID, content string) (string, string, int, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return "", "", 0, errors.New("workspace directory not found")
	}
	sid := SanitizeMemorySessionID(sessionID)

	memoryWriteMu.Lock()
	defer memoryWriteMu.Unlock()

	sourceMD := strings.TrimSpace(content)
	if sourceMD == "" && sid != "" {
		for _, cand := range []string{
			hoxipaths.ProjectSessionMemoryFile(projectRoot, sid),
			hoxipaths.LegacyProjectSessionMemoryFile(projectRoot, sid),
		} {
			if !isMemoryPathWithinRoot(projectRoot, cand) {
				continue
			}
			if raw, err := os.ReadFile(cand); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
				sourceMD = string(raw)
				break
			}
		}
	}
	rules := ExtractPromotableRulesFromSessionMemory(sourceMD)
	if len(rules) == 0 && sourceMD != "" {
		for _, line := range strings.Split(sourceMD, "\n") {
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "Session:") && !strings.HasPrefix(trimmed, "Updated:") {
				rules = append(rules, trimmed)
				if len(rules) >= 6 {
					break
				}
			}
		}
	}
	if len(rules) == 0 {
		return "", "", 0, ErrNoPromotableRules
	}

	projFile := hoxipaths.ProjectMemoryFile(projectRoot)
	if projFile == "" || !isMemoryPathWithinRoot(projectRoot, projFile) {
		return "", "", 0, ErrInvalidMemoryPath
	}
	existingBytes, err := os.ReadFile(projFile)
	if err != nil {
		legacyProj := hoxipaths.LegacyProjectMemoryFile(projectRoot)
		if isMemoryPathWithinRoot(projectRoot, legacyProj) {
			existingBytes, _ = os.ReadFile(legacyProj)
		}
	}
	existing := strings.TrimRight(normalizeMemoryLF(string(existingBytes)), " \t\n")
	if existing == "" {
		existing = "# Project Memory"
	}

	existingRuleSet := make(map[string]bool)
	for _, rawLine := range strings.Split(existing, "\n") {
		if key := normalizeProjectRuleKey(rawLine); key != "" {
			existingRuleSet[key] = true
		}
	}

	added := 0
	var b strings.Builder
	b.WriteString(existing)
	b.WriteByte('\n')
	for _, rule := range rules {
		key := normalizeProjectRuleKey(rule)
		if key == "" || existingRuleSet[key] {
			continue
		}
		existingRuleSet[key] = true
		b.WriteString("- ")
		b.WriteString(rule)
		b.WriteByte('\n')
		added++
	}
	updated := b.String()
	if len(updated) > MaxMemoryDocumentBytes {
		return "", "", 0, errors.New("memory document exceeds maximum size")
	}

	if err := os.MkdirAll(filepath.Dir(projFile), 0o755); err != nil {
		return "", "", 0, err
	}
	if !isMemoryPathWithinRoot(projectRoot, projFile) {
		return "", "", 0, ErrInvalidMemoryPath
	}
	worktree.EnsureGitExclude(projectRoot, ".hoxi/")
	if err := hoxipaths.WriteFileAtomic(projFile, []byte(updated), 0o600); err != nil {
		return "", "", 0, err
	}
	return projFile, updated, added, nil
}

func normalizeMemoryLF(s string) string {
	if strings.IndexByte(s, '\r') < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func isEightDimHeading(line string) bool {
	switch strings.TrimSpace(line) {
	case "## 1. Primary Request and Intent",
		"## 2. Key Technical Concepts",
		"## 3. Files and Code Sections",
		"## 4. Errors and Fixes",
		"## 5. Problem Solving",
		"## 6. All User Messages",
		"## 7. Pending Tasks",
		"## 8. Current Work":
		return true
	default:
		return false
	}
}

func isHeaderMetaLine(trimmedLine string) bool {
	return trimmedLine == "" ||
		trimmedLine == autoSessionMemoryHeading ||
		trimmedLine == "# Hoxi Session Memory Baseline" ||
		strings.HasPrefix(trimmedLine, "Session: ") ||
		strings.HasPrefix(trimmedLine, "Updated: ")
}

func isVolatileAutoMemoryLine(sectionHeading, trimmedLine, latestSummary string) bool {
	if isHeaderMetaLine(trimmedLine) {
		return true
	}
	switch sectionHeading {
	case "## 1. Primary Request and Intent":
		return strings.HasPrefix(trimmedLine, "- Initial Request: ") ||
			strings.HasPrefix(trimmedLine, "- Latest Request: ") ||
			strings.HasPrefix(trimmedLine, "- Active engineering session in ") ||
			strings.HasPrefix(trimmedLine, "- User Constraint: ")
	case "## 2. Key Technical Concepts":
		return strings.HasPrefix(trimmedLine, "- Runtime: ") ||
			strings.HasPrefix(trimmedLine, "- Model: ") ||
			strings.HasPrefix(trimmedLine, "- Branch: ")
	case "## 3. Files and Code Sections":
		return trimmedLine == "- No explicit file paths recorded yet." ||
			strings.HasPrefix(trimmedLine, "- `")
	case "## 4. Errors and Fixes":
		return trimmedLine == "- No unresolved tool errors recorded in this session."
	case "## 5. Problem Solving":
		return strings.HasPrefix(trimmedLine, "- Executed ") ||
			strings.HasPrefix(trimmedLine, "- Verification: ") ||
			strings.HasPrefix(trimmedLine, "- Latest Summary: ")
	case "## 6. All User Messages":
		return trimmedLine == "- None" || numberedListItemRe.MatchString(trimmedLine)
	case "## 7. Pending Tasks":
		return trimmedLine == "- All tracked tasks completed or none queued." ||
			strings.HasPrefix(trimmedLine, "- [pending] ") ||
			strings.HasPrefix(trimmedLine, "- [in_progress] ") ||
			strings.HasPrefix(trimmedLine, "- [completed] ") ||
			strings.HasPrefix(trimmedLine, "- [cancelled] ")
	case "## 8. Current Work":
		return trimmedLine == "- Ready for next instruction." ||
			strings.HasPrefix(trimmedLine, "- Addressing") ||
			(latestSummary != "" && trimmedLine == "- "+latestSummary)
	default:
		return false
	}
}

type markdownSection struct {
	heading string
	lines   []string
}

func splitMarkdownSections(block string) (headerLines []string, sections []markdownSection) {
	var cur *markdownSection
	for rest := block; rest != ""; {
		raw, tail, hasMore := strings.Cut(rest, "\n")
		if hasMore {
			rest = tail
		} else {
			rest = ""
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "## ") {
			sections = append(sections, markdownSection{heading: trimmed})
			cur = &sections[len(sections)-1]
			continue
		}
		if cur == nil {
			headerLines = append(headerLines, raw)
		} else {
			cur.lines = append(cur.lines, raw)
		}
	}
	return headerLines, sections
}

func collectTrimmedLineSet(md string, dst map[string]bool) {
	for rest := md; rest != ""; {
		line, tail, hasMore := strings.Cut(rest, "\n")
		if hasMore {
			rest = tail
		} else {
			rest = ""
		}
		if t := strings.TrimSpace(line); t != "" {
			dst[t] = true
		}
	}
}

func extractLatestSummaryLine(block string) string {
	const prefix = "- Latest Summary: "
	for rest := block; rest != ""; {
		line, tail, hasMore := strings.Cut(rest, "\n")
		if hasMore {
			rest = tail
		} else {
			rest = ""
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, prefix) {
			return strings.TrimSpace(t[len(prefix):])
		}
	}
	return ""
}

func isAgyBrainMemoryLine(trimmedLine string) bool {
	lower := strings.ToLower(filepath.ToSlash(trimmedLine))
	return strings.Contains(lower, "/.gemini/antigravity-cli/brain/") ||
		strings.Contains(lower, "/.gemini/antigravity/brain/")
}

// sanitizeSessionMemoryMarkdown strips any persisted Antigravity per-conversation
// brain/<id> file references from a session memory document before injecting it
// into a handoff prompt, so a new conversation never mistakes a prior
// conversation's brain/<id> directory for its own.
func sanitizeSessionMemoryMarkdown(md string) string {
	if md == "" || !isAgyBrainMemoryLine(md) {
		return md
	}
	var out []string
	inFilesSec := false
	keptFiles := 0
	flushEmptyFiles := func() {
		if inFilesSec && keptFiles == 0 {
			out = append(out, "- No explicit file paths recorded yet.")
		}
		inFilesSec = false
	}
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flushEmptyFiles()
			if trimmed == "## 3. Files and Code Sections" {
				inFilesSec = true
				keptFiles = 0
			}
			out = append(out, line)
			continue
		}
		if inFilesSec && strings.HasPrefix(trimmed, "- ") {
			if isAgyBrainMemoryLine(trimmed) {
				continue
			}
			keptFiles++
		} else if isAgyBrainMemoryLine(trimmed) && strings.HasPrefix(trimmed, "- `") {
			continue
		}
		out = append(out, line)
	}
	flushEmptyFiles()
	return strings.Join(out, "\n")
}
