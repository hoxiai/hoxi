package runner

import (
	"path/filepath"
	"testing"
)

func TestBrowserSessionScope(t *testing.T) {
	root := t.TempDir()
	worktree := t.TempDir()
	old := t.TempDir()
	s := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	rec := SessionRecord{ID: "ses_hoxi_browser", Runtime: "opencode", ProjectDir: root, WorktreePath: worktree,
		ExternalID: "ses_engine_browser", OpenCodeIDs: []string{"ses_engine_browser", "ses_old_engine"},
		OpenCodeDirectories: map[string]string{"ses_engine_browser": worktree, "ses_old_engine": old}}
	if err := s.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, dir, want string
		denied        bool
	}{
		{rec.ID, worktree, rec.ID, false},
		{rec.ExternalID, worktree, rec.ID, false},
		{"ses_unadopted", root, "ses_unadopted", false},
		{rec.ID, root, "", true},
		{rec.ExternalID, root, "", true},
		{"ses_old_engine", old, "", true},
		{"ses_old_engine", worktree, "", true},
	} {
		got, err := s.ResolveBrowserSession(tc.id, tc.dir)
		if got != tc.want || (err != nil) != tc.denied {
			t.Errorf("resolve %s in %s: %q, %v", tc.id, tc.dir, got, err)
		}
	}
}
