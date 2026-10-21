package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestFolderMoveTriggersRelinkAfterProfileUpdate(t *testing.T) {
	homeDir := setupTestEnv(t)
	oldRoot := mkdirTempProject(t, homeDir, "old-project")
	newRoot := mkdirTempProject(t, homeDir, "new-project")

	store := NewStore("")
	projectID, err := store.EnsureProjectForDirectory(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	var gotProjectID, gotOld, gotNew string
	store.SetFolderRelinkHandler(func(projectID, oldPath, newPath string) error {
		gotProjectID, gotOld, gotNew = projectID, oldPath, newPath
		return nil
	})
	if err := os.RemoveAll(oldRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(projectID, saveWorkspaceRequest{
		Folders: []workspaceFolderInput{{Path: newRoot, IsPrimary: true}},
	}); err != nil {
		t.Fatal(err)
	}
	if gotProjectID != projectID || gotOld != oldRoot || gotNew != newRoot {
		t.Fatalf("relink = (%q, %q, %q), want (%q, %q, %q)",
			gotProjectID, gotOld, gotNew, projectID, oldRoot, newRoot)
	}
	if got, ok := store.ProjectIDForDirectory(newRoot); !ok || got != projectID {
		t.Fatalf("new root project = %q, %v; want %q", got, ok, projectID)
	}
}
