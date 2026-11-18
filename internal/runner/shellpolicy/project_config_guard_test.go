package shellpolicy

import (
	"path/filepath"
	"testing"
)

func TestProjectConfigCannotBeChangedThroughGrants(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{"hoxi.json", filepath.Join(root, "hoxi.json"), filepath.Join(root, "nested", "hoxi.json")} {
		if !isProtectedWorktreePath(root, target) {
			t.Errorf("not protected: %s", target)
		}
		if !fileGrantRefused(target) {
			t.Errorf("file grant allowed: %s", target)
		}
		item := map[string]interface{}{"permission": "edit", "patterns": []string{target}}
		for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted} {
			if approvalAutoApproves(item, ApprovalPolicy{Mode: mode}, map[string]interface{}{"edit": "allow"}, root) {
				t.Errorf("%s auto-approved %s", mode, target)
			}
		}
	}
}
