package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	MaxMemoryDocumentBytes   = 256 << 10
	autoSessionMemoryHeading = "# Hoxi Session Memory (8-Dimension Compaction)"
)

var numberedListItemRe = regexp.MustCompile(`^\d+\.\s+`)

// ErrNoPromotableRules indicates that session memory contained no promotable rules or notes.
var ErrNoPromotableRules = errors.New("no promotable rules or notes found in session memory")

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

func isMemoryPathWithinRoot(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	slash := filepath.ToSlash(rel)
	return slash != "." && slash != ".." && !strings.HasPrefix(slash, "../")
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
		return "", "", errors.New("could not resolve memory file path")
	}

	memoryWriteMu.Lock()
	defer memoryWriteMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		return "", "", err
	}
	worktree.EnsureGitExclude(projectRoot, ".hoxi/")
	normalized := strings.TrimRight(strings.ReplaceAll(content, "\r\n", "\n"), " \t\n")
	if normalized != "" {
		normalized += "\n"
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
		return "", "", 0, errors.New("could not resolve project memory file path")
	}
	existingBytes, err := os.ReadFile(projFile)
	if err != nil {
		existingBytes, _ = os.ReadFile(hoxipaths.LegacyProjectMemoryFile(projectRoot))
	}
	existing := strings.TrimRight(strings.ReplaceAll(string(existingBytes), "\r\n", "\n"), " \t\n")
	if existing == "" {
		existing = "# Project Memory"
	}

	added := 0
	var b strings.Builder
	b.WriteString(existing)
	b.WriteByte('\n')
	for _, rule := range rules {
		if strings.Contains(existing, rule) {
			continue
		}
		b.WriteString("- ")
		b.WriteString(rule)
		b.WriteByte('\n')
		added++
	}
	updated := b.String()

	if err := os.MkdirAll(filepath.Dir(projFile), 0o755); err != nil {
		return "", "", 0, err
	}
	worktree.EnsureGitExclude(projectRoot, ".hoxi/")
	if err := hoxipaths.WriteFileAtomic(projFile, []byte(updated), 0o600); err != nil {
		return "", "", 0, err
	}
	return projFile, updated, added, nil
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

func isVolatileAutoMemoryLine(sectionHeading, trimmedLine string) bool {
	if trimmedLine == "" ||
		trimmedLine == autoSessionMemoryHeading ||
		strings.HasPrefix(trimmedLine, "Session: ") ||
		strings.HasPrefix(trimmedLine, "Updated: ") {
		return true
	}
	switch sectionHeading {
	case "## 1. Primary Request and Intent":
		return strings.HasPrefix(trimmedLine, "- Initial Request: ") ||
			strings.HasPrefix(trimmedLine, "- Latest Request: ") ||
			strings.HasPrefix(trimmedLine, "- Active engineering session in ")
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
		return true
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
	for _, raw := range strings.Split(block, "\n") {
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

// MergeSessionMemoryMarkdown preserves manual user edits (custom preambles,
// custom sections, and user-added lines within the 8-dimension sections) when
// automatic turn compaction refreshes a session memory file.
func MergeSessionMemoryMarkdown(existing, autoMD string) string {
	existingNorm := strings.TrimSpace(strings.ReplaceAll(existing, "\r\n", "\n"))
	autoNorm := strings.TrimSpace(strings.ReplaceAll(autoMD, "\r\n", "\n"))
	if existingNorm == "" {
		return autoMD
	}
	if autoNorm == "" {
		return existingNorm + "\n"
	}

	idx := strings.Index(existingNorm, autoSessionMemoryHeading)
	if idx < 0 {
		return existingNorm + "\n\n" + autoNorm + "\n"
	}

	preamble := strings.TrimSpace(existingNorm[:idx])
	existingAuto := existingNorm[idx:]

	autoLineSet := make(map[string]bool)
	for _, line := range strings.Split(autoNorm, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			autoLineSet[t] = true
		}
	}

	existingHeader, existingSections := splitMarkdownSections(existingAuto)
	var customHeaderLines []string
	for _, line := range existingHeader {
		t := strings.TrimSpace(line)
		if t != "" && !autoLineSet[t] && !isVolatileAutoMemoryLine("", t) {
			customHeaderLines = append(customHeaderLines, line)
		}
	}

	customLinesByHeading := make(map[string][]string)
	var customSections []markdownSection
	for _, sec := range existingSections {
		if !isEightDimHeading(sec.heading) {
			customSections = append(customSections, sec)
			continue
		}
		for _, line := range sec.lines {
			t := strings.TrimSpace(line)
			if t == "" || autoLineSet[t] || isVolatileAutoMemoryLine(sec.heading, t) {
				continue
			}
			customLinesByHeading[sec.heading] = append(customLinesByHeading[sec.heading], line)
		}
	}

	if preamble == "" && len(customHeaderLines) == 0 && len(customLinesByHeading) == 0 && len(customSections) == 0 {
		return autoMD
	}

	autoHeader, autoSections := splitMarkdownSections(autoNorm)
	var b strings.Builder
	if preamble != "" {
		b.WriteString(preamble)
		b.WriteString("\n\n")
	}
	for _, line := range autoHeader {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range customHeaderLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(customHeaderLines) > 0 {
		b.WriteByte('\n')
	}
	for i, sec := range autoSections {
		b.WriteString(sec.heading)
		b.WriteByte('\n')
		lines := sec.lines
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		for _, extra := range customLinesByHeading[sec.heading] {
			b.WriteString(extra)
			b.WriteByte('\n')
		}
		if i < len(autoSections)-1 || len(customSections) > 0 {
			b.WriteByte('\n')
		}
	}
	for i, sec := range customSections {
		b.WriteString(sec.heading)
		b.WriteByte('\n')
		lines := sec.lines
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		if i < len(customSections)-1 {
			b.WriteByte('\n')
		}
	}
	return fmt.Sprintf("%s\n", strings.TrimRight(b.String(), "\n"))
}
