package workspace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupTestEnv(t *testing.T) string {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	return homeDir
}

func mkdirTempProject(t *testing.T, base, name string) string {
	t.Helper()
	p := filepath.Join(base, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("failed to mkdir %s: %v", p, err)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != "" {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

func TestNormalizeWorkspaceIdentityColor(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"#abc", "#AABBCC"},
		{" #1f2 ", "#11FF22"},
		{"#5ea0f7", "#5EA0F7"},
		{"#AABBCC", "#AABBCC"},
		{"5ea0f7", ""},
		{"#1234", ""},
		{"#zzzzzz", ""},
	}
	for _, tc := range cases {
		if got := normalizeWorkspaceIdentityColor(tc.raw); got != tc.want {
			t.Errorf("normalizeWorkspaceIdentityColor(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestValidateWorkspaceFolders(t *testing.T) {
	homeDir := setupTestEnv(t)
	projA := mkdirTempProject(t, homeDir, "proj-a")
	projB := mkdirTempProject(t, homeDir, "proj-b")

	// Create a regular file (should be ignored)
	filePath := filepath.Join(homeDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Empty input -> error
	if _, err := validateWorkspaceFolders(nil); err == nil {
		t.Fatal("expected error for empty folders input")
	}

	// Only non-existent or file inputs -> error
	if _, err := validateWorkspaceFolders([]workspaceFolderInput{
		{Path: "   "},
		{Path: filepath.Join(homeDir, "does-not-exist")},
		{Path: filePath},
	}); err == nil {
		t.Fatal("expected error when no valid directories remain")
	}

	// Root or home directory -> error
	if _, err := validateWorkspaceFolders([]workspaceFolderInput{{Path: "/"}}); err == nil {
		t.Fatal("expected error for root directory")
	}
	if _, err := validateWorkspaceFolders([]workspaceFolderInput{{Path: homeDir}}); err == nil {
		t.Fatal("expected error for home directory")
	}

	// Deduplication and primary promotion when IsPrimary is false on all inputs
	got, err := validateWorkspaceFolders([]workspaceFolderInput{
		{Path: projA, IsPrimary: false},
		{Path: projA, IsPrimary: true}, // duplicate should be skipped
		{Path: projB, IsPrimary: false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || !got[0].IsPrimary || got[0].Path != projA || got[1].IsPrimary || got[1].Path != projB {
		t.Fatalf("unexpected validated folders: %+v", got)
	}

	// Explicit primary on second folder moves it to index 0
	got2, err := validateWorkspaceFolders([]workspaceFolderInput{
		{Path: projA, IsPrimary: false},
		{Path: projB, IsPrimary: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got2) != 2 || got2[0].Path != projB || !got2[0].IsPrimary || got2[1].Path != projA || got2[1].IsPrimary {
		t.Fatalf("expected projB to be primary at index 0, got %+v", got2)
	}
}

func TestStoreLifecycleAndPersistence(t *testing.T) {
	homeDir := setupTestEnv(t)
	seedDir := mkdirTempProject(t, homeDir, "seed-project")
	projB := mkdirTempProject(t, homeDir, "alpha-project")
	projC := mkdirTempProject(t, homeDir, "beta-project")

	// 1. NewStore seeds from seedDir and writes ~/.hoxi/workspaces.json
	store := NewStore(seedDir)
	list := store.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 seeded workspace, got %d", len(list))
	}
	if list[0].Name != "seed-project" || list[0].Color != defaultWorkspaceIdentityColor {
		t.Fatalf("unexpected seeded workspace: %+v", list[0])
	}

	// 2. Create with duplicate name resolution & custom short hex color
	ws1, err := store.Create(saveWorkspaceRequest{
		Name:    "seed-project", // collides with seeded workspace
		Color:   "#f0a",
		Folders: []workspaceFolderInput{{Path: projB, IsPrimary: true}},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if ws1.Name != "seed-project (2)" {
		t.Fatalf("expected duplicate name 'seed-project (2)', got %q", ws1.Name)
	}
	if ws1.Color != "#FF00AA" {
		t.Fatalf("expected normalized color #FF00AA, got %q", ws1.Color)
	}

	// 3. Reload from disk should preserve existing workspaces without re-seeding
	reloaded := NewStore(projC)
	if len(reloaded.List()) != 2 {
		t.Fatalf("expected 2 workspaces after reload, got %d", len(reloaded.List()))
	}

	// 4. Update existing workspace by ID (keep existing folder ID, keep existing color when omitted)
	origFolderID := ws1.Folders[0].ID
	updated, err := store.Update(ws1.ID, saveWorkspaceRequest{
		Name:  "seed-project (2)", // self-collision should not increment suffix
		Color: "",
		Folders: []workspaceFolderInput{
			{Path: projB, IsPrimary: true},
			{Path: projC, IsPrimary: false},
		},
	})
	if err != nil {
		t.Fatalf("Update by ID failed: %v", err)
	}
	if updated.ID != ws1.ID || updated.Name != "seed-project (2)" || updated.Color != "#FF00AA" {
		t.Fatalf("unexpected updated metadata: %+v", updated)
	}
	if len(updated.Folders) != 2 || updated.Folders[0].ID != origFolderID {
		t.Fatalf("expected original folder ID %q to be preserved, got %+v", origFolderID, updated.Folders)
	}

	// 5. Update by folder path & upsert when ID does not match
	byPath, err := store.Update(projC, saveWorkspaceRequest{
		Name:    "Renamed Via Path",
		Folders: []workspaceFolderInput{{Path: projB, IsPrimary: true}},
	})
	if err != nil || byPath.ID != ws1.ID || byPath.Name != "Renamed Via Path" {
		t.Fatalf("Update by folder path failed: %+v, err=%v", byPath, err)
	}

	upserted, err := store.Update("non-existent-id", saveWorkspaceRequest{
		Name:    "Upserted",
		Folders: []workspaceFolderInput{{Path: projC, IsPrimary: true}},
	})
	if err != nil || upserted.Name != "Upserted" {
		t.Fatalf("Update upsert failed: %+v, err=%v", upserted, err)
	}
	if _, err := store.Update("   ", saveWorkspaceRequest{}); err == nil {
		t.Fatal("expected error for empty update ID")
	}

	// 6. Remove and ListDismissed
	if store.Remove("   ") {
		t.Fatal("expected false when removing empty target")
	}
	if !store.Remove(upserted.ID) {
		t.Fatal("expected true when removing existing workspace by ID")
	}
	dismissed := store.ListDismissed()
	if len(dismissed) == 0 {
		t.Fatal("expected dismissed list to contain removed workspace ID and paths")
	}

	// Re-creating workspace with projC should clear projC from dismissed list
	_, err = store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: projC, IsPrimary: true}},
	})
	if err != nil {
		t.Fatalf("re-create failed: %v", err)
	}
	for _, d := range store.ListDismissed() {
		if comparableFolderPath(d) == comparableFolderPath(projC) {
			t.Fatalf("expected %q to be removed from dismissed list upon re-create", projC)
		}
	}
}

func TestWorkspaceHTTPHandlers(t *testing.T) {
	homeDir := setupTestEnv(t)
	projA := mkdirTempProject(t, homeDir, "http-proj-a")
	projB := mkdirTempProject(t, homeDir, "http-proj-b")

	store := NewStore("")
	mux := http.NewServeMux()
	store.RegisterRoutes(mux)

	// 1. GET /api/hoxi/workspaces (initially empty)
	req := httptest.NewRequest(http.MethodGet, "/api/hoxi/workspaces", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}

	// 2. POST /api/hoxi/workspaces (valid)
	postBody, _ := json.Marshal(saveWorkspaceRequest{
		Name:    "HTTP Workspace",
		Color:   "#123456",
		Folders: []workspaceFolderInput{{Path: projA, IsPrimary: true}},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/workspaces", bytes.NewReader(postBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created workspaceProfile
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}

	// 3. POST /api/hoxi/workspaces (bad JSON & invalid folders)
	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/workspaces", bytes.NewBufferString("{bad"))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON on POST, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/hoxi/workspaces", bytes.NewBufferString(`{"folders":[]}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty folders on POST, got %d", rec.Code)
	}

	// 4. PUT /api/hoxi/workspaces/{id}
	putBody, _ := json.Marshal(saveWorkspaceRequest{
		Name:    "Updated HTTP Workspace",
		Folders: []workspaceFolderInput{{Path: projB, IsPrimary: true}},
	})
	req = httptest.NewRequest(http.MethodPut, "/api/hoxi/workspaces/"+created.ID, bytes.NewReader(putBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// PUT with invalid JSON
	req = httptest.NewRequest(http.MethodPut, "/api/hoxi/workspaces/"+created.ID, bytes.NewBufferString("invalid"))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid PUT JSON, got %d", rec.Code)
	}

	// PUT without ID in path or body
	req = httptest.NewRequest(http.MethodPut, "/api/hoxi/workspaces", bytes.NewBufferString(`{"folders":[]}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing ID on PUT, got %d", rec.Code)
	}

	// 5. DELETE /api/hoxi/workspaces/{id}
	req = httptest.NewRequest(http.MethodDelete, "/api/hoxi/workspaces/"+created.ID, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var delResp struct {
		OK      bool `json:"ok"`
		Deleted bool `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &delResp); err != nil || !delResp.OK || !delResp.Deleted {
		t.Fatalf("unexpected DELETE response: %+v, err=%v", delResp, err)
	}

	// DELETE via query param ?path=
	req = httptest.NewRequest(http.MethodDelete, "/api/hoxi/workspaces?path="+projB, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE by query param status = %d", rec.Code)
	}

	// 6. Method Not Allowed
	req = httptest.NewRequest(http.MethodOptions, "/api/hoxi/workspaces", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for OPTIONS, got %d", rec.Code)
	}
}
