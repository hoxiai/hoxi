package skills

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

//go:embed defaults/*/SKILL.md
var defaultSkillFiles embed.FS

// DefaultUserDir is the Hoxi-managed directory that holds bundled skills.
// Hoxi registers it in the engine config so every project can discover it.
func DefaultUserDir() string {
	return hoxipaths.Join("skills")
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
	if extra, err := seedExternalSkillDir(root); err == nil {
		seeded += extra
	}
	return seeded, nil
}

func seedExternalSkillDir(root string) (int, error) {
	candidates := []string{os.Getenv("HOXI_EXTRA_SKILLS_DIR")}
	if wd, err := os.Getwd(); err == nil && wd != "" {
		candidates = append(candidates, filepath.Clean(filepath.Join(wd, "..", "hoxi-work", "skills")))
	}
	seeded := 0
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
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
