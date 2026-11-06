package gateway

import (
	"path/filepath"
	"testing"
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
