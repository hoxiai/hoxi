package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The audit log is rendered in Settings and copied into bug reports, so a
// secret that survives redaction is a leak with a long tail.
func TestRedactApprovalTextCoversQuotedAndJSONSecrets(t *testing.T) {
	t.Parallel()
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

// Short and prefixed credential names, CLI password flags and URL userinfo
// all carry secrets; the value must go while the command stays readable.
func TestRedactApprovalTextCoversPasswordFormsAndFlags(t *testing.T) {
	t.Parallel()
	const secret = "S3cretVal9"
	cases := map[string]string{
		`PW=` + secret + ` ./deploy.sh`:                                                     `PW=[redacted] ./deploy.sh`,
		`DB_PW=` + secret:                                                                   `DB_PW=[redacted]`,
		`export MYSQL_PWD=` + secret:                                                        `export MYSQL_PWD=[redacted]`,
		`pwd=` + secret:                                                                     `pwd=[redacted]`,
		`pass=` + secret:                                                                    `pass=[redacted]`,
		`PGPASSWORD=` + secret + ` psql -h db`:                                              `PGPASSWORD=[redacted] psql -h db`,
		`SSHPASS=` + secret + ` sshpass -e ssh host`:                                        `SSHPASS=[redacted] sshpass -e ssh host`,
		`openssl rsa -passphrase=` + secret:                                                 `openssl rsa -passphrase=[redacted]`,
		`{"client_secret":"` + secret + `"}`:                                                `{"client_secret":"[redacted]"}`,
		`{"private_key": "` + secret + `"}`:                                                 `{"private_key": "[redacted]"}`,
		`GITHUB_TOKEN=` + secret + ` gh pr list`:                                            `GITHUB_TOKEN=[redacted] gh pr list`,
		`mysql -uroot -p` + secret + ` shop`:                                                `mysql -uroot -p[redacted] shop`,
		`mysqldump -h db -p'` + secret + `' shop`:                                           `mysqldump -h db -p[redacted] shop`,
		`sshpass -p ` + secret + ` ssh root@host`:                                           `sshpass -p [redacted] ssh root@host`,
		`docker login -u bot -p ` + secret + ` ghcr.io`:                                     `docker login -u bot -p [redacted] ghcr.io`,
		`curl -u admin:` + secret + ` https://api.example.com`:                              `curl -u admin:[redacted] https://api.example.com`,
		`curl --user 'admin:` + secret + `' https://x`:                                      `curl --user 'admin:[redacted]' https://x`,
		`git clone https://bot:` + secret + `@github.com/o/r.git`:                           `git clone https://bot:[redacted]@github.com/o/r.git`,
		`psql postgres://app:` + secret + `@db:5432/app`:                                    `psql postgres://app:[redacted]@db:5432/app`,
		`vault login --token ` + secret:                                                     `vault login --token [redacted]`,
		`curl -H 'Authorization: Basic ` + secret + `' https://x`:                           `curl -H 'Authorization: [redacted]' https://x`,
		`curl "https://x/api?access_token=` + secret + `&page=2"`:                           `curl "https://x/api?access_token=[redacted]&page=2"`,
		`echo -----BEGIN RSA PRIVATE KEY----- ` + secret + ` -----END RSA PRIVATE KEY-----`: `echo [redacted]`,
	}
	for input, want := range cases {
		got := redactApprovalText(input, 400)
		if strings.Contains(got, secret) {
			t.Errorf("redact(%q) = %q still contains the secret", input, got)
		}
		if got != want {
			t.Errorf("redact(%q) = %q, want %q", input, got, want)
		}
		if again := redactApprovalText(got, 400); again != got {
			t.Errorf("redaction is not idempotent: %q -> %q", got, again)
		}
	}
}

// Ordinary commands that merely contain credential-like words must stay
// readable, or the audit log stops telling the user what ran.
func TestRedactApprovalTextLeavesOrdinaryCommandsAlone(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`pwd`,
		`cd "$(pwd)" && ls`,
		`export PATH=$PWD/bin:$PATH`,
		`path=/usr/local/bin make`,
		`bypass=1 ./run.sh`,
		`compass=north node app.js`,
		`mysql -uroot -p shop`,
		`mysql -h db -P3306 -uroot shop`,
		`docker run -u 1000:1000 alpine`,
		`curl --user-agent hoxi https://example.com`,
		`git clone https://github.com/hoxiai/hoxi.git`,
		`git config credential.helper osxkeychain`,
		`max_tokens=4096 python eval.py`,
		`npm run task-runner-build`,
		`cp -p a.txt b.txt`,
		`echo basic setup done`,
	} {
		if got := redactApprovalText(input, 400); got != input {
			t.Errorf("redact(%q) = %q, want it unchanged", input, got)
		}
	}
}

