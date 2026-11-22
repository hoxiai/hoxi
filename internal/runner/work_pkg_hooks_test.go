package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSessionBundleResolverLifecycleValidationAndEnvScrubbing(t *testing.T) {
	home := resolveExistingAncestors(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	projectDir := resolveExistingAncestors(filepath.Join(home, "projects", "shop"))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(projectDir, nil, nil, filepath.Join(home, "runner-sessions.json"))
	t.Cleanup(svc.CloseAll)

	// Unregistered resolver returns empty spec without error.
	empty, err := svc.ResolveSessionBundle("ses_1", projectDir)
	if err != nil || !empty.IsEmpty() {
		t.Fatalf("expected empty spec when unset, got (%+v, %v)", empty, err)
	}

	versionDir := filepath.Join(home, ".hoxi", "work", "packages", "ecom-ops", "versions", "1.0.0")
	currentLink := filepath.Join(home, ".hoxi", "work", "packages", "ecom-ops", "current")
	skillDir := filepath.Join(versionDir, "skills")
	runtimeBin := filepath.Join(home, ".hoxi", "work", "runtimes", "ecom-ops", "bin")
	runtimeRoot := filepath.Join(home, ".hoxi", "work", "runtimes", "ecom-ops")
	toolchainBin := filepath.Join(home, ".hoxi", "work", "toolchains", "uv", "bin")

	for _, d := range []string{skillDir, runtimeBin, toolchainBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(versionDir, currentLink); err != nil {
		t.Fatal(err)
	}

	svc.SetSessionBundleResolver(func(sessionID, directory string) (SessionBundleSpec, error) {
		if sessionID != "ses_bound" {
			return SessionBundleSpec{}, nil
		}
		return SessionBundleSpec{
			AppIDs:            []string{"ecom-ops", "ecom-ops"},
			ExtraSkillDirs:    []string{filepath.Join(currentLink, "skills"), skillDir},
			ExtraPathDirs:     []string{runtimeBin, toolchainBin, runtimeBin},
			ReadOnlyExecRoots: []string{currentLink, runtimeRoot},
			ExtraEnv: map[string]string{
				// Safe keys that must survive:
				"HOXI_WORK_APP_ID":         "ecom-ops",
				"WORK_DEFAULT_MARKETPLACE": "US",
				"UV_LINK_MODE":             "copy",
				"PLAYWRIGHT_BROWSERS_PATH": filepath.Join(home, ".hoxi", "work", "toolchains", "browsers"),
				"PYTHONUNBUFFERED":         "1",
				// Secret / credential keys & values that must be stripped:
				"OPENAI_API_KEY":          "sk-proj-1234567890abcdefghijklmnop",
				"ANTHROPIC_API_KEY":       "sk-ant-1234567890abcdefghijklmnop",
				"SHOPIFY_ACCESS_TOKEN":    "shpat_secret",
				"STORE_CLIENT_SECRET":     "topsecret",
				"DB_PASSWORD":             "hunter2",
				"SSH_AUTH_SOCK":           "/tmp/agent.sock",
				"KUBECONFIG":              "/tmp/kubeconfig",
				"PLAIN_LOOKING_GH_PAT":    "ghp_1234567890abcdefghijklmnopqrstuvwx",
				"PLAIN_LOOKING_URL_CRED":  "https://admin:s3cret@api.example.com/v1",
				"PLAIN_LOOKING_BEARER":    "Bearer eyJhbGciOiJIUzI1NiJ9.secret",
				"PLAIN_LOOKING_RELAY_KEY": "hk-live-1234567890abcdef1234",
				"HOXI_WORK_SECRET":        "still-a-secret",
				// Dangerous dynamic-linker / runtime / shell / Hoxi-control keys that must be stripped:
				"PATH":                   "/tmp/hijack:/usr/bin",
				"HOME":                   "/tmp/fake-home",
				"LD_PRELOAD":             "/tmp/evil.so",
				"LD_LIBRARY_PATH":        "/tmp/lib",
				"DYLD_INSERT_LIBRARIES":  "/tmp/evil.dylib",
				"NODE_OPTIONS":           "--require=/tmp/preload.js",
				"PYTHONPATH":             "/tmp/pyhijack",
				"PYTHONSTARTUP":          "/tmp/startup.py",
				"BASH_ENV":               "/tmp/bashenv.sh",
				"ENV":                    "/tmp/shinit.sh",
				"BASH_FUNC_ls%%":         "() { /tmp/evil; }",
				"GIT_SSH_COMMAND":        "evil-ssh",
				"HTTPS_PROXY":            "http://127.0.0.1:9999",
				"SSL_CERT_FILE":          "/tmp/mitm.pem",
				"HOXI_HOME":              "/tmp/spoofed-hoxi",
				"HOXI_HOOK_TOKEN":        "forged",
				"HOXI_HOOK_URL":          "http://127.0.0.1:9999/hook",
				"HOXI_RUNNER_SESSION_ID": "ses_other",
				"OPENCODE_PERMISSION":    `{"*":"allow"}`,
				"CLAUDE_CONFIG_DIR":      "/tmp/claude",
				"GEMINI_CLI_HOME":        "/tmp/gemini",
				"NEWLINE_INJECTION":      "good\nLD_PRELOAD=/tmp/evil.so",
				"BAD KEY":                "val",
			},
		}, nil
	})

	spec, err := svc.ResolveSessionBundle("ses_bound", projectDir)
	if err != nil {
		t.Fatalf("ResolveSessionBundle failed: %v", err)
	}
	if len(spec.AppIDs) != 1 || spec.AppIDs[0] != "ecom-ops" {
		t.Fatalf("AppIDs = %v, want [ecom-ops]", spec.AppIDs)
	}
	if len(spec.ExtraSkillDirs) != 1 || spec.ExtraSkillDirs[0] != skillDir {
		t.Fatalf("ExtraSkillDirs = %v, want [%s]", spec.ExtraSkillDirs, skillDir)
	}
	if len(spec.ExtraPathDirs) != 2 || spec.ExtraPathDirs[0] != runtimeBin || spec.ExtraPathDirs[1] != toolchainBin {
		t.Fatalf("ExtraPathDirs = %v, want [%s %s]", spec.ExtraPathDirs, runtimeBin, toolchainBin)
	}
	if len(spec.ReadOnlyExecRoots) != 2 || spec.ReadOnlyExecRoots[0] != versionDir || spec.ReadOnlyExecRoots[1] != runtimeRoot {
		t.Fatalf("ReadOnlyExecRoots = %v, want [%s %s]", spec.ReadOnlyExecRoots, versionDir, runtimeRoot)
	}

	wantEnv := map[string]string{
		"HOXI_WORK_APP_ID":         "ecom-ops",
		"WORK_DEFAULT_MARKETPLACE": "US",
		"UV_LINK_MODE":             "copy",
		"PLAYWRIGHT_BROWSERS_PATH": filepath.Join(home, ".hoxi", "work", "toolchains", "browsers"),
		"PYTHONUNBUFFERED":         "1",
	}
	if len(spec.ExtraEnv) != len(wantEnv) {
		t.Fatalf("sanitized ExtraEnv = %#v, want %#v", spec.ExtraEnv, wantEnv)
	}
	for k, wantVal := range wantEnv {
		if got := spec.ExtraEnv[k]; got != wantVal {
			t.Errorf("ExtraEnv[%q] = %q, want %q", k, got, wantVal)
		}
	}

	// Verify ApplyEnv appends ExtraPathDirs to the tail of PATH without shadowing /usr/bin.
	mergedEnv := spec.ApplyEnv([]string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"})
	var gotPath string
	for _, kv := range mergedEnv {
		if after, ok := strings.CutPrefix(kv, "PATH="); ok {
			gotPath = after
		}
	}
	wantPath := strings.Join([]string{"/usr/bin", "/bin", runtimeBin, toolchainBin}, string(os.PathListSeparator))
	if gotPath != wantPath {
		t.Fatalf("ApplyEnv PATH = %q, want %q", gotPath, wantPath)
	}

	// Clearing resolver with nil restores empty behavior.
	svc.SetSessionBundleResolver(nil)
	cleared, err := svc.ResolveSessionBundle("ses_bound", projectDir)
	if err != nil || !cleared.IsEmpty() {
		t.Fatalf("expected cleared resolver to return empty spec, got (%+v, %v)", cleared, err)
	}
}

func TestSessionBundleRejectsUnsafePathsAndSandboxMismatch(t *testing.T) {
	home := resolveExistingAncestors(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	projectDir := resolveExistingAncestors(filepath.Join(home, "projects", "repo"))
	otherDir := resolveExistingAncestors(filepath.Join(home, "projects", "other"))
	sshDir := filepath.Join(home, ".ssh")
	storesDir := filepath.Join(home, ".hoxi", "work", "stores")
	sessionsDir := filepath.Join(home, ".hoxi", "sessions")
	workWorkspaceDir := filepath.Join(home, ".hoxi", "work", "workspace")
	gitDir := filepath.Join(projectDir, ".git")
	pkgDir := filepath.Join(home, ".hoxi", "work", "packages", "bad-app")

	for _, d := range []string{projectDir, otherDir, sshDir, storesDir, sessionsDir, workWorkspaceDir, gitDir, pkgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	regularFile := filepath.Join(projectDir, "README.md")
	if err := os.WriteFile(regularFile, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	escapingLink := filepath.Join(pkgDir, "current")
	if err := os.MkdirAll(filepath.Join(otherDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(otherDir, escapingLink); err != nil {
		t.Fatal(err)
	}
	siblingPkgVer := filepath.Join(home, ".hoxi", "work", "packages", "other-app", "versions", "1.0.0")
	crossAppPkg := filepath.Join(home, ".hoxi", "work", "packages", "cross-app")
	for _, d := range []string{siblingPkgVer, crossAppPkg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	crossAppLink := filepath.Join(crossAppPkg, "current")
	if err := os.Symlink(siblingPkgVer, crossAppLink); err != nil {
		t.Fatal(err)
	}

	badSpecs := []struct {
		name string
		spec SessionBundleSpec
	}{
		{"invalid appId", SessionBundleSpec{AppIDs: []string{"../evil"}}},
		{"relative skill dir", SessionBundleSpec{ExtraSkillDirs: []string{"skills/local"}}},
		{"tilde path dir", SessionBundleSpec{ExtraPathDirs: []string{"~/bin"}}},
		{"whitespace padded path", SessionBundleSpec{ExtraPathDirs: []string{" " + otherDir}}},
		{"path separator injection", SessionBundleSpec{ExtraPathDirs: []string{otherDir + string(os.PathListSeparator) + "/tmp"}}},
		{"dotdot segment", SessionBundleSpec{ExtraSkillDirs: []string{projectDir + "/../other"}}},
		{"non-existent dir", SessionBundleSpec{ExtraSkillDirs: []string{filepath.Join(projectDir, "does-not-exist")}}},
		{"regular file not dir", SessionBundleSpec{ExtraSkillDirs: []string{regularFile}}},
		{"filesystem root", SessionBundleSpec{ReadOnlyExecRoots: []string{string(filepath.Separator)}}},
		{"user home root", SessionBundleSpec{ReadOnlyExecRoots: []string{home}}},
		{"credential ssh dir", SessionBundleSpec{ExtraSkillDirs: []string{sshDir}}},
		{"protected work stores", SessionBundleSpec{ExtraSkillDirs: []string{storesDir}}},
		{"hoxi sessions dir", SessionBundleSpec{ReadOnlyExecRoots: []string{sessionsDir}}},
		{"mutable work workspace as exec root", SessionBundleSpec{ReadOnlyExecRoots: []string{workWorkspaceDir}}},
		{"git directory", SessionBundleSpec{ExtraSkillDirs: []string{gitDir}}},
		{"temp directory /private/tmp", SessionBundleSpec{ExtraPathDirs: []string{"/private/tmp"}}},
		{"symlink escaping packages root", SessionBundleSpec{ReadOnlyExecRoots: []string{escapingLink}}},
		{"nested skill dir under escaping package symlink", SessionBundleSpec{ExtraSkillDirs: []string{filepath.Join(escapingLink, "skills")}}},
		{"cross-app package symlink", SessionBundleSpec{ReadOnlyExecRoots: []string{crossAppLink}}},
	}

	for _, tc := range badSpecs {
		if _, err := SanitizeSessionBundleSpec(tc.spec); err == nil {
			t.Errorf("%s: expected SanitizeSessionBundleSpec(%+v) to fail", tc.name, tc.spec)
		}
	}

	// Verify session sandbox directory mismatch fails closed before invoking resolver.
	svc := NewService(projectDir, nil, nil, filepath.Join(home, "runner-sessions.json"))
	t.Cleanup(svc.CloseAll)
	if err := svc.store.Put(SessionRecord{
		ID:           "ses_isolated",
		Runtime:      "native",
		SandboxMode:  "local",
		ProjectDir:   projectDir,
		WorktreePath: projectDir,
	}); err != nil {
		t.Fatal(err)
	}

	called := false
	svc.SetSessionBundleResolver(func(_, _ string) (SessionBundleSpec, error) {
		called = true
		return SessionBundleSpec{}, nil
	})
	if _, err := svc.ResolveSessionBundle("ses_isolated", otherDir); err == nil {
		t.Fatal("expected mismatched session directory to fail")
	}
	if called {
		t.Fatal("resolver must not be invoked when session directory mismatches sandbox")
	}

	// Propagates resolver error cleanly.
	wantErr := errors.New("bundle manifest corrupted")
	svc.SetSessionBundleResolver(func(_, _ string) (SessionBundleSpec, error) {
		return SessionBundleSpec{}, wantErr
	})
	if _, err := svc.ResolveSessionBundle("ses_isolated", projectDir); !errors.Is(err, wantErr) {
		t.Fatalf("expected resolver error %v, got %v", wantErr, err)
	}
}

func TestSessionBundleResolverConcurrentAccess(t *testing.T) {
	home := resolveExistingAncestors(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOXI_HOME", filepath.Join(home, ".hoxi"))

	skillDir := filepath.Join(home, ".hoxi", "work", "packages", "app-a", "current", "skills")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	svc := NewService(home, nil, nil, filepath.Join(home, "runner-sessions.json"))
	t.Cleanup(svc.CloseAll)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(iter int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				if (iter+j)%3 == 0 {
					svc.SetSessionBundleResolver(nil)
					continue
				}
				svc.SetSessionBundleResolver(func(sessionID, _ string) (SessionBundleSpec, error) {
					return SessionBundleSpec{
						AppIDs:         []string{"app-a"},
						ExtraSkillDirs: []string{skillDir},
						ExtraEnv: map[string]string{
							"WORK_SESSION":   sessionID,
							"OPENAI_API_KEY": "sk-secret-must-strip-1234567890123456",
							"LD_PRELOAD":     "/tmp/evil.so",
						},
					}, nil
				})
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				spec, err := svc.ResolveSessionBundle("ses_race", home)
				if err != nil {
					t.Errorf("concurrent ResolveSessionBundle failed: %v", err)
					return
				}
				if _, leaked := spec.ExtraEnv["OPENAI_API_KEY"]; leaked {
					t.Errorf("secret leaked during concurrent resolution")
					return
				}
				if _, leaked := spec.ExtraEnv["LD_PRELOAD"]; leaked {
					t.Errorf("LD_PRELOAD leaked during concurrent resolution")
					return
				}
			}
		}()
	}
	wg.Wait()
}
