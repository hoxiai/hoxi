package provider

import (
	"os"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// approvalPermissionKeys are the OpenCode permissions Hoxi's approval policy
// decides. OpenCode must ask for them so every request reaches the runner's
// policy; an "allow" here would run the tool before Hoxi ever sees it.
var approvalPermissionKeys = []string{"edit", "webfetch", "websearch", "codesearch", "bash", "hoxi_browser", "hoxi_tools"}

// normalizeApprovalPermissions keeps OpenCode asking Hoxi. Pattern objects
// saved from approval cards get a "*": "ask" fallback, because OpenCode's
// built-in default for an unmatched pattern is allow (a single "git *" rule
// would otherwise let every other command run unasked). "deny" stays.
// Turning legacy "allow" values into "ask" waits until the approval policy
// file exists, so the desktop can first migrate them into the policy.
func normalizeApprovalPermissions(doc map[string]interface{}, enforceAsk bool) bool {
	perm, _ := doc["permission"].(map[string]interface{})
	changed := false
	if perm == nil {
		perm = map[string]interface{}{}
		doc["permission"] = perm
		changed = true
	}
	for _, key := range approvalPermissionKeys {
		switch v := perm[key].(type) {
		case nil:
			perm[key] = "ask"
			changed = true
		case string:
			if v != "ask" && v != "deny" && (enforceAsk || v != "allow") {
				perm[key] = "ask"
				changed = true
			}
		case map[string]interface{}:
			if ds, exists := v["**"]; exists && ds != "ask" && ds != "deny" {
				delete(v, "**")
				changed = true
			}
			star := v["*"]
			doubleStar := v["**"]
			if (star != "ask" && star != "deny") && (doubleStar != "ask" && doubleStar != "deny") {
				v["*"] = "ask"
				changed = true
			} else if star != nil && star != "ask" && star != "deny" {
				v["*"] = "ask"
				changed = true
			}
		default:
			perm[key] = "ask"
			changed = true
		}
	}
	return changed
}

func approvalPolicyExists() bool {
	path := hoxipaths.ApprovalPolicyPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// PermissionRules returns the OpenCode `permission` block, whose pattern
// rules the runner's approval policy applies to every runtime.
func (g *Service) PermissionRules() map[string]interface{} {
	perm, _ := g.loadHoxiConfigDoc()["permission"].(map[string]interface{})
	return perm
}

// EnforceAskPermissions rewrites the OpenCode permission block after the
// approval policy is saved, and reloads OpenCode once no session is busy.
func (g *Service) EnforceAskPermissions() {
	g.configPersistMu.Lock()
	doc := g.loadHoxiConfigDoc()
	changed := normalizeApprovalPermissions(doc, approvalPolicyExists())
	var err error
	if changed {
		err = g.persistHoxiConfigFiles(doc)
	}
	g.configPersistMu.Unlock()
	if changed && err == nil {
		g.reloadOpenCodeGlobalConfig()
	}
}
