package runner

import (
	"path/filepath"
	"testing"
)

func TestEditedFilesIgnoreTextDeltasAndTrackCorrections(t *testing.T) {
	t.Parallel()
	svc := NewService("/repo", nil, nil, filepath.Join(t.TempDir(), "sessions"))
	s := svc.store
	if err := s.Put(SessionRecord{ID: "root", ProjectDir: "/repo", WorktreePath: "/repo"}); err != nil {
		t.Fatal(err)
	}
	s.SetActive("root", true)
	s.UpsertTurnInfo("root", MessageInfo{ID: "msg", SessionID: "root"})
	s.UpsertTurnPart("root", MessagePart{ID: "text", MessageID: "msg", Type: "text"})
	edit := func(id, file, status string) {
		s.UpsertTurnPart("root", MessagePart{ID: id, MessageID: "msg", Type: "tool", Tool: "write",
			State: &ToolState{Status: status, Input: map[string]interface{}{"filePath": file}}})
	}
	edit("one", "/repo/a.go", "completed")
	edit("two", "/repo/a.go", "completed")
	if got := svc.sessionEditedFileSet("root"); len(got) != 1 {
		t.Fatalf("initial paths = %v", got)
	}
	revision := s.editMutations.Load()
	for i := 0; i < 100; i++ {
		s.AppendPartDelta("root", "msg", "text", "token")
		s.UpsertTurnInfo("root", MessageInfo{ID: "msg", SessionID: "root"})
		if got := svc.sessionEditedFileSet("root"); len(got) != 1 {
			t.Fatalf("text changed edit set: %v", got)
		}
	}
	if s.editMutations.Load() != revision {
		t.Fatal("text streaming invalidated the edit cache")
	}
	edit("one", "/repo/b.go", "completed")
	if got := svc.sessionEditedFileSet("root"); len(got) != 2 {
		t.Fatalf("correction lost another part's edit: %v", got)
	}
	edit("two", "/repo/a.go", "error")
	if got := svc.sessionEditedFileSet("root"); len(got) != 1 {
		t.Fatalf("corrected status kept an obsolete contribution: %v", got)
	} else if _, ok := got["b.go"]; !ok {
		t.Fatalf("corrected path missing: %v", got)
	}
	if _, _, err := s.Update("root", func(rec *SessionRecord) { rec.Turns = nil }); err != nil {
		t.Fatal(err)
	}
	if got := svc.sessionEditedFileSet("root"); len(got) != 0 {
		t.Fatalf("replaced transcript retained edits: %v", got)
	}
}

func TestEditedFilesObserveGrandchildrenAndDeletedSessions(t *testing.T) {
	t.Parallel()
	svc := NewService("/repo", nil, nil, filepath.Join(t.TempDir(), "sessions"))
	for _, rec := range []SessionRecord{
		{ID: "root", ProjectDir: "/repo", WorktreePath: "/repo"},
		{ID: "child", ParentID: "root", Subagent: true, ProjectDir: "/repo", WorktreePath: "/repo"},
		{ID: "grandchild", ParentID: "child", Subagent: true, ProjectDir: "/repo", WorktreePath: "/repo"},
	} {
		if err := svc.store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	svc.store.UpsertTurnInfo("child", MessageInfo{ID: "child_msg"})
	svc.store.UpsertTurnPart("child", MessagePart{ID: "task", MessageID: "child_msg", Type: "tool", Tool: "task",
		State: &ToolState{Status: "running", Input: map[string]interface{}{"task_id": "grandchild"}}})
	_ = svc.sessionEditedFileSet("root")
	svc.store.UpsertTurnInfo("grandchild", MessageInfo{ID: "grand_msg"})
	svc.store.UpsertTurnPart("grandchild", MessagePart{ID: "edit", MessageID: "grand_msg", Type: "tool", Tool: "edit",
		State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": "/repo/grand.go"}}})
	if _, ok := svc.sessionEditedFileSet("root")["grand.go"]; !ok {
		t.Fatal("grandchild update did not invalidate root file set")
	}
	if err := svc.store.Delete("grandchild"); err != nil {
		t.Fatal(err)
	}
	if got := svc.sessionEditedFileSet("root"); len(got) != 0 {
		t.Fatalf("deleted grandchild retained edits: %v", got)
	}
}

func TestColdEditCacheUsesInitialZeroRevisionWithoutHydratingHistory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seed := NewStore(dir)
	if err := seed.Put(SessionRecord{ID: "bench", ProjectDir: "/repo", Turns: performanceTurns(20)}); err != nil {
		t.Fatal(err)
	}
	svc := NewService("/repo", nil, nil, dir)
	if got := svc.sessionEditedFileSet("bench"); len(got) != 20 {
		t.Fatal("cold file set lost edits")
	}
	entry, cached := svc.editedFiles.Load("bench")
	if !cached || entry.stamp != 0 {
		t.Fatal("initial immutable history was not cached at revision zero")
	}
	if svc.store.records["bench"].turnsLoaded {
		t.Fatal("file set query hydrated the full transcript")
	}
}
