package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

func TestIsAllowedLocalOrigin(t *testing.T) {
	allowed := []string{
		"wails://wails",
		"wails://wails.localhost",
		"http://localhost:5173",
		"http://127.0.0.1:8080",
		"http://[::1]:3000",
		"https://wails.localhost",
		"http://app.wails.localhost",
	}
	for _, origin := range allowed {
		if !isAllowedLocalOrigin(origin) {
			t.Errorf("expected origin %q to be allowed", origin)
		}
	}

	rejected := []string{
		"",
		"https://evil.com",
		"http://127.0.0.1.evil.com",
		"http://localhost.attacker.org",
		"https://notwails.localhost.evil.com",
		"javascript:alert(1)",
	}
	for _, origin := range rejected {
		if isAllowedLocalOrigin(origin) {
			t.Errorf("expected origin %q to be rejected", origin)
		}
	}
}

func TestGatewayCORSWhitelistEnforcement(t *testing.T) {
	gw := New(0, "test-pass", t.TempDir(), nil, nil)

	// Disallowed origin preflight should receive 403 Forbidden and no CORS reflection
	reqEvil := httptest.NewRequest(http.MethodOptions, "/global/health", nil)
	reqEvil.Header.Set("Origin", "https://evil.example.com")
	recEvil := httptest.NewRecorder()
	gw.ServeHTTP(recEvil, reqEvil)

	if recEvil.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed origin preflight, got %d", recEvil.Code)
	}
	if got := recEvil.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected empty Access-Control-Allow-Origin for evil origin, got %q", got)
	}

	// Allowed local origin preflight should receive 204 and reflected local origin
	reqLocal := httptest.NewRequest(http.MethodOptions, "/global/health", nil)
	reqLocal.Header.Set("Origin", "http://localhost:5173")
	recLocal := httptest.NewRecorder()
	gw.ServeHTTP(recLocal, reqLocal)

	if recLocal.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for allowed origin preflight, got %d", recLocal.Code)
	}
	if got := recLocal.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected Access-Control-Allow-Origin to be http://localhost:5173, got %q", got)
	}
}

type chunkedStringReader struct {
	chunks []string
	idx    int
}

func (r *chunkedStringReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.idx])
	r.chunks[r.idx] = r.chunks[r.idx][n:]
	if len(r.chunks[r.idx]) == 0 {
		r.idx++
	}
	return n, nil
}

func (r *chunkedStringReader) Close() error { return nil }

