package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoxiai/hoxi/internal/runner"
)

func TestSkillSurfaceRecognizesDefaultWorkDirectory(t *testing.T) {
	g := &Gateway{}
	home := t.TempDir()
	work := filepath.Join(home, ".hoxi", "workspaces", "default")
	if got := g.skillSurface(work); got != "work" {
		t.Fatalf("default Work directory = %q, want work", got)
	}
	if got := g.skillSurface(filepath.Join(work, ".hoxi", "worktrees", "ses_1")); got != "work" {
		t.Fatalf("worktree under the Work directory = %q, want work", got)
	}
	if got := g.skillSurface(filepath.Join(home, "code", "app"), ""); got != "code" {
		t.Fatalf("code project = %q, want code", got)
	}
}

func TestResolveSkillContextDraftWorkSurfaceResolvesActiveBundle(t *testing.T) {
	rawHome := t.TempDir()
	home, err := filepath.EvalSymlinks(rawHome)
	if err != nil {
		home = rawHome
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	workDir := filepath.Join(home, ".hoxi", "workspaces", "default")
	codeDir := filepath.Join(home, "code", "app")
	skillDir := filepath.Join(home, ".hoxi", "work", "packages", "demo-ops", "versions", "1.0.0", "skills")
	for _, d := range []string{workDir, codeDir, skillDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	rnr := runner.NewService(workDir, nil, nil, filepath.Join(home, "runner-sessions.json"))
	t.Cleanup(rnr.CloseAll)
	rnr.SetSessionBundleResolver(func(sessionID, directory string) (runner.SessionBundleSpec, error) {
		if sessionID == "" {
			return runner.SessionBundleSpec{
				AppIDs:         []string{"demo-ops"},
				ExtraSkillDirs: []string{skillDir},
			}, nil
		}
		return runner.SessionBundleSpec{}, nil
	})

	g := &Gateway{
		rootDir: codeDir,
		runner:  rnr,
	}

	workCtx, err := g.resolveSkillContext(context.Background(), workDir, "", "")
	if err != nil {
		t.Fatalf("resolveSkillContext(work draft) failed: %v", err)
	}
	if workCtx.Surface != "work" {
		t.Fatalf("workCtx.Surface = %q, want work", workCtx.Surface)
	}
	if len(workCtx.ExtraSkillDirs) != 1 || workCtx.ExtraSkillDirs[0] != skillDir {
		t.Fatalf("workCtx.ExtraSkillDirs = %v, want [%s]", workCtx.ExtraSkillDirs, skillDir)
	}

	codeCtx, err := g.resolveSkillContext(context.Background(), codeDir, "", "")
	if err != nil {
		t.Fatalf("resolveSkillContext(code draft) failed: %v", err)
	}
	if codeCtx.Surface != "code" {
		t.Fatalf("codeCtx.Surface = %q, want code", codeCtx.Surface)
	}
	if len(codeCtx.ExtraSkillDirs) != 0 {
		t.Fatalf("expected code draft to have zero ExtraSkillDirs, got %v", codeCtx.ExtraSkillDirs)
	}
}
