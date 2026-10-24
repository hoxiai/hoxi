package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

// setTestApprovalPolicy pins the approval policy for gate tests. Smart mode
// auto-approves only the read-only command list, so gate mechanics stay
// observable.
func setTestApprovalPolicy(t *testing.T, svc *Service, p ApprovalPolicy) {
	t.Helper()
	if _, err := svc.policy.set(p); err != nil {
		t.Fatal(err)
	}
}

func policyRequest(t *testing.T, srv *httptest.Server, method, source string, body interface{}) (int, approvalPolicyResponse) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, srv.URL+"/api/hoxi/permission/policy", reader)
	if source != "" {
		req.Header.Set(ApprovalSourceHeader, source)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out approvalPolicyResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func newPolicyServer(t *testing.T, storePath string) (*Service, *httptest.Server) {
	t.Helper()
	svc := NewService(t.TempDir(), nil, nil, storePath)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return svc, srv
}

func TestApprovalPolicyPersistsAndRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	policyPath := filepath.Join(filepath.Dir(storePath), approvalPolicyFile)
	_, srv := newPolicyServer(t, storePath)

	code, got := policyRequest(t, srv, http.MethodGet, "", nil)
	if code != http.StatusOK || got.Exists || got.Policy.Mode != ApprovalModeAuto {
		t.Fatalf("default policy: %d %+v", code, got)
	}
	if info, err := os.Stat(policyPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("default policy file must be seeded on startup with 0600: %v %v", info, err)
	}
	for _, bad := range []map[string]string{
		{"mode": "yolo"},
		{"mode": "auto", "bash": "sometimes"},
		{"mode": "smart", "edit": "smart"},
	} {
		if code, _ := policyRequest(t, srv, http.MethodPut, "", bad); code != http.StatusBadRequest {
			t.Fatalf("invalid policy %v accepted: %d", bad, code)
		}
	}
	if code, got := policyRequest(t, srv, http.MethodGet, "", nil); code != http.StatusOK || got.Exists || got.Policy.Mode != ApprovalModeAuto {
		t.Fatalf("rejected policies must not overwrite seeded default: %d %+v", code, got)
	}
	code, got = policyRequest(t, srv, http.MethodPut, "", ApprovalPolicy{Mode: ApprovalModeManual, Edit: "allow"})
	if code != http.StatusOK || !got.Exists || got.Policy.Mode != ApprovalModeManual || got.Policy.Edit != "allow" {
		t.Fatalf("save: %d %+v", code, got)
	}
	if info, err := os.Stat(policyPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("policy file: %v %v", info, err)
	}
	reloaded := NewService(t.TempDir(), nil, nil, storePath)
	if p, exists := reloaded.policy.get(); !exists || p.Mode != ApprovalModeManual || p.Edit != "allow" {
		t.Fatalf("reloaded policy: %+v exists=%v", p, exists)
	}
}

func TestApprovalPolicyIsNotReachableOverTheTunnel(t *testing.T) {
	t.Parallel()
	_, srv := newPolicyServer(t, filepath.Join(t.TempDir(), "sessions.json"))
	if code, _ := policyRequest(t, srv, http.MethodGet, "tunnel", nil); code != http.StatusForbidden {
		t.Fatalf("tunnel GET: %d", code)
	}
	if code, _ := policyRequest(t, srv, http.MethodPut, "tunnel", ApprovalPolicy{Mode: ApprovalModeAuto}); code != http.StatusForbidden {
		t.Fatalf("tunnel PUT: %d", code)
	}
}

