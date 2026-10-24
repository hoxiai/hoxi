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

// staleOpenCodePermissions keeps listing its requests whatever the reply,
// like an OpenCode list that lags behind a reply or a reply that lands on an
// instance that does not hold the request.
type staleOpenCodePermissions struct {
	mu          sync.Mutex
	pending     []map[string]interface{}
	replyStatus int
	replies     map[string]int
}

func (f *staleOpenCodePermissions) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/permission":
		writeJSON(w, http.StatusOK, f.pending)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/permission/") && strings.HasSuffix(r.URL.Path, "/reply"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/permission/"), "/reply")
		f.replies[id]++
		if f.replyStatus == http.StatusNotFound {
			writeJSONError(w, http.StatusNotFound, "Permission request not found: "+id)
			return
		}
		writeJSON(w, f.replyStatus, true)
	default:
		http.NotFound(w, r)
	}
}

func (f *staleOpenCodePermissions) replyCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replies[id]
}

func newStaleOpenCodeService(t *testing.T, replyStatus int, item map[string]interface{}) (*Service, *staleOpenCodePermissions, string) {
	t.Helper()
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeAuto})
	fake := &staleOpenCodePermissions{pending: []map[string]interface{}{item}, replyStatus: replyStatus, replies: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(server.Close)
	svc.ConfigureOpenCode(server.URL, "secret")
	if err := svc.store.Put(SessionRecord{ID: "ses_oc_stale", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-stale"}); err != nil {
		t.Fatal(err)
	}
	return svc, fake, dir
}

func listedIDs(list []map[string]interface{}) []string {
	ids := []string{}
	for _, item := range list {
		id, _ := item["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func timeoutAudits(svc *Service, id string) int {
	n := 0
	for _, e := range svc.audit.list("", 500) {
		if e.RequestID == id && e.Source == approvalSourceTimeout {
			n++
		}
	}
	return n
}

func waitReplySettled(t *testing.T, svc *Service, id string) {
	t.Helper()
	waitFor(t, "reply goroutine to finish", func() bool {
		return svc.openCodeAutoReplyState(id) != openCodeAutoReplyInflight
	})
}

// A request whose timeout reject OpenCode accepted (or no longer knows) must
// not be claimed, shown, rejected and audited again when a stale listing
// keeps returning it: that loop filled the audit ring every ~4 seconds.
func TestOpenCodeTimedOutRequestStaysSettledWhileStillListed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		status      int
		wantAudited int
	}{
		{"reply accepted", http.StatusOK, 1},
		{"reply not found", http.StatusNotFound, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const id = "per_oc_stale_timeout"
			// Older than the wait, so the timer fires at once.
			created := float64(time.Now().Add(-20 * time.Minute).UnixMilli())
			svc, fake, dir := newStaleOpenCodeService(t, tc.status, map[string]interface{}{
				"id": id, "sessionID": "oc-stale", "permission": "bash", "patterns": []interface{}{"rm -rf build"}, "createdAt": created,
			})
			ctx := context.Background()

			first := listedIDs(svc.listUnifiedPermissions(ctx, dir))
			if len(first) != 1 || first[0] != id {
				t.Fatalf("first listing = %v, want the destructive request shown", first)
			}
			waitFor(t, "timeout reject", func() bool { return fake.replyCount(id) >= 1 })
			waitReplySettled(t, svc, id)
			// A 404 is retried once per candidate directory, so compare with
			// the count after the first timeout rather than a fixed number.
			replies := fake.replyCount(id)

			for poll := 0; poll < 5; poll++ {
				if got := listedIDs(svc.listUnifiedPermissions(ctx, dir)); len(got) != 0 {
					t.Fatalf("poll %d listed %v after the timeout settled it", poll, got)
				}
				time.Sleep(30 * time.Millisecond)
				waitReplySettled(t, svc, id)
			}
			if got := fake.replyCount(id); got != replies {
				t.Fatalf("OpenCode got %d timeout replies, want %d (one timeout)", got, replies)
			}
			if got := timeoutAudits(svc, id); got != tc.wantAudited {
				t.Fatalf("timeout audit entries = %d, want %d", got, tc.wantAudited)
			}
			if _, asked := svc.gate.openCodeAskFor(id); asked {
				t.Fatal("a settled request must not stay remembered as shown to the user")
			}
		})
	}
}

