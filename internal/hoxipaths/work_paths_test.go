package hoxipaths

import (
	"path/filepath"
	"testing"
)

func TestWorkPathHelpersRespectHomeAndHOXIHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", "")

	base := filepath.Join(home, ".hoxi", "work")
	if got := WorkDir(); got != base {
		t.Fatalf("WorkDir() = %q, want %q", got, base)
	}
	if got, want := WorkPackagesDir(), filepath.Join(base, "packages"); got != want {
		t.Fatalf("WorkPackagesDir() = %q, want %q", got, want)
	}
	if got, want := WorkToolchainsDir(), filepath.Join(base, "toolchains"); got != want {
		t.Fatalf("WorkToolchainsDir() = %q, want %q", got, want)
	}
	if got, want := WorkRuntimesDir(), filepath.Join(base, "runtimes"); got != want {
		t.Fatalf("WorkRuntimesDir() = %q, want %q", got, want)
	}
	if got, want := WorkWorkspaceDir(), filepath.Join(base, "workspace"); got != want {
		t.Fatalf("WorkWorkspaceDir() = %q, want %q", got, want)
	}
	if got, want := WorkStoresDir(), filepath.Join(base, "stores"); got != want {
		t.Fatalf("WorkStoresDir() = %q, want %q", got, want)
	}
	if got, want := WorkEnvProfilePath(), filepath.Join(base, "env-profile.json"); got != want {
		t.Fatalf("WorkEnvProfilePath() = %q, want %q", got, want)
	}
	if got, want := WorkAppRegistryPath(), filepath.Join(home, ".hoxi", "work-apps.json"); got != want {
		t.Fatalf("WorkAppRegistryPath() = %q, want %q", got, want)
	}

	customHome := t.TempDir()
	t.Setenv("HOXI_HOME", customHome)
	customWork := filepath.Join(customHome, "work")
	if got := WorkDir(); got != customWork {
		t.Fatalf("WorkDir() with HOXI_HOME = %q, want %q", got, customWork)
	}
	if got, want := WorkPackageDir("ecom-suite"), filepath.Join(customWork, "packages", "ecom-suite"); got != want {
		t.Fatalf("WorkPackageDir() = %q, want %q", got, want)
	}
	if got, want := WorkPackageCurrentDir("ecom-suite"), filepath.Join(customWork, "packages", "ecom-suite", "current"); got != want {
		t.Fatalf("WorkPackageCurrentDir() = %q, want %q", got, want)
	}
	if got, want := WorkPackageVersionDir("ecom-suite", "1.2.0"), filepath.Join(customWork, "packages", "ecom-suite", "versions", "1.2.0"); got != want {
		t.Fatalf("WorkPackageVersionDir() = %q, want %q", got, want)
	}
	if got, want := WorkRuntimeDir("ecom-suite"), filepath.Join(customWork, "runtimes", "ecom-suite"); got != want {
		t.Fatalf("WorkRuntimeDir() = %q, want %q", got, want)
	}
	if got, want := WorkToolchainDir("python-3.12"), filepath.Join(customWork, "toolchains", "python-3.12"); got != want {
		t.Fatalf("WorkToolchainDir() = %q, want %q", got, want)
	}
	if got, want := WorkStoreDir("amz-us"), filepath.Join(customWork, "stores", "amz-us"); got != want {
		t.Fatalf("WorkStoreDir() = %q, want %q", got, want)
	}
	if got, want := WorkStoreFilePath("amz-us"), filepath.Join(customWork, "stores", "amz-us.json"); got != want {
		t.Fatalf("WorkStoreFilePath() = %q, want %q", got, want)
	}
}

