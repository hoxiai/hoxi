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

func listProjectIDs(store *Store, projectID, directory string) []string {
	infos := store.ListProject(projectID, directory)
	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestStoreListProjectUsesStableProjectID(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")

	store := NewStore(t.TempDir())
	for _, rec := range []SessionRecord{
		{ID: "ses_first", ProjectID: "prj_first", ProjectDir: first, WorktreePath: first},
		{ID: "ses_second", ProjectID: "prj_second", ProjectDir: second, WorktreePath: second},
		{ID: "ses_legacy", ProjectDir: first, WorktreePath: first},
	} {
		if err := store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}

	got := listProjectIDs(store, "prj_first", first)
	want := []string{"ses_first", "ses_legacy"}
	sort.Strings(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ListProject(prj_first) = %v, want %v", got, want)
	}
	// A project ID wins over the requested directory: another project's
	// sessions never leak in through a path match.
	if ids := listProjectIDs(store, "prj_first", second); len(ids) != 1 || ids[0] != "ses_first" {
		t.Fatalf("ListProject(prj_first, second) = %v", ids)
	}
}

type fixedProjectResolver struct{ id string }

func (r fixedProjectResolver) ProjectIDForDirectory(string) (string, bool) {
	return r.id, r.id != ""
}

func (r fixedProjectResolver) EnsureProjectForDirectory(string) (string, error) {
	return r.id, nil
}

func (r fixedProjectResolver) ProjectRoots(string) []string {
	return nil
}

func TestSetProjectResolverBackfillsLegacySessionsOnce(t *testing.T) {
	workDir := t.TempDir()
	svc := NewService(workDir, nil, nil, t.TempDir())
	for _, rec := range []SessionRecord{
		{ID: "ses_legacy_a", ProjectDir: workDir, WorktreePath: workDir, CreatedAt: 11, UpdatedAt: 111},
		{ID: "ses_legacy_b", ProjectDir: workDir, WorktreePath: workDir, CreatedAt: 22, UpdatedAt: 222},
		{ID: "ses_owned", ProjectID: "prj_owned", ProjectDir: workDir, WorktreePath: workDir, CreatedAt: 33, UpdatedAt: 333},
	} {
		if err := svc.store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}

	svc.SetProjectResolver(fixedProjectResolver{id: "prj_backfilled"})

	for id, updatedAt := range map[string]int64{"ses_legacy_a": 111, "ses_legacy_b": 222} {
		rec, ok := svc.store.Get(id)
		if !ok || rec.ProjectID != "prj_backfilled" {
			t.Fatalf("%s project = %q, %v", id, rec.ProjectID, ok)
		}
		if rec.UpdatedAt != updatedAt {
			t.Fatalf("%s UpdatedAt = %d, want %d (backfill must not look recent)", id, rec.UpdatedAt, updatedAt)
		}
	}
	rec, ok := svc.store.Get("ses_owned")
	if !ok || rec.ProjectID != "prj_owned" || rec.UpdatedAt != 333 {
		t.Fatalf("existing record changed: %+v, %v", rec, ok)
	}
}
