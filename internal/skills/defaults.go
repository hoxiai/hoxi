package skills

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

//go:embed defaults/*/SKILL.md
var defaultSkillFiles embed.FS

var (
	extraSkillsMu        sync.RWMutex
	extraSkillDirs       []string
	legacyBundledSlugs   = []string{"hoxi-tools"}
	frontmatterAuthorRe  = regexp.MustCompile(`(?m)^\s*author:\s*["']?([^"'\r\n]+)["']?\s*$`)
	frontmatterVersionRe = regexp.MustCompile(`(?m)^\s*version:\s*["']?([^"'\r\n]+)["']?\s*$`)
)

// RegisterExtraSkillDir registers an additional directory of Hoxi-authored
// bundled skills (for example from a private local extension).
func RegisterExtraSkillDir(dir string) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return
	}
	extraSkillsMu.Lock()
	defer extraSkillsMu.Unlock()
	for _, existing := range extraSkillDirs {
		if existing == trimmed {
			return
		}
	}
	extraSkillDirs = append(extraSkillDirs, trimmed)
}

// RegisterLegacySkillSlug registers a retired bundled skill directory slug to
// remove if unmodified and Hoxi-authored.
func RegisterLegacySkillSlug(slug string) {
	trimmed := strings.TrimSpace(slug)
	if trimmed == "" {
		return
	}
	extraSkillsMu.Lock()
	defer extraSkillsMu.Unlock()
	for _, existing := range legacyBundledSlugs {
		if existing == trimmed {
			return
		}
	}
	legacyBundledSlugs = append(legacyBundledSlugs, trimmed)
}

// DefaultUserDir is the Hoxi-managed directory that holds bundled skills.
// Hoxi registers it in the engine config so every project can discover it.
func DefaultUserDir() string {
	return hoxipaths.Join("skills")
}

// SeedDefaults installs bundled skills into DefaultUserDir, migrates retired
// Hoxi-authored bundled skill names, upgrades outdated Hoxi-authored bundled
// skills when their embedded version advances, and preserves user-authored or
// locally customized skills.
func SeedDefaults() (int, error) {
	root := DefaultUserDir()
	if root == "" {
		return 0, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, err
	}
	cleanupLegacyBundledSkills(root)

	entries, err := fs.ReadDir(defaultSkillFiles, "defaults")
	if err != nil {
		return 0, err
	}
	seeded := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		data, err := defaultSkillFiles.ReadFile(path.Join("defaults", name, "SKILL.md"))
		if err != nil {
			return seeded, err
		}
		target := filepath.Join(root, name, "SKILL.md")
		if existing, statErr := os.ReadFile(target); statErr == nil {
			if shouldUpgradeBundledSkill(existing, data) {
				if err := replaceSkillFile(target, data); err != nil {
					return seeded, err
				}
				seeded++
			}
			continue
		} else if !os.IsNotExist(statErr) {
			return seeded, statErr
		}
		if err := writeNewSkillFile(target, data); err != nil {
			return seeded, err
		}
		seeded++
	}
	for _, dir := range candidateExternalSkillDirs() {
		if extra, err := seedSkillDirectory(root, dir); err == nil {
			seeded += extra
		}
	}
	return seeded, nil
}

func candidateExternalSkillDirs() []string {
	extraSkillsMu.RLock()
	registered := append([]string(nil), extraSkillDirs...)
	extraSkillsMu.RUnlock()

	seen := make(map[string]struct{})
	var out []string
	add := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		if abs, err := filepath.Abs(trimmed); err == nil {
			trimmed = abs
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	add(os.Getenv("HOXI_EXTRA_SKILLS_DIR"))
	for _, d := range registered {
		add(d)
	}
	return out
}

func cleanupLegacyBundledSkills(root string) {
	extraSkillsMu.RLock()
	slugs := append([]string(nil), legacyBundledSlugs...)
	extraSkillsMu.RUnlock()
	for _, oldSlug := range slugs {
		dir := filepath.Join(root, oldSlug)
		skillPath := filepath.Join(dir, "SKILL.md")
		raw, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		author, _ := extractFrontmatterMeta(raw)
		if strings.EqualFold(author, "Hoxi") {
			_ = os.RemoveAll(dir)
		}
	}
}

func extractFrontmatterMeta(data []byte) (author, version string) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", ""
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", ""
	}
	header := text[4 : 4+end]
	if m := frontmatterAuthorRe.FindStringSubmatch(header); len(m) == 2 {
		author = strings.TrimSpace(m[1])
	}
	if m := frontmatterVersionRe.FindStringSubmatch(header); len(m) == 2 {
		version = strings.TrimSpace(m[1])
	}
	return author, version
}

func shouldUpgradeBundledSkill(existing, embedded []byte) bool {
	if bytes.Equal(existing, embedded) {
		return false
	}
	existAuthor, existVer := extractFrontmatterMeta(existing)
	embedAuthor, embedVer := extractFrontmatterMeta(embedded)
	if !strings.EqualFold(existAuthor, "Hoxi") || !strings.EqualFold(embedAuthor, "Hoxi") {
		return false
	}
	return embedVer != "" && existVer != embedVer
}

func seedSkillDirectory(root, dir string) (int, error) {
	if strings.TrimSpace(dir) == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil
	}
	seeded := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		srcDir := filepath.Join(dir, name)
		src := filepath.Join(srcDir, "SKILL.md")
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		targetDir := filepath.Join(root, name)
		target := filepath.Join(targetDir, "SKILL.md")
		upgraded := false
		if existing, statErr := os.ReadFile(target); statErr == nil {
			if shouldUpgradeBundledSkill(existing, data) {
				if replaceSkillFile(target, data) == nil {
					seeded++
					upgraded = true
				}
			}
		} else if os.IsNotExist(statErr) {
			if writeNewSkillFile(target, data) == nil {
				seeded++
				upgraded = true
			}
		}
		seedExternalSkillSubdir(filepath.Join(srcDir, "scripts"), filepath.Join(targetDir, "scripts"), upgraded)
	}
	return seeded, nil
}

func seedExternalSkillSubdir(srcSubdir, dstSubdir string, overwrite bool) {
	items, err := os.ReadDir(srcSubdir)
	if err != nil {
		return
	}
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcSubdir, item.Name()))
		if err != nil {
			continue
		}
		dstFile := filepath.Join(dstSubdir, item.Name())
		if overwrite {
			_ = replaceSkillFile(dstFile, data)
		} else if _, err := os.Stat(dstFile); os.IsNotExist(err) {
			_ = writeNewSkillFile(dstFile, data)
		}
	}
}

func replaceSkillFile(target string, data []byte) error {
	return hoxipaths.WriteFileAtomic(target, data, 0o644)
}

// writeNewSkillFile writes atomically and fails instead of replacing an
// existing target, so concurrent starts cannot clobber user edits.
func writeNewSkillFile(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".SKILL.md.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Link(tmpName, target); err != nil {
		if os.IsExist(err) {
			return nil
		}
		// Hard links are unavailable on some filesystems; fall back to an
		// exclusive create so an existing file is still never replaced.
		file, createErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if createErr != nil {
			if os.IsExist(createErr) {
				return nil
			}
			return createErr
		}
		if _, writeErr := file.Write(data); writeErr != nil {
			file.Close()
			os.Remove(target)
			return writeErr
		}
		return file.Close()
	}
	return nil
}
