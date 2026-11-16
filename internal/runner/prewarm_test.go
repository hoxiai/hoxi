package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type projectRootsResolver struct{ roots []string }

func (r projectRootsResolver) ProjectIDForDirectory(string) (string, bool) { return "", false }
func (r projectRootsResolver) EnsureProjectForDirectory(string) (string, error) {
	return "", nil
}
func (r projectRootsResolver) ProjectRoots(string) []string { return r.roots }
func (r projectRootsResolver) AllProjectRoots() []string    { return r.roots }

func TestStartProjectPrewarmWarmsActiveRootWithoutPollingIdleProjects(t *testing.T) {
	t.Parallel()
	activeRoot := t.TempDir()
	idleRoot := t.TempDir()

	var mu sync.Mutex
	hits := map[string]int{}
	warmed := map[string]int{}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		hits[r.URL.Query().Get("directory")]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer engine.Close()

	svc := NewService(activeRoot, nil, nil, t.TempDir())
	svc.ConfigureOpenCode(engine.URL, "pw")
	svc.SetProjectResolver(projectRootsResolver{roots: []string{idleRoot, activeRoot}})
	svc.SetDirectoryWarmer(func(dir string) {
		mu.Lock()
		warmed[dir]++
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartProjectPrewarm(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		done := hits[activeRoot] > 0 && warmed[activeRoot] > 0
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("prewarm did not warm active root %s", activeRoot)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if hits[idleRoot] != 0 || warmed[idleRoot] != 0 {
		mu.Unlock()
		t.Fatalf("expected idle historical root %s not to be eagerly prewarmed: hits=%v warmed=%v", idleRoot, hits, warmed)
	}
	mu.Unlock()

	// Explicit on-demand warm still works for non-startup directories.
	svc.warmDirectoryAsync(idleRoot)
	for {
		mu.Lock()
		ok := warmed[idleRoot] == 1
		mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected on-demand warmDirectoryAsync to warm %s", idleRoot)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
}

func TestPrewarmProjectRootsSkipsMissingDirectories(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var queried []string
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queried = append(queried, r.URL.Query().Get("directory"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer engine.Close()

	missing := filepath.Join(t.TempDir(), "gone")
	svc := NewService(missing, nil, nil, t.TempDir())
	svc.ConfigureOpenCode(engine.URL, "pw")
	svc.SetProjectResolver(projectRootsResolver{roots: []string{"", missing}})

	if !svc.prewarmProjectRoots(context.Background()) {
		t.Fatal("expected completion when there is no existing root to warm")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queried) != 0 {
		t.Fatalf("engine queried for missing directories: %v", queried)
	}
}

func TestPrewarmProjectRootsRequestsRetryWhenEngineDown(t *testing.T) {
	t.Parallel()
	svc := NewService(t.TempDir(), nil, nil, t.TempDir())
	svc.ConfigureOpenCode("http://127.0.0.1:1", "pw")
	svc.SetProjectResolver(projectRootsResolver{roots: []string{t.TempDir()}})

	if svc.prewarmProjectRoots(context.Background()) {
		t.Fatal("expected retry cadence while the engine is unreachable")
	}
}

func TestIndexPrewarmDecoupledFromEngineKeepAliveAndCapped(t *testing.T) {
	t.Parallel()
	activeRoot := t.TempDir()
	roots := make([]string, maxPrewarmIndexRoots+4)
	for i := range roots {
		roots[i] = t.TempDir()
	}

	var mu sync.Mutex
	warmed := map[string]int{}
	hasDeadline := false

	svc := NewService(activeRoot, nil, nil, t.TempDir())
	svc.ConfigureOpenCode("http://127.0.0.1:1", "pw")
	svc.SetProjectResolver(projectRootsResolver{roots: roots})
	svc.SetDirectoryWarmerContext(func(ctx context.Context, dir string) {
		mu.Lock()
		warmed[dir]++
		if _, ok := ctx.Deadline(); ok {
			hasDeadline = true
		}
		mu.Unlock()
	})

	ctx := context.Background()
	svc.prewarmIndexRoots(ctx)
	_ = svc.prewarmProjectRoots(ctx)
	_ = svc.prewarmProjectRoots(ctx)
	svc.prewarmIndexRoots(ctx)

	mu.Lock()
	defer mu.Unlock()
	if !hasDeadline {
		t.Fatal("expected directory warmer context to carry a timeout deadline")
	}
	if len(warmed) != maxPrewarmIndexRoots || warmed[activeRoot] != 1 {
		t.Fatalf("expected index prewarm capped at %d active root, got %v", maxPrewarmIndexRoots, warmed)
	}
}
