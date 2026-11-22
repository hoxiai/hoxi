package hoxipaths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLILinkLifecycleAndGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test")
	}

	tmp := t.TempDir()
	appExe := filepath.Join(tmp, "Hoxi.app", "Contents", "MacOS", "Hoxi")
	if err := os.MkdirAll(filepath.Dir(appExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appExe, []byte("#!/bin/sh\necho hoxi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Rejects temporary go-build executables (prevents dead links after `go run` exits).
	goBuildExe := filepath.Join(tmp, "go-build123456", "b001", "exe", "hoxi")
	if err := os.MkdirAll(filepath.Dir(goBuildExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goBuildExe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCLISymlink(goBuildExe, binDir); err == nil {
		t.Fatal("expected InstallCLISymlink to reject temporary go-build executable")
	}
	if st := InspectCLILink(goBuildExe, binDir); st.Error == "" {
		t.Fatalf("expected InspectCLILink to surface error for go-build executable, got %+v", st)
	}

	// 2. Installs symlink cleanly and reports InPath accurately.
	linkPath, err := InstallCLISymlink(appExe, binDir)
	if err != nil {
		t.Fatalf("InstallCLISymlink failed: %v", err)
	}
	if linkPath != filepath.Join(binDir, "hoxi") {
		t.Fatalf("unexpected linkPath %q", linkPath)
	}

	stInPath := InspectCLILink(appExe, binDir+":/usr/bin")
	if !stInPath.Installed || !stInPath.Current || !stInPath.InPath || stInPath.PathHint != "" {
		t.Fatalf("expected installed+current+inPath status, got %+v", stInPath)
	}

	// 3. Detects outdated symlink when app moves to a new location.
	movedExe := filepath.Join(tmp, "Applications", "Hoxi.app", "Contents", "MacOS", "Hoxi")
	if err := os.MkdirAll(filepath.Dir(movedExe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(movedExe, []byte("#!/bin/sh\necho hoxi-v2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stOutdated := InspectCLILink(movedExe, binDir)
	if !stOutdated.Installed || stOutdated.Current {
		t.Fatalf("expected outdated symlink status after app move, got %+v", stOutdated)
	}

	// Reinstalling updates the symlink atomically to movedExe.
	if _, err := InstallCLISymlink(movedExe, binDir); err != nil {
		t.Fatalf("reinstall failed: %v", err)
	}
	stUpdated := InspectCLILink(movedExe, binDir)
	if !stUpdated.Installed || !stUpdated.Current {
		t.Fatalf("expected updated symlink to be current, got %+v", stUpdated)
	}

	// 4. Uninstall removes Hoxi symlink, but never touches unrelated symlinks.
	otherDir := filepath.Join(tmp, "other-bin")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unrelatedTarget := filepath.Join(tmp, "custom-tool")
	if err := os.WriteFile(unrelatedTarget, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	unrelatedLink := filepath.Join(otherDir, "hoxi")
	if err := os.Symlink(unrelatedTarget, unrelatedLink); err != nil {
		t.Fatal(err)
	}
	if removed, err := UninstallCLISymlink(movedExe, otherDir); err != nil || len(removed) != 0 {
		t.Fatalf("expected unrelated symlink to stay untouched, removed=%v err=%v", removed, err)
	}
	if _, err := os.Lstat(unrelatedLink); err != nil {
		t.Fatalf("unrelated symlink was removed: %v", err)
	}

	removed, err := UninstallCLISymlink(movedExe, binDir)
	if err != nil || len(removed) != 1 || removed[0] != linkPath {
		t.Fatalf("UninstallCLISymlink = %v, %v; want [%s], nil", removed, err, linkPath)
	}
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, err=%v", linkPath, err)
	}

	// 5. Legacy copied Hoxi binary is detected as outdated and upgraded to symlink,
	// whereas an unrelated regular file named 'hoxi' is never overwritten.
	foreignFile := filepath.Join(binDir, "hoxi")
	if err := os.WriteFile(foreignFile, []byte("#!/bin/sh\necho third-party-tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCLISymlink(movedExe, binDir); err == nil {
		t.Fatal("expected refusal when non-Hoxi regular file exists at destination")
	}

	legacyCopy := []byte("fake-mach-o\x00github.com/hoxiai/hoxi\x00Hoxi CLI (0.2.0)\x00")
	if err := os.WriteFile(foreignFile, legacyCopy, 0o755); err != nil {
		t.Fatal(err)
	}
	stLegacy := InspectCLILink(movedExe, binDir)
	if !stLegacy.Installed || stLegacy.Current {
		t.Fatalf("expected legacy copied Hoxi binary to report Installed=true Current=false, got %+v", stLegacy)
	}
	RefreshOutdatedCLILink(movedExe, binDir)
	infoAfter, err := os.Lstat(foreignFile)
	if err != nil || infoAfter.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected legacy copied Hoxi binary to be upgraded to symlink, mode=%v err=%v", infoAfter, err)
	}
}

func TestResolveStableExecutablePrefersStablePathOverCellar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test")
	}
	tmp := t.TempDir()
	cellarBin := filepath.Join(tmp, "Cellar", "hoxi", "0.7.0", "bin", "hoxi")
	if err := os.MkdirAll(filepath.Dir(cellarBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cellarBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	optBinDir := filepath.Join(tmp, "opt-homebrew-bin")
	if err := os.MkdirAll(optBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stableLink := filepath.Join(optBinDir, "hoxi")
	if err := os.Symlink(cellarBin, stableLink); err != nil {
		t.Fatal(err)
	}

	got, _, err := ResolveStableExecutable(cellarBin, optBinDir+":/usr/bin")
	if err != nil {
		t.Fatalf("ResolveStableExecutable error: %v", err)
	}
	if got != filepath.Clean(stableLink) {
		t.Fatalf("ResolveStableExecutable = %q; want stable link %q (not Cellar path)", got, stableLink)
	}
	if strings.Contains(strings.ToLower(got), "/cellar/") {
		t.Fatalf("expected non-Cellar path, got %q", got)
	}
}
