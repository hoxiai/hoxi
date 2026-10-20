package skills

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed defaults/*/SKILL.md
var defaultSkillFiles embed.FS

// DefaultUserDir is the Hoxi-managed directory that holds bundled skills.
// Hoxi registers it in the engine config so every project can discover it.
func DefaultUserDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "skills")
}

// SeedDefaults installs bundled skills into DefaultUserDir and never
// overwrites an existing file, so local edits are preserved.
func SeedDefaults() (int, error) {
	root := DefaultUserDir()
	if root == "" {
		return 0, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, err
	}
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
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return seeded, err
		}
		if err := writeNewSkillFile(target, data); err != nil {
			return seeded, err
		}
		seeded++
	}
	return seeded, nil
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
