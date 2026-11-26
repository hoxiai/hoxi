package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizePathScopeValidationAndSymlinkEscape(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "internal", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "escape_link")); err != nil {
		t.Fatal(err)
	}

	got, err := NormalizePathScope(ws, PathScope{
		ReadOnlyPaths: []string{"./internal/auth/", `config\prod.yaml`, "internal/auth", "  "},
		WritablePaths: []string{"web/src/pages/", "./docs"},
	})
	if err != nil {
		t.Fatalf("NormalizePathScope valid failed: %v", err)
	}
	if strings.Join(got.ReadOnlyPaths, ",") != "internal/auth,config/prod.yaml" {
		t.Fatalf("unexpected ReadOnlyPaths: %v", got.ReadOnlyPaths)
	}
	if strings.Join(got.WritablePaths, ",") != "web/src/pages,docs" {
		t.Fatalf("unexpected WritablePaths: %v", got.WritablePaths)
	}

	badCases := []string{
		"/etc/passwd",
		"../outside",
		"internal/../../etc",
		"~/.ssh",
		"C:\\Windows\\System32",
		".",
		"internal/*",
		"internal/?",
		"internal/[a]",
		"internal/\"bad\"",
		"internal/bad\nline",
		"escape_link/sub",
	}
	for _, bad := range badCases {
		if _, err := NormalizeRelativePaths(ws, []string{bad}); err == nil {
			t.Errorf("expected invalid path %q to be rejected", bad)
		}
	}
}

