package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQuotedCDPathsKeepDirectoryScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project space")
	if err := os.MkdirAll(filepath.Join(root, "src locale", "nested folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`cd "src locale" && node -e "console.log(1)"`,
		`cd 'src locale' && python3 -c 'print(1)'`,
		`cd 'src~ locale' && node -e "console.log(1)"`,
		`cd "src locale" 2>/dev/null && node -e "console.log(1)"`,
		`cd "src locale" && cd "nested folder" && node -e "console.log(1)"`,
		`cd "` + filepath.ToSlash(filepath.Join(root, "src locale")) + `" && node -e "console.log(1)"`,
		`cd "../sibling folder" && node -e "console.log(1)"`,
	} {
		if !approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, root) {
			t.Errorf("quoted ordinary directory asked: %s", command)
		}
	}
	if got := decideCommandPermissionIn(`cd "src locale" && git status`, root); got != "allow" {
		t.Errorf("scoped smart-mode command asked: %s", got)
	}
	for _, command := range []string{
		`cd "$UNKNOWN/locale" && node -e "console.log(1)"`,
		`cd "src locale"more && node -e "console.log(1)"`,
		`cd "src locale/*" && node -e "console.log(1)"`,
		`cd "-src locale" && node -e "console.log(1)"`,
		`cd "" && node -e "console.log(1)"`,
		`cd "/" && node -e "console.log(1)"`,
		`cd ".git" && node -e "console.log(1)"`,
		`cd "src locale/../.git" && node -e "console.log(1)"`,
		`cd "$HOME/.ssh" && node -e "console.log(1)"`,
		`cd '$HOME/code path' && node -e "console.log(1)"`,
		`cd "~/code path" && node -e "console.log(1)"`,
		`cd "src locale" && git log --output=../.git/config`,
		`cd .' ls' ; r\m -rf .`,
	} {
		if approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, root) {
			t.Errorf("quoted path escaped directory checks: %s", command)
		}
	}
}

func TestQuotedCDPathsResolveSymlinksBeforeApproval(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside folder")
	secret := filepath.Join(t.TempDir(), ".ssh")
	for _, dir := range []string{inside, secret} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, target string
		allow        bool
	}{
		{"linked folder", inside, true},
		{"escape folder", t.TempDir(), false},
		{"secret folder", secret, false},
	} {
		if err := os.Symlink(tc.target, filepath.Join(root, tc.name)); err != nil {
			t.Fatal(err)
		}
		command := `cd "` + tc.name + `" && node -e "console.log(1)"`
		if got := approvalAutoApproves(bashRequest(command), ApprovalPolicy{Mode: ApprovalModeAuto}, nil, root); got != tc.allow {
			t.Errorf("%s: approved=%v, want %v", tc.name, got, tc.allow)
		}
	}
}
