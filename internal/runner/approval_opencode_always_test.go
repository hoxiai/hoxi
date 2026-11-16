package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestOpenCodeWildcardMatch(t *testing.T) {
	for _, c := range []struct {
		s, pattern string
		want       bool
	}{
		{"git status", "git status *", true},
		{"git status --short", "git status *", true},
		{"git statusx", "git status *", false},
		{"src/a/b.go", "*", true},
		{"a\nb", "*", true},
		{"/u/.agents/skills/a/scripts/run.sh", "/u/.agents/skills/a/*", true},
		{"/u/.agents/other", "/u/.agents/skills/*", false},
		{"ab", "a?", true},
		{"axb", "a.b", false},
		{"a(b)", "a(b)", true},
		{`C:\x\y`, "C:/x/*", true},
		{"Git status", "git status *", false},
	} {
		if got := openCodeWildcardMatch(c.s, c.pattern); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.s, c.pattern, got, c.want)
		}
	}
}

// An "always" reply keeps nothing for a request no session grant may cover.
func TestOpenCodeAlwaysGrantRefusesFloors(t *testing.T) {
	for name, item := range map[string]map[string]interface{}{
		"protected edit":  {"permission": "edit", "patterns": []interface{}{".git/hooks/pre-commit"}, "always": []interface{}{"*"}},
		"opaque command":  {"permission": "bash", "patterns": []interface{}{`bash -c "$X"`}, "always": []interface{}{"bash *"}, "metadata": map[string]interface{}{"command": `bash -c "$X"`}},
		"rm -rf":          {"permission": "bash", "patterns": []interface{}{"rm -rf dist"}, "always": []interface{}{"rm *"}, "metadata": map[string]interface{}{"command": "rm -rf dist"}},
		"secret read":     {"permission": "read", "patterns": []interface{}{".env"}, "always": []interface{}{"*"}},
		"doom loop":       {"permission": "doom_loop", "patterns": []interface{}{"edit"}, "always": []interface{}{"edit"}},
		"no always given": {"permission": "edit", "patterns": []interface{}{"src/a.go"}},
	} {
		if perm, patterns, ok := openCodeAlwaysGrant(item); ok {
			t.Errorf("%s: kept %s %v", name, perm, patterns)
		}
	}
	perm, patterns, ok := openCodeAlwaysGrant(map[string]interface{}{"permission": "edit", "patterns": []interface{}{"src/a.go"}, "always": []interface{}{"*"}})
	if !ok || perm != "edit" || len(patterns) != 1 || patterns[0] != "*" {
		t.Fatalf("ordinary edit keeps edit *, got %q %v %v", perm, patterns, ok)
	}
}

