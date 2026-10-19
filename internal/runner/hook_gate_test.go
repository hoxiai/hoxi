package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

type gateFixture struct {
	svc     *Service
	srv     *httptest.Server
	tracker *statusbar.Tracker
	dir     string
	env     map[string]string
	revoke  func()
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	dir := t.TempDir()
	tracker := statusbar.NewTracker()
	svc := NewService(dir, nil, tracker, filepath.Join(t.TempDir(), "sessions.json"))
	if err := svc.store.Put(SessionRecord{ID: "ses_agy_gate", Runtime: "antigravity", ProjectDir: dir, WorktreePath: dir, ExternalID: "conv-1"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc.SetHookBaseURL(srv.URL)
	envList, revoke := svc.issueHookEnv("ses_agy_gate", "msg_a_1")
	env := map[string]string{}
	for _, kv := range envList {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return &gateFixture{svc: svc, srv: srv, tracker: tracker, dir: dir, env: env, revoke: revoke}
}

func (f *gateFixture) payload(command, cwd, conv string) []byte {
	if cwd == "" {
		cwd = f.dir
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"conversationId": conv,
		"stepIdx":        4,
		"workspacePaths": []string{f.dir},
		"toolCall": map[string]interface{}{
			"name": "run_command",
			"args": map[string]interface{}{"CommandLine": command, "Cwd": cwd, "toolSummary": "Run it"},
		},
	})
	return raw
}

// callHook goes through the real `hoxi agy-hook` client, exactly as agy would.
func (f *gateFixture) callHook(t *testing.T, body []byte) agyhook.Decision {
	t.Helper()
	var out bytes.Buffer
	agyhook.Run(bytes.NewReader(body), &out, func(k string) string { return f.env[k] })
	var d agyhook.Decision
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("bad hook output %q", out.String())
	}
	return d
}

func (f *gateFixture) asyncHook(t *testing.T, body []byte) chan agyhook.Decision {
	ch := make(chan agyhook.Decision, 1)
	go func() { ch <- f.callHook(t, body) }()
	return ch
}

func waitForEvent(t *testing.T, ch chan SSEEvent, typ string) SSEEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type == typ {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", typ)
		}
	}
}

func (f *gateFixture) reply(t *testing.T, id, reply, message string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"reply": reply, "message": message})
	resp, err := http.Post(f.srv.URL+"/api/hoxi/runner/permission/"+id+"/reply", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestHookGateRejectsBadCredentials(t *testing.T) {
	f := newGateFixture(t)
	post := func(token, session string) int {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+hookPreToolPath, bytes.NewReader(f.payload("git status", "", "")))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set(agyhook.HeaderSession, session)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	token := f.env[agyhook.EnvHookToken]
	if got := post("", "ses_agy_gate"); got != http.StatusUnauthorized {
		t.Fatalf("missing token: %d", got)
	}
	if got := post(token, "ses_other"); got != http.StatusUnauthorized {
		t.Fatalf("wrong session: %d", got)
	}
	if got := post(token, "ses_agy_gate"); got != http.StatusOK {
		t.Fatalf("valid credential: %d", got)
	}
	f.revoke()
	if got := post(token, "ses_agy_gate"); got != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", got)
	}
	if d := f.callHook(t, f.payload("git status", "", "")); d.Decision != "deny" {
		t.Fatalf("hook with revoked token must fail closed, got %q", d.Decision)
	}
}

func TestHookGateAutoApprovesSafeCommandsAndPassesOtherTools(t *testing.T) {
	f := newGateFixture(t)
	if d := f.callHook(t, f.payload("git status && go test ./...", "", "conv-1")); d.Decision != "auto_approve" {
		t.Fatalf("safe command: %q", d.Decision)
	}
	other, _ := json.Marshal(map[string]interface{}{"toolCall": map[string]interface{}{"name": "view_file"}})
	if d := f.callHook(t, other); d.Decision != "allow" {
		t.Fatalf("non-command tool should pass through, got %q", d.Decision)
	}
}

func TestHookGateDeniesForeignConversationAndDirectory(t *testing.T) {
	f := newGateFixture(t)
	if d := f.callHook(t, f.payload("git status", "", "conv-other")); d.Decision != "deny" {
		t.Fatalf("foreign conversation: %q", d.Decision)
	}
	if d := f.callHook(t, f.payload("git status", t.TempDir(), "conv-1")); d.Decision != "deny" {
		t.Fatalf("outside cwd: %q", d.Decision)
	}
	if d := f.callHook(t, f.payload("git status", filepath.Join(f.dir, "sub", ".."), "conv-1")); d.Decision != "auto_approve" {
		t.Fatalf("cwd inside session dir: %q", d.Decision)
	}
}

