package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAllProjectRootsEnumeratesProjectsOncePerFolder(t *testing.T) {
	homeDir := setupTestEnv(t)
	first := mkdirTempProject(t, homeDir, "first")
	second := mkdirTempProject(t, homeDir, "second")

	store := NewStore("")
	if _, err := store.EnsureProjectForDirectory(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureProjectForDirectory(second); err != nil {
		t.Fatal(err)
	}
	// Re-resolving an existing project must not duplicate its root.
	if _, err := store.EnsureProjectForDirectory(first); err != nil {
		t.Fatal(err)
	}

	roots := store.AllProjectRoots()
	if len(roots) != 2 || !containsString(roots, first) || !containsString(roots, second) {
		t.Fatalf("AllProjectRoots = %v, want [%s %s]", roots, first, second)
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestEnsureProjectForDirectoryKeepsStableIDForWorktrees(t *testing.T) {
	homeDir := setupTestEnv(t)
	root := mkdirTempProject(t, homeDir, "project")
	worktree := filepath.Join(root, ".hoxi", "worktrees", "ses_worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	store := NewStore("")
	projectID, err := store.EnsureProjectForDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.EnsureProjectForDirectory(root)
	if err != nil || again != projectID {
		t.Fatalf("EnsureProjectForDirectory is not stable: %q, %v", again, err)
	}
	viaWorktree, ok := store.ProjectIDForDirectory(worktree)
	if !ok || viaWorktree != projectID {
		t.Fatalf("worktree resolved to %q, %v; want %q", viaWorktree, ok, projectID)
	}

	// The ID survives a restart and the auto-created project stays hidden.
	reopened := NewStore("")
	if got, ok := reopened.ProjectIDForDirectory(root); !ok || got != projectID {
		t.Fatalf("persisted project = %q, %v; want %q", got, ok, projectID)
	}
	found := false
	for _, profile := range reopened.List() {
		if profile.ID == projectID {
			found = true
			if !profile.Hidden {
				t.Fatal("auto-created project must be hidden from the sidebar")
			}
		}
	}
	if !found {
		t.Fatal("auto-created project was not persisted")
	}
}

func TestNestedProjectKeepsItsOwnIdentity(t *testing.T) {
	homeDir := setupTestEnv(t)
	outer := mkdirTempProject(t, homeDir, "outer")
	inner := mkdirTempProject(t, outer, "inner")

	store := NewStore("")
	outerID, err := store.EnsureProjectForDirectory(outer)
	if err != nil {
		t.Fatal(err)
	}
	innerID, err := store.EnsureProjectForDirectory(inner)
	if err != nil {
		t.Fatal(err)
	}
	if innerID == outerID {
		t.Fatal("nested directory must not reuse the outer project ID")
	}
	if got, _ := store.ProjectIDForDirectory(outer); got != outerID {
		t.Fatalf("outer project = %q, want %q", got, outerID)
	}
	if got, _ := store.ProjectIDForDirectory(inner); got != innerID {
		t.Fatalf("inner project = %q, want %q", got, innerID)
	}
}

func TestEnsureProjectFromWorktreeUsesTheRoot(t *testing.T) {
	homeDir := setupTestEnv(t)
	root := mkdirTempProject(t, homeDir, "project")
	worktree := filepath.Join(root, ".hoxi", "worktrees", "ses_orphan")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	store := NewStore("")
	worktreeID, err := store.EnsureProjectForDirectory(worktree)
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := store.EnsureProjectForDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if worktreeID != rootID {
		t.Fatalf("worktree project = %q, root project = %q", worktreeID, rootID)
	}
	roots := store.ProjectRoots(rootID)
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("project roots = %v, want [%s]", roots, root)
	}
}

func TestSavingAutoProjectPromotesItWithoutChangingItsID(t *testing.T) {
	homeDir := setupTestEnv(t)
	root := mkdirTempProject(t, homeDir, "project")

	store := NewStore("")
	autoID, err := store.EnsureProjectForDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: root, IsPrimary: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != autoID {
		t.Fatalf("saved project ID = %q, want auto ID %q", saved.ID, autoID)
	}
	if saved.Hidden {
		t.Fatal("saved workspace must be visible")
	}
	if got, _ := store.ProjectIDForDirectory(root); got != autoID {
		t.Fatalf("project ID changed after promotion: %q", got)
	}
}

func TestExplicitFolderReplaceRelinksAndKeepsFolderID(t *testing.T) {
	homeDir := setupTestEnv(t)
	oldRoot := mkdirTempProject(t, homeDir, "old-project")
	newRoot := mkdirTempProject(t, homeDir, "new-project")

	store := NewStore("")
	projectID, err := store.EnsureProjectForDirectory(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	folderID := store.List()[0].Folders[0].ID
	var gotProjectID, gotOld, gotNew string
	store.SetFolderRelinkHandler(func(projectID, oldPath, newPath string) error {
		gotProjectID, gotOld, gotNew = projectID, oldPath, newPath
		return nil
	})
	if err := os.RemoveAll(oldRoot); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Update(projectID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{ID: folderID, Path: newRoot, IsPrimary: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotProjectID != projectID || gotOld != oldRoot || gotNew != newRoot {
		t.Fatalf("relink = (%q, %q, %q), want (%q, %q, %q)",
			gotProjectID, gotOld, gotNew, projectID, oldRoot, newRoot)
	}
	if len(saved.Folders) != 1 || saved.Folders[0].ID != folderID {
		t.Fatalf("explicit replace must keep the folder ID %q: %+v", folderID, saved.Folders)
	}
	if got, ok := store.ProjectIDForDirectory(newRoot); !ok || got != projectID {
		t.Fatalf("new root project = %q, %v; want %q", got, ok, projectID)
	}
}

func TestFolderPathChangeWithoutIDNeverRelinks(t *testing.T) {
	homeDir := setupTestEnv(t)
	oldRoot := mkdirTempProject(t, homeDir, "old-project")
	newRoot := mkdirTempProject(t, homeDir, "new-project")

	store := NewStore("")
	ws, err := store.Create(saveWorkspaceRequest{Folders: []workspaceFolderInput{{Path: oldRoot, IsPrimary: true}}})
	if err != nil {
		t.Fatal(err)
	}
	relinked := false
	store.SetFolderRelinkHandler(func(_, _, _ string) error { relinked = true; return nil })
	if err := os.RemoveAll(oldRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(ws.ID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: newRoot, IsPrimary: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if relinked {
		t.Fatal("a path change without a folder ID must not relink sessions")
	}
}

func TestExplicitReplaceKeepsSessionsWhenOldFolderStillExists(t *testing.T) {
	homeDir := setupTestEnv(t)
	a := mkdirTempProject(t, homeDir, "proj-a")
	b := mkdirTempProject(t, homeDir, "proj-b")

	store := NewStore("")
	ws, err := store.Create(saveWorkspaceRequest{Folders: []workspaceFolderInput{{Path: a, IsPrimary: true}}})
	if err != nil {
		t.Fatal(err)
	}
	relinked := false
	store.SetFolderRelinkHandler(func(_, _, _ string) error { relinked = true; return nil })
	if _, err := store.Update(ws.ID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{ID: ws.Folders[0].ID, Path: b, IsPrimary: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if relinked {
		t.Fatal("an existing old folder must not have its sessions moved")
	}
}

// Regression: re-saving a workspace whose middle folder was deleted must not
// relink that folder's sessions into an unrelated sibling folder.
func TestDeletedMiddleFolderIsNotRelinkedToSibling(t *testing.T) {
	homeDir := setupTestEnv(t)
	a := mkdirTempProject(t, homeDir, "proj-a")
	b := mkdirTempProject(t, homeDir, "proj-b")
	c := mkdirTempProject(t, homeDir, "proj-c")

	store := NewStore("")
	ws, err := store.Create(saveWorkspaceRequest{
		Name: "multi",
		Folders: []workspaceFolderInput{
			{Path: a, IsPrimary: true}, {Path: b}, {Path: c},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var relinks [][2]string
	store.SetFolderRelinkHandler(func(_, oldPath, newPath string) error {
		relinks = append(relinks, [2]string{oldPath, newPath})
		return nil
	})
	if err := os.RemoveAll(b); err != nil {
		t.Fatal(err)
	}
	// User only renames the workspace; the UI still lists all three folders.
	saved, err := store.Update(ws.ID, saveWorkspaceRequest{
		Name: "multi-renamed",
		Folders: []workspaceFolderInput{
			{Path: a, IsPrimary: true}, {Path: b}, {Path: c},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range relinks {
		t.Errorf("unexpected relink %s -> %s", r[0], r[1])
	}
	if len(saved.Folders) != 3 || saved.Folders[1].Path != b || saved.Folders[1].ID != ws.Folders[1].ID {
		t.Fatalf("missing folder must be kept with its ID: %+v", saved.Folders)
	}
}

func TestDeletedPrimaryFolderIsNotRelinkedToSibling(t *testing.T) {
	homeDir := setupTestEnv(t)
	a := mkdirTempProject(t, homeDir, "proj-a")
	b := mkdirTempProject(t, homeDir, "proj-b")

	store := NewStore("")
	ws, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: a, IsPrimary: true}, {Path: b}},
	})
	if err != nil {
		t.Fatal(err)
	}
	relinked := false
	store.SetFolderRelinkHandler(func(_, _, _ string) error { relinked = true; return nil })
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	// Re-save with the missing primary still listed, then with it removed:
	// neither may move the primary's sessions into b.
	kept, err := store.Update(ws.ID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: a, IsPrimary: true}, {Path: b}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Folders) != 2 || kept.Folders[0].Path != a {
		t.Fatalf("missing primary must be kept: %+v", kept.Folders)
	}
	dropped, err := store.Update(ws.ID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: b, IsPrimary: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if relinked {
		t.Fatal("deleting the primary folder must not relink its sessions")
	}
	if len(dropped.Folders) != 1 || dropped.Folders[0].ID != ws.Folders[1].ID {
		t.Fatalf("surviving folder must keep its ID: %+v", dropped.Folders)
	}
}

func TestSaveAbsorbsHiddenProjectsForEveryFolder(t *testing.T) {
	homeDir := setupTestEnv(t)
	a := mkdirTempProject(t, homeDir, "proj-a")
	b := mkdirTempProject(t, homeDir, "proj-b")
	c := mkdirTempProject(t, homeDir, "proj-c")

	store := NewStore("")
	hiddenB, err := store.EnsureProjectForDirectory(b)
	if err != nil {
		t.Fatal(err)
	}
	hiddenC, err := store.EnsureProjectForDirectory(c)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: a, IsPrimary: true}, {Path: b}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID != hiddenB {
		t.Fatalf("hidden owner of a non-primary folder must be promoted: got %q want %q", ws.ID, hiddenB)
	}
	updated, err := store.Update(ws.ID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: a, IsPrimary: true}, {Path: b}, {Path: c}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := store.CanonicalProjectID(hiddenC); !ok || got != updated.ID {
		t.Fatalf("absorbed hidden ID resolves to %q, %v; want %q", got, ok, updated.ID)
	}
	owners := 0
	for _, profile := range store.List() {
		for _, folder := range profile.Folders {
			if folder.Path == c {
				owners++
			}
		}
	}
	if owners != 1 {
		t.Fatalf("folder c owned by %d projects, want 1", owners)
	}
}

func TestSaveRejectsFolderOfAnotherVisibleWorkspace(t *testing.T) {
	homeDir := setupTestEnv(t)
	a := mkdirTempProject(t, homeDir, "proj-a")
	b := mkdirTempProject(t, homeDir, "proj-b")

	store := NewStore("")
	if _, err := store.Create(saveWorkspaceRequest{Folders: []workspaceFolderInput{{Path: a, IsPrimary: true}}}); err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(saveWorkspaceRequest{Folders: []workspaceFolderInput{{Path: b, IsPrimary: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: b, IsPrimary: true}, {Path: a}},
	}); err == nil {
		t.Fatal("creating a workspace with a folder owned by another workspace must fail")
	}
	if _, err := store.Update(other.ID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: b, IsPrimary: true}, {Path: a}},
	}); err == nil {
		t.Fatal("adding a folder owned by another workspace must fail")
	}
}

func TestReAddingRemovedFolderReusesProjectID(t *testing.T) {
	homeDir := setupTestEnv(t)
	root := mkdirTempProject(t, homeDir, "project")

	store := NewStore("")
	ws, err := store.Create(saveWorkspaceRequest{Folders: []workspaceFolderInput{{Path: root, IsPrimary: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := store.Remove(ws.ID); !removed || err != nil {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	// Survives a restart through the persisted tombstone.
	reopened := NewStore("")
	again, err := reopened.Create(saveWorkspaceRequest{Folders: []workspaceFolderInput{{Path: root, IsPrimary: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != ws.ID {
		t.Fatalf("re-added project ID = %q, want %q", again.ID, ws.ID)
	}

	if _, err := reopened.Remove(again.ID); err != nil {
		t.Fatal(err)
	}
	ensured, err := reopened.EnsureProjectForDirectory(root)
	if err != nil || ensured != ws.ID {
		t.Fatalf("session registration after removal = %q, %v; want %q", ensured, err, ws.ID)
	}
}

func TestEnsureProjectRejectsHomeAndRoot(t *testing.T) {
	homeDir := setupTestEnv(t)
	store := NewStore("")
	for _, dir := range []string{homeDir, "/"} {
		if id, err := store.EnsureProjectForDirectory(dir); err == nil {
			t.Fatalf("EnsureProjectForDirectory(%q) = %q, want error", dir, id)
		}
	}
	if len(store.List()) != 0 {
		t.Fatalf("no project may be registered: %+v", store.List())
	}
}

func TestResolveProjectDoesNotRegister(t *testing.T) {
	homeDir := setupTestEnv(t)
	root := mkdirTempProject(t, homeDir, "project")
	store := NewStore("")
	mux := http.NewServeMux()
	store.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/projects/resolve?directory="+root, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ProjectID string `json:"projectID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.ProjectID != "" {
		t.Fatalf("unregistered directory resolved to %q, %v", body.ProjectID, err)
	}
	if len(store.List()) != 0 {
		t.Fatal("GET resolve must not register a project")
	}

	id, err := store.EnsureProjectForDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hoxi/projects/resolve?directory="+root, nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.ProjectID != id {
		t.Fatalf("registered directory resolved to %q, %v; want %q", body.ProjectID, err, id)
	}
}

func TestCreatingNestedWorkspaceKeepsParentSeparate(t *testing.T) {
	homeDir := setupTestEnv(t)
	parent := mkdirTempProject(t, homeDir, "parent")
	child := mkdirTempProject(t, parent, "client")

	store := NewStore("")
	parentID, err := store.EnsureProjectForDirectory(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: parent, IsPrimary: true}},
	}); err != nil {
		t.Fatal(err)
	}
	childProfile, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: child, IsPrimary: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if childProfile.ID == parentID {
		t.Fatal("nested workspace reused the parent project ID")
	}
	if got, _ := store.ProjectIDForDirectory(parent); got != parentID {
		t.Fatalf("parent project = %q, want %q", got, parentID)
	}
	if got, _ := store.ProjectIDForDirectory(child); got != childProfile.ID {
		t.Fatalf("child project = %q, want %q", got, childProfile.ID)
	}
}

func TestFolderCannotBelongToTwoVisibleWorkspaces(t *testing.T) {
	homeDir := setupTestEnv(t)
	parent := mkdirTempProject(t, homeDir, "parent")
	child := mkdirTempProject(t, parent, "client")

	store := NewStore("")
	if _, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{
			{Path: parent, IsPrimary: true},
			{Path: child, IsPrimary: false},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: child, IsPrimary: true}},
	}); err == nil {
		t.Fatal("expected the child folder to be rejected while the parent owns it")
	}
}

func TestLegacyDuplicateFolderPrefersChildWorkspace(t *testing.T) {
	homeDir := setupTestEnv(t)
	parent := mkdirTempProject(t, homeDir, "parent")
	child := mkdirTempProject(t, parent, "client")

	store := NewStore("")
	store.workspaces = []workspaceProfile{
		{
			ID: "parent", Name: "parent",
			Folders: []workspaceFolder{
				{ID: "p1", Path: parent, IsPrimary: true},
				{ID: "p2", Path: child, IsPrimary: false},
			},
		},
		{
			ID: "child", Name: "child",
			Folders: []workspaceFolder{{ID: "c1", Path: child, IsPrimary: true}},
		},
	}
	if got, _ := store.ProjectIDForDirectory(child); got != "child" {
		t.Fatalf("child directory resolved to %q, want its own workspace", got)
	}
	if got, _ := store.ProjectIDForDirectory(parent); got != "parent" {
		t.Fatalf("parent directory resolved to %q, want parent", got)
	}
}
