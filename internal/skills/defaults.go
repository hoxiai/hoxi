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

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

//go:embed defaults/*/SKILL.md
var defaultSkillFiles embed.FS

// legacyBundledSkillSlugs lists retired bundled skill directories that were
// renamed in a newer release and should be cleaned up if unmodified.
// shoply-ops is the Work extension skill renamed to shopark-ops.
var legacyBundledSkillSlugs = []string{"hoxi-tools", "shoply-ops"}

var (
	frontmatterAuthorRe  = regexp.MustCompile(`(?m)^\s*author:\s*["']?([^"'\r\n]+)["']?\s*$`)
	frontmatterVersionRe = regexp.MustCompile(`(?m)^\s*version:\s*["']?([^"'\r\n]+)["']?\s*$`)
)

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
	if extra, err := seedExternalSkillDir(root); err == nil {
		seeded += extra
	}
	return seeded, nil
}

func cleanupLegacyBundledSkills(root string) {
	for _, oldSlug := range legacyBundledSkillSlugs {
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

func seedExternalSkillDir(root string) (int, error) {
	dir := strings.TrimSpace(os.Getenv("HOXI_EXTRA_SKILLS_DIR"))
	if dir == "" {
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
		src := filepath.Join(dir, name, "SKILL.md")
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		target := filepath.Join(root, name, "SKILL.md")
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if err := writeNewSkillFile(target, data); err == nil {
			seeded++
		}
	}
	return seeded, nil
}

func replaceSkillFile(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".SKILL.md.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
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
