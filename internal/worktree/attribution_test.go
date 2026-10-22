package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttributionPreferencePersistsAndDefaultsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", AttributionFile)
	if !LoadAttribution(path).Enabled {
		t.Fatal("a missing preference must keep attribution on")
	}
	if !LoadAttribution("").Enabled {
		t.Fatal("an unknown Hoxi home must keep attribution on")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"not json", `{}`, `{"enabled":"no"}`} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if !LoadAttribution(path).Enabled {
			t.Fatalf("malformed preference %q must keep attribution on", raw)
		}
	}
	if backups, _ := filepath.Glob(path + ".corrupt-*"); len(backups) == 0 {
		t.Fatal("expected malformed attribution files to be backed up with .corrupt-* suffix")
	}

	t.Cleanup(func() { SetAttributionEnabled(true) })
	if err := SaveAttribution(path, false); err != nil {
		t.Fatal(err)
	}
	if AttributionEnabled() {
		t.Fatal("saving must apply the preference immediately")
	}
	if LoadAttribution(path).Enabled {
		t.Fatal("an explicit opt-out must persist")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("preference file mode = %o, want 600", perm)
	}
	if err := SaveAttribution("", true); err == nil {
		t.Fatal("saving without a path must fail")
	}
}

func TestWithCoAuthorHonoursPreference(t *testing.T) {
	if got := withCoAuthor("  fix: x  ", false); got != "fix: x" {
		t.Fatalf("disabled attribution must leave the message alone, got %q", got)
	}
	if got := withCoAuthor("fix: x", true); got != "fix: x\n\n"+HoxiCoAuthorTrailer {
		t.Fatalf("enabled attribution must append the trailer, got %q", got)
	}
	existing := "fix: x\n\nCo-Authored-By: Hoxi <code@hoxi.ai>"
	if got := withCoAuthor(existing, true); got != existing {
		t.Fatalf("an existing trailer must not be duplicated, got %q", got)
	}
	if got := withCoAuthor("   ", true); got != "" {
		t.Fatalf("an empty message stays empty, got %q", got)
	}
}

func TestSnapshotCommitFollowsAttributionPreference(t *testing.T) {
	dir := initTempGitRepo(t)
	t.Cleanup(func() { SetAttributionEnabled(true) })
	lastBody := func() string {
		out, err := runGit(dir, "log", "-1", "--format=%B")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	SetAttributionEnabled(false)
	if err := os.WriteFile(filepath.Join(dir, "off.txt"), []byte("off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := snapshotWorktree(dir, "ses_off"); err != nil {
		t.Fatal(err)
	}
	if body := lastBody(); strings.Contains(body, HoxiCoAuthorTrailer) {
		t.Fatalf("attribution off must not add the trailer:\n%s", body)
	}

	SetAttributionEnabled(true)
	if err := os.WriteFile(filepath.Join(dir, "on.txt"), []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := snapshotWorktree(dir, "ses_on"); err != nil {
		t.Fatal(err)
	}
	if body := lastBody(); !strings.Contains(body, HoxiCoAuthorTrailer) {
		t.Fatalf("attribution on must add the trailer:\n%s", body)
	}
}
