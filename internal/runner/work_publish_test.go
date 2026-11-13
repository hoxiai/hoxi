package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkPublicationRequiresDesktopOnceAcrossAllApprovalModes(t *testing.T) {
	for _, mode := range []string{ApprovalModeManual, ApprovalModeAuto, ApprovalModeSmart, ApprovalModeTrusted} {
		t.Run(mode, func(t *testing.T) {
			f := newGateFixture(t)
			defer f.revoke()
			setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: mode})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- f.svc.RequestWorkPublishApproval(ctx, "ses_agy_gate", f.dir, "store-a", map[string]any{"draftId": "drf_a", "revision": "v1"})
			}()
			id := waitWorkPublication(t, f)
			f.svc.reconcilePendingApprovals()
			select {
			case err := <-done:
				t.Fatal("publication settled without user approval", err)
			case <-time.After(30 * time.Millisecond):
			}
			for _, denied := range []struct{ body, source string }{
				{`{"reply":"always"}`, "desktop"},
				{`{"reply":"once","grantClass":true}`, "desktop"},
				{`{"reply":"once"}`, "tunnel"},
			} {
				r := httptest.NewRequest("POST", "/api/hoxi/permission/"+id+"/reply", strings.NewReader(denied.body))
				r.Header.Set(ApprovalSourceHeader, denied.source)
				w := httptest.NewRecorder()
				f.svc.replyRunnerPermission(w, r, id)
				if w.Code != http.StatusForbidden {
					t.Fatal("invalid publication approval accepted", denied, w.Code)
				}
			}
			r := httptest.NewRequest("POST", "/api/hoxi/permission/"+id+"/reply", strings.NewReader(`{"reply":"once"}`))
			r.Header.Set(ApprovalSourceHeader, "desktop")
			w := httptest.NewRecorder()
			f.svc.replyRunnerPermission(w, r, id)
			if w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func waitWorkPublication(t *testing.T, f *gateFixture) string {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		f.svc.gate.mu.Lock()
		for id, p := range f.svc.gate.pending {
			if permissionName(p.req) == "hoxi_work_publish" {
				f.svc.gate.mu.Unlock()
				return id
			}
		}
		f.svc.gate.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("publication did not enter the shared permission plane")
	return ""
}

func TestWorkPublicationRejectsUnmanagedWrongDirectoryUnattendedAndCancellation(t *testing.T) {
	f := newGateFixture(t)
	defer f.revoke()
	for _, tc := range []struct{ session, dir string }{{"ses_unknown", f.dir}, {"ses_agy_gate", t.TempDir()}} {
		if err := f.svc.RequestWorkPublishApproval(context.Background(), tc.session, tc.dir, "store-a", nil); err == nil {
			t.Fatal("invalid session scope accepted", tc)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.svc.RequestWorkPublishApproval(ctx, "ses_agy_gate", f.dir, "store-a", nil) }()
	_ = waitWorkPublication(t, f)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled publication was approved")
	}
	setTestApprovalPolicy(t, f.svc, ApprovalPolicy{Mode: ApprovalModeUnattended})
	if err := f.svc.RequestWorkPublishApproval(context.Background(), "ses_agy_gate", f.dir, "store-a", nil); err == nil {
		t.Fatal("unattended publication accepted")
	}
}