func TestHookGateAsksUserAndRelaysOnce(t *testing.T) {
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	result := f.asyncHook(t, f.payload("rm -rf build", "", "conv-1"))

	asked := waitForEvent(t, events, "permission.asked")
	id, _ := asked.Properties["id"].(string)
	if !strings.HasPrefix(id, runnerPermissionID) || asked.Properties["permission"] != "bash" {
		t.Fatalf("unexpected request: %#v", asked.Properties)
	}
	tool, _ := asked.Properties["tool"].(map[string]interface{})
	if tool["callID"] != "call_msg_a_1_4" || tool["messageID"] != "msg_a_1" {
		t.Fatalf("request not linked to the tool card: %#v", tool)
	}
	if f.tracker.Snapshot().Attention != 1 {
		t.Fatalf("status bar attention = %d, want 1", f.tracker.Snapshot().Attention)
	}
	if list := f.svc.listPendingApprovals(f.dir); len(list) != 1 {
		t.Fatalf("pending list = %d, want 1", len(list))
	}
	if list := f.svc.listPendingApprovals(t.TempDir()); len(list) != 0 {
		t.Fatalf("pending list for another directory = %d, want 0", len(list))
	}

	if code := f.reply(t, id, "once", ""); code != http.StatusOK {
		t.Fatalf("reply status %d", code)
	}
	if d := <-result; d.Decision != "auto_approve" {
		t.Fatalf("once: %q", d.Decision)
	}
	replied := waitForEvent(t, events, "permission.replied")
	if replied.Properties["requestID"] != id || replied.Properties["reply"] != "once" {
		t.Fatalf("replied event: %#v", replied.Properties)
	}
	if f.tracker.Snapshot().Attention != 0 {
		t.Fatalf("status bar attention not cleared")
	}
	if code := f.reply(t, id, "once", ""); code != http.StatusNotFound {
		t.Fatalf("second reply status %d, want 404", code)
	}
}

func TestHookGateRejectCarriesUserMessage(t *testing.T) {
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	result := f.asyncHook(t, f.payload("npm install lodash", "", "conv-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.reply(t, id, "reject", "use pnpm instead")
	d := <-result
	if d.Decision != "deny" || !strings.Contains(d.Reason, "use pnpm instead") {
		t.Fatalf("reject: %+v", d)
	}
}

func TestHookGateAlwaysSkipsRepeatsButNeverDestructive(t *testing.T) {
	f := newGateFixture(t)
	events, unsub := f.svc.Subscribe("ses_agy_gate")
	defer unsub()
	for _, cmd := range []string{"npm install lodash", "rm -rf build"} {
		result := f.asyncHook(t, f.payload(cmd, "", "conv-1"))
		id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
		f.reply(t, id, "always", "")
		if d := <-result; d.Decision != "auto_approve" {
			t.Fatalf("always %q: %q", cmd, d.Decision)
		}
	}
	if d := f.callHook(t, f.payload("npm install lodash", "", "conv-1")); d.Decision != "auto_approve" {
		t.Fatalf("repeat after always should skip the prompt, got %q", d.Decision)
	}
	result := f.asyncHook(t, f.payload("rm -rf build", "", "conv-1"))
	id, _ := waitForEvent(t, events, "permission.asked").Properties["id"].(string)
	f.svc.cancelSessionApprovals("ses_agy_gate", "stopped")
	if d := <-result; d.Decision != "deny" {
		t.Fatalf("destructive repeat must ask again and deny on stop, got %q", d.Decision)
	}
	if code := f.reply(t, id, "once", ""); code != http.StatusNotFound {
		t.Fatalf("reply after cancel: %d", code)
	}
}

func TestHookGateTimesOutClosed(t *testing.T) {
	f := newGateFixture(t)
	f.svc.gate.waitLimit = 50 * time.Millisecond
	d := f.callHook(t, f.payload("rm -rf build", "", "conv-1"))
	if d.Decision != "deny" || !strings.Contains(d.Reason, "timed out") {
		t.Fatalf("timeout: %+v", d)
	}
	if len(f.svc.listPendingApprovals("")) != 0 || f.tracker.Snapshot().Attention != 0 {
		t.Fatalf("timed-out request was not cleaned up")
	}
}

func TestIssueHookEnvWithoutBaseURLIsPassthrough(t *testing.T) {
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	env, revoke := svc.issueHookEnv("ses_agy_x", "msg")
	defer revoke()
	if env != nil {
		t.Fatalf("expected no hook env before SetHookBaseURL, got %v", env)
	}
}
