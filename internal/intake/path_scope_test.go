package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type scopeMockSessions struct {
	mu          sync.Mutex
	ensureReqs  []RunSessionRequest
	runReqs     []RunSessionRequest
	violations  []string
	verifyErr   error
	verifyCalls int
}

func (m *scopeMockSessions) EnsureSession(_ context.Context, req RunSessionRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureReqs = append(m.ensureReqs, req)
	if req.SessionID != "" {
		return req.SessionID, nil
	}
	return "ses_qa_scoped_1", nil
}

func (m *scopeMockSessions) RunTurn(_ context.Context, _ string, req RunSessionRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runReqs = append(m.runReqs, req)
	return "Fixed issue cleanly.", nil
}

func (m *scopeMockSessions) SanitizedTimeline(string) (SanitizedTimeline, bool) {
	return SanitizedTimeline{}, true
}

func (m *scopeMockSessions) SubscribeSession(string) (<-chan struct{}, func()) {
	return nil, func() {}
}

func (m *scopeMockSessions) Abort(string) {}

func (m *scopeMockSessions) VerifyPathScope(string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verifyCalls++
	return append([]string(nil), m.violations...), m.verifyErr
}

func TestLinkPathScopeValidationUpdateAndPortalIsolation(t *testing.T) {
	proj := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "internal", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(proj, "escape_dir")); err != nil {
		t.Fatal(err)
	}

	sess := &scopeMockSessions{}
	svc := NewService(filepath.Join(t.TempDir(), "intake.json"), nil, sess, nil)
	defer svc.Close()

	// Reject invalid paths instead of silently dropping them.
	for _, bad := range []string{"/etc", "../outside", "internal/*", ".", "escape_dir/sub"} {
		if _, err := svc.CreateLink(CreateLinkInput{
			Directory:     proj,
			Label:         "QA",
			ReadOnlyPaths: []string{bad},
		}); err == nil {
			t.Errorf("expected CreateLink to reject invalid readOnlyPath %q", bad)
		}
	}

	// Create link with read-only & writable paths.
	pub, err := svc.CreateLink(CreateLinkInput{
		Directory:     proj,
		Label:         "QA-小王",
		ReadOnlyPaths: []string{"./internal/auth/", `config\prod.yaml`, "internal/auth"},
		WritablePaths: []string{"web/src/pages/"},
	})
	if err != nil {
		t.Fatalf("CreateLink valid failed: %v", err)
	}
	if strings.Join(pub.ReadOnlyPaths, ",") != "internal/auth,config/prod.yaml" {
		t.Fatalf("unexpected ReadOnlyPaths: %v", pub.ReadOnlyPaths)
	}
	if strings.Join(pub.WritablePaths, ",") != "web/src/pages" {
		t.Fatalf("unexpected WritablePaths: %v", pub.WritablePaths)
	}

	// Update link scope via HTTP PUT without changing token/URL; remote callers stay 403.
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux, func(r *http.Request) bool { return r.Header.Get("X-Remote") == "1" })

	putBody := `{"id":"` + pub.ID + `","readOnlyPaths":["internal/auth","migrations"],"writablePaths":["web/src"]}`
	remReq := httptest.NewRequest(http.MethodPut, "/api/hoxi/intake/links", strings.NewReader(putBody))
	remReq.Header.Set("X-Remote", "1")
	remRec := httptest.NewRecorder()
	mux.ServeHTTP(remRec, remReq)
	if remRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for remote PUT /api/hoxi/intake/links, got %d", remRec.Code)
	}

	okRec := httptest.NewRecorder()
	mux.ServeHTTP(okRec, httptest.NewRequest(http.MethodPut, "/api/hoxi/intake/links", strings.NewReader(putBody)))
	if okRec.Code != http.StatusOK {
		t.Fatalf("PUT /api/hoxi/intake/links failed: %d %s", okRec.Code, okRec.Body.String())
	}
	var updated PublicLink
	if err := json.Unmarshal(okRec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.PortalPath != pub.PortalPath {
		t.Fatalf("UpdateLink must preserve portalPath: got %q want %q", updated.PortalPath, pub.PortalPath)
	}
	if strings.Join(updated.ReadOnlyPaths, ",") != "internal/auth,migrations" ||
		strings.Join(updated.WritablePaths, ",") != "web/src" {
		t.Fatalf("unexpected updated scope: ro=%v rw=%v", updated.ReadOnlyPaths, updated.WritablePaths)
	}

	// Portal info & portal tasks MUST NEVER leak internal readOnlyPaths or writablePaths.
	tok := strings.TrimPrefix(pub.PortalPath, "/qa/")
	link, err := svc.AuthorizePortalToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := svc.SubmitPortalTask(link, SubmitTaskInput{
		Title:       "按钮错位",
		Description: "首页按钮换行",
	})
	if err != nil {
		t.Fatal(err)
	}

	infoRec := httptest.NewRecorder()
	infoReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/intake/portal/info", nil)
	infoReq.Header.Set("X-Hoxi-Intake-Token", tok)
	mux.ServeHTTP(infoRec, infoReq)
	if infoRec.Code != http.StatusOK {
		t.Fatalf("portal info failed: %d", infoRec.Code)
	}
	rawPortal := infoRec.Body.String()
	for _, secret := range []string{"readOnlyPaths", "writablePaths", "internal/auth", "migrations", "web/src"} {
		if strings.Contains(rawPortal, secret) {
			t.Fatalf("portal info leaked internal scope detail %q:\n%s", secret, rawPortal)
		}
	}

	// Approving task forwards link path scope to runner and puts guardrails into System prompt (never untrusted tag).
	if _, err := svc.ApproveAndRun(context.Background(), ApproveTaskInput{TaskID: submitted.ID}); err != nil {
		t.Fatalf("ApproveAndRun failed: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tasks := svc.ListDesktopTasks(proj)
		if len(tasks) == 1 && tasks[0].Status == StatusSucceeded {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	sess.mu.Lock()
	if len(sess.ensureReqs) != 1 || len(sess.runReqs) != 1 {
		sess.mu.Unlock()
		t.Fatalf("expected 1 ensure & 1 run call, got ensure=%d run=%d", len(sess.ensureReqs), len(sess.runReqs))
	}
	req := sess.runReqs[0]
	vCalls := sess.verifyCalls
	sess.mu.Unlock()
	if strings.Join(req.ReadOnlyPaths, ",") != "internal/auth,migrations" ||
		strings.Join(req.WritablePaths, ",") != "web/src" {
		t.Fatalf("RunSessionRequest missing scope: ro=%v rw=%v", req.ReadOnlyPaths, req.WritablePaths)
	}
	if !strings.Contains(req.System, "internal/auth") || !strings.Contains(req.System, "web/src") {
		t.Fatalf("expected System prompt to carry path scope rules, got:\n%s", req.System)
	}
	if strings.Contains(req.Prompt, "internal/auth") {
		t.Fatalf("untrusted prompt block must not include internal path rules:\n%s", req.Prompt)
	}
	if vCalls != 1 {
		t.Fatalf("expected post-turn VerifyPathScope to run once, got %d", vCalls)
	}

	// Post-turn violation marks task failed with sanitized non-leaking error message.
	sess.mu.Lock()
	sess.violations = []string{"internal/auth/token.go"}
	sess.mu.Unlock()
	sub2, err := svc.SubmitPortalTask(link, SubmitTaskInput{
		Title:       "另一个问题",
		Description: "复现步骤",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveAndRun(context.Background(), ApproveTaskInput{TaskID: sub2.ID}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	var failedTask TaskItem
	for time.Now().Before(deadline) {
		for _, item := range svc.ListDesktopTasks(proj) {
			if item.ID == sub2.ID && item.Status == StatusFailed {
				failedTask = item
				break
			}
		}
		if failedTask.ID != "" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if failedTask.Status != StatusFailed {
		t.Fatalf("expected task with path-scope diff violation to settle as failed, got %+v", failedTask)
	}
	if strings.Contains(failedTask.Error, "internal/auth") || !bytes.Contains([]byte(failedTask.Error), []byte("只读")) {
		t.Fatalf("expected sanitized scope violation error without leaking repo path, got %q", failedTask.Error)
	}
}