// Entries written before the current rules must not keep plaintext on disk:
// loading re-redacts them and atomically rewrites the file.
func TestApprovalAuditLoadReRedactsLegacyEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, approvalAuditFile)
	legacy := approvalAuditFileDoc{Version: 1, Entries: []ApprovalAuditEntry{
		{ID: "aud_1", SessionID: "ses_a", Summary: "PW=S3cretVal9 ./deploy.sh", Decision: "once", Source: "desktop"},
		{ID: "aud_2", SessionID: "ses_a", Summary: "ls -la", Decision: "once", Source: "desktop"},
	}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := newApprovalAuditStore(filepath.Join(dir, "sessions.json"))
	entries := store.list("", 10)
	if len(entries) != 2 || entries[0].Summary != "PW=[redacted] ./deploy.sh" || entries[1].Summary != "ls -la" {
		t.Fatalf("unexpected entries after load: %+v", entries)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "S3cretVal9") || !strings.Contains(string(onDisk), "ls -la") {
		t.Fatalf("audit file was not rewritten cleanly: %s", onDisk)
	}
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("rewritten audit file must stay 0600: %v %v", info, err)
	}
	if leftovers, _ := filepath.Glob(path + ".tmp-*"); len(leftovers) != 0 {
		t.Fatalf("atomic write left temp files: %v", leftovers)
	}
}

// A clean file is not rewritten, and a corrupt one is moved aside rather
// than overwritten.
func TestApprovalAuditLoadKeepsCleanAndCorruptFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, approvalAuditFile)
	clean := []byte(`{"version":1,"entries":[{"id":"aud_1","timestamp":1,"requestID":"","sessionID":"s","permission":"bash","summary":"ls","decision":"once","source":"desktop"}]}`)
	if err := os.WriteFile(path, clean, 0o600); err != nil {
		t.Fatal(err)
	}
	newApprovalAuditStore(filepath.Join(dir, "sessions.json"))
	if got, _ := os.ReadFile(path); string(got) != string(clean) {
		t.Fatalf("clean audit file was rewritten: %s", got)
	}

	corrupt := []byte(`{"version":1,"entries":[{"summary":"PW=S3cretVal9"`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	store := newApprovalAuditStore(filepath.Join(dir, "sessions.json"))
	if got := store.list("", 10); len(got) != 0 {
		t.Fatalf("corrupt file must load empty, got %+v", got)
	}
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("corrupt file must be backed up once, got %v", backups)
	}
	if got, _ := os.ReadFile(backups[0]); string(got) != string(corrupt) {
		t.Fatalf("backup does not hold the original bytes: %s", got)
	}
}

func TestApprovalGrantJSONOmitsRawKey(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	routine := []map[string]interface{}{
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/code/acme/web/app/themes/main/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/fvm/versions/3.44.9/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/code/acme/storefront/admin/src/**"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/code/acme/web/src/truncate.ts"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.hoxi/skills/skill-authoring/*"}},
		{"permission": "edit", "patterns": []interface{}{"/repo/.hoxi/worktrees/ses_123/src/App.tsx"}},
	}
	for _, item := range routine {
		if isDestructivePermissionRequest(item) {
			t.Errorf("routine external target classified destructive: %v", item["patterns"])
		}
	}

	sensitive := []map[string]interface{}{
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.ssh/config"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.ssh/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.ssh"}},
		{"permission": "external_directory", "patterns": []interface{}{"/etc/passwd"}},
		{"permission": "external_directory", "patterns": []interface{}{"/etc"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.zshrc"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/Library/LaunchAgents/*"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.config/gh/hosts.yml"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/code/acme/web/.env.production"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.hoxi/hoxi.db"}},
		{"permission": "external_directory", "patterns": []interface{}{"/Users/dev/.config/opencode/opencode.json"}},
		{"permission": "external_directory", "patterns": []interface{}{}},
		{"permission": "tool", "patterns": []interface{}{"Read"}, "metadata": map[string]interface{}{"externalDirectory": true, "args": `{"file_path":"/etc/passwd"}`}},
		{"permission": "edit", "patterns": []interface{}{"/Users/dev/.ssh/authorized_keys"}},
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
