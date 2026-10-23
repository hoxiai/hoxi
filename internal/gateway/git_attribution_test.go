package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/worktree"
)

func TestGitAttributionRouteAcceptsModesAndLegacyToggle(t *testing.T) {
	t.Cleanup(func() { worktree.SetAttributionMode(worktree.AttributionAuto) })
	g := New(0, "test", t.TempDir(), nil, nil)
	defer g.CloseBrowser()

	put := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, localRequest(http.MethodPut, "/api/hoxi/git/attribution", strings.NewReader(body)))
		return rec
	}
	get := func() worktree.AttributionState {
		t.Helper()
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, localRequest(http.MethodGet, "/api/hoxi/git/attribution", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET attribution = %d: %s", rec.Code, rec.Body.String())
		}
		var state worktree.AttributionState
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	if state := get(); state.Mode != worktree.AttributionAuto {
		t.Fatalf("default mode = %q, want auto", state.Mode)
	}
	for _, mode := range []worktree.AttributionMode{worktree.AttributionAlways, worktree.AttributionNever, worktree.AttributionAuto} {
		if rec := put(`{"mode":"` + string(mode) + `"}`); rec.Code != http.StatusOK {
			t.Fatalf("PUT mode %q = %d: %s", mode, rec.Code, rec.Body.String())
		}
		state := get()
		if state.Mode != mode {
			t.Fatalf("stored mode = %q, want %q", state.Mode, mode)
		}
		if state.Enabled != (mode != worktree.AttributionNever) {
			t.Fatalf("legacy enabled mirror = %v for mode %q", state.Enabled, mode)
		}
	}

	// The pre-mode boolean toggle still works: false becomes never.
	if rec := put(`{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT legacy false = %d: %s", rec.Code, rec.Body.String())
	}
	if state := get(); state.Mode != worktree.AttributionNever || state.Enabled {
		t.Fatalf("legacy false must store never, got %+v", state)
	}
	if rec := put(`{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT legacy true = %d: %s", rec.Code, rec.Body.String())
	}
	if state := get(); state.Mode != worktree.AttributionAlways || !state.Enabled {
		t.Fatalf("legacy true must store always, got %+v", state)
	}

	for _, body := range []string{`{}`, `{"mode":"sometimes"}`, `{"enabled":"yes"}`, `not json`, `{"mode":"auto","extra":1}`} {
		if rec := put(body); rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT %q = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}
}