// A request the user answered must not come back, or be timed out, when the
// listing still returns it.
func TestOpenCodeUserReplyStaysSettledWhileStillListed(t *testing.T) {
	t.Parallel()
	const id = "per_oc_stale_user"
	svc, fake, dir := newStaleOpenCodeService(t, http.StatusOK, map[string]interface{}{
		"id": id, "sessionID": "oc-stale", "permission": "bash", "patterns": []interface{}{"rm -rf build"},
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()

	if got := listedIDs(svc.listUnifiedPermissions(ctx, dir)); len(got) != 1 {
		t.Fatalf("first listing = %v, want the request shown", got)
	}
	body, _ := json.Marshal(map[string]string{"reply": "reject"})
	resp, err := http.Post(srv.URL+"/api/hoxi/permission/"+id+"/reply?directory="+dir, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user reply: %d", resp.StatusCode)
	}
	for poll := 0; poll < 3; poll++ {
		if got := listedIDs(svc.listUnifiedPermissions(ctx, dir)); len(got) != 0 {
			t.Fatalf("poll %d listed %v after the user answered it", poll, got)
		}
	}
	// A second answer to the same request is refused rather than resent.
	resp, err = http.Post(srv.URL+"/api/hoxi/permission/"+id+"/reply?directory="+dir, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second reply: %d, want 409", resp.StatusCode)
	}
	if got := fake.replyCount(id); got != 1 {
		t.Fatalf("OpenCode got %d replies, want 1", got)
	}
}

// Polling while a request waits arms a single timeout: OpenCode requests
// carry no createdAt, and arming one timer per poll made a timeout fire every
// poll interval for a whole wait period after the deadline.
func TestOpenCodeRepeatedPollsArmOneTimeout(t *testing.T) {
	t.Parallel()
	const id = "per_oc_poll_once"
	wait := ApprovalPolicy{Mode: ApprovalModeAuto}.approvalWaitLimit()
	created := float64(time.Now().Add(-wait + 300*time.Millisecond).UnixMilli())
	svc, fake, dir := newStaleOpenCodeService(t, http.StatusOK, map[string]interface{}{
		"id": id, "sessionID": "oc-stale", "permission": "bash", "patterns": []interface{}{"rm -rf build"}, "createdAt": created,
	})
	ctx := context.Background()
	for poll := 0; poll < 10; poll++ {
		svc.listUnifiedPermissions(ctx, dir)
	}
	waitFor(t, "timeout reject", func() bool { return fake.replyCount(id) >= 1 })
	waitReplySettled(t, svc, id)
	time.Sleep(100 * time.Millisecond)
	if got := fake.replyCount(id); got != 1 {
		t.Fatalf("OpenCode got %d timeout replies, want exactly 1", got)
	}
	if got := timeoutAudits(svc, id); got != 1 {
		t.Fatalf("timeout audit entries = %d, want 1", got)
	}
}

// Settling never approves: an expired settle mark lets the request be shown
// and judged again, and a request still pending is never answered "once".
func TestOpenCodeSettledMarkExpires(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.settleOpenCodePermission("per_oc_expired")
	if got := svc.openCodeAutoReplyState("per_oc_expired"); got != openCodeAutoReplySettled {
		t.Fatalf("state = %d, want settled", got)
	}
	svc.openCodeAutoReplies.Store("per_oc_expired", openCodeSettledMark{until: time.Now().Add(-time.Second)})
	if got := svc.openCodeAutoReplyState("per_oc_expired"); got != 0 {
		t.Fatalf("expired settle mark state = %d, want 0", got)
	}
	if !svc.claimOpenCodeReply("per_oc_expired") {
		t.Fatal("an expired settle mark must not block a new answer")
	}
}

func TestRejectOpenCodeSessionPermissionsSkipsSettledAndSettlesPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc := NewService(dir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeManual})

	fake := &staleOpenCodePermissions{
		pending: []map[string]interface{}{
			{"id": "per_oc_already_settled", "sessionID": "oc-stop", "permission": "bash", "patterns": []interface{}{"echo done"}},
			{"id": "per_oc_pending_stop", "sessionID": "oc-stop", "permission": "bash", "patterns": []interface{}{"rm -rf dist"}},
		},
		replyStatus: http.StatusOK,
		replies:     map[string]int{},
	}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")

	rec := SessionRecord{ID: "ses_oc_stop", Runtime: "opencode", ProjectDir: dir, WorktreePath: dir, ExternalID: "oc-stop"}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	// Simulate per_oc_already_settled having been approved previously, while
	// per_oc_pending_stop is waiting on the user.
	svc.recordOpenCodeAudit("per_oc_already_settled", rec.ID, dir, "once", "", "desktop")
	svc.settleOpenCodePermission("per_oc_already_settled")
	svc.gate.rememberOpenCodeAsk("per_oc_pending_stop", openCodeAsk{
		item:      fake.pending[1],
		sessionID: rec.ID,
		dir:       dir,
	})

	svc.rejectOpenCodeSessionPermissions(context.Background(), rec)

	if got := fake.replyCount("per_oc_already_settled"); got != 0 {
		t.Fatalf("already-settled permission got %d stop replies, want 0", got)
	}
	if got := fake.replyCount("per_oc_pending_stop"); got != 1 {
		t.Fatalf("pending permission got %d stop replies, want 1", got)
	}
	if got := svc.openCodeAutoReplyState("per_oc_pending_stop"); got != openCodeAutoReplySettled {
		t.Fatalf("pending permission state after stop = %d, want settled", got)
	}
	if _, stillAsked := svc.gate.openCodeAskFor("per_oc_pending_stop"); stillAsked {
		t.Fatal("stopped permission must be removed from openCodeAsks so timeout does not re-fire")
	}
	for _, e := range svc.audit.list(rec.ID, 20) {
		if e.RequestID == "per_oc_already_settled" && e.Decision == "reject" {
			t.Fatalf("already-settled permission must not get a conflicting system reject audit: %+v", e)
		}
	}
}

