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
	for _, slug := range []string{"skill-authoring", "hoxi-tools"} {
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