func TestSSETapReadCloserReassemblesLinesWithoutAllocationScanner(t *testing.T) {
	src := &chunkedStringReader{
		chunks: []string{
			"event: message\r\nda",
			"ta: {\"type\":\"session.status\"}\n",
			"\n",
			"data: {\"type\":\"session.idle\"}\r\n",
		},
	}

	var lines []string
	tap := newSSETapReadCloser(src, func(line string) {
		lines = append(lines, line)
	})
	defer tap.Close()

	fullBytes, err := io.ReadAll(tap)
	if err != nil {
		t.Fatalf("unexpected ReadAll error: %v", err)
	}
	if !strings.Contains(string(fullBytes), "session.status") {
		t.Fatalf("expected full stream bytes to be preserved, got %q", string(fullBytes))
	}

	want := []string{
		"event: message",
		`data: {"type":"session.status"}`,
		"",
		`data: {"type":"session.idle"}`,
	}
	if len(lines) != len(want) {
		t.Fatalf("expected %d lines, got %d (%v)", len(want), len(lines), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestGatewayHealthBootstrapDialogAndSystemInfo(t *testing.T) {
	rootDir := t.TempDir()
	tracker := statusbar.NewTracker()
	gw := New(4199, "secret-pass", rootDir, nil, tracker)
	gw.SetWSBaseURL("http://127.0.0.1:4199")

	// 1. GET /healthz
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recHealth := httptest.NewRecorder()
	gw.ServeHTTP(recHealth, reqHealth)
	if recHealth.Code != http.StatusOK || !strings.Contains(recHealth.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected /healthz response: code=%d body=%s", recHealth.Code, recHealth.Body.String())
	}

	// 2. GET /api/hoxi/bootstrap
	reqBoot := httptest.NewRequest(http.MethodGet, "/api/hoxi/bootstrap", nil)
	recBoot := httptest.NewRecorder()
	gw.ServeHTTP(recBoot, reqBoot)
	if recBoot.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/hoxi/bootstrap, got %d", recBoot.Code)
	}
	var bootPayload struct {
		RootDir   string            `json:"rootDir"`
		WSBaseURL string            `json:"wsBaseURL"`
		Status    statusbar.Summary `json:"status"`
	}
	if err := json.Unmarshal(recBoot.Body.Bytes(), &bootPayload); err != nil {
		t.Fatalf("unmarshal bootstrap payload: %v", err)
	}
	if bootPayload.RootDir != rootDir || bootPayload.WSBaseURL != "http://127.0.0.1:4199" || !bootPayload.Status.Available {
		t.Fatalf("unexpected bootstrap payload: %+v", bootPayload)
	}

	// 3. GET /api/hoxi/dialog/open-directory (nil callback, success callback, error callback)
	reqDialog := httptest.NewRequest(http.MethodGet, "/api/hoxi/dialog/open-directory", nil)
	recDialog := httptest.NewRecorder()
	gw.ServeHTTP(recDialog, reqDialog)
	if recDialog.Code != http.StatusOK || !strings.Contains(recDialog.Body.String(), `"path":""`) {
		t.Fatalf("unexpected nil dialog response: %s", recDialog.Body.String())
	}

	gw.SetOpenDirectoryDialog(func() (string, error) {
		return "/Users/test/workspace", nil
	})
	recDialogOK := httptest.NewRecorder()
	gw.ServeHTTP(recDialogOK, httptest.NewRequest(http.MethodGet, "/api/hoxi/dialog/open-directory", nil))
	if !strings.Contains(recDialogOK.Body.String(), `"/Users/test/workspace"`) {
		t.Fatalf("expected selected path in response, got %s", recDialogOK.Body.String())
	}

	gw.SetOpenDirectoryDialog(func() (string, error) {
		return "", errors.New("dialog failed")
	})
	recDialogErr := httptest.NewRecorder()
	gw.ServeHTTP(recDialogErr, httptest.NewRequest(http.MethodGet, "/api/hoxi/dialog/open-directory", nil))
	if !strings.Contains(recDialogErr.Body.String(), `"error":"dialog failed"`) {
		t.Fatalf("expected error in dialog response, got %s", recDialogErr.Body.String())
	}

	// 4. GET & POST /api/hoxi/system-info
	recSysPost := httptest.NewRecorder()
	gw.ServeHTTP(recSysPost, httptest.NewRequest(http.MethodPost, "/api/hoxi/system-info", nil))
	if recSysPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /api/hoxi/system-info, got %d", recSysPost.Code)
	}

	recSysGet := httptest.NewRecorder()
	gw.ServeHTTP(recSysGet, httptest.NewRequest(http.MethodGet, "/api/hoxi/system-info", nil))
	if recSysGet.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /api/hoxi/system-info, got %d", recSysGet.Code)
	}
	var sysInfo systemRuntimeInfoResponse
	if err := json.Unmarshal(recSysGet.Body.Bytes(), &sysInfo); err != nil {
		t.Fatalf("unmarshal system-info: %v", err)
	}
	if sysInfo.RootDir != rootDir || sysInfo.OpenCodePort != 4199 || sysInfo.OS == "" || sysInfo.GoVersion == "" {
		t.Fatalf("unexpected system-info response: %+v", sysInfo)
	}
}

