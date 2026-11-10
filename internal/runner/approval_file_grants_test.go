package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func editReq(patterns []string, meta map[string]interface{}) map[string]interface{} {
	item := req("edit", patterns...)
	item["always"] = []interface{}{"*"}
	if meta != nil {
		item["metadata"] = meta
	}
	return item
}

func mustMkdirFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrantableEditFiles(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "code", "proj")
	skill := filepath.Join(root, ".agents", "skills", "ship", "scripts", "deploy.sh")
	mustMkdirFile(t, skill)
	mustMkdirFile(t, filepath.Join(dir, ".git", "config"))
	mustMkdirFile(t, filepath.Join(dir, "src", "run.sh"))
	file := func(p string) []string { return []string{fileGrantPrefix + p} }

	// A protected file the grant may cover: the same file however it is named.
	for name, tc := range map[string]struct {
		item map[string]interface{}
		want []string
	}{
		"skill script outside the project": {
			editReq([]string{"../../.agents/skills/ship/scripts/deploy.sh"}, map[string]interface{}{"filepath": skill}),
			file(skill),
		},
		"skill in the project": {editReq([]string{".agents/skills/deploy/SKILL.md"}, nil), file(filepath.Join(dir, ".agents/skills/deploy/SKILL.md"))},
		"hoxi skill":           {editReq([]string{".hoxi/skills/deploy/SKILL.md"}, nil), file(filepath.Join(dir, ".hoxi/skills/deploy/SKILL.md"))},
		"vscode settings":      {editReq([]string{".vscode/settings.json"}, nil), file(filepath.Join(dir, ".vscode/settings.json"))},
		"two skill files": {
			editReq([]string{".agents/skills/a.sh", ".agents/skills/b.sh"}, nil),
			[]string{fileGrantPrefix + filepath.Join(dir, ".agents/skills/a.sh"), fileGrantPrefix + filepath.Join(dir, ".agents/skills/b.sh")},
		},
	} {
		if got := grantableEditFiles(tc.item, dir); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: grants = %v, want %v", name, got, tc.want)
		}
		if got := grantableRequestClasses(tc.item, dir); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: grantableRequestClasses = %v, want %v", name, got, tc.want)
		}
	}

	// Secrets and files that steer the approval or sandbox are never granted.
	link := filepath.Join(dir, ".agents", "skills", "link.sh")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, ".git", "config"), link); err != nil {
		t.Fatal(err)
	}
	refused := map[string]map[string]interface{}{
		"git config":             editReq([]string{".git/config"}, nil),
		"git hook":               editReq([]string{".git/hooks/pre-commit"}, nil),
		"opencode plugin":        editReq([]string{".opencode/plugins/x.js"}, nil),
		"hoxi state":             editReq([]string{".hoxi/approval-policy.json"}, nil),
		"claude settings":        editReq([]string{".claude/settings.json"}, nil),
		"gemini settings":        editReq([]string{".gemini/settings.json"}, nil),
		"agents hooks config":    editReq([]string{filepath.Join(root, ".agents", "hooks.json")}, nil),
		"agents hook script":     editReq([]string{".agents/hooks/pre.sh"}, nil),
		"dotenv":                 editReq([]string{".env"}, nil),
		"envrc":                  editReq([]string{".envrc"}, nil),
		"ssh authorized keys":    editReq([]string{filepath.Join(root, ".ssh", "authorized_keys")}, nil),
		"system file":            editReq([]string{"/etc/hosts"}, nil),
		"symlink into git":       editReq([]string{".agents/skills/link.sh"}, nil),
		"glob":                   editReq([]string{".agents/*"}, nil),
		"home shorthand":         editReq([]string{"~/.agents/skills/x.sh"}, nil),
		"mixed with git":         editReq([]string{".agents/skills/a.sh", ".git/config"}, nil),
		"metadata names git":     editReq([]string{".agents/skills/a.sh"}, map[string]interface{}{"filepath": filepath.Join(dir, ".git/config")}),
		"plain edit":             editReq([]string{"src/main.go"}, nil),
		"flagged destructive":    editReq([]string{".agents/skills/a.sh"}, map[string]interface{}{"destructive": true}),
		"no targets":             editReq(nil, nil),
		"read of a skill":        req("read", ".agents/skills/a.sh"),
		"external directory ask": req("external_directory", filepath.Join(root, ".agents", "skills", "*")),
		"patch moving a skill into git hooks": editReq([]string{".agents/skills/a.sh"}, map[string]interface{}{
			"filepath": filepath.Join(dir, ".agents/skills/a.sh"),
			"files": []interface{}{map[string]interface{}{
				"filePath": filepath.Join(dir, ".agents/skills/a.sh"),
				"type":     "move",
				"movePath": filepath.Join(dir, ".git/hooks/pre-commit"),
			}},
		}),
	}
	for name, item := range refused {
		if got := grantableEditFiles(item, dir); got != nil {
			t.Errorf("%s must not be grantable, got %v", name, got)
		}
		if got := grantableRequestClasses(item, dir); got != nil {
			t.Errorf("%s: grantableRequestClasses = %v", name, got)
		}
	}
	if got := grantableEditFiles(editReq([]string{".envrc"}, nil), ""); got != nil {
		t.Errorf("no session directory, no grant: %v", got)
	}
	// A move out of the project is judged by where it lands too.
	moved := editReq([]string{"src/run.sh"}, map[string]interface{}{
		"filepath": filepath.Join(dir, "src/run.sh"),
		"files": []interface{}{map[string]interface{}{
			"filePath": filepath.Join(dir, "src/run.sh"), "type": "move", "movePath": filepath.Join(dir, ".git/hooks/pre-commit"),
		}},
	})
	if !isDestructivePermissionRequest(moved) {
		t.Error("a patch moving a file into .git/hooks must ask")
	}
}

