package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestEnsureOpenCodeModelAvailableReloadsIdleDirectoryInstance(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	var disposed atomic.Int32

	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config/providers":
			models := map[string]any{
				"CodeArts-deepseek-v4.1-flash": map[string]any{},
			}
			if disposed.Load() > 0 {
				models = map[string]any{
					"loomy-DeepSeek-V4.1-Flash": map[string]any{},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"providers": []map[string]any{
					{"id": "subapi", "models": models},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_ = json.NewEncoder(w).Encode(map[string]SessionStatus{})
		case r.Method == http.MethodPost && r.URL.Path == "/instance/dispose":
			if got := r.URL.Query().Get("directory"); got != project {
				t.Errorf("dispose directory = %q, want %q", got, project)
			}
			disposed.Add(1)
			_ = json.NewEncoder(w).Encode(true)
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()

	svc := NewService(project, worktree.NewManager(project), nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(engine.URL, "secret")

	resolved, err := svc.ensureOpenCodeModelAvailable(context.Background(), project, "subapi", "loomy-DeepSeek-V4.1-Flash", true)
	if err != nil {
		t.Fatalf("expected reload to make model available, got err=%v", err)
	}
	if resolved != "loomy-DeepSeek-V4.1-Flash" {
		t.Fatalf("resolved = %q, want loomy-DeepSeek-V4.1-Flash", resolved)
	}
	if disposed.Load() != 1 {
		t.Fatalf("expected 1 instance dispose call, got %d", disposed.Load())
	}
}

func TestEnsureOpenCodeModelAvailableSkipsDisposeWhenBusyAndMatchesCaseInsensitive(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	var disposed atomic.Int32

	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config/providers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"providers": []map[string]any{
					{
						"id": "subapi",
						"models": map[string]any{
							"Loomy-DeepSeek-V4.1-Flash":    map[string]any{},
							"CodeArts-deepseek-v4.1-flash": map[string]any{},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_ = json.NewEncoder(w).Encode(map[string]SessionStatus{
				"ses_busy": {Type: "busy"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/instance/dispose":
			disposed.Add(1)
			_ = json.NewEncoder(w).Encode(true)
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()

	svc := NewService(project, worktree.NewManager(project), nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.ConfigureOpenCode(engine.URL, "secret")

	resolved, err := svc.ensureOpenCodeModelAvailable(context.Background(), project, "subapi", "loomy-DeepSeek-V4.1-Flash", true)
	if err != nil {
		t.Fatalf("expected case-insensitive model resolution, got err=%v", err)
	}
	if resolved != "Loomy-DeepSeek-V4.1-Flash" {
		t.Fatalf("resolved = %q, want Loomy-DeepSeek-V4.1-Flash", resolved)
	}
	if disposed.Load() != 0 {
		t.Fatalf("expected busy directory not to dispose instance, got %d", disposed.Load())
	}

	_, err = svc.ensureOpenCodeModelAvailable(context.Background(), project, "subapi", "missing-model", true)
	var unavail *engineModelUnavailableError
	if !errors.As(err, &unavail) {
		t.Fatalf("expected engineModelUnavailableError for missing model, got %v", err)
	}
	if disposed.Load() != 0 {
		t.Fatalf("expected busy directory not to dispose instance on missing model, got %d", disposed.Load())
	}
}
