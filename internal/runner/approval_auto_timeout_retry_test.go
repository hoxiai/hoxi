package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTimedOutOpenCodeService lists a request older than the approval wait
// that the manual policy shows to the user, so only the timeout answers it.
func newTimedOutOpenCodeService(t *testing.T, id string, retries []time.Duration, statuses ...int) (*Service, *flakyOpenCodePermissions, string) {
	t.Helper()
	svc, fake, dir := newFlakyOpenCodeService(t, id, time.Now().Add(-20*time.Minute), statuses...)
	setTestApprovalPolicy(t, svc, ApprovalPolicy{Mode: ApprovalModeManual})
	svc.openCodeTimeoutRetries = retries
	return svc, fake, dir
}

func assertOnlyRejects(t *testing.T, replies []string) {
	t.Helper()
	for i, reply := range replies {
		if reply != "reject" {
			t.Fatalf("OpenCode reply %d = %q in %v: the timeout may only reject", i, reply, replies)
		}
	}
}

// A timeout reject OpenCode refuses is sent again after a backoff instead of
// silently leaving the timed-out request to wait for the user forever.
func TestOpenCodeTimeoutRetriesRefusedReject(t *testing.T) {
	t.Parallel()
	const id = "per_oc_timeout_retry"
	svc, fake, dir := newTimedOutOpenCodeService(t, id, []time.Duration{20 * time.Millisecond}, http.StatusInternalServerError)

	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 1 || got[0] != id {
		t.Fatalf("first listing = %v, want the request shown to the user", got)
	}
	waitFor(t, "timeout settled", func() bool {
		return svc.openCodeAutoReplyState(id) == openCodeAutoReplySettled
	})
	got := fake.sent()
	if len(got) != 2 {
		t.Fatalf("OpenCode replies = %v, want the refused reject and its retry", got)
	}
	assertOnlyRejects(t, got)
	if n := timeoutAudits(svc, id); n != 1 {
		t.Fatalf("timeout audit entries = %d, want only the accepted reject", n)
	}
	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 0 {
		t.Fatalf("listing after the retried reject = %v, want it settled", got)
	}
}

// The original case: the policy's approval fails, the timeout's first reject
// fails too, and the retry still rejects (never approves) the request.
func TestOpenCodeTimeoutRetryAfterFailedPolicyReplyOnlyRejects(t *testing.T) {
	t.Parallel()
	const id = "per_oc_timeout_retry_policy"
	svc, fake, dir := newFlakyOpenCodeService(t, id, time.Now().Add(-20*time.Minute),
		http.StatusInternalServerError, http.StatusInternalServerError)
	svc.openCodeTimeoutRetries = []time.Duration{20 * time.Millisecond}

	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 0 {
		t.Fatalf("first listing = %v, want the request claimed by the policy", got)
	}
	waitFor(t, "timeout settled", func() bool {
		return svc.openCodeAutoReplyState(id) == openCodeAutoReplySettled
	})
	got := fake.sent()
	if len(got) != 3 || got[0] != "once" {
		t.Fatalf("OpenCode replies = %v, want the policy's failed once, then two timeout rejects", got)
	}
	assertOnlyRejects(t, got[1:])
	for _, e := range svc.audit.list("", 50) {
		if e.RequestID == id && e.Decision != "reject" {
			t.Fatalf("audit %+v: a refused reply must never be logged as an approval", e)
		}
	}
	if n := timeoutAudits(svc, id); n != 1 {
		t.Fatalf("timeout audit entries = %d, want 1", n)
	}
}

// Retries are bounded. Once they run out the request stays shown to the
// user, and neither polling nor the policy answers it again.
func TestOpenCodeTimeoutRetriesStopAfterTheLimit(t *testing.T) {
	t.Parallel()
	const id = "per_oc_timeout_retry_limit"
	retries := []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	svc, fake, dir := newTimedOutOpenCodeService(t, id, retries,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusInternalServerError, http.StatusInternalServerError)

	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 1 {
		t.Fatalf("first listing = %v, want the request shown", got)
	}
	waitFor(t, "every timeout attempt", func() bool { return len(fake.sent()) >= 1+len(retries) })
	time.Sleep(100 * time.Millisecond)
	for poll := 0; poll < 3; poll++ {
		if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 1 || got[0] != id {
			t.Fatalf("poll %d listed %v, want the request still shown to the user", poll, got)
		}
	}
	time.Sleep(50 * time.Millisecond)
	got := fake.sent()
	if len(got) != 1+len(retries) {
		t.Fatalf("OpenCode replies = %v, want the first reject plus %d retries and nothing more", got, len(retries))
	}
	assertOnlyRejects(t, got)
	if n := timeoutAudits(svc, id); n != 0 {
		t.Fatalf("timeout audit entries = %d, want none for refused rejects", n)
	}
	if state := svc.openCodeAutoReplyState(id); state != 0 {
		t.Fatalf("state after the last refused reject = %d, want the request unclaimed for the user", state)
	}
}

// A user answer OpenCode accepts between two timeout attempts ends the
// retries.
func TestOpenCodeTimeoutRetriesStopOnceTheUserAnswers(t *testing.T) {
	t.Parallel()
	const id = "per_oc_timeout_retry_user"
	svc, fake, dir := newTimedOutOpenCodeService(t, id, []time.Duration{300 * time.Millisecond}, http.StatusInternalServerError)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if got := listedIDs(svc.listUnifiedPermissions(context.Background(), dir)); len(got) != 1 {
		t.Fatalf("first listing = %v, want the request shown", got)
	}
	waitFor(t, "first timeout reject", func() bool { return len(fake.sent()) == 1 })
	waitFor(t, "first reject released", func() bool { return svc.openCodeAutoReplyState(id) == 0 })
	if status := postOpenCodeReply(t, srv.URL, id, dir, "reject"); status != http.StatusOK {
		t.Fatalf("user reply between timeout attempts: %d, want 200", status)
	}
	time.Sleep(450 * time.Millisecond)
	if got := fake.sent(); len(got) != 2 {
		t.Fatalf("OpenCode replies = %v, want the refused timeout reject and the user's reply only", got)
	}
	if n := timeoutAudits(svc, id); n != 0 {
		t.Fatalf("timeout audit entries = %d, want none after the user answered", n)
	}
}