// One click covers later edits of that exact file in that session; any other
// file, or a file that may never be granted, still asks.
func TestOpenCodeFileGrantCoversOnlyTheSameFile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, ".agents", "skills", "deploy", "run.sh")
	mustMkdirFile(t, script)
	svc := NewService(dir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	fake := &fakeOpenCodePermissions{replies: map[string]string{}}
	upstream := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer upstream.Close()
	svc.ConfigureOpenCode(upstream.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_file", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-file"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rec, _ := svc.store.Get("ses_oc_file")
	events, unsub := svc.Subscribe("ses_oc_file")
	defer unsub()

	relay := func(id, rel string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]interface{}{"type": "permission.asked", "properties": map[string]interface{}{
			"id": id, "sessionID": "oc-file", "permission": "edit",
			"patterns": []string{rel}, "always": []string{"*"},
			"metadata": map[string]interface{}{"filepath": filepath.Join(dir, rel), "diff": "-x\n+y\n"},
		}})
		svc.relayOpenCodeEvent(rec, raw)
	}
	askedFor := func(id string) SSEEvent {
		t.Helper()
		for {
			evt := waitForEvent(t, events, "permission.asked")
			if got, _ := evt.Properties["id"].(string); got == id {
				return evt
			}
		}
	}
	grant := func(id string) int {
		t.Helper()
		body, _ := json.Marshal(map[string]interface{}{"reply": "once", "grantClass": true})
		resp, err := http.Post(srv.URL+"/api/hoxi/permission/"+id+"/reply?directory="+dir, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	const rel = ".agents/skills/deploy/run.sh"

	relay("per_file1", rel)
	meta, _ := askedFor("per_file1").Properties["metadata"].(map[string]interface{})
	if got, _ := meta[grantClassesMetaKey].([]string); !reflect.DeepEqual(got, []string{fileGrantPrefix + script}) {
		t.Fatalf("card must be offered this file, got %#v", meta[grantClassesMetaKey])
	}
	if code := grant("per_file1"); code != http.StatusOK {
		t.Fatalf("grant reply: %d", code)
	}
	if got := fake.reply("per_file1"); got != "once" {
		t.Fatalf("OpenCode must only get a one-time approval (its always is `*`), got %q", got)
	}

	relay("per_file2", rel)
	waitFor(t, "same file auto reply", func() bool { return fake.reply("per_file2") == "once" })
	waitFor(t, "grant audit", func() bool {
		for _, e := range svc.audit.list("ses_oc_file", 10) {
			if e.RequestID == "per_file2" && e.Source == grantSourceSession {
				return true
			}
		}
		return false
	})

	relay("per_file3", ".agents/skills/deploy/other.sh")
	askedFor("per_file3")
	if got := fake.reply("per_file3"); got != "" {
		t.Fatalf("another file must still ask, got %q", got)
	}
	relay("per_file4", ".git/config")
	gitMeta, _ := askedFor("per_file4").Properties["metadata"].(map[string]interface{})
	if gitMeta[grantClassesMetaKey] != nil {
		t.Fatalf(".git/config must not offer a grant: %#v", gitMeta)
	}
	if code := grant("per_file4"); code != http.StatusBadRequest {
		t.Fatalf("granting .git/config: %d", code)
	}

	// Listed as a file grant; once revoked the same file asks again.
	var grantID string
	for _, g := range svc.listApprovalGrants("ses_oc_file", "") {
		if g.Kind == grantKindFile && g.Pattern == script {
			grantID = g.ID
		}
	}
	if grantID == "" {
		t.Fatal("file grant missing from the grants list")
	}
	if n := svc.revokeApprovalGrants(grantID, "", false); n != 1 {
		t.Fatalf("revoked %d grants", n)
	}
	relay("per_file5", rel)
	askedFor("per_file5")
	if got := fake.reply("per_file5"); got != "" {
		t.Fatalf("after revoke the file must ask again, got %q", got)
	}
}
