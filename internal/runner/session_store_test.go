package runner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRunnerMultiRuntimeMemoryInjectionAndSync(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	storeDir := t.TempDir()
	svc := NewService(workDir, nil, nil, storeDir)

	rec := SessionRecord{
		ID:           "ses_hoxi_mem_1",
		Runtime:      string(RuntimeClaudeCode),
		ProjectDir:   workDir,
		WorktreePath: workDir,
		SandboxMode:  "local",
		ModelID:      "claude-sonnet-4-6",
		Branch:       "main",
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_hoxi_mem_1", Role: "user"},
				Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_hoxi_mem_1", MessageID: "msg_u1", Type: "text", Text: "Optimize runner memory sync"}},
			},
			{
				Info: MessageInfo{ID: "msg_a1", SessionID: "ses_hoxi_mem_1", Role: "assistant"},
				Parts: []MessagePart{
					{
						ID: "prt_t1", SessionID: "ses_hoxi_mem_1", MessageID: "msg_a1", Type: "tool", Tool: "edit",
						State: &ToolState{Status: "completed", Input: map[string]interface{}{"filePath": "internal/runner/memory.go"}},
					},
					{
						ID: "prt_txt1", SessionID: "ses_hoxi_mem_1", MessageID: "msg_a1", Type: "text", Text: "Implemented 8-dimension memory sync.",
					},
				},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(workDir, ".hoxi", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "project.md"), []byte("# Project\n- Real project rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc.syncSessionMemoryFile(rec.ID)

	sessMemPath := filepath.Join(workDir, ".hoxi", "memory", "sessions", rec.ID+".md")
	rawSess, err := os.ReadFile(sessMemPath)
	if err != nil {
		t.Fatalf("expected session memory file created: %v", err)
	}
	if !strings.Contains(string(rawSess), "## 1. Primary Request and Intent") || !strings.Contains(string(rawSess), "internal/runner/memory.go") {
		t.Fatalf("unexpected session memory content: %s", string(rawSess))
	}
	if _, err := os.Stat(filepath.Join(workDir, ".opencode")); !os.IsNotExist(err) {
		t.Fatalf("memory sync must never create .opencode, stat err = %v", err)
	}

	injected := runtimeMemoryContext(rec)
	if !strings.Contains(injected, "<project_memory>") || !strings.Contains(injected, "<session_handoff_memory>") {
		t.Fatalf("expected project and session memory blocks, got: %s", injected)
	}
	if strings.Contains(injected, "Updated:") {
		t.Fatalf("expected volatile Updated timestamp stripped from injected context: %s", injected)
	}

	// Once ExternalID is set and ContextPending is false, native resume takes over without re-injecting
	rec.ExternalID = "claude-ext-1"
	rec.ContextPending = false
	if got := runtimeMemoryContext(rec); got != "" {
		t.Fatalf("expected empty memory injection on native resume turn, got: %s", got)
	}
}

func TestSyncSessionMemoryNeverSeedsPlaceholderProjectMemory(t *testing.T) {
	t.Parallel()
	projDir := t.TempDir()
	workDir := t.TempDir()
	svc := NewService(projDir, nil, nil, t.TempDir())
	rec := SessionRecord{
		ID:           "ses_hoxi_mem_seed",
		Runtime:      string(RuntimeClaudeCode),
		ProjectDir:   projDir,
		WorktreePath: workDir,
		Turns: []MessageTurn{{
			Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_hoxi_mem_seed", Role: "user"},
			Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_hoxi_mem_seed", MessageID: "msg_u1", Type: "text", Text: "hi"}},
		}},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(rec.ID)
	projFile := filepath.Join(projDir, ".hoxi", "memory", "project.md")
	if _, err := os.Stat(projFile); !os.IsNotExist(err) {
		t.Fatalf("project.md must not be seeded when the project has none, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(projDir, ".opencode")); !os.IsNotExist(err) {
		t.Fatalf("memory sync must never create .opencode, stat err = %v", err)
	}
	if got := runtimeMemoryContext(rec); strings.Contains(got, "<project_memory>") {
		t.Fatalf("expected no project memory block, got: %s", got)
	}

	// A pre-migration .opencode/memory/project.md is still honored and copied
	// forward once so the user does not lose long-lived project rules.
	legacyProj := filepath.Join(projDir, ".opencode", "memory", "project.md")
	if err := os.MkdirAll(filepath.Dir(legacyProj), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyProj, []byte("# Project\n- Real rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(rec.ID)
	raw, err := os.ReadFile(projFile)
	if err != nil || !strings.Contains(string(raw), "Real rule") {
		t.Fatalf("expected legacy project.md migrated to .hoxi/memory, got %q, %v", raw, err)
	}
}

func TestRuntimeMemoryContextReadsLegacyOpencodeMemory(t *testing.T) {
	t.Parallel()
	projDir := t.TempDir()
	legacySessDir := filepath.Join(projDir, ".opencode", "memory", "sessions")
	if err := os.MkdirAll(legacySessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySessDir, "ses_legacy.md"), []byte("## 8. Next Steps\n- Resume legacy work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := SessionRecord{ID: "ses_legacy", ProjectDir: projDir, WorktreePath: projDir}
	got := runtimeMemoryContext(rec)
	if !strings.Contains(got, "Resume legacy work") {
		t.Fatalf("expected legacy session memory to be injected, got: %s", got)
	}
}

func TestProjectMemoryRootForIsolatedSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	wt := filepath.Join(root, ".hoxi", "worktrees", "ses_wt")
	svc := NewService(root, nil, nil, t.TempDir())
	rec := SessionRecord{ID: "ses_wt", ProjectDir: root, WorktreePath: wt, SandboxMode: "worktree"}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Turns = []MessageTurn{{
		Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_wt", Role: "user"},
		Parts: []MessagePart{{ID: "prt_u1", SessionID: "ses_wt", MessageID: "msg_u1", Type: "text", Text: "hello"}},
	}}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(rec.ID)
	if _, err := os.Stat(filepath.Join(root, ".hoxi", "memory", "sessions", "ses_wt.md")); err != nil {
		t.Fatalf("isolated session memory must live at the project root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".hoxi", "memory")); !os.IsNotExist(err) {
		t.Fatalf("worktree must not hold its own memory copy, stat err = %v", err)
	}
}

func TestDeleteConversationNotifiesRootAndSubagentChildren(t *testing.T) {
	repo := t.TempDir()
	svc := NewService(repo, nil, nil, filepath.Join(t.TempDir(), "sessions"))
	var deleted []string
	svc.SetSessionDeletedHook(func(id string) {
		if _, ok := svc.Store().Meta(id); ok {
			t.Errorf("hook for %s ran before its transcript was deleted", id)
		}
		deleted = append(deleted, id)
	})
	for _, rec := range []SessionRecord{
		{ID: "ses_cld_root", Runtime: string(RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo},
		{ID: "ses_cld_child", Runtime: string(RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo, ParentID: "ses_cld_root", Subagent: true},
		{ID: "ses_cld_other", Runtime: string(RuntimeClaudeCode), ProjectDir: repo, WorktreePath: repo},
	} {
		if err := svc.Store().Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteConversation(context.Background(), "ses_cld_root"); err != nil {
		t.Fatal(err)
	}
	sort.Strings(deleted)
	if strings.Join(deleted, ",") != "ses_cld_child,ses_cld_root" {
		t.Fatalf("hook calls = %v, want root and its subagent child", deleted)
	}
	if _, ok := svc.Store().Meta("ses_cld_other"); !ok {
		t.Fatal("unrelated sessions must survive")
	}
}