// OpenCode never receives "always": the session keeps the patterns in Hoxi,
// ordinary requests they cover run, and protected files, secrets, destructive
// commands and other sessions still ask.
func TestOpenCodeAlwaysStaysInHoxiForOneSession(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(dir, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeManual})
	fake := &fakeOpenCodePermissions{replies: map[string]string{}}
	upstream := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer upstream.Close()
	svc.ConfigureOpenCode(upstream.URL, "secret")
	for _, rec := range []SessionRecord{
		{ID: "ses_oc_always", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-always"},
		{ID: "ses_oc_other", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-other"},
	} {
		if err := svc.store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rec, _ := svc.store.Get("ses_oc_always")
	other, _ := svc.store.Get("ses_oc_other")
	events, unsub := svc.Subscribe("ses_oc_always")
	defer unsub()
	otherEvents, unsubOther := svc.Subscribe("ses_oc_other")
	defer unsubOther()

	relay := func(r SessionRecord, props map[string]interface{}) {
		t.Helper()
		props["sessionID"] = r.ExternalID
		raw, _ := json.Marshal(map[string]interface{}{"type": "permission.asked", "properties": props})
		svc.relayOpenCodeEvent(r, raw)
	}
	edit := func(id, rel string) map[string]interface{} {
		return map[string]interface{}{"id": id, "permission": "edit", "patterns": []string{rel}, "always": []string{"*"},
			"metadata": map[string]interface{}{"filepath": filepath.Join(dir, rel), "diff": "-x\n+y\n"}}
	}
	shell := func(id, command, always string) map[string]interface{} {
		return map[string]interface{}{"id": id, "permission": "bash", "patterns": []string{command}, "always": []string{always},
			"metadata": map[string]interface{}{"command": command}}
	}
	asked := func(ch chan SSEEvent, id string) {
		t.Helper()
		for {
			if evt := waitForEvent(t, ch, "permission.asked"); evt.Properties["id"] == id {
				return
			}
		}
	}
	always := func(id string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"reply": "always"})
		resp, err := http.Post(srv.URL+"/api/hoxi/permission/"+id+"/reply?directory="+dir, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: always reply status %d", id, resp.StatusCode)
		}
		if got := fake.reply(id); got != "once" {
			t.Fatalf("%s: OpenCode must only get once (its always is instance-wide), got %q", id, got)
		}
	}
	stillAsks := func(ch chan SSEEvent, id string) {
		t.Helper()
		asked(ch, id)
		if got := fake.reply(id); got != "" {
			t.Fatalf("%s must still ask, got %q", id, got)
		}
	}

	relay(rec, edit("per_e1", "src/main.go"))
	asked(events, "per_e1")
	always("per_e1")
	waitFor(t, "always audited", func() bool {
		for _, e := range svc.audit.list("ses_oc_always", 10) {
			if e.RequestID == "per_e1" && e.Decision == "always" {
				return true
			}
		}
		return false
	})

	relay(rec, edit("per_e2", "src/other.go"))
	waitFor(t, "covered edit runs", func() bool { return fake.reply("per_e2") == "once" })
	for id, rel := range map[string]string{
		"per_e3": ".git/hooks/pre-commit",
		"per_e4": ".opencode/plugins/x.js",
		"per_e5": ".env",
	} {
		relay(rec, edit(id, rel))
		stillAsks(events, id)
	}
	relay(other, edit("per_e6", "src/main.go"))
	stillAsks(otherEvents, "per_e6")

	relay(rec, shell("per_b1", "git branch feature", "git branch *"))
	asked(events, "per_b1")
	always("per_b1")
	relay(rec, shell("per_b2", "git branch --list", "git branch *"))
	waitFor(t, "covered command runs", func() bool { return fake.reply("per_b2") == "once" })
	relay(rec, shell("per_b3", "git branch -D main", "git branch *"))
	stillAsks(events, "per_b3")

	// Unknown to Hoxi: no grant, and OpenCode still only gets once.
	always("per_unknown")

	var editGrant string
	for _, g := range svc.listApprovalGrants("ses_oc_always", "") {
		if g.Kind == grantKindOpenCode && g.Tool == "edit" && g.Pattern == "*" {
			editGrant = g.ID
		}
	}
	if editGrant == "" {
		t.Fatalf("edit grant missing: %+v", svc.listApprovalGrants("", ""))
	}
	if n := len(svc.listApprovalGrants("ses_oc_other", "")); n != 0 {
		t.Fatalf("other session must hold no grant, got %d", n)
	}
	if n := svc.revokeApprovalGrants(editGrant, "", false); n != 1 {
		t.Fatalf("revoked %d grants", n)
	}
	relay(rec, edit("per_e7", "src/main.go"))
	stillAsks(events, "per_e7")
}

// A grant covers only requests of its permission, and never a secret read.
func TestOpenCodeAlwaysCoversOnlyItsPermission(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.gate.addOpenCodeAlwaysGrants("ses_a", "read", []string{"*"}, "desktop")
	for _, c := range []struct {
		item map[string]interface{}
		want bool
	}{
		{map[string]interface{}{"permission": "read", "patterns": []interface{}{"docs/a.md"}}, true},
		{map[string]interface{}{"permission": "read", "patterns": []interface{}{".env"}}, false},
		{map[string]interface{}{"permission": "edit", "patterns": []interface{}{"docs/a.md"}}, false},
		{map[string]interface{}{"permission": "read"}, false},
	} {
		if got := svc.openCodeAlwaysApproves(c.item, "ses_a"); got != c.want {
			t.Errorf("%v: approves = %v, want %v", c.item, got, c.want)
		}
		if svc.openCodeAlwaysApproves(c.item, "ses_b") {
			t.Errorf("%v: another session must not be covered", c.item)
		}
	}
}