func TestWorkIdentifierValidationRejectsTraversalAndReservedNames(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())

	valid := []string{"ecom-ops", "analytics_v2", "com.acme.ops", "app1"}
	for _, id := range valid {
		got, err := ValidateWorkAppID(id)
		if err != nil || got != id {
			t.Fatalf("ValidateWorkAppID(%q) = (%q, %v), want (%q, nil)", id, got, err, id)
		}
	}

	invalid := []string{
		"",
		" ",
		" ecom",
		"ecom ",
		".",
		"..",
		"../stores",
		"a..b",
		"foo/bar",
		`foo\bar`,
		".git",
		".hidden",
		"-flag",
		"current",
		"versions",
		"hoxi.json",
		"env-profile.json",
		"work-apps.json",
		"models.json",
		"app\x00id",
		"app\nid",
	}
	for _, bad := range invalid {
		if _, err := ValidateWorkAppID(bad); err == nil {
			t.Errorf("ValidateWorkAppID(%q) unexpectedly succeeded", bad)
		}
		if got := WorkPackageDir(bad); got != "" {
			t.Errorf("WorkPackageDir(%q) = %q, want empty", bad, got)
		}
		if got := WorkPackageCurrentDir(bad); got != "" {
			t.Errorf("WorkPackageCurrentDir(%q) = %q, want empty", bad, got)
		}
		if got := WorkRuntimeDir(bad); got != "" {
			t.Errorf("WorkRuntimeDir(%q) = %q, want empty", bad, got)
		}
	}
	if got := WorkPackageVersionDir("ecom-ops", "../1.0.0"); got != "" {
		t.Fatalf("WorkPackageVersionDir with traversal version = %q, want empty", got)
	}
}

func TestSensitiveWorkStateFilesRemainProtected(t *testing.T) {
	customHome := t.TempDir()
	t.Setenv("HOXI_HOME", customHome)

	mustProtectNames := []string{
		"work-apps.json",
		"work-apps.json.lock",
		"env-profile.json",
		"env-profile.json.lock",
		"work/env-profile.json",
		"work/env-profile.json.lock",
		"work-stores.json",
		"work/stores.json",
		"work/packages.json",
	}
	for _, name := range mustProtectNames {
		if !IsProtectedStateFile(name) {
			t.Errorf("expected %q to be registered in ProtectedStateFiles", name)
		}
	}

	protectedPaths := []string{
		WorkAppRegistryPath(),
		WorkAppRegistryPath() + ".lock",
		WorkEnvProfilePath(),
		WorkEnvProfilePath() + ".lock",
		WorkStoresDir(),
		WorkStoreDir("shopify-main"),
		WorkStoreFilePath("shopify-main"),
		filepath.Join(WorkStoresDir(), "nested", "token.json"),
		filepath.Join("/Users/alice/.hoxi/work/env-profile.json"),
		filepath.Join("/Users/alice/.hoxi/work/stores/amz.json"),
		".hoxi/work/stores/secret.json",
	}
	for _, p := range protectedPaths {
		if !IsProtectedWorkStatePath(p) {
			t.Errorf("expected IsProtectedWorkStatePath(%q) = true", p)
		}
		if !IsDeniedDocOrSpreadsheetPath(p) && customHome == "" {
			t.Errorf("expected IsDeniedDocOrSpreadsheetPath(%q) = true", p)
		}
	}
	// Paths under ~/.hoxi (not custom non-dot HOXI_HOME) are also denied by doc/spreadsheet guard.
	for _, dotHoxiPath := range []string{
		"/Users/alice/.hoxi/work/env-profile.json",
		"/Users/alice/.hoxi/work/stores/amz.json",
		"/Users/alice/.hoxi/work-apps.json",
	} {
		if !IsDeniedDocOrSpreadsheetPath(dotHoxiPath) {
			t.Errorf("expected IsDeniedDocOrSpreadsheetPath(%q) = true", dotHoxiPath)
		}
	}

	nonProtectedPaths := []string{
		"",
		WorkWorkspaceDir(),
		filepath.Join(WorkWorkspaceDir(), "draft.md"),
		WorkPackageCurrentDir("ecom-ops"),
		filepath.Join(WorkPackageCurrentDir("ecom-ops"), "SKILL.md"),
		WorkRuntimeDir("ecom-ops"),
		WorkToolchainsDir(),
	}
	for _, p := range nonProtectedPaths {
		if IsProtectedWorkStatePath(p) {
			t.Errorf("expected IsProtectedWorkStatePath(%q) = false", p)
		}
	}
}
