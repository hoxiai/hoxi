package provider

import "testing"

func TestNormalizeApprovalPermissionsKeepsOpenCodeAsking(t *testing.T) {
	doc := map[string]interface{}{"permission": map[string]interface{}{
		"edit":               "allow",
		"webfetch":           map[string]interface{}{"https://docs.hoxi.ai/*": "allow"},
		"bash":               map[string]interface{}{"git *": "allow", "*": "ask"},
		"websearch":          "deny",
		"external_directory": map[string]interface{}{"/Users/u/code/x/*": "allow"},
	}}
	// Before the approval policy exists, legacy allow values are left for the
	// desktop to migrate, but pattern objects already get their fallback.
	if !normalizeApprovalPermissions(doc, false) {
		t.Fatal("a pattern object without * must change")
	}
	perm := doc["permission"].(map[string]interface{})
	if perm["edit"] != "allow" {
		t.Fatalf("edit migrated too early: %v", perm["edit"])
	}
	if web := perm["webfetch"].(map[string]interface{}); web["*"] != "ask" || web["https://docs.hoxi.ai/*"] != "allow" {
		t.Fatalf("webfetch: %v", web)
	}
	if !normalizeApprovalPermissions(doc, true) {
		t.Fatal("enforcing must change legacy allow values")
	}
	for key, want := range map[string]interface{}{"edit": "ask", "websearch": "deny", "codesearch": "ask"} {
		if perm[key] != want {
			t.Errorf("%s = %v, want %v", key, perm[key], want)
		}
	}
	if bash := perm["bash"].(map[string]interface{}); len(bash) != 2 {
		t.Fatalf("bash rules must be kept verbatim: %v", bash)
	}
	if ext := perm["external_directory"].(map[string]interface{}); len(ext) != 1 {
		t.Fatalf("external_directory is OpenCode's own default ask and must stay untouched: %v", ext)
	}
	if normalizeApprovalPermissions(doc, true) {
		t.Fatal("normalizing twice must be a no-op")
	}
}

func TestNormalizeApprovalPermissionsCreatesTheBlockOnlyWhenEnforcing(t *testing.T) {
	doc := map[string]interface{}{}
	if normalizeApprovalPermissions(doc, false) || doc["permission"] != nil {
		t.Fatal("no permission block must be invented before migration")
	}
	if !normalizeApprovalPermissions(doc, true) {
		t.Fatal("enforcing must create the block")
	}
	if perm := doc["permission"].(map[string]interface{}); perm["bash"] != "ask" || perm["edit"] != "ask" {
		t.Fatalf("permission: %v", perm)
	}
}
