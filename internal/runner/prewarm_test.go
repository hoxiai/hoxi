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

func TestStartProjectPrewarmWarmsEveryProjectRoot(t *testing.T) {
	t.Parallel()
	rootA := t.TempDir()
	rootB := t.TempDir()

	var mu sync.Mutex
	hits := map[string]int{}
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

	svc := NewService(t.TempDir(), nil, nil, t.TempDir())
	svc.ConfigureOpenCode(engine.URL, "pw")
	svc.SetProjectResolver(projectRootsResolver{roots: []string{rootA, rootB}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartProjectPrewarm(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		done := hits[rootA] > 0 && hits[rootB] > 0
		snapshot := map[string]int{}
		for k, v := range hits {
			snapshot[k] = v
		}
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("prewarm did not reach every root: %v", snapshot)
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

	svc := NewService(t.TempDir(), nil, nil, t.TempDir())
	svc.ConfigureOpenCode(engine.URL, "pw")
	missing := filepath.Join(t.TempDir(), "gone")
	svc.SetProjectResolver(projectRootsResolver{roots: []string{"", missing}})

	if !svc.prewarmProjectRoots(context.Background()) {
		t.Fatal("expected steady cadence when there is nothing to warm")
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
