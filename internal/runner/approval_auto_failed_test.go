package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

// flakyOpenCodePermissions lists one request and answers replies with the
// queued statuses, then 200.
type flakyOpenCodePermissions struct {
	mu       sync.Mutex
	pending  []map[string]interface{}
	statuses []int
	replies  []string
}

func (f *flakyOpenCodePermissions) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/permission":
		writeJSON(w, http.StatusOK, f.pending)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/permission/") && strings.HasSuffix(r.URL.Path, "/reply"):
		var body struct {
			Reply string `json:"reply"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.replies = append(f.replies, body.Reply)
		status := http.StatusOK
		if len(f.statuses) > 0 {
			status, f.statuses = f.statuses[0], f.statuses[1:]
		}
		if status >= 300 {
			writeJSONError(w, status, "opencode failed")
			return
		}
		writeJSON(w, status, true)
	default:
		http.NotFound(w, r)
	}
}

func (f *flakyOpenCodePermissions) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.replies...)
}

// newFlakyOpenCodeService lists an in-worktree edit the auto policy approves.
func newFlakyOpenCodeService(t *testing.T, id string, created time.Time, statuses ...int) (*Service, *flakyOpenCodePermissions, string) {
	t.Helper()
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	item := map[string]interface{}{
		"id": id, "sessionID": "oc-flaky", "permission": "edit", "patterns": []interface{}{"src/main.ts"},
		"createdAt": float64(created.UnixMilli()),
	}
	fake := &flakyOpenCodePermissions{pending: []map[string]interface{}{item}, statuses: statuses}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(server.Close)
	svc.ConfigureOpenCode(server.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_flaky", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-flaky"}); err != nil {
		t.Fatal(err)
	}
	return svc, fake, dir
}

// failPolicyReply lets the policy answer the listed request and waits for
// OpenCode to refuse that answer.
func failPolicyReply(t *testing.T, svc *Service, fake *flakyOpenCodePermissions, id, dir string) {
	t.Helper()
	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 0 {
		t.Fatalf("first listing = %v, want the request claimed by the policy", got)
	}
	waitFor(t, "failed policy reply", func() bool {
		return svc.openCodeAutoReplyState(id) == openCodeAutoReplyFailed
	})
	if got := fake.sent(); len(got) != 1 || got[0] != "once" {
		t.Fatalf("policy replies = %v, want one \"once\"", got)
	}
}

func postOpenCodeReply(t *testing.T, srvURL, id, dir, reply string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"reply": reply})
	resp, err := http.Post(srvURL+"/api/hoxi/permission/"+id+"/reply?directory="+dir, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// A request whose policy reply OpenCode refused is shown to the user, and the
// user's answer must reach OpenCode instead of being refused with 409.
func TestOpenCodeUserAnswersRequestAfterFailedPolicyReply(t *testing.T) {
	t.Parallel()
	const id = "per_oc_failed_user"
	svc, fake, dir := newFlakyOpenCodeService(t, id, time.Now(), http.StatusInternalServerError)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	failPolicyReply(t, svc, fake, id, dir)
	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 1 || got[0] != id {
		t.Fatalf("listing after the failed reply = %v, want the request shown", got)
	}
	if got := fake.sent(); len(got) != 1 {
		t.Fatalf("OpenCode replies = %v, the policy must not retry", got)
	}
	if status := postOpenCodeReply(t, srv.URL, id, dir, "reject"); status != http.StatusOK {
		t.Fatalf("user reply after a failed policy reply: %d, want 200", status)
	}
	if got := fake.sent(); len(got) != 2 || got[1] != "reject" {
		t.Fatalf("OpenCode replies = %v, want the user's reject second", got)
	}
	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 0 {
		t.Fatalf("listing after the user answered = %v, want it settled", got)
	}
}

// A user answer OpenCode also refuses leaves the request failed: still shown
// and answerable, and never retried by the policy.
func TestOpenCodeFailedUserReplyKeepsPolicyFromRetrying(t *testing.T) {
	t.Parallel()
	const id = "per_oc_failed_twice"
	svc, fake, dir := newFlakyOpenCodeService(t, id, time.Now(), http.StatusInternalServerError, http.StatusInternalServerError)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	failPolicyReply(t, svc, fake, id, dir)
	if status := postOpenCodeReply(t, srv.URL, id, dir, "reject"); status != http.StatusInternalServerError {
		t.Fatalf("user reply: %d, want OpenCode's 500 passed through", status)
	}
	if got := svc.openCodeAutoReplyState(id); got != openCodeAutoReplyFailed {
		t.Fatalf("state after the failed user reply = %d, want failed", got)
	}
	for poll := 0; poll < 3; poll++ {
		if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 1 {
			t.Fatalf("poll %d listed %v, want the request still shown", poll, got)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if got := fake.sent(); len(got) != 2 {
		t.Fatalf("OpenCode replies = %v, the policy must not answer again", got)
	}
	if status := postOpenCodeReply(t, srv.URL, id, dir, "reject"); status != http.StatusOK {
		t.Fatalf("second user reply: %d, want 200", status)
	}
	if got := fake.sent(); len(got) != 3 || got[2] != "reject" {
		t.Fatalf("OpenCode replies = %v, want the user's retry", got)
	}
}

// A request whose policy reply failed is still bound by the approval timeout,
// and the timeout only ever rejects it.
func TestOpenCodeTimeoutRejectsRequestAfterFailedPolicyReply(t *testing.T) {
	t.Parallel()
	const id = "per_oc_failed_timeout"
	// Older than the wait, so the timeout fires as soon as it is armed.
	svc, fake, dir := newFlakyOpenCodeService(t, id, time.Now().Add(-20*time.Minute), http.StatusInternalServerError)

	// The timeout takes over as soon as the policy reply fails, so the
	// failed state may already be gone; the reply order shows it happened.
	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 0 {
		t.Fatalf("first listing = %v, want the request claimed by the policy", got)
	}
	waitFor(t, "timeout reject", func() bool { return len(fake.sent()) >= 2 })
	waitFor(t, "timeout settled", func() bool {
		return svc.openCodeAutoReplyState(id) == openCodeAutoReplySettled
	})
	if got := fake.sent(); len(got) != 2 || got[0] != "once" || got[1] != "reject" {
		t.Fatalf("OpenCode replies = %v, want the policy's failed once, then the timeout's reject", got)
	}
	if got := timeoutAudits(svc, id); got != 1 {
		t.Fatalf("timeout audit entries = %d, want 1", got)
	}
	for _, e := range svc.audit.list("", 50) {
		if e.RequestID == id && e.Decision != "reject" {
			t.Fatalf("audit %+v: a failed policy reply must never be logged as an approval", e)
		}
	}
}

// The policy's own claim never takes over a failed request.
func TestOpenCodePolicyClaimSkipsFailedRequest(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.openCodeAutoReplies.Store("per_oc_failed", openCodeAutoReplyFailed)
	if svc.claimOpenCodeReply("per_oc_failed") {
		t.Fatal("the policy claim must not retry a failed reply")
	}
	release, ok := svc.claimOpenCodeAnswer("per_oc_failed")
	if !ok {
		t.Fatal("a user or timeout answer must take over a failed reply")
	}
	if _, again := svc.claimOpenCodeAnswer("per_oc_failed"); again {
		t.Fatal("only one answer may be in flight")
	}
	release()
	if got := svc.openCodeAutoReplyState("per_oc_failed"); got != openCodeAutoReplyFailed {
		t.Fatalf("state after release = %d, want failed", got)
	}
}
