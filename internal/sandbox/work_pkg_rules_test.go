package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func TestSessionAppPackageRulesGrantReadExecAndEnforceDenyWrite(t *testing.T) {
	home := resolvePathSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", "")

	workspace := resolvePathSymlinks(t.TempDir())
	versionDir := filepath.Join(home, ".hoxi", "work", "packages", "ecom-ops", "versions", "1.0.0")
	currentLink := filepath.Join(home, ".hoxi", "work", "packages", "ecom-ops", "current")
	runtimeDir := filepath.Join(home, ".hoxi", "work", "runtimes", "ecom-ops")
	storesDir := filepath.Join(home, ".hoxi", "work", "stores")
	envProfile := filepath.Join(home, ".hoxi", "work", "env-profile.json")

	for _, d := range []string{versionDir, runtimeDir, storesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(versionDir, currentLink); err != nil {
		t.Fatal(err)
	}

	rules, err := BuildWorkAppPackageRules("ecom-ops")
	if err != nil {
		t.Fatalf("BuildWorkAppPackageRules failed: %v", err)
	}
	for _, wantRead := range []string{currentLink, versionDir, runtimeDir} {
		if !hasPathRule(rules.AllowRead, wantRead, PathSubtree) {
			t.Errorf("expected AllowRead to include subtree %s, got %+v", wantRead, rules.AllowRead)
		}
		if !hasPathRule(rules.DenyWrite, wantRead, PathSubtree) {
			t.Errorf("expected DenyWrite to include subtree %s, got %+v", wantRead, rules.DenyWrite)
		}
	}

	// Pass package/runtime in extraWritable to verify WithWorkAppPackageAccess strips them!
	base, err := DefaultProfile(workspace, NetworkDeny, currentLink, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an upstream profile that broadly denied reading ~/.hoxi/work/packages.
	base.Filesystem.DenyRead = append(base.Filesystem.DenyRead, PathRule{
		Path: filepath.Join(home, ".hoxi", "work", "packages"),
		Kind: PathSubtree,
	})

	profile, err := WithWorkAppPackageAccess(base, "ecom-ops")
	if err != nil {
		t.Fatalf("WithWorkAppPackageAccess failed: %v", err)
	}

	for _, stripped := range []string{currentLink, versionDir, runtimeDir} {
		if hasPathRule(profile.Filesystem.AllowWrite, stripped, PathSubtree) {
			t.Errorf("AllowWrite must not keep app bundle/runtime root %s", stripped)
		}
	}
	for _, mustDenyWrite := range []string{
		currentLink,
		versionDir,
		runtimeDir,
		filepath.Join(home, ".hoxi", "work", "packages"),
		filepath.Join(home, ".hoxi", "work", "runtimes"),
		filepath.Join(home, ".hoxi", "work", "toolchains"),
		storesDir,
	} {
		if !hasPathRule(profile.Filesystem.DenyWrite, mustDenyWrite, PathSubtree) {
			t.Errorf("DenyWrite missing subtree %s", mustDenyWrite)
		}
	}
	if !hasPathRule(profile.Filesystem.DenyWrite, envProfile, PathLiteral) {
		t.Errorf("DenyWrite missing literal %s", envProfile)
	}
	if !hasPathRule(profile.Filesystem.DenyRead, storesDir, PathSubtree) {
		t.Errorf("DenyRead missing stores subtree %s", storesDir)
	}
	if !hasPathRule(profile.Filesystem.DenyRead, envProfile, PathLiteral) {
		t.Errorf("DenyRead missing env-profile.json literal %s", envProfile)
	}
	if hasPathRule(profile.Filesystem.DenyRead, filepath.Join(home, ".hoxi", "work", "packages"), PathSubtree) {
		t.Errorf("DenyRead must unblock bound package root")
	}

	text, err := ProfileText(profile)
	if err != nil {
		t.Fatalf("ProfileText failed: %v", err)
	}
	for _, want := range []string{
		"(allow process-exec)",
		"(allow file-read*)",
		strings.ReplaceAll(versionDir, `\`, `\\`),
		strings.ReplaceAll(runtimeDir, `\`, `\\`),
		strings.ReplaceAll(storesDir, `\`, `\\`),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered profile missing %q:\n%s", want, text)
		}
	}
}

func TestWorkAppPackageRulesRejectInvalidIDsAndSymlinkEscapes(t *testing.T) {
	home := resolvePathSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", "")

	workspace := resolvePathSymlinks(t.TempDir())
	if _, err := BuildWorkAppPackageRules(); err == nil {
		t.Fatal("expected empty appIDs list to fail")
	}
	for _, bad := range []string{"", " ", "../stores", "a/b", ".git", "current", "hoxi.json"} {
		if _, err := WorkPackageSessionProfile(workspace, NetworkDeny, []string{bad}); err == nil {
			t.Errorf("expected invalid appId %q to be rejected", bad)
		}
		if _, err := WorkSetupInstallerProfile(bad, NetworkDeny); err == nil {
			t.Errorf("expected installer invalid appId %q to be rejected", bad)
		}
	}

	// Symlink escape: packages/evil-app/current -> ~/.ssh
	sshDir := filepath.Join(home, ".ssh")
	pkgAppDir := filepath.Join(home, ".hoxi", "work", "packages", "evil-app")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgAppDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sshDir, filepath.Join(pkgAppDir, "current")); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWorkAppPackageRules("evil-app"); err == nil {
		t.Fatal("expected symlink escape from packages/<appId>/current to ~/.ssh to fail")
	}

	// Symlink escape: runtimes/evil-rt -> ~/.hoxi
	runtimesRoot := filepath.Join(home, ".hoxi", "work", "runtimes")
	if err := os.MkdirAll(runtimesRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".hoxi"), filepath.Join(runtimesRoot, "evil-rt")); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWorkAppPackageRules("evil-rt"); err == nil {
		t.Fatal("expected symlink escape from runtimes/<appId> to fail")
	}
	if _, err := WorkSetupInstallerProfile("evil-rt", NetworkDeny); err == nil {
		t.Fatal("expected installer profile to reject symlinked runtime escaping runtimes root")
	}
}

func TestWithReadOnlyExecRootsValidatesAndDeniesWrites(t *testing.T) {
	home := resolvePathSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", "")

	workspace := resolvePathSymlinks(t.TempDir())
	base, err := DefaultProfile(workspace, NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}

	pkgCurrent := filepath.Join(home, ".hoxi", "work", "packages", "analytics", "current")
	rtDir := filepath.Join(home, ".hoxi", "work", "runtimes", "analytics")
	for _, d := range []string{pkgCurrent, rtDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	updated, err := WithReadOnlyExecRoots(base, pkgCurrent, rtDir)
	if err != nil {
		t.Fatalf("WithReadOnlyExecRoots failed: %v", err)
	}
	if !hasPathRule(updated.Filesystem.DenyWrite, pkgCurrent, PathSubtree) ||
		!hasPathRule(updated.Filesystem.DenyWrite, rtDir, PathSubtree) {
		t.Fatalf("expected DenyWrite on read-only exec roots, got %+v", updated.Filesystem.DenyWrite)
	}

	rejected := []string{
		"",
		"relative/path",
		"~/bin",
		"/",
		home,
		"/private/tmp",
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".hoxi", "work", "stores"),
		filepath.Join(home, ".hoxi", "sessions"),
		filepath.Join(workspace, ".git"),
		filepath.Join(pkgCurrent, "missing-subdir"),
		pkgCurrent + "/../current",
	}
	for _, bad := range rejected {
		if _, err := WithReadOnlyExecRoots(base, bad); err == nil {
			t.Errorf("expected WithReadOnlyExecRoots(%q) to fail", bad)
		}
	}
}

func TestWorkSetupInstallerProfileStrictlyConfinesWritesAndHardDeniesTargets(t *testing.T) {
	home := resolvePathSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", "")

	profile, err := WorkSetupInstallerProfile("ecom-ops", NetworkAllow)
	if err != nil {
		t.Fatalf("WorkSetupInstallerProfile failed: %v", err)
	}

	toolchains := resolvePathSymlinks(hoxipaths.WorkToolchainsDir())
	appRuntime := resolvePathSymlinks(hoxipaths.WorkRuntimeDir("ecom-ops"))
	allowedTempSet := make(map[string]bool)
	for _, rule := range canonicalSubtrees([]string{os.TempDir(), "/tmp", "/private/tmp", "/private/var/folders"}) {
		allowedTempSet[rule.Path] = true
	}

	if !hasPathRule(profile.Filesystem.AllowWrite, toolchains, PathSubtree) {
		t.Fatalf("installer AllowWrite missing toolchains %s", toolchains)
	}
	if !hasPathRule(profile.Filesystem.AllowWrite, appRuntime, PathSubtree) {
		t.Fatalf("installer AllowWrite missing app runtime %s", appRuntime)
	}
	for _, rule := range profile.Filesystem.AllowWrite {
		if rule.Kind != PathSubtree {
			t.Fatalf("unexpected AllowWrite rule kind: %+v", rule)
		}
		if rule.Path != toolchains && rule.Path != appRuntime && !allowedTempSet[rule.Path] {
			t.Fatalf("installer AllowWrite contains unauthorized path: %s", rule.Path)
		}
	}
	if len(profile.Filesystem.NameDenyExemptions) != 0 {
		t.Fatalf("installer profile must not grant NameDenyExemptions: %+v", profile.Filesystem.NameDenyExemptions)
	}
	for _, name := range []string{".git", "hoxi.json"} {
		if !containsString(profile.Filesystem.DenyNames, name) {
			t.Errorf("installer DenyNames missing %q", name)
		}
	}

	// Verify hard-denies: ~/.hoxi protected files, packages/stores, home shell rc files, global system dirs.
	for _, protectedLiteral := range []string{
		filepath.Join(home, ".hoxi", "models.json"),
		filepath.Join(home, ".hoxi", "work-apps.json"),
		filepath.Join(home, ".hoxi", "work", "env-profile.json"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".zprofile"),
		filepath.Join(home, ".profile"),
	} {
		if !hasPathRule(profile.Filesystem.DenyWrite, protectedLiteral, PathLiteral) {
			t.Errorf("installer DenyWrite missing literal %s", protectedLiteral)
		}
	}
	for _, protectedSubtree := range []string{
		filepath.Join(home, ".hoxi", "sessions"),
		filepath.Join(home, ".hoxi", "work", "packages"),
		filepath.Join(home, ".hoxi", "work", "stores"),
		filepath.Join(home, ".hoxi", "work", "workspace"),
		filepath.Join(home, ".config", "fish"),
		"/System",
		"/Library",
		"/Applications",
		"/usr",
		"/bin",
		"/sbin",
		"/etc",
		"/private/etc",
		"/opt",
	} {
		if !hasPathRule(profile.Filesystem.DenyWrite, protectedSubtree, PathSubtree) {
			t.Errorf("installer DenyWrite missing subtree %s", protectedSubtree)
		}
	}

	text, err := ProfileText(profile)
	if err != nil {
		t.Fatalf("ProfileText(installer) failed: %v", err)
	}
	for _, want := range []string{
		"(allow network-outbound)",
		`(^|/)\.git(/|$)`,
		`(^|/)hoxi\.json(/|$)`,
		".zshrc",
		`"` + strings.ReplaceAll(filepath.Clean("/usr"), `\`, `\\`) + `"`,
		`"` + strings.ReplaceAll(filepath.Clean("/etc"), `\`, `\\`) + `"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("installer Seatbelt text missing %q:\n%s", want, text)
		}
	}

	// Also verify RestrictToWorkSetupInstaller narrows a broad DefaultProfile.
	broad, err := DefaultProfile(resolvePathSymlinks(t.TempDir()), NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := RestrictToWorkSetupInstaller(broad, "ecom-ops")
	if err != nil {
		t.Fatalf("RestrictToWorkSetupInstaller failed: %v", err)
	}
	if len(restricted.Filesystem.AllowWrite) != len(profile.Filesystem.AllowWrite) {
		t.Fatalf("RestrictToWorkSetupInstaller kept extra AllowWrite rules: %+v", restricted.Filesystem.AllowWrite)
	}
}

func TestWorkSandboxRulesEnforcedWhenSeatbeltAvailable(t *testing.T) {
	if !CanApply() {
		t.Skip("seatbelt sandbox_apply unavailable or blocked by outer sandbox")
	}
	home := resolvePathSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", "")

	workspace := resolvePathSymlinks(t.TempDir())
	pkgCurrent := filepath.Join(home, ".hoxi", "work", "packages", "demo-app", "current")
	rtDir := filepath.Join(home, ".hoxi", "work", "runtimes", "demo-app")
	toolchains := filepath.Join(home, ".hoxi", "work", "toolchains")
	storesDir := filepath.Join(home, ".hoxi", "work", "stores")
	for _, d := range []string{pkgCurrent, rtDir, toolchains, storesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	toolScript := filepath.Join(pkgCurrent, "run.sh")
	if err := os.WriteFile(toolScript, []byte("#!/bin/sh\necho bundle-ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	storeSecret := filepath.Join(storesDir, "amz.json")
	if err := os.WriteFile(storeSecret, []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	sessProfile, err := WorkPackageSessionProfile(workspace, NetworkDeny, []string{"demo-app"})
	if err != nil {
		t.Fatal(err)
	}
	execCheck := func(p Profile, cmd string) error {
		bin, args, err := WrapShell(p, cmd)
		if err != nil {
			t.Fatal(err)
		}
		return runCommand(bin, args...)
	}
	if err := execCheck(sessProfile, shellQuote(toolScript)); err != nil {
		t.Fatalf("session could not execute bound package script: %v", err)
	}
	if err := execCheck(sessProfile, "echo tamper > "+shellQuote(filepath.Join(pkgCurrent, "tamper.txt"))); err == nil {
		t.Fatal("session mutated bound package bundle")
	}
	if err := execCheck(sessProfile, "echo tamper > "+shellQuote(filepath.Join(rtDir, "tamper.txt"))); err == nil {
		t.Fatal("session mutated bound runtime dir")
	}
	if err := execCheck(sessProfile, "cat "+shellQuote(storeSecret)); err == nil {
		t.Fatal("session read protected work store secret")
	}

	instProfile, err := WorkSetupInstallerProfile("demo-app", NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	if err := execCheck(instProfile, "echo ok > "+shellQuote(filepath.Join(toolchains, "uv.txt"))); err != nil {
		t.Fatalf("installer could not write toolchains: %v", err)
	}
	if err := execCheck(instProfile, "echo ok > "+shellQuote(filepath.Join(rtDir, "venv.txt"))); err != nil {
		t.Fatalf("installer could not write app runtime: %v", err)
	}
	for _, blocked := range []string{
		filepath.Join(pkgCurrent, "evil.txt"),
		filepath.Join(storesDir, "evil.txt"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".hoxi", "models.json"),
		filepath.Join(rtDir, "hoxi.json"),
	} {
		if err := execCheck(instProfile, "echo bad > "+shellQuote(blocked)); err == nil {
			t.Fatalf("installer unexpectedly wrote to %s", blocked)
		}
	}
}

func hasPathRule(rules []PathRule, target string, kind PathKind) bool {
	want := resolvePathSymlinks(target)
	for _, r := range rules {
		if r.Kind != kind {
			continue
		}
		if r.Path == target || r.Path == want || resolvePathSymlinks(r.Path) == want {
			return true
		}
	}
	return false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
