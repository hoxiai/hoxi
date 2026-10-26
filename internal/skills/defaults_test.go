package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedDefaultsInstallsBundledSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	seeded, err := SeedDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if seeded < 1 {
		t.Fatalf("seeded = %d, want at least 1", seeded)
	}

	target := filepath.Join(home, ".hoxi", "skills", "skill-authoring", "SKILL.md")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: skill-authoring") {
		t.Fatalf("seeded skill missing name: %q", string(data[:80]))
	}

	custom := []byte("---\nname: skill-authoring\ndescription: locally edited\n---\n")
	if err := os.WriteFile(target, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	seeded, err = SeedDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if seeded != 0 {
		t.Fatalf("second seed = %d, want 0", seeded)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, custom) {
		t.Fatal("seed overwrote an existing skill file")
	}
}

func TestDefaultSkillMatchesRepoCopy(t *testing.T) {
	for _, slug := range []string{"skill-authoring", "tools"} {
		embedded, err := defaultSkillFiles.ReadFile("defaults/" + slug + "/SKILL.md")
		if err != nil {
			t.Fatalf("read embedded %s: %v", slug, err)
		}
		repoPath := filepath.Join("..", "..", ".opencode", "skills", slug, "SKILL.md")
		repo, err := os.ReadFile(repoPath)
		if err != nil {
			t.Fatalf("read repo skill copy (%s): %v", slug, err)
		}
		if !bytes.Equal(embedded, repo) {
			t.Fatalf("bundled default and .opencode/skills/%s/SKILL.md differ", slug)
		}
	}
}

func TestUserSkillRootsIncludeHoxiSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	want := canonicalizeDir(filepath.Join(home, ".hoxi", "skills"))
	for _, root := range userSkillRoots() {
		if root.dir == want {
			if root.label != "~/.hoxi/skills" {
				t.Fatalf("label = %q, want ~/.hoxi/skills", root.label)
			}
			return
		}
	}
	t.Fatalf("userSkillRoots missing %q", want)
}

func TestClassifyHoxiDefaultSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := filepath.Join(home, ".hoxi", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("---\nname: demo\ndescription: demo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	source, label, resolved, canOpen := classifySkillLocation(RequestContext{Directory: t.TempDir()}, file)
	if source != "user" {
		t.Fatalf("source = %q, want user", source)
	}
	if !canOpen {
		t.Fatal("expected bundled skills to be openable")
	}
	if resolved == "" {
		t.Fatal("expected resolved path")
	}
	if !strings.HasPrefix(label, "~/.hoxi/skills/") {
		t.Fatalf("label = %q, want ~/.hoxi/skills/ prefix", label)
	}
}

func TestSeedDefaultsMigratesLegacyAndUpgradesOutdatedBundledSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	legacyDir := filepath.Join(home, ".hoxi", "skills", "hoxi-tools")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyContent := "---\nname: hoxi-tools\nmetadata:\n  author: \"Hoxi\"\n  version: \"1.0.0\"\n---\n# Legacy\n"
	if err := os.WriteFile(filepath.Join(legacyDir, "SKILL.md"), []byte(legacyContent), 0o644); err != nil {
		t.Fatal(err)
	}

	toolsDir := filepath.Join(home, ".hoxi", "skills", "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldTools := "---\nname: tools\nmetadata:\n  author: \"Hoxi\"\n  version: \"1.0.0\"\n---\n# Old tools\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "SKILL.md"), []byte(oldTools), 0o644); err != nil {
		t.Fatal(err)
	}

	seeded, err := SeedDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if seeded < 2 {
		t.Fatalf("expected skill-authoring install + tools upgrade, got seeded=%d", seeded)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("expected legacy ~/.hoxi/skills/hoxi-tools to be removed, stat err=%v", err)
	}
	upgraded, err := os.ReadFile(filepath.Join(toolsDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(upgraded), `version: "1.1.0"`) {
		t.Fatalf("expected tools/SKILL.md to be upgraded to 1.1.0, got:\n%s", string(upgraded))
	}
}
