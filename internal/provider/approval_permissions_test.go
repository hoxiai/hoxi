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

func TestNormalizeApprovalPermissionsCreatesBlockOnFreshInstallAndOverwritesStarAllow(t *testing.T) {
	doc := map[string]interface{}{}
	if !normalizeApprovalPermissions(doc, false) {
		t.Fatal("fresh install without permission block must default to ask")
	}
	perm, _ := doc["permission"].(map[string]interface{})
	if perm == nil || perm["bash"] != "ask" || perm["edit"] != "ask" || perm["codesearch"] != "ask" {
		t.Fatalf("permission: %v", perm)
	}

	starAllowDoc := map[string]interface{}{
		"permission": map[string]interface{}{
			"bash":     map[string]interface{}{"git *": "allow", "*": "allow"},
			"webfetch": map[string]interface{}{"https://docs.hoxi.ai/*": "allow", "**": "allow"},
		},
	}
	if !normalizeApprovalPermissions(starAllowDoc, false) {
		t.Fatal("wildcard allow in pattern map must be rewritten to ask")
	}
	starPerm := starAllowDoc["permission"].(map[string]interface{})
	if bash := starPerm["bash"].(map[string]interface{}); bash["*"] != "ask" || bash["git *"] != "allow" {
		t.Fatalf("bash wildcard allow not overwritten: %v", bash)
	}
	if web := starPerm["webfetch"].(map[string]interface{}); web["*"] != "ask" || web["**"] != nil {
		t.Fatalf("webfetch ** allow not cleaned up: %v", web)
	}
}
