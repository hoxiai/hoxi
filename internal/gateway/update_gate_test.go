package gateway

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
	"github.com/hoxiai/hoxi/internal/statusbar"
	"github.com/hoxiai/hoxi/internal/workspace"
	"github.com/hoxiai/hoxi/internal/worktree"
)

func updateTestGateway(t *testing.T, handler http.HandlerFunc) *Gateway {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	return &Gateway{openCodePort: n, openCode: ocapi.NewForPort(n, "test-secret"), rootDir: t.TempDir(), status: statusbar.NewTracker(), mux: http.NewServeMux()}
}

func serveUpdateTest(g *Gateway, method, path string) int {
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(method, "http://localhost"+path, nil))
	return w.Code
}

func TestUpdateDrainingAdmission(t *testing.T) {
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	g.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		g.updateMu.Lock()
		count := g.updateMutations
		g.updateMu.Unlock()
		if count != 0 {
			t.Errorf("exempt request counted: %d", count)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if !g.BeginUpdate() || !g.UpdateDraining() {
		t.Fatal("idle gateway did not drain")
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD"} {
		if code := serveUpdateTest(g, method, "/anything"); code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %d", method, code)
		}
	}
	for _, request := range [][2]string{{"GET", "/anything"}, {"OPTIONS", "/anything"}, {"POST", "/api/hoxi/updater/install"}, {"POST", "/api/hoxi/updater"}} {
		if code := serveUpdateTest(g, request[0], request[1]); code != http.StatusNoContent {
			t.Errorf("exempt %v: got %d", request, code)
		}
	}
	if code := serveUpdateTest(g, "POST", "/api/hoxi/updater-other"); code != http.StatusServiceUnavailable {
		t.Fatalf("updater prefix bypass: %d", code)
	}
	g.ReleaseUpdate()
	if g.UpdateDraining() {
		t.Fatal("release retained drain")
	}
}

func TestUpdateInflightMutation(t *testing.T) {
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	g.mux.HandleFunc("/write", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})
	go func() {
		defer close(done)
		serveUpdateTest(g, "POST", "/write")
	}()
	<-entered
	accepted := g.BeginUpdate()
	close(release)
	<-done
	if accepted || g.UpdateDraining() {
		t.Fatal("admitted update during an active mutation")
	}
	if !g.BeginUpdate() {
		t.Fatal("completed mutation still counted")
	}
}

func TestUpdateForcedAdmissionWhileBusy(t *testing.T) {
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	g.status.SetSessionBusy("ses_test", true)
	if g.BeginUpdate() || g.UpdateDraining() {
		t.Fatal("busy gateway admitted a normal update")
	}
	if !g.BeginUpdateForced() || !g.UpdateDraining() {
		t.Fatal("forced update must drain while sessions are busy")
	}
	g.ReleaseUpdate()
	if g.UpdateDraining() {
		t.Fatal("release retained forced drain")
	}
}

func TestUpdateAuthoritativeStatus(t *testing.T) {
	for _, body := range []string{`{"s":{"type":"busy"}}`, `{"s":{"type":"retry"}}`, `{"s":{"type":"unknown"}}`, `{"s":{}}`, `{"s":null}`, `null`, `[]`, `{`, `{} {}`, strings.Repeat(" ", (1<<20)+1)} {
		t.Run(fmt.Sprintf("%.40s", body), func(t *testing.T) {
			g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			if g.BeginUpdate() || g.UpdateDraining() {
				t.Fatal("unsafe status admitted or drain leaked")
			}
		})
	}
	for _, code := range []int{http.StatusUnauthorized, http.StatusInternalServerError, http.StatusFound} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/session/status")
				w.WriteHeader(code)
				fmt.Fprint(w, `{}`)
			})
			if g.BeginUpdate() {
				t.Fatal("HTTP failure admitted")
			}
		})
	}
}

func TestUpdateRevalidation(t *testing.T) {
	var mu sync.Mutex
	body := `{"s":{"type":"idle"}}`
	calls := 0
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		fmt.Fprint(w, body)
	})
	if !g.BeginUpdate() || !g.BeginUpdate() {
		t.Fatal("reentrant idle check failed")
	}
	mu.Lock()
	if calls != 2 {
		t.Errorf("expected fresh status on reentry, got %d calls", calls)
	}
	body = `{"s":{"type":"busy"}}`
	mu.Unlock()
	if g.BeginUpdate() || !g.UpdateDraining() {
		t.Fatal("failed revalidation must preserve existing drain")
	}
	g.ReleaseUpdate()
}

func TestUpdateKnownDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	folder := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".hoxi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".hoxi", "workspaces.json"), []byte(fmt.Sprintf(`{"workspaces":[{"folders":[{"path":%q}]}]}`, folder)), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := make(map[string]bool)
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "test-secret" || r.URL.Path != "/session/status" {
			t.Error("missing scoped authenticated status request")
		}
		mu.Lock()
		seen[r.URL.Query().Get("directory")] = true
		mu.Unlock()
		fmt.Fprint(w, `{}`)
	})
	g.worktrees = worktree.NewManager(g.rootDir)
	g.workspaces = workspace.NewStore(g.rootDir)
	persisted := filepath.Join(g.rootDir, ".hoxi", "worktrees", "persisted")
	if err := os.MkdirAll(persisted, 0o755); err != nil {
		t.Fatal(err)
	}
	observed := t.TempDir()
	serveUpdateTest(g, "GET", "/healthz?directory="+observed)
	if !g.BeginUpdate() {
		t.Fatal("idle directories rejected")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, dir := range []string{g.rootDir, persisted, observed, folder} {
		if !seen[dir] {
			t.Errorf("directory not checked: %s", dir)
		}
	}
}

func TestUpdateDrainsBeforeRemoteCheck(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `{}`)
	})
	result := make(chan bool, 1)
	go func() { result <- g.BeginUpdate() }()
	<-entered
	code := serveUpdateTest(g, "POST", "/write")
	draining := g.UpdateDraining()
	close(release)
	accepted := <-result
	if code != http.StatusServiceUnavailable || !draining || !accepted {
		t.Fatalf("remote check admission: status=%d draining=%v accepted=%v", code, draining, accepted)
	}
}

func TestUpdateLocalStatus(t *testing.T) {
	for _, state := range []string{"running", "attention", "unavailable", "missing"} {
		t.Run(state, func(t *testing.T) {
			g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("local denial should not query OpenCode")
			})
			switch state {
			case "running":
				g.status.SetSessionBusy("s", true)
			case "attention":
				g.status.SetPendingApproval("p", "s", true)
			case "unavailable":
				g.status.SetAvailable(false)
			case "missing":
				g.status = nil
			}
			if g.BeginUpdate() || g.UpdateDraining() {
				t.Fatal("unsafe local state admitted")
			}
		})
	}
}

func TestUpdateStatusTimeout(t *testing.T) {
	orig := updateStatusTimeout
	updateStatusTimeout = 200 * time.Millisecond
	t.Cleanup(func() { updateStatusTimeout = orig })

	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	start := time.Now()
	if g.BeginUpdate() || g.UpdateDraining() {
		t.Fatal("timeout admitted or drain leaked")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("status check exceeded shared deadline: %s", elapsed)
	}
}

func TestUpdateIgnoresDeletedHistoricalDirectory(t *testing.T) {
	var deleted string
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("directory") == deleted {
			t.Error("deleted directory queried")
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{}`)
	})
	g.worktrees = worktree.NewManager(g.rootDir)
	deleted = filepath.Join(g.rootDir, ".hoxi", "worktrees", "deleted")
	if err := os.MkdirAll(deleted, 0700); err != nil {
		t.Fatal(err)
	}
	g.worktrees.ForDirectory(deleted) // A previously visited manager root can also disappear.
	serveUpdateTest(g, "GET", "/healthz?directory="+deleted)
	if !g.updateDirectories[deleted] {
		t.Fatal("fixture directory was not observed")
	}
	if err := os.RemoveAll(deleted); err != nil {
		t.Fatal(err)
	}
	if !g.BeginUpdate() {
		t.Fatal("historical directory blocked update forever")
	}
	if g.updateDirectories[deleted] {
		t.Fatal("deleted directory was not pruned")
	}
}

func TestRejectedScopeCannotPoisonUpdateDirectories(t *testing.T) {
	foreign := t.TempDir()
	g := updateTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("directory") == foreign {
			t.Error("rejected directory queried")
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{}`)
	})
	g.worktrees = worktree.NewManager(g.rootDir)
	if _, err := g.worktrees.Create("ses_bound"); err != nil {
		t.Fatal(err)
	}
	if code := serveUpdateTest(g, "GET", "/session/ses_bound?directory="+foreign); code != http.StatusForbidden {
		t.Fatalf("invalid scope returned %d", code)
	}
	if g.updateDirectories[foreign] {
		t.Fatal("rejected directory persisted")
	}
	if !g.BeginUpdate() {
		t.Fatal("rejected request blocked update")
	}
}
