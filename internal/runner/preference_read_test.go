package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func setupAttributionPreference(t *testing.T, custom bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HOXI_HOME", "")
	if custom {
		t.Setenv("HOXI_HOME", filepath.Join(home, "custom-state"))
	}
	path := hoxipaths.Join(worktree.AttributionFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mode":"auto","enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAttributionPreferenceReadApproval(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "custom"}[custom], func(t *testing.T) {
			path := setupAttributionPreference(t, custom)
			for _, item := range []map[string]interface{}{
				req("read", path),
				toolReq("read_file", map[string]interface{}{"args": map[string]interface{}{"Path": path, "StartLine": 1, "EndLine": 50}}),
				toolReq("Read", map[string]interface{}{"filepath": path, "externalDirectory": true}),
				{"permission": "external_directory", "patterns": []interface{}{path}, "metadata": map[string]interface{}{"tool": "read_file"}},
			} {
				if isDestructivePermissionRequest(item) {
					t.Errorf("ordinary preference read marked sensitive: %#v", item)
				}
				for _, mode := range []string{ApprovalModeAuto, ApprovalModeSmart, ApprovalModeTrusted} {
					if !approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, nil, t.TempDir()) {
						t.Errorf("preference read asked in %s: %#v", mode, item)
					}
				}
				if cat := unattendedHardDeny(item, t.TempDir()); cat != "" {
					t.Errorf("preference read hard denied: %s", cat)
				}
			}
			for _, item := range []map[string]interface{}{
				req("edit", path), req("write", path),
				toolReq("Write", map[string]interface{}{"args": map[string]interface{}{"file_path": path}}),
			} {
				for _, mode := range []string{ApprovalModeAuto, ApprovalModeSmart, ApprovalModeTrusted} {
					if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, map[string]interface{}{"edit": "allow"}, t.TempDir()) {
						t.Errorf("preference write bypassed approval in %s: %#v", mode, item)
					}
				}
			}
		})
	}
}

func TestAttributionPreferenceReadRejectsAmbiguousAndBroaderRequests(t *testing.T) {
	path := setupAttributionPreference(t, false)
	root := filepath.Dir(path)
	for _, target := range []string{
		path + " ", " " + path, root, root + "/*", path + ".bak", root + "/./" + worktree.AttributionFile,
		root + "/../.hoxi/" + worktree.AttributionFile, filepath.Join(t.TempDir(), ".hoxi", worktree.AttributionFile),
		filepath.Join(root, "auth.json"), filepath.Join(root, "models.json"),
	} {
		item := toolReq("read_file", map[string]interface{}{"filepath": strings.TrimSpace(target), "args": map[string]interface{}{"Path": target}})
		if isAttributionPreferenceReadRequest(item, strings.TrimSpace(target)) ||
			approvalAutoApproves(item, ApprovalPolicy{Mode: ApprovalModeAuto}, nil, t.TempDir()) {
			t.Errorf("unsafe preference target approved: %q", target)
		}
	}
	for _, item := range []map[string]interface{}{
		req("external_directory", path),
		{"permission": "external_directory", "patterns": []interface{}{path}, "metadata": map[string]interface{}{"tool": "unknown_tool"}},
		{"permission": "read", "patterns": []interface{}{path}, "metadata": map[string]interface{}{"tool": "unknown_tool"}},
		{"permission": "external_directory", "patterns": []interface{}{path}, "metadata": map[string]interface{}{"tool": "read_file", "command": "cat file"}},
		{"permission": "external_directory", "patterns": []interface{}{path}, "metadata": map[string]interface{}{"tool": "read_file", "destructive": true}},
		{"permission": "external_directory", "patterns": []interface{}{path}, "always": []interface{}{root + "/*"}, "metadata": map[string]interface{}{"tool": "read_file"}},
		toolReq("read_many_files", map[string]interface{}{"args": map[string]interface{}{"paths": []interface{}{path, filepath.Join(root, "auth.json")}}}),
	} {
		if approvalAutoApproves(item, ApprovalPolicy{Mode: ApprovalModeAuto}, nil, t.TempDir()) {
			t.Errorf("broader/non-read request used preference exemption: %#v", item)
		}
	}
	spoofed := filepath.Join(t.TempDir(), ".hoxi", worktree.AttributionFile)
	if isAttributionPreferenceReadRequest(toolReq("read_file", map[string]interface{}{"hoxiHome": filepath.Dir(spoofed)}), spoofed) {
		t.Fatal("request metadata must not select the trusted Hoxi home")
	}
}

func TestAttributionPreferenceReadRejectsSymlinkAndDirectoryTargets(t *testing.T) {
	for _, targetName := range []string{"auth.json", "other.json", "directory"} {
		t.Run(targetName, func(t *testing.T) {
			path := setupAttributionPreference(t, false)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(filepath.Dir(path), targetName)
			if targetName == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if isAttributionPreferenceReadPath(path) || approvalAutoApproves(req("read", path), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, t.TempDir()) {
				t.Fatal("symlink or directory masquerading as preference file was approved")
			}
		})
	}
	t.Run("custom-root-directory", func(t *testing.T) {
		path := setupAttributionPreference(t, true)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if approvalAutoApproves(req("read", path), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, t.TempDir()) {
			t.Fatal("custom-root directory used the exact-file exception")
		}
	})
}

func TestAttributionPreferenceReadChecksRealRoot(t *testing.T) {
	for _, secret := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-root", true: "credential-root"}[secret], func(t *testing.T) {
			home := t.TempDir()
			actual := t.TempDir()
			if secret {
				actual = filepath.Join(actual, ".ssh")
			}
			if err := os.MkdirAll(actual, 0o700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(home, ".hoxi")
			if err := os.Symlink(actual, root); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOXI_HOME", root)
			if got := isAttributionPreferenceReadPath(filepath.Join(root, worktree.AttributionFile)); got == secret {
				t.Errorf("root proof=%v, secret=%v", got, secret)
			}
		})
	}
}

func TestHookReadFileAttributionPreference(t *testing.T) {
	path := setupAttributionPreference(t, false)
	f := newGateFixture(t)
	defer f.revoke()
	defer f.srv.CloseClientConnections()
	for _, tc := range []struct {
		tool, path string
		allow      bool
	}{
		{"read_file", path, true}, {"read_file", path + " ", false}, {"write_file", path, false},
		{"read_file", `"` + path + `"`, false},
	} {
		args := map[string]interface{}{"Path": tc.path, "StartLine": 1, "EndLine": 50}
		_, _, destructive, external := f.svc.inspectHookToolRisk("ses_agy_gate", tc.tool, args)
		if destructive == tc.allow || !external {
			t.Errorf("%s %q: destructive=%v external=%v", tc.tool, tc.path, destructive, external)
		}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1", "stepIdx": 4, "workspacePaths": []string{f.dir},
		"toolCall": map[string]interface{}{"name": "read_file", "args": map[string]interface{}{"Path": path, "StartLine": 1, "EndLine": 50}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := waitDecision(t, f.asyncHook(t, payload), "attribution read_file")
	if result.Decision != "auto_approve" {
		t.Fatalf("read_file preference raised approval: %+v", result)
	}
}
