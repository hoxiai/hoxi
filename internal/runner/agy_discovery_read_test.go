package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bundleDiscoveryCommand = `node -e 'const fs = require("fs"); const f = fs.readdirSync("web/dist/assets").find(x => x.startsWith("surface-work-") && !x.startsWith("surface-work-core-")); console.log("FILE:", f); const code = fs.readFileSync("web/dist/assets/" + f, "utf8"); const idx = code.indexOf("renderComposer"); console.log(code.slice(Math.max(0, idx - 600), idx + 1200));'`

func TestAgyDiscoveryReadRetriesWithScopedTools(t *testing.T) {
	f := newGateFixture(t)
	t.Cleanup(func() { f.svc.cancelSessionApprovals("ses_agy_gate", "test finished") })
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	assets := filepath.Join(f.dir, "web", "dist", "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(assets, "surface-work-Abcd.js")
	if err := os.WriteFile(asset, []byte("renderComposer"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := func(step int, tool string, args map[string]interface{}) []byte {
		request := map[string]interface{}{
			"conversationId": "conv-1",
			"toolCall":       map[string]interface{}{"name": tool, "args": args},
		}
		if step >= 0 {
			request["stepIdx"] = step
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	command := func(step int, cmd string) []byte {
		return payload(step, "run_command", map[string]interface{}{"Cwd": f.dir, "CommandLine": cmd})
	}
	variant := strings.Replace(bundleDiscoveryCommand, `!x.startsWith("surface-work-core-")`, `!x.startsWith("surface-work-core-") && x.endsWith(".js")`, 1)
	first := command(0, bundleDiscoveryCommand)
	// A transport redelivery carries the same step index, including step zero.
	for _, body := range [][]byte{first, first, first, command(1, variant)} {
		d := waitDecision(t, f.asyncHook(t, body), "scoped read hint")
		if d.Decision != "deny" || !strings.Contains(d.Reason, "list_dir") || !strings.Contains(d.Reason, "exact path") {
			t.Fatalf("expected a file-tool retry, got %+v", d)
		}
	}
	for _, body := range [][]byte{
		payload(2, "list_dir", map[string]interface{}{"DirectoryPath": assets}),
		payload(3, "view_file", map[string]interface{}{"AbsolutePath": asset}),
	} {
		if d := waitDecision(t, f.asyncHook(t, body), "scoped read"); d.Decision != "auto_approve" {
			t.Fatalf("ordinary asset inspection: %+v", d)
		}
	}

	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	mustAsk := func(t *testing.T, body []byte, permission string) {
		t.Helper()
		result := f.asyncHook(t, body)
		asked := waitForEvent(t, events, "permission.asked")
		if asked.Properties["permission"] != permission {
			t.Errorf("permission = %v, want %s", asked.Properties["permission"], permission)
		}
		if status := f.reply(t, firstString(asked.Properties["id"]), "reject", ""); status != 200 {
			t.Fatalf("reject status %d", status)
		}
		if d := waitDecision(t, result, "human decision"); d.Decision != "deny" || strings.Contains(d.Reason, "list_dir") {
			t.Fatalf("expected user rejection, got %+v", d)
		}
	}
	// The next distinct call can explicitly request review for the same script.
	mustAsk(t, command(4, bundleDiscoveryCommand), "bash")
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto, Bash: "ask"})
	overrideCmd := strings.Replace(bundleDiscoveryCommand, "renderComposer", "renderHeader", 1)
	mustAsk(t, command(5, overrideCmd), "bash")
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	f.svc.SetPermissionRulesSource(func() map[string]interface{} {
		return map[string]interface{}{"bash": map[string]interface{}{"node *": "ask"}}
	})
	mustAsk(t, command(6, overrideCmd), "bash")
	f.svc.SetPermissionRulesSource(nil)
	// Older/incomplete hook payloads cannot identify two distinct attempts.
	t.Run("missing step index uses human review", func(t *testing.T) {
		mustAsk(t, command(-1, strings.Replace(bundleDiscoveryCommand, "renderComposer", "renderFooter", 1)), "bash")
	})
	t.Run("batch read checks every target", func(t *testing.T) {
		mustAsk(t, payload(8, "read_many_files", map[string]interface{}{
			"paths": []string{asset, filepath.Join(assets, ".env")},
		}), "tool")
		mustAsk(t, payload(9, "read_many_files", map[string]interface{}{
			"AbsolutePath": asset, "paths": []string{filepath.Join(assets, ".env")},
		}), "tool")
	})
	// The selected file can change between discovery and the scoped read.
	// The read tool must still evaluate its actual target.
	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(secret, []byte("test fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, asset); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	mustAsk(t, payload(7, "view_file", map[string]interface{}{"AbsolutePath": asset}), "tool")
}
