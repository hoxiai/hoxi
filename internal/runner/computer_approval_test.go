package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestComputerInputsNeverAutoApprove(t *testing.T) {
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeSmart, ApprovalModeManual, ApprovalModeTrusted, ApprovalModeUnattended} {
		for _, item := range []map[string]interface{}{req("hoxi_computer", "click Fixture"), toolReq("hoxi_computer", nil)} {
			if approvalAutoApprovesIsolated(item, ApprovalPolicy{Mode: mode}, map[string]interface{}{"*": "allow", "hoxi_computer": "allow"}, "/repo/.hoxi/worktrees/one", true) {
				t.Fatal("computer input auto-approved", mode)
			}
			if !isDestructivePermissionRequest(item) {
				t.Fatal("computer input did not retain approval warning")
			}
		}
	}
}

func TestComputerApprovalOnlyAcceptsDesktopOnceAndIsCancelable(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		allowed, _ := f.svc.RequestComputerApproval(ctx, "ses_agy_gate", f.dir, "Fixture", map[string]any{"action": "click"})
		done <- allowed
	}()
	var id string
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		f.svc.gate.mu.Lock()
		for key, pending := range f.svc.gate.pending {
			if permissionName(pending.req) == "hoxi_computer" {
				id = key
			}
		}
		f.svc.gate.mu.Unlock()
		if id != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if id == "" {
		t.Fatal("computer action did not enter shared permission plane")
	}
	for _, reply := range []struct{ body, source string }{{`{"reply":"once","grantClass":true}`, "desktop"}, {`{"reply":"once"}`, "tunnel"}, {`{"reply":"always"}`, "tunnel"}} {
		request := httptest.NewRequest("POST", "/api/hoxi/permission/"+id+"/reply", strings.NewReader(reply.body))
		request.Header.Set(ApprovalSourceHeader, reply.source)
		w := httptest.NewRecorder()
		f.svc.replyRunnerPermission(w, request, id)
		if w.Code != http.StatusForbidden {
			t.Fatal("non-desktop or class computer approval accepted", w.Code, w.Body.String())
		}
	}
	f.svc.CancelComputerApprovals("ses_agy_gate")
	select {
	case allowed := <-done:
		if allowed {
			t.Fatal("canceled input approved")
		}
	case <-ctx.Done():
		t.Fatal("stop did not clear pending computer approval")
	}

}

func waitComputerPending(t *testing.T, f *gateFixture) string {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		f.svc.gate.mu.Lock()
		for key, pending := range f.svc.gate.pending {
			if permissionName(pending.req) == "hoxi_computer" {
				f.svc.gate.mu.Unlock()
				return key
			}
		}
		f.svc.gate.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("computer action did not enter shared permission plane")
	return ""
}

// Desktop input acts outside the worktree sandbox, so no approval mode, policy
// sweep or desktop "always" reply may approve it: each input waits for a
// one-time desktop allow.
func TestComputerInputAlwaysWaitsForDesktopOnce(t *testing.T) {
	for _, mode := range []string{ApprovalModeAuto, ApprovalModeTrusted, ApprovalModeSmart, ApprovalModeManual} {
		t.Run(mode, func(t *testing.T) {
			f := newGateFixture(t)
			defer f.revoke()
			setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: mode})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan bool, 1)
			go func() {
				allowed, _ := f.svc.RequestComputerApproval(ctx, "ses_agy_gate", f.dir, "Fixture", map[string]any{"action": "type", "text": "rm -rf ~"})
				done <- allowed
			}()
			id := waitComputerPending(t, f)
			f.svc.reconcilePendingApprovals()
			select {
			case allowed := <-done:
				t.Fatal("computer input settled without desktop approval", mode, allowed)
			case <-time.After(50 * time.Millisecond):
			}
			always := httptest.NewRequest("POST", "/api/hoxi/permission/"+id+"/reply", strings.NewReader(`{"reply":"always"}`))
			always.Header.Set(ApprovalSourceHeader, "desktop")
			w := httptest.NewRecorder()
			f.svc.replyRunnerPermission(w, always, id)
			if w.Code != http.StatusForbidden {
				t.Fatal("desktop always accepted for computer input", mode, w.Code, w.Body.String())
			}
			once := httptest.NewRequest("POST", "/api/hoxi/permission/"+id+"/reply", strings.NewReader(`{"reply":"once"}`))
			once.Header.Set(ApprovalSourceHeader, "desktop")
			w = httptest.NewRecorder()
			f.svc.replyRunnerPermission(w, once, id)
			if w.Code != http.StatusOK || !<-done {
				t.Fatal("desktop once did not approve computer input", mode, w.Code, w.Body.String())
			}
		})
	}
}

func TestComputerScopeRequiresManagedSession(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()
	if _, err := f.svc.ResolveComputerSession("ses_unknown", f.dir); err == nil {
		t.Fatal("unmanaged desktop session accepted")
	}
	if _, err := f.svc.ResolveComputerSession("ses_agy_gate", t.TempDir()); err == nil {
		t.Fatal("wrong desktop worktree accepted")
	}
}
