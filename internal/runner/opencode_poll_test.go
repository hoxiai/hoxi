package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/statusbar"
)

func TestOpenCodePollSkipsCleanIdleWorktreesAndRunsConcurrently(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	svc := NewService(projectDir, nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))

	const worktreeCount = 6
	worktrees := make([]string, worktreeCount)
	for i := 0; i < worktreeCount; i++ {
		wt := filepath.Join(projectDir, ".hoxi", "worktrees", fmt.Sprintf("ses_wt_%d", i))
		worktrees[i] = wt
		if err := svc.store.Put(SessionRecord{
			ID:           fmt.Sprintf("ses_wt_%d", i),
			Runtime:      "opencode",
			ProjectDir:   projectDir,
			WorktreePath: wt,
			ExternalID:   fmt.Sprintf("oc_ext_%d", i),
			UpdatedAt:    int64(1000 + i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	permHits := make(map[string]int)
	questionHits := make(map[string]int)
	var activeInflight atomic.Int32
	var maxConcurrent atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := activeInflight.Add(1)
		for {
			prev := maxConcurrent.Load()
			if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		activeInflight.Add(-1)

		qDir := r.URL.Query().Get("directory")
		mu.Lock()
		switch r.URL.Path {
		case "/permission":
			permHits[qDir]++
		case "/question":
			questionHits[qDir]++
		}
		mu.Unlock()
		writeJSON(w, http.StatusOK, []map[string]interface{}{})
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")

	// First poll inspects projectDir + all 6 worktrees concurrently.
	_ = svc.listUnifiedPermissions(context.Background(), projectDir)
	_ = svc.listUnifiedQuestions(context.Background(), projectDir)
	if got := maxConcurrent.Load(); got < 2 {
		t.Fatalf("expected concurrent OpenCode directory fan-out >= 2, got %d", got)
	}

	// Subsequent project switches / polls skip all 6 verified-empty idle worktrees
	// and only query the active projectDir root.
	for i := 0; i < 3; i++ {
		_ = svc.listUnifiedPermissions(context.Background(), projectDir)
		_ = svc.listUnifiedQuestions(context.Background(), projectDir)
	}

	mu.Lock()
	for _, wt := range worktrees {
		if permHits[wt] != 1 {
			t.Fatalf("worktree %s permission hits = %d, want 1", wt, permHits[wt])
		}
		if questionHits[wt] != 1 {
			t.Fatalf("worktree %s question hits = %d, want 1", wt, questionHits[wt])
		}
	}
	if permHits[projectDir] != 4 {
		t.Fatalf("projectDir permission hits = %d, want 4", permHits[projectDir])
	}
	mu.Unlock()

	// Advancing UpdatedAt on ses_wt_0 re-arms polling for worktrees[0] once.
	if _, _, err := svc.store.Update("ses_wt_0", func(r *SessionRecord) {
		r.Title = "touched"
	}); err != nil {
		t.Fatal(err)
	}
	_ = svc.listUnifiedPermissions(context.Background(), projectDir)
	_ = svc.listUnifiedPermissions(context.Background(), projectDir)

	mu.Lock()
	wt0HitsAfterUpdate := permHits[worktrees[0]]
	wt1HitsAfterUpdate := permHits[worktrees[1]]
	mu.Unlock()
	if wt0HitsAfterUpdate != 2 {
		t.Fatalf("updated worktree[0] permission hits = %d, want 2", wt0HitsAfterUpdate)
	}
	if wt1HitsAfterUpdate != 1 {
		t.Fatalf("untouched worktree[1] permission hits = %d, want 1", wt1HitsAfterUpdate)
	}
}

type countingProjectResolver struct {
	roots       []string
	dirLookups  atomic.Int32
	projectByID string
}

func (r *countingProjectResolver) ProjectIDForDirectory(directory string) (string, bool) {
	r.dirLookups.Add(1)
	norm := normalizeComparePath(directory)
	for _, root := range r.roots {
		if normalizeComparePath(root) == norm {
			return r.projectByID, true
		}
	}
	return "", false
}

func (r *countingProjectResolver) EnsureProjectForDirectory(directory string) (string, error) {
	id, _ := r.ProjectIDForDirectory(directory)
	return id, nil
}

func (r *countingProjectResolver) ProjectRoots(projectID string) []string {
	if projectID == r.projectByID {
		return r.roots
	}
	return nil
}

func TestConversationListMemoizesDirResolutionAndQueriesRootsConcurrently(t *testing.T) {
	t.Parallel()
	roots := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	svc := NewService(roots[0], nil, statusbar.NewTracker(), filepath.Join(t.TempDir(), "sessions.json"))
	resolver := &countingProjectResolver{roots: roots, projectByID: "prj_multi"}
	svc.SetProjectResolver(resolver)

	var active atomic.Int32
	var maxConcurrent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := active.Add(1)
		for {
			prev := maxConcurrent.Load()
			if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		active.Add(-1)

		qDir := r.URL.Query().Get("directory")
		switch r.URL.Path {
		case "/session":
			items := make([]map[string]interface{}, 0, 20)
			for i := 0; i < 20; i++ {
				items = append(items, map[string]interface{}{
					"id":        fmt.Sprintf("oc_%s_%d", filepath.Base(qDir), i),
					"directory": qDir,
					"time":      map[string]interface{}{"updated": 100 + i},
				})
			}
			writeJSON(w, http.StatusOK, items)
		case "/session/status":
			writeJSON(w, http.StatusOK, map[string]interface{}{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc.ConfigureOpenCode(server.URL, "secret")

	got := svc.listConversationsWithSurface(context.Background(), "prj_multi", roots[0], "code")
	if len(got) != 60 {
		t.Fatalf("conversations = %d, want 60", len(got))
	}
	if maxConcurrent.Load() < 2 {
		t.Fatalf("expected concurrent multi-root /session queries >= 2, got %d", maxConcurrent.Load())
	}
	// 60 sessions across 3 roots should only resolve each unique directory once (<= 3 lookups).
	if lookups := resolver.dirLookups.Load(); lookups > 3 {
		t.Fatalf("ProjectIDForDirectory lookups = %d, want <= 3 (memoized per directory)", lookups)
	}
	_ = svc.conversationStatuses(context.Background(), "prj_multi", roots[0])
}
