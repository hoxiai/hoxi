package runner

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The audit log is rendered in Settings and copied into bug reports, so a
// secret that survives redaction is a leak with a long tail.
func TestRedactApprovalTextCoversQuotedAndJSONSecrets(t *testing.T) {
	cases := map[string]string{
		`mysql --password="hunter2" db`:                          `hunter2`,
		`deploy --token='abc123'`:                                `abc123`,
		`mysql --password=hunter2`:                               `hunter2`,
		`curl -H 'x-api-key: abc123' https://x`:                  `abc123`,
		`curl -H "Authorization: Bearer sk-secret-token-123456"`: `sk-secret-token-123456`,
		`{"password":"hunter2","apiKey":"sk-live-abcdef"}`:       `hunter2`,
		`{"api_key":"sk-live-abcdef"}`:                           `sk-live-abcdef`,
		`export OPENCODE_SERVER_PASSWORD=s3cret`:                 `s3cret`,
	}
	for input, secret := range cases {
		got := redactApprovalText(input, 400)
		if strings.Contains(got, secret) {
			t.Errorf("redact(%q) = %q still contains %q", input, got, secret)
		}
	}
	if got := redactApprovalText(`export FOO=bar`, 400); got != `export FOO=bar` {
		t.Errorf("ordinary env assignment must stay readable, got %q", got)
	}
}

func TestApprovalGrantJSONOmitsRawKey(t *testing.T) {
	g := newApprovalGate()
	g.addAlways("ses_x", `curl -H 'Authorization: Bearer sk-live-123456' https://x`, "desktop")
	raw, err := json.Marshal(g.listGrants())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-live-123456") || strings.Contains(string(raw), `"key"`) {
		t.Fatalf("serialized grants leaked the raw key: %s", raw)
	}
}

func TestOpenCodeAuditWithoutSnapshotKeepsSessionAndRedacts(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_audit", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-1"}); err != nil {
		t.Fatal(err)
	}
	// A reply that beats the 4s listPermissions poll: no snapshot exists yet,
	// but the caller knows which session stopped it.
	svc.recordOpenCodeAudit("per_oc_unseen", "ses_oc_audit", dir, "reject", "the session was stopped", "system")
	entries := svc.audit.list("ses_oc_audit", 10)
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %+v", entries)
	}
	e := entries[0]
	if e.SessionID != "ses_oc_audit" || e.Source != "system" || e.Decision != "reject" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.Directory == "" {
		t.Fatalf("entry lost its directory: %+v", e)
	}
}

func TestOpenCodePermissionEventFeedsTheAuditSnapshot(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_event", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-event"}); err != nil {
		t.Fatal(err)
	}
	rec, _ := svc.store.Get("ses_oc_event")
	raw := []byte(`{"type":"permission.asked","properties":{"id":"per_oc_event","sessionID":"oc-event","permission":"bash","patterns":["mysql --password=\"hunter2\" db"]}}`)
	svc.relayOpenCodeEvent(rec, raw)
	svc.recordOpenCodeApprovalAudit("per_oc_event", dir, "once", "", "tunnel")
	entries := svc.audit.list("ses_oc_event", 10)
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %+v", entries)
	}
	if entries[0].Source != "tunnel" || entries[0].Summary == "per_oc_event" {
		t.Fatalf("permission.asked was not captured before the reply: %+v", entries[0])
	}
	if strings.Contains(entries[0].Summary, "hunter2") {
		t.Fatalf("audit summary leaked a password: %q", entries[0].Summary)
	}
}

// OpenCode raises external_directory for every read/write outside the session
// directory. Treating all of them as destructive made "auto approve" prompt on
// routine sibling-folder work, so only credential/execution targets stay high
// risk.
func TestExternalDirectoryRiskClassifiesRoutineAndSensitiveTargets(t *testing.T) {
	routine := []map[string]interface{}{
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/code/hoxi/apay/app/themes/ainode/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/fvm/versions/3.44.9/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/code/kuaiqin/shoply/admin/src/**"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/code/hoxi/apay/src/truncate.ts"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.hoxi/skills/shoply-ops/*"}},
		{"permission": "edit", "patterns": []interface{}{"/repo/.hoxi/worktrees/ses_123/src/App.tsx"}},
	}
	for _, item := range routine {
		if isDestructivePermissionRequest(item) {
			t.Errorf("routine external target classified destructive: %v", item["patterns"])
		}
	}

	sensitive := []map[string]interface{}{
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.ssh/config"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.ssh/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.ssh"}},
		{"permission": "external_directory", "patterns": []interface{}{"/etc/passwd"}},
		{"permission": "external_directory", "patterns": []interface{}{"/etc"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.zshrc"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/Library/LaunchAgents/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.config/gh/hosts.yml"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/code/hoxi/apay/.env.production"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.hoxi/hoxi.db"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/hugh/.config/opencode/opencode.json"}},
		{"permission": "external_directory", "patterns": []interface{}{}},
		{"permission": "tool", "patterns": []interface{}{"Read"}, "metadata": map[string]interface{}{"externalDirectory": true, "args": `{"file_path":"/etc/passwd"}`}},
		{"permission": "edit", "patterns": []interface{}{"/Users/hugh/.ssh/authorized_keys"}},
		{"permission": "edit", "patterns": []interface{}{"/repo/.hoxi/worktrees/ses_123/.git/config"}},
	}
	for _, item := range sensitive {
		if !isDestructivePermissionRequest(item) {
			t.Errorf("sensitive external target must stay destructive: %v", item["patterns"])
		}
	}

	// A tool request flagged externalDirectory for a sibling folder is routine
	// too, but destructive metadata always wins.
	if isDestructivePermissionRequest(map[string]interface{}{
		"permission": "tool",
		"metadata":   map[string]interface{}{"externalDirectory": true, "args": `{"file_path":"/Users/hugh/code/hoxi/ainode/src/a.ts"}`},
	}) {
		t.Error("sibling-folder tool access must not be classified destructive")
	}
	if !isDestructivePermissionRequest(map[string]interface{}{
		"permission": "tool",
		"metadata":   map[string]interface{}{"externalDirectory": true, "destructive": true},
	}) {
		t.Error("explicit destructive metadata must still win")
	}
}
