package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectSkillPathsAndLiveDenial(t *testing.T) {
	f := newSkillFixture(t)
	writeSkill(t, filepath.Join(f.project, "custom", "custom-review", "SKILL.md"), "custom-review", "Review.")
	put := func(raw string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.project, "hoxi.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(`{"skills":{"paths":["custom"]},"permission":{"skill":{"custom-review":"deny"}}}`)
	svc := NewService("", "", func(_ context.Context, _, _, _ string) (RequestContext, error) {
		return RequestContext{Directory: f.project, ProjectRoot: f.project, Runtime: "native"}, nil
	})
	got := listExposures(t, svc, f.project)
	if got["custom-review"].Exposure != ExposureUnavailable {
		t.Fatal(got["custom-review"])
	}
	put(`{"skills":{"paths":["custom"]},"permission":{"skill":{"custom-review":"allow"}}}`)
	got = listExposures(t, svc, f.project)
	if got["custom-review"].Exposure != ExposureAuto {
		t.Fatal("live config change did not apply", got["custom-review"])
	}
	if err := os.WriteFile(filepath.Join(f.hoxiDir, "..", "models.json"), []byte(`{"permission":{"skill":{"custom-review":"deny"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got = listExposures(t, svc, f.project)
	if got["custom-review"].Exposure != ExposureUnavailable {
		t.Fatal("project allow overrode local denial")
	}
}

func TestProjectSyncedSkillSymlinkDoesNotEscape(t *testing.T) {
	f := newSkillFixture(t)
	outside := t.TempDir()
	writeSkill(t, filepath.Join(outside, "bucket", "secret", "SKILL.md"), "secret", "outside")
	if err := os.MkdirAll(filepath.Join(f.project, "custom"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.project, "custom", "synced")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(f.project, "hoxi.json"), []byte(`{"skills":{"paths":["custom"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := scanLocalSkills(f.project, f.project)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name == "secret" {
			t.Fatal("synced symlink imported outside skill")
		}
	}
}

func TestProjectSkillRootSymlinkEscapeRejected(t *testing.T) {
	f := newSkillFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(f.project, "escape")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(f.project, "hoxi.json"), []byte(`{"skills":{"paths":["escape"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := scanLocalSkills(f.project, f.project); err == nil {
		t.Fatal("symlink root escape accepted")
	}
}
