package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func TestManualSessionMemorySurvivesAutomaticSync(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	svc := &Service{store: NewStore(t.TempDir())}
	sid := "ses_audit_manual_memory"
	rec := SessionRecord{
		ID:           sid,
		Runtime:      "claude-code",
		ProjectDir:   project,
		WorktreePath: project,
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_audit", SessionID: sid, Role: "user"},
				Parts: []MessagePart{{ID: "prt_audit", MessageID: "msg_audit", SessionID: sid, Type: "text", Text: "Implement the feature"}},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)

	target := hoxipaths.ProjectSessionMemoryFile(project, sid)
	edited := "# User-edited memory\n- MANUAL_RULE_MARKER_987\n"
	if _, _, err := SaveMemoryDocument(project, "session", sid, edited); err != nil {
		t.Fatal(err)
	}

	// Simulate subsequent turn completion refreshing session memory.
	rec.Turns = append(rec.Turns, MessageTurn{
		Info:  MessageInfo{ID: "msg_audit_2", SessionID: sid, Role: "assistant"},
		Parts: []MessagePart{{ID: "prt_audit_2", MessageID: "msg_audit_2", SessionID: sid, Type: "text", Text: "Completed first pass"}},
	})
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "MANUAL_RULE_MARKER_987") {
		t.Fatalf("expected manual memory to survive automatic turn sync, got:\n%s", string(got))
	}
	if !strings.Contains(string(got), "Completed first pass") {
		t.Fatalf("expected updated automatic turn summary alongside manual notes, got:\n%s", string(got))
	}

	// Also verify in-section manual additions inside the 8-dimension structure survive.
	inSectionEdited := string(got) + "\n## 9. Manual Architecture Notes\n- KEEP_CUSTOM_SECTION_456\n"
	if _, _, err := SaveMemoryDocument(project, "session", sid, inSectionEdited); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)
	got2, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got2), "MANUAL_RULE_MARKER_987") || !strings.Contains(string(got2), "KEEP_CUSTOM_SECTION_456") {
		t.Fatalf("expected both preamble and custom section to survive sync, got:\n%s", string(got2))
	}
}

func TestManualCurrentWorkAndStandardSectionEditsSurviveAutomaticSync(t *testing.T) {
	t.Parallel()
	svc := &Service{store: NewStore(t.TempDir())}
	project := t.TempDir()
	sid := "ses_audit_edit"
	rec := SessionRecord{
		ID:           sid,
		Runtime:      "claude-code",
		ProjectDir:   project,
		WorktreePath: project,
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_a", SessionID: sid, Role: "user"},
				Parts: []MessagePart{{ID: "prt_a", MessageID: "msg_a", SessionID: sid, Type: "text", Text: "Implement a feature"}},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)

	p := hoxipaths.ProjectSessionMemoryFile(project, sid)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, "## 8. Current Work")
	if start < 0 {
		t.Fatalf("missing section 8 in:\n%s", text)
	}
	edited := text[:start] + "## 8. Current Work\n- MANUAL_CURRENT_WORK_MARKER\n"
	if _, _, err := SaveMemoryDocument(project, "session", sid, edited); err != nil {
		t.Fatal(err)
	}

	svc.syncSessionMemoryFile(sid)
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "MANUAL_CURRENT_WORK_MARKER") {
		t.Fatalf("expected manual Current Work edit to survive sync, got:\n%s", string(got))
	}

	// Ensure it also survives a subsequent turn where assistant summary updates.
	rec.Turns = append(rec.Turns, MessageTurn{
		Info:  MessageInfo{ID: "msg_b", SessionID: sid, Role: "assistant"},
		Parts: []MessagePart{{ID: "prt_b", MessageID: "msg_b", SessionID: sid, Type: "text", Text: "Finished implementation"}},
	})
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)
	got2, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got2), "MANUAL_CURRENT_WORK_MARKER") {
		t.Fatalf("expected manual Current Work edit to survive subsequent turn sync, got:\n%s", string(got2))
	}
}