func TestReconcilePendingApprovalsHonorsUnattendedIsolationAndHardDeny(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	worktreeDir := filepath.Join(projectDir, ".hoxi", "worktrees", "ses_agy_iso")
	svc := NewService(projectDir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeManual})

	rec := SessionRecord{
		ID:             "ses_agy_iso",
		Runtime:        string(RuntimeAntigravity),
		ProjectDir:     projectDir,
		WorktreePath:   worktreeDir,
		SandboxMode:    "worktree",
		CommandSandbox: true,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}

	allowPa := &pendingApproval{
		sid:   rec.ID,
		reply: make(chan approvalReply, 1),
		done:  make(chan struct{}),
		req: map[string]interface{}{
			"id":         "per_rnr_allow",
			"sessionID":  rec.ID,
			"permission": "bash",
			"patterns":   []string{"bash -c 'echo ok'"},
			"metadata":   map[string]interface{}{"command": "bash -c 'echo ok'"},
		},
	}
	denyPa := &pendingApproval{
		sid:   rec.ID,
		reply: make(chan approvalReply, 1),
		done:  make(chan struct{}),
		req: map[string]interface{}{
			"id":         "per_rnr_push",
			"sessionID":  rec.ID,
			"permission": "bash",
			"patterns":   []string{"git push origin main"},
			"metadata":   map[string]interface{}{"command": "git push origin main"},
		},
	}
	svc.gate.mu.Lock()
	svc.gate.pending["per_rnr_allow"] = allowPa
	svc.gate.pending["per_rnr_push"] = denyPa
	svc.gate.mu.Unlock()

	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	svc.reconcilePendingApprovals()

	select {
	case ans := <-allowPa.reply:
		if ans.Reply != "once" || ans.Source != approvalSourcePolicy {
			t.Fatalf("isolated allow reply = %+v, want once/policy", ans)
		}
	default:
		t.Fatal("expected isolated command to be approved on switch to unattended")
	}

	select {
	case ans := <-denyPa.reply:
		if ans.Reply != "reject" || ans.Source != approvalSourceUnattended {
			t.Fatalf("hard-deny reply = %+v, want reject/unattended", ans)
		}
	default:
		t.Fatal("expected hard-deny command to be rejected on switch to unattended")
	}
}