func TestApplyPathScopeProfileTextAndPrecedence(t *testing.T) {
	ws := resolvePathSymlinks(t.TempDir())
	if err := os.MkdirAll(filepath.Join(ws, "internal", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "web", "src", "policy"), 0o755); err != nil {
		t.Fatal(err)
	}

	base, err := DefaultProfile(ws, NetworkAllow)
	if err != nil {
		t.Fatal(err)
	}

	// Mode 1: ReadOnlyPaths only
	roProf, err := ApplyPathScope(base, ws, PathScope{ReadOnlyPaths: []string{"internal/auth"}})
	if err != nil {
		t.Fatal(err)
	}
	roText, err := ProfileText(roProf)
	if err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(ws, "internal", "auth")
	if !strings.Contains(roText, "(deny process-exec") || !strings.Contains(roText, authPath) {
		t.Fatalf("expected DenyWrite and DenyExec for %s in profile:\n%s", authPath, roText)
	}

	// Mode 2: WritablePaths narrowed + ReadOnlyPaths exception inside writable
	rwProf, err := ApplyPathScope(base, ws, PathScope{
		WritablePaths: []string{"web/src"},
		ReadOnlyPaths: []string{"web/src/policy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	webSrc := filepath.Join(ws, "web", "src")
	webPolicy := filepath.Join(ws, "web", "src", "policy")
	hasWSRoot := false
	hasWebSrc := false
	for _, rule := range rwProf.Filesystem.AllowWrite {
		if filepath.Clean(rule.Path) == ws {
			hasWSRoot = true
		}
		if filepath.Clean(rule.Path) == webSrc {
			hasWebSrc = true
		}
	}
	if hasWSRoot {
		t.Fatalf("expected workspace root %s to be replaced by narrowed WritablePaths in AllowWrite", ws)
	}
	if !hasWebSrc {
		t.Fatalf("expected %s to be in narrowed AllowWrite", webSrc)
	}
	hasPolicyDeny := false
	for _, rule := range rwProf.Filesystem.DenyWrite {
		if filepath.Clean(rule.Path) == webPolicy {
			hasPolicyDeny = true
		}
	}
	if !hasPolicyDeny {
		t.Fatalf("expected ReadOnlyPaths %s to remain in DenyWrite even inside WritablePaths", webPolicy)
	}

	// Scoped on-demand profile has distinct path & trust isolation from unscoped profile
	m := NewManager(t.TempDir())
	pUnscoped, err := m.WriteOnDemand(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := PathScope{ReadOnlyPaths: []string{"internal/auth"}}
	pScoped, err := m.WriteOnDemandWithScope(ws, nil, scope)
	if err != nil {
		t.Fatal(err)
	}
	if pScoped == pUnscoped {
		t.Fatalf("scoped on-demand profile must not overwrite unscoped profile: %s", pScoped)
	}
	if !m.OnDemandTrustedWithScope(pScoped, ws, scope) {
		t.Fatal("expected scoped on-demand profile to be trusted for its own scope")
	}
	if m.OnDemandTrustedWithScope(pUnscoped, ws, scope) {
		t.Fatal("unscoped on-demand profile must NOT be trusted for a scoped session")
	}
}

func TestApplyPathScopeSeatbeltEnforcement(t *testing.T) {
	if !CanApply() {
		t.Skip("seatbelt sandbox_apply unavailable or blocked by outer sandbox")
	}
	ws := resolvePathSymlinks(t.TempDir())
	outsideTmp := resolvePathSymlinks(t.TempDir())
	authDir := filepath.Join(ws, "internal", "auth")
	webDir := filepath.Join(ws, "web", "src")
	policyDir := filepath.Join(webDir, "policy")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(authDir, "token.go")
	authScript := filepath.Join(authDir, "run.sh")
	policyFile := filepath.Join(policyDir, "rule.ts")
	if err := os.WriteFile(authFile, []byte("package auth\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authScript, []byte("#!/bin/sh\necho pwned\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyFile, []byte("export const strict = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	base, err := DefaultProfile(ws, NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	prof, err := ApplyPathScope(base, ws, PathScope{
		ReadOnlyPaths: []string{"internal/auth", "web/src/policy"},
		WritablePaths: []string{"web/src"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Reading core read-only file succeeds.
	bin, args, err := WrapShell(prof, "cat "+shellQuote(authFile)+" && cat "+shellQuote(policyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := runCommand(bin, args...); err != nil {
		t.Fatalf("expected reading read-only files to succeed: %v", err)
	}

	// Writing inside writable path and external temp dir succeeds.
	okFile := filepath.Join(webDir, "ok.txt")
	tmpScratch := filepath.Join(outsideTmp, "scratch.txt")
	bin, args, err = WrapShell(prof, "echo ok > "+shellQuote(okFile)+" && echo tmp > "+shellQuote(tmpScratch))
	if err != nil {
		t.Fatal(err)
	}
	if err := runCommand(bin, args...); err != nil {
		t.Fatalf("expected writing inside writable path and temp dir to succeed: %v", err)
	}

	// Writing, deleting, chmod, renaming, or directly executing inside ReadOnlyPaths
	// (including ReadOnlyPaths nested inside WritablePaths) and writing anywhere else
	// in the workspace outside WritablePaths must fail.
	for _, cmd := range []string{
		"echo bad >> " + shellQuote(authFile),
		"rm -f " + shellQuote(authFile),
		"chmod 777 " + shellQuote(authFile),
		"mv " + shellQuote(authFile) + " " + shellQuote(filepath.Join(webDir, "stolen.go")),
		shellQuote(authScript),
		"echo bad >> " + shellQuote(policyFile),
		"echo outside > " + shellQuote(filepath.Join(ws, "root.txt")),
		"mv " + shellQuote(okFile) + " " + shellQuote(filepath.Join(ws, "escaped.txt")),
	} {
		bin, args, err = WrapShell(prof, cmd)
		if err != nil {
			t.Fatal(err)
		}
		if err := runCommand(bin, args...); err == nil {
			t.Fatalf("expected confined command to fail: %s", cmd)
		}
	}
}

func TestApplyPathScopeBlocksNestedAIRuleFileCreation(t *testing.T) {
	ws := resolvePathSymlinks(t.TempDir())
	subDir := filepath.Join(ws, "packages", "feature")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base, err := DefaultProfile(ws, NetworkDeny)
	if err != nil {
		t.Fatal(err)
	}
	prof, err := ApplyPathScope(base, ws, PathScope{
		ReadOnlyPaths: []string{"AGENTS.md", "CLAUDE.md", ".cursorrules"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text, err := ProfileText(prof)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `[Aa][Gg][Ee][Nn][Tt][Ss]\.[Mm][Dd]`) {
		t.Fatalf("expected case-insensitive AGENTS.md regex deny rule in Seatbelt profile:\n%s", text)
	}
	if !CanApply() {
		return
	}
	// Attempting to create brand-new nested rule files inside an otherwise writable subdirectory must fail at kernel layer.
	for _, target := range []string{
		filepath.Join(subDir, "AGENTS.md"),
		filepath.Join(subDir, "claude.md"),
		filepath.Join(subDir, ".cursorrules"),
	} {
		bin, args, wErr := WrapShell(prof, "echo evil > "+shellQuote(target))
		if wErr != nil {
			t.Fatal(wErr)
		}
		if err := runCommand(bin, args...); err == nil {
			t.Fatalf("expected Seatbelt to block creating nested rule file %s", target)
		}
	}
	// Ordinary file in the same subdirectory remains writable.
	bin, args, wErr := WrapShell(prof, "echo ok > "+shellQuote(filepath.Join(subDir, "notes.md")))
	if wErr != nil {
		t.Fatal(wErr)
	}
	if err := runCommand(bin, args...); err != nil {
		t.Fatalf("expected ordinary nested file write to succeed: %v", err)
	}
}
