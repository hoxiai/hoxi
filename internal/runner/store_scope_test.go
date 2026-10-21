package runner

import (
	"path/filepath"
	"sort"
	"testing"
)

func listIDs(store *Store, directory string) []string {
	infos := store.List(directory)
	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
	}
	sort.Strings(ids)
	return ids
}

func assertListIDs(t *testing.T, store *Store, directory string, want ...string) {
	t.Helper()
	got := listIDs(store, directory)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("List(%q) = %v, want %v", directory, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List(%q) = %v, want %v", directory, got, want)
		}
	}
}

func TestStoreListScopesSessionsToTheirOwnWorkspace(t *testing.T) {
	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	inner := filepath.Join(outer, "inner")
	sibling := filepath.Join(base, "sibling")

	store := NewStore(t.TempDir())
	records := []SessionRecord{
		{ID: "ses_outer", ProjectDir: outer, WorktreePath: outer},
		{ID: "ses_inner", ProjectDir: inner, WorktreePath: inner},
		{ID: "ses_sibling", ProjectDir: sibling, WorktreePath: sibling},
		{ID: "ses_outer_wt", ProjectDir: outer, WorktreePath: filepath.Join(outer, ".hoxi", "worktrees", "ses_outer_wt")},
	}
	for _, rec := range records {
		if err := store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}

	assertListIDs(t, store, outer, "ses_outer", "ses_outer_wt")
	assertListIDs(t, store, inner, "ses_inner")
	assertListIDs(t, store, sibling, "ses_sibling")
	assertListIDs(t, store, "", "ses_outer", "ses_inner", "ses_sibling", "ses_outer_wt")
}

func TestStoreListIncludesWorktreeSessionForItsWorktreeDirectory(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	worktree := filepath.Join(project, ".hoxi", "worktrees", "ses_isolated")

	store := NewStore(t.TempDir())
	if err := store.Put(SessionRecord{
		ID: "ses_isolated", ProjectDir: project, WorktreePath: worktree,
	}); err != nil {
		t.Fatal(err)
	}

	assertListIDs(t, store, worktree, "ses_isolated")
}
