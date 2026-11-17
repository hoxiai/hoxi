package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	seedDefaultsOnce      sync.Once
	frontmatterNameRe     = regexp.MustCompile(`(?m)^\s*name:\s*["']?([^"'\r\n]+)["']?\s*$`)
	frontmatterDescLineRe = regexp.MustCompile(`(?m)^\s*description:\s*(.+)$`)
)

// scanLocalSkills discovers SKILL.md files directly from workspace and user
// skill directories so Hoxi's skill catalog and inline skill loader work 100%
// natively without requiring an OpenCode daemon.
func scanLocalSkills(directory, projectRoot string) []OpenCodeSkillInfo {
	seedDefaultsOnce.Do(func() {
		_, _ = SeedDefaults()
	})

	var searchDirs []string
	seenDirs := make(map[string]bool)
	addSkillRoot := func(dir string) {
		c := canonicalizeDir(dir)
		if c == "" || seenDirs[c] {
			return
		}
		if st, err := os.Stat(c); err != nil || !st.IsDir() {
			return
		}
		seenDirs[c] = true
		searchDirs = append(searchDirs, c)
	}

	for _, base := range []string{directory, projectRoot} {
		if strings.TrimSpace(base) == "" {
			continue
		}
		addSkillRoot(filepath.Join(base, ".hoxi", "skills"))
		addSkillRoot(filepath.Join(base, ".agents", "skills"))
		addSkillRoot(filepath.Join(base, ".claude", "skills"))
		addSkillRoot(filepath.Join(base, ".opencode", "skills"))
	}

	if defDir := DefaultUserDir(); defDir != "" {
		addSkillRoot(defDir)
	}
	for _, r := range userSkillRoots() {
		if strings.HasSuffix(r.label, "/skills") {
			addSkillRoot(r.dir)
		} else {
			addSkillRoot(filepath.Join(r.dir, "skills"))
		}
	}

	var results []OpenCodeSkillInfo
	seenNames := make(map[string]bool)

	for _, root := range searchDirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, slug := range names {
			skillPath := filepath.Join(root, slug, "SKILL.md")
			raw, err := os.ReadFile(skillPath)
			if err != nil || len(raw) == 0 || len(raw) > maxUpstreamSkillBytes {
				continue
			}
			content := string(raw)
			name, desc := parseSkillFrontmatter(content, slug)
			if name == "" || seenNames[name] {
				continue
			}
			seenNames[name] = true
			results = append(results, OpenCodeSkillInfo{
				Name:        name,
				Description: desc,
				Location:    skillPath,
				Content:     content,
			})
		}
	}
	return results
}

func parseSkillFrontmatter(content, fallbackSlug string) (name, description string) {
	name = strings.TrimSpace(fallbackSlug)
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return name, firstParagraphSummary(normalized)
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return name, firstParagraphSummary(normalized)
	}
	header := normalized[4 : 4+end]
	body := strings.TrimSpace(normalized[4+end+4:])
	if idx := strings.IndexByte(body, '\n'); idx >= 0 && strings.HasPrefix(body, "---") {
		body = strings.TrimSpace(body[idx+1:])
	}

	if m := frontmatterNameRe.FindStringSubmatch(header); len(m) == 2 {
		if parsed := strings.TrimSpace(m[1]); parsed != "" {
			name = parsed
		}
	}
	if m := frontmatterDescLineRe.FindStringSubmatch(header); len(m) == 2 {
		rawDesc := strings.TrimSpace(m[1])
		rawDesc = strings.Trim(rawDesc, `"'`)
		if rawDesc != "" && rawDesc != "|" && rawDesc != ">" {
			description = rawDesc
		}
	}
	if description == "" {
		description = firstParagraphSummary(body)
	}
	return name, description
}

func firstParagraphSummary(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if len(t) > 200 {
			return t[:200] + "..."
		}
		return t
	}
	return ""
}