func TestMemorySymlinkCannotEscapeProject(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".hoxi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, ".hoxi", "memory")); err != nil {
		t.Skip(err)
	}
	target := filepath.Join(outside, "project.md")
	if err := os.WriteFile(target, []byte("outside user file"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, saveErr := SaveMemoryDocument(project, "project", "", "overwritten")
	if !errors.Is(saveErr, ErrInvalidMemoryPath) {
		t.Fatalf("expected ErrInvalidMemoryPath for symlinked memory dir, got %v", saveErr)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "outside user file" {
		t.Fatalf("outside file modified via symlink: content=%q err=%v", got, err)
	}

	_, _, _, promoteErr := PromoteSessionMemoryToProject(project, "ses_1", "- User Constraint: never escape project\n")
	if !errors.Is(promoteErr, ErrInvalidMemoryPath) {
		t.Fatalf("expected ErrInvalidMemoryPath on PromoteSessionMemoryToProject with symlinked memory dir, got %v", promoteErr)
	}
	gotAfterPromote, err := os.ReadFile(target)
	if err != nil || string(gotAfterPromote) != "outside user file" {
		t.Fatalf("outside file modified on promote via symlink: content=%q err=%v", gotAfterPromote, err)
	}
}

func TestAutoMemoryRemainsBoundedWithoutManualEdits(t *testing.T) {
	t.Parallel()
	var merged string
	const rounds = 1800
	for i := 0; i < rounds; i++ {
		var auto strings.Builder
		auto.WriteString(autoSessionMemoryHeading + "\n\n## 1. Primary Request and Intent\n")
		for j := max(0, i-47); j <= i; j++ {
			fmt.Fprintf(&auto, "- User Constraint: must preserve auto rule %04d %s\n", j, strings.Repeat("x", 170))
		}
		auto.WriteString("\n## 8. Current Work\n- Addressing feature\n")
		merged = MergeSessionMemoryMarkdown(merged, auto.String())
	}
	retained := strings.Count(merged, "- User Constraint:")
	if retained > maxSessionConstraints || len(merged) > MaxMemoryDocumentBytes {
		t.Fatalf("automatic memory grew unbounded: retained=%d (max %d), bytes=%d (max %d)", retained, maxSessionConstraints, len(merged), MaxMemoryDocumentBytes)
	}
}

func TestManualMemoryDeletionsAndInitialCapSurviveAutomaticSync(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	svc := &Service{store: NewStore(t.TempDir())}
	sid := "ses_audit_delete_and_cap"

	// 1. Massive todowrite + long paths on initial turn must never exceed MaxMemoryDocumentBytes (256 KiB).
	var hugeTodos []interface{}
	for i := 0; i < 600; i++ {
		hugeTodos = append(hugeTodos, map[string]interface{}{
			"content": fmt.Sprintf("task_%04d_%s", i, strings.Repeat("todo_payload_", 40)),
			"status":  "pending",
		})
	}
	rec := SessionRecord{
		ID:           sid,
		Runtime:      "claude-code",
		ProjectDir:   project,
		WorktreePath: project,
		Turns: []MessageTurn{
			{
				Info: MessageInfo{ID: "msg_u1", SessionID: sid, Role: "user"},
				Parts: []MessagePart{{
					ID: "prt_u1", MessageID: "msg_u1", SessionID: sid, Type: "text",
					Text: "Build feature. Note: must keep API v1 stable. Also: never drop user deletions.",
				}},
			},
			{
				Info: MessageInfo{ID: "msg_a1", SessionID: sid, Role: "assistant"},
				Parts: []MessagePart{
					{
						ID: "prt_t1", MessageID: "msg_a1", SessionID: sid, Type: "tool", Tool: "todowrite",
						State: &ToolState{
							Status: "completed",
							Input:  map[string]interface{}{"todos": hugeTodos},
						},
					},
					{
						ID: "prt_a1", MessageID: "msg_a1", SessionID: sid, Type: "text",
						Text: "Created todo list.",
					},
				},
			},
		},
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)

	sessionFile := hoxipaths.ProjectSessionMemoryFile(project, sid)
	initialBytes, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(initialBytes) > MaxMemoryDocumentBytes {
		t.Fatalf("initial session memory size %d exceeds MaxMemoryDocumentBytes (%d)", len(initialBytes), MaxMemoryDocumentBytes)
	}
	if !strings.Contains(string(initialBytes), "- User Constraint: Also: never drop user deletions") {
		t.Fatalf("expected constraint in initial session memory, got:\n%s", string(initialBytes))
	}

	// 2. Manually delete the "- User Constraint: Also: never drop user deletions" line and save; next sync must NOT restore it.
	withoutConstraint := strings.ReplaceAll(string(initialBytes), "- User Constraint: Also: never drop user deletions\n", "")
	if withoutConstraint == string(initialBytes) {
		t.Fatalf("failed to strip target constraint from initial memory:\n%s", string(initialBytes))
	}
	if _, _, err := SaveMemoryDocument(project, "session", sid, withoutConstraint); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)

	afterLineDelete, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(afterLineDelete), "- User Constraint: Also: never drop user deletions") {
		t.Fatalf("manually deleted constraint was restored on sync:\n%s", string(afterLineDelete))
	}

	// 3. Manually clear the entire session memory document; next sync must keep it empty rather than restoring all auto sections.
	if _, _, err := SaveMemoryDocument(project, "session", sid, ""); err != nil {
		t.Fatal(err)
	}
	svc.syncSessionMemoryFile(sid)

	afterClear, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(afterClear)) != "" {
		t.Fatalf("manually cleared session memory was restored on sync:\n%s", string(afterClear))
	}

	// 4. PromoteSessionMemoryToProject must deduplicate by exact normalized line rather than substring match.
	if _, _, err := SaveMemoryDocument(project, "project", "", "# Project Memory\n- 严禁修改公开 API 签名与序列化字段\n"); err != nil {
		t.Fatal(err)
	}
	_, updatedProj, addedCount, err := PromoteSessionMemoryToProject(project, sid, "## 1. Primary Request and Intent\n- User Constraint: 严禁修改公开 API 签名\n- User Constraint: 严禁修改公开 API 签名与序列化字段\n")
	if err != nil {
		t.Fatalf("PromoteSessionMemoryToProject error: %v", err)
	}
	if addedCount != 1 {
		t.Fatalf("expected shorter distinct rule to be added (addedCount=1), got %d:\n%s", addedCount, updatedProj)
	}
	if !strings.Contains(updatedProj, "\n- 严禁修改公开 API 签名\n") {
		t.Fatalf("expected shorter rule to be appended as its own bullet, got:\n%s", updatedProj)
	}
}
