package worktree

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAttributionPreferencePersistsAndDefaultsToAuto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", AttributionFile)
	if mode := LoadAttribution(path).Mode; mode != AttributionAuto {
		t.Fatalf("a missing preference must default to auto, got %q", mode)
	}
	if mode := LoadAttribution("").Mode; mode != AttributionAuto {
		t.Fatalf("an unknown Hoxi home must default to auto, got %q", mode)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"not json", `{}`, `{"mode":"sometimes"}`, `{"enabled":"no"}`} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if mode := LoadAttribution(path).Mode; mode != AttributionAuto {
			t.Fatalf("malformed preference %q must default to auto, got %q", raw, mode)
		}
	}
	if backups, _ := filepath.Glob(path + ".corrupt-*"); len(backups) == 0 {
		t.Fatal("expected malformed attribution files to be backed up with .corrupt-* suffix")
	}

	// The pre-mode boolean toggle still loads: true was always, false was never.
	for raw, want := range map[string]AttributionMode{
		`{"enabled":true}`:  AttributionAlways,
		`{"enabled":false}`: AttributionNever,
	} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if mode := LoadAttribution(path).Mode; mode != want {
			t.Fatalf("legacy preference %q loaded as %q, want %q", raw, mode, want)
		}
	}

	t.Cleanup(func() { SetAttributionMode(AttributionAuto) })
	if err := SaveAttribution(path, AttributionNever); err != nil {
		t.Fatal(err)
	}
	if CurrentAttributionMode() != AttributionNever {
		t.Fatal("saving must apply the preference immediately")
	}
	if AttributionEnabledFor(t.TempDir()) {
		t.Fatal("never must disable attribution in every directory")
	}
	if LoadAttribution(path).Mode != AttributionNever {
		t.Fatal("an explicit mode must persist")
	}
	// Older Hoxi builds only read `enabled`, so the mirror must stay coherent.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"enabled":false`) {
		t.Fatalf("never must persist enabled:false for older builds, got %s", raw)
	}
	if err := SaveAttribution(path, AttributionAuto); err != nil {
		t.Fatal(err)
	}
	if raw, err = os.ReadFile(path); err != nil || !strings.Contains(string(raw), `"enabled":true`) {
		t.Fatalf("auto must persist enabled:true for older builds, got %s (err=%v)", raw, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Fatalf("preference file mode = %o, want 600", perm)
	}
	if err := SaveAttribution("", AttributionAuto); err == nil {
		t.Fatal("saving without a path must fail")
	}
	if err := SaveAttribution(path, "sometimes"); err == nil {
		t.Fatal("saving an unknown mode must fail")
	}
}

func TestAttributionEnabledForCountsOnlySessionWorktreesInAuto(t *testing.T) {
	t.Cleanup(func() { SetAttributionMode(AttributionAuto) })
	project := filepath.Join(t.TempDir(), "project")
	session := filepath.Join(project, ".hoxi", "worktrees", "ses_auto")

	SetAttributionMode(AttributionAuto)
	if !AttributionEnabledFor(session) {
		t.Fatal("auto must attribute isolated session worktrees")
	}
	// A nested repository inside the session worktree belongs to the session.
	if !AttributionEnabledFor(filepath.Join(session, "vendor", "lib")) {
		t.Fatal("auto must attribute repositories nested in a session worktree")
	}
	for _, dir := range []string{project, filepath.Join(project, "sub"), t.TempDir(), ""} {
		if AttributionEnabledFor(dir) {
			t.Fatalf("auto must leave commits in %q untouched", dir)
		}
	}

	SetAttributionMode(AttributionAlways)
	if !AttributionEnabledFor(project) {
		t.Fatal("always must attribute every repository")
	}

	SetAttributionMode(AttributionNever)
	for _, dir := range []string{session, project} {
		if AttributionEnabledFor(dir) {
			t.Fatalf("never must disable attribution in %q", dir)
		}
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
	t.Cleanup(func() { SetAttributionMode(AttributionAuto) })
	lastBody := func() string {
		out, err := runGit(dir, "log", "-1", "--format=%B")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	SetAttributionMode(AttributionNever)
	if err := os.WriteFile(filepath.Join(dir, "off.txt"), []byte("off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := snapshotWorktree(dir, "ses_off"); err != nil {
		t.Fatal(err)
	}
	if body := lastBody(); strings.Contains(body, HoxiCoAuthorTrailer) {
		t.Fatalf("attribution off must not add the trailer:\n%s", body)
	}

	// auto: a plain project repo (not a session worktree) stays untouched…
	if err := os.WriteFile(filepath.Join(dir, "auto.txt"), []byte("auto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := snapshotWorktree(dir, "ses_auto"); err != nil {
		t.Fatal(err)
	}
	if body := lastBody(); strings.Contains(body, HoxiCoAuthorTrailer) {
		t.Fatalf("auto must not attribute the project repo:\n%s", body)
	}

	// …while a session worktree gets it.
	SetAttributionMode(AttributionAlways)
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