func TestDevPulseWithGitRepositoryAndCache(t *testing.T) {
	if !isGeneratedOrLockFile("") ||
		!isGeneratedOrLockFile(".hoxi/worktrees/s1/file.go") ||
		!isGeneratedOrLockFile("web/dist/index.js") ||
		!isGeneratedOrLockFile("package-lock.json") ||
		!isGeneratedOrLockFile("bundle.min.js") ||
		isGeneratedOrLockFile("internal/gateway/gateway.go") {
		t.Fatal("unexpected isGeneratedOrLockFile classification")
	}

	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}

	runGit("init")
	runGit("config", "user.email", "test@hoxi.local")
	runGit("config", "user.name", "Hoxi Test")

	mainGo := filepath.Join(repoDir, "main.go")
	lockFile := filepath.Join(repoDir, "package-lock.json")
	if err := os.WriteFile(mainGo, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(lockFile, []byte("{\"lockfileVersion\":3}\n"), 0o644); err != nil {
		t.Fatalf("write package-lock.json: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "initial commit")

	// Modify tracked file
	if err := os.WriteFile(mainGo, []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(1) }\n"), 0o644); err != nil {
		t.Fatalf("modify main.go: %v", err)
	}
	// Add untracked source file and untracked lockfile (which should be ignored)
	if err := os.WriteFile(filepath.Join(repoDir, "helper.go"), []byte("package main\nvar x = 1\n"), 0o644); err != nil {
		t.Fatalf("write helper.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "yarn.lock"), []byte("# lock\n"), 0o644); err != nil {
		t.Fatalf("write yarn.lock: %v", err)
	}

	gw := New(0, "pass", repoDir, nil, nil)

	// POST should return 405
	recPost := httptest.NewRecorder()
	gw.ServeHTTP(recPost, httptest.NewRequest(http.MethodPost, "/api/hoxi/dev-pulse", nil))
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /api/hoxi/dev-pulse, got %d", recPost.Code)
	}

	// First GET computes git stats
	recGet := httptest.NewRecorder()
	gw.ServeHTTP(recGet, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse?directory="+repoDir, nil))
	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET /api/hoxi/dev-pulse, got %d", recGet.Code)
	}
	var pulse gitDailyPulseResponse
	if err := json.Unmarshal(recGet.Body.Bytes(), &pulse); err != nil {
		t.Fatalf("unmarshal dev-pulse: %v", err)
	}
	if pulse.TodayCommits != 1 || pulse.CommittedFiles != 1 || pulse.CommittedAdditions != 2 {
		t.Fatalf("unexpected committed stats (lockfile should be ignored): %+v", pulse)
	}
	if pulse.UncommittedFiles != 2 || pulse.UncommittedAdditions < 3 {
		t.Fatalf("unexpected uncommitted stats: %+v", pulse)
	}

	// Second GET should hit the in-memory TTL cache
	recCached := httptest.NewRecorder()
	gw.ServeHTTP(recCached, httptest.NewRequest(http.MethodGet, "/api/hoxi/dev-pulse?directory="+repoDir, nil))
	if recCached.Code != http.StatusOK {
		t.Fatalf("expected 200 for cached dev-pulse, got %d", recCached.Code)
	}
}

func TestSPAStaticHandlerAndReverseProxyWithSSETap(t *testing.T) {
	const testPass = "proxy-secret-123"

	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != testPass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/session":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"ses_test"}]`))
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"ses_1\",\"status\":{\"type\":\"busy\"}}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"permission.asked\",\"properties\":{\"id\":\"perm_1\",\"sessionID\":\"ses_1\"}}\n\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockUpstream.Close()

	port := mockUpstream.Listener.Addr().(*net.TCPAddr).Port
	mapFS := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>Hoxi SPA</title>")},
		"assets/app.js": {Data: []byte("console.log('hoxi');")},
	}
	tracker := statusbar.NewTracker()
	gw := New(port, testPass, t.TempDir(), mapFS, tracker)

	// 1. Direct static asset serving
	recAsset := httptest.NewRecorder()
	gw.ServeHTTP(recAsset, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if recAsset.Code != http.StatusOK || !strings.Contains(recAsset.Body.String(), "console.log('hoxi')") {
		t.Fatalf("expected static asset body, got code=%d body=%q", recAsset.Code, recAsset.Body.String())
	}

	// 2. SPA fallback to index.html on unknown client route
	recSPA := httptest.NewRecorder()
	gw.ServeHTTP(recSPA, httptest.NewRequest(http.MethodGet, "/unknown/route", nil))
	if recSPA.Code != http.StatusOK || !strings.Contains(recSPA.Body.String(), "<title>Hoxi SPA</title>") {
		t.Fatalf("expected SPA index.html fallback, got code=%d body=%q", recSPA.Code, recSPA.Body.String())
	}

	// 3. Reverse proxy Basic Auth injection on /session and /api/session
	for _, p := range []string{"/session", "/api/session"} {
		recSess := httptest.NewRecorder()
		gw.ServeHTTP(recSess, httptest.NewRequest(http.MethodGet, p, nil))
		if recSess.Code != http.StatusOK || !strings.Contains(recSess.Body.String(), "ses_test") {
			t.Fatalf("proxy %s failed: code=%d body=%q", p, recSess.Code, recSess.Body.String())
		}
	}

	// 4. Reverse proxy SSE tapping updates statusbar.Tracker
	recEvent := httptest.NewRecorder()
	gw.ServeHTTP(recEvent, httptest.NewRequest(http.MethodGet, "/event", nil))
	if recEvent.Code != http.StatusOK {
		t.Fatalf("expected 200 on /event, got %d", recEvent.Code)
	}
	snap := tracker.Snapshot()
	if snap.Running != 1 || snap.Attention != 1 {
		t.Fatalf("expected tracker Running=1 Attention=1 after SSE stream, got %+v", snap)
	}

	// 5. Proxy ErrorHandler when upstream is down
	mockUpstream.Close()
	recDown := httptest.NewRecorder()
	gw.ServeHTTP(recDown, httptest.NewRequest(http.MethodGet, "/session", nil))
	if recDown.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway when upstream is closed, got %d", recDown.Code)
	}
}