func TestCorruptApprovalPolicyIsBackedUpNotTrusted(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	policyPath := filepath.Join(filepath.Dir(storePath), approvalPolicyFile)
	if err := os.WriteFile(policyPath, []byte(`{"mode":"yolo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(t.TempDir(), nil, nil, storePath)
	if p, exists := svc.policy.get(); exists || p.Mode != ApprovalModeAuto {
		t.Fatalf("invalid file must fall back to the default: %+v exists=%v", p, exists)
	}
	backups, _ := filepath.Glob(policyPath + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("expected one backup, got %v", backups)
	}
	if _, err := svc.policy.set(ApprovalPolicy{Mode: ApprovalModeSmart}); err != nil {
		t.Fatalf("an explicit save after backup must succeed: %v", err)
	}
}

func TestAutoModeRunsWithoutPromptOrAttention(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	if d := f.callHook(t, f.payload("npm install lodash", "", "conv-1")); d.Decision != "auto_approve" {
		t.Fatalf("auto mode: %q", d.Decision)
	}
	select {
	case evt := <-events:
		if evt.Type == "permission.asked" {
			t.Fatalf("auto mode must not publish a prompt: %#v", evt.Properties)
		}
	default:
	}
	if f.tracker.Snapshot().Attention != 0 {
		t.Fatal("auto-approved command must not raise Attention (or a phone push)")
	}
	entries := f.svc.audit.list("ses_agy_gate", 10)
	if len(entries) != 1 || entries[0].Source != approvalSourcePolicy || entries[0].Decision != "once" {
		t.Fatalf("policy approval must be audited: %+v", entries)
	}
	// Destructive commands still reach the user in auto mode.
	result := f.asyncHook(t, f.payload("rm -rf build", "", "conv-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "reject", "no")
	if d := <-result; d.Decision != "deny" {
		t.Fatalf("destructive in auto mode: %q", d.Decision)
	}
}

func TestManualModeAsksForReadOnlyCommandsAndWorktreeEdits(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeManual})
	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()
	result := f.asyncDecide(t, f.payload("Bash", "git status", "", "cc-uuid-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "reject", "manual")
	if d := <-result; !strings.HasPrefix(d, "deny|") {
		t.Fatalf("manual mode git status: %q", d)
	}
	edit, _ := json.Marshal(map[string]interface{}{
		"session_id": "cc-uuid-1", "cwd": f.dir, "hook_event_name": "PreToolUse",
		"tool_name": "Edit", "tool_use_id": "toolu_edit", "tool_input": map[string]interface{}{"file_path": filepath.Join(f.dir, "a.go")},
	})
	result = f.asyncDecide(t, edit)
	id, _ = waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "once", "")
	if d := <-result; d != "allow|" {
		t.Fatalf("manual mode worktree edit after once: %q", d)
	}
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	if d := f.decide(t, edit); d != "" {
		t.Fatalf("auto mode worktree edit must be left to acceptEdits, got %q", d)
	}
}

func TestSecretReadsInsideTheWorktreeAlwaysAsk(t *testing.T) {
	t.Parallel()
	f := newClaudeGateFixture(t)
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	events, unsub := f.svc.Subscribe("ses_cc_gate")
	defer unsub()
	read := func(name string) []byte {
		raw, _ := json.Marshal(map[string]interface{}{
			"session_id": "cc-uuid-1", "cwd": f.dir, "hook_event_name": "PreToolUse",
			"tool_name": "Read", "tool_use_id": "toolu_" + name, "tool_input": map[string]interface{}{"file_path": filepath.Join(f.dir, name)},
		})
		return raw
	}
	if d := f.decide(t, read("main.go")); d != "" {
		t.Fatalf("plain read: %q", d)
	}
	if d := f.decide(t, read(".env.example")); d != "" {
		t.Fatalf(".env.example read: %q", d)
	}
	result := f.asyncDecide(t, read(".env"))
	asked := waitForEvent(t, events, "permission.asked")
	if meta, _ := asked.Properties["metadata"].(map[string]interface{}); meta["destructive"] != true {
		t.Fatalf(".env read must be marked destructive: %#v", asked.Properties)
	}
	id, _ := asked.Properties["id"].(string)
	f.reply(t, id, "reject", "secret")
	if d := <-result; !strings.HasPrefix(d, "deny|") {
		t.Fatalf(".env read after reject: %q", d)
	}

	g := newGateFixture(t)
	setTestApprovalPolicy(t, g.svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	agyEvents, agyUnsub := g.svc.Subscribe("ses_agy_gate")
	defer agyUnsub()
	agyRead, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"toolCall":       map[string]interface{}{"name": "view_file", "args": map[string]interface{}{"AbsolutePath": filepath.Join(g.dir, ".env.local")}},
	})
	res := g.asyncHook(t, agyRead)
	id, _ = waitForEvent(t, agyEvents, "permission.asked").Properties["id"].(string)
	g.reply(t, id, "reject", "secret")
	if d := <-res; d.Decision != "deny" {
		t.Fatalf("agy .env.local read after reject: %q", d.Decision)
	}
}

func TestPolicyChangeReleasesWaitingRequests(t *testing.T) {
	t.Parallel()
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	result := f.asyncHook(t, f.payload("npm install lodash", "", "conv-1"))
	waitForEvent(t, events, "permission.asked")
	// A distinct step, or the gate would fold it into the first call.
	rmCall, _ := json.Marshal(map[string]interface{}{
		"conversationId": "conv-1",
		"stepIdx":        9,
		"toolCall":       map[string]interface{}{"name": "run_command", "args": map[string]interface{}{"CommandLine": "rm -rf build", "Cwd": f.dir}},
	})
	destructive := f.asyncHook(t, rmCall)
	destructiveID, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)

	if code, _ := policyRequest(t, f.srv, http.MethodPut, "", ApprovalPolicy{Mode: ApprovalModeAuto}); code != http.StatusOK {
		t.Fatalf("save policy: %d", code)
	}
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("waiting request after switching to auto: %q", d.Decision)
	}
	if list := f.svc.listPendingApprovals(""); len(list) != 1 || list[0]["id"] != destructiveID {
		t.Fatalf("destructive request must keep waiting: %+v", list)
	}
	f.reply(t, destructiveID, "reject", "no")
	<-destructive
}

// fakeOpenCodePermissions serves GET /permission and records replies.
type fakeOpenCodePermissions struct {
	mu      sync.Mutex
	pending []map[string]interface{}
	replies map[string]string
}

func (f *fakeOpenCodePermissions) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/permission":
		writeJSON(w, http.StatusOK, f.pending)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/permission/") && strings.HasSuffix(r.URL.Path, "/reply"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/permission/"), "/reply")
		var body struct {
			Reply string `json:"reply"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.replies[id] = body.Reply
		kept := f.pending[:0]
		for _, p := range f.pending {
			if p["id"] != id {
				kept = append(kept, p)
			}
		}
		f.pending = kept
		writeJSON(w, http.StatusOK, true)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOpenCodePermissions) reply(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replies[id]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOpenCodeRequestsAreAnsweredByThePolicy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracker := statusbar.NewTracker()
	svc := NewService(dir, nil, tracker, filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	fake := &fakeOpenCodePermissions{replies: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_policy", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-policy"}); err != nil {
		t.Fatal(err)
	}
	rec, _ := svc.store.Get("ses_oc_policy")
	events, unsub := svc.Subscribe("ses_oc_policy")
	defer unsub()

	svc.relayOpenCodeEvent(rec, []byte(`{"type":"permission.asked","properties":{"id":"per_oc_ok","sessionID":"oc-policy","permission":"bash","patterns":["npm install lodash"]}}`))
	waitFor(t, "policy reply", func() bool { return fake.reply("per_oc_ok") == "once" })
	svc.relayOpenCodeEvent(rec, []byte(`{"type":"permission.asked","properties":{"id":"per_oc_rm","sessionID":"oc-policy","permission":"bash","patterns":["rm -rf build"]}}`))
	asked := waitForEvent(t, events, "permission.asked")
	if asked.Properties["id"] != "per_oc_rm" {
		t.Fatalf("only the destructive request may reach the user, got %#v", asked.Properties)
	}
	if fake.reply("per_oc_rm") != "" {
		t.Fatal("the policy must not answer a destructive request")
	}
	if got := tracker.Snapshot().Attention; got != 1 {
		t.Fatalf("Attention = %d, want 1 (the destructive request only)", got)
	}
	waitFor(t, "policy audit", func() bool {
		for _, e := range svc.audit.list("ses_oc_policy", 10) {
			if e.RequestID == "per_oc_ok" && e.Source == approvalSourcePolicy {
				return true
			}
		}
		return false
	})

	// The pending list (polled by the desktop, CLI and phone) answers what the
	// policy allows and only returns what needs the user.
	fake.mu.Lock()
	fake.pending = []map[string]interface{}{
		{"id": "per_oc_list_ok", "sessionID": "oc-policy", "permission": "edit", "patterns": []interface{}{"src/a.ts"}},
		{"id": "per_oc_list_env", "sessionID": "oc-policy", "permission": "read", "patterns": []interface{}{dir + "/.env"}},
	}
	fake.mu.Unlock()
	list := svc.listUnifiedPermissions(context.Background(), dir)
	ids := []string{}
	for _, item := range list {
		id, _ := item["id"].(string)
		ids = append(ids, id)
	}
	if len(ids) != 1 || ids[0] != "per_oc_list_env" {
		t.Fatalf("pending list = %v, want only the .env read", ids)
	}
	waitFor(t, "list policy reply", func() bool { return fake.reply("per_oc_list_ok") == "once" })

	// Raw stream frames for sessions the runner does not drive are claimed too.
	line := `data: {"directory":"` + dir + `","payload":{"type":"permission.asked","properties":{"id":"per_oc_raw","sessionID":"oc-unmanaged","permission":"edit","patterns":["src/b.ts"]}}}`
	if !svc.claimOpenCodeSSELine(line, "") {
		t.Fatal("raw permission frame allowed by the policy must be dropped")
	}
	waitFor(t, "raw policy reply", func() bool { return fake.reply("per_oc_raw") == "once" })
	if svc.claimOpenCodeSSELine(`data: {"type":"permission.asked","properties":{"id":"per_oc_raw_rm","permission":"bash","patterns":["rm -rf x"]}}`, dir) {
		t.Fatal("destructive raw frame must reach the user")
	}

	// The auto-reply goroutine audits after the engine answers, so wait for
	// the audit writes as well; they land in the store directory and would
	// otherwise race t.TempDir cleanup.
	waitFor(t, "all policy audits", func() bool {
		for _, id := range []string{"per_oc_ok", "per_oc_list_ok", "per_oc_raw"} {
			found := false
			for _, e := range svc.audit.list("", 50) {
				if e.RequestID == id && e.Source == approvalSourcePolicy {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	})
}

// A hard deny in unattended mode is audited once, as the reject OpenCode
// actually received, and never as an approval.
func TestOpenCodeUnattendedHardDenyAuditsOnlyTheReject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	fake := &fakeOpenCodePermissions{replies: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_hard", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-hard"}); err != nil {
		t.Fatal(err)
	}
	rec, _ := svc.store.Get("ses_oc_hard")

	svc.relayOpenCodeEvent(rec, []byte(`{"type":"permission.asked","properties":{"id":"per_oc_push","sessionID":"oc-hard","permission":"bash","patterns":["git push origin main"]}}`))
	waitFor(t, "hard deny reply", func() bool { return fake.reply("per_oc_push") == "reject" })
	// The reply goroutine settles its claim only after the audit write lands,
	// so waiting on it also keeps the write away from t.TempDir cleanup.
	waitFor(t, "hard deny audit", func() bool {
		return svc.openCodeAutoReplyState("per_oc_push") == openCodeAutoReplySettled
	})
	var decisions []string
	for _, e := range svc.audit.list("ses_oc_hard", 10) {
		if e.RequestID != "per_oc_push" {
			continue
		}
		decisions = append(decisions, e.Decision)
		if e.Source != approvalSourceUnattended {
			t.Fatalf("hard deny source = %q, want %q", e.Source, approvalSourceUnattended)
		}
	}
	if len(decisions) != 1 || decisions[0] != "reject" {
		t.Fatalf("hard deny audit decisions = %v, want exactly [reject]", decisions)
	}
	deferred := svc.deferred.list()
	if len(deferred) != 1 || deferred[0].Command != "git push origin main" || deferred[0].Category != unattendedCategoryPublish {
		t.Fatalf("deferred = %+v, want one publish entry with command from patterns", deferred)
	}

	// An in-directory edit on a non-isolated OpenCode session must not be
	// hard-denied as "write outside the worktree"; it falls through to the
	// full-trust rules instead.
	svc.relayOpenCodeEvent(rec, []byte(`{"type":"permission.asked","properties":{"id":"per_oc_edit","sessionID":"oc-hard","permission":"edit","patterns":["src/main.ts"]}}`))
	waitFor(t, "in-dir edit reply", func() bool { return fake.reply("per_oc_edit") == "once" })
	waitFor(t, "in-dir edit audit", func() bool {
		return svc.openCodeAutoReplyState("per_oc_edit") == openCodeAutoReplySettled
	})
}

func TestOpenCodeUnattendedFailedReplyDoesNotRecordAuditOrDeferred(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_fail", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-fail"}); err != nil {
		t.Fatal(err)
	}

	failed := make(chan struct{}, 1)
	item := map[string]interface{}{
		"id":         "per_oc_push_fail",
		"sessionID":  "ses_oc_fail",
		"permission": "bash",
		"patterns":   []string{"git push origin main"},
	}
	if !svc.claimOpenCodePermission(item, "ses_oc_fail", dir, func() { failed <- struct{}{} }) {
		t.Fatal("first claim should attempt auto-reply")
	}
	<-failed
	if svc.claimOpenCodePermission(item, "ses_oc_fail", dir, nil) {
		t.Fatal("failed auto-reply must leave the request pending for the user")
	}
	if got := svc.audit.list("ses_oc_fail", 10); len(got) != 0 {
		t.Fatalf("audit after failed reply = %+v, want empty", got)
	}
	if got := svc.deferred.list(); len(got) != 0 {
		t.Fatalf("deferred after failed reply = %+v, want empty", got)
	}
	if _, loaded := svc.turnStats.Load("ses_oc_fail"); loaded {
		t.Fatal("failed reply must not increment turn counts")
	}
}
