package sessionstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVisitUsageTurnsCachesSummaryAndRebuildsOnChangeOrCorruption(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	now := time.Now().UnixMilli()

	rec := SessionRecord{
		ID:         "ses_usage_1",
		Runtime:    string(RuntimeAntigravity),
		ProjectDir: "/repo",
		CreatedAt:  now - 5000,
		UpdatedAt:  now,
		Turns: []MessageTurn{
			{
				Info:  MessageInfo{ID: "msg_u1", SessionID: "ses_usage_1", Role: "user", Time: MessageTime{Created: now - 4000}},
				Parts: []MessagePart{{ID: "prt_u1", MessageID: "msg_u1", Type: "text", Text: "hello"}},
			},
			{
				Info: MessageInfo{
					ID: "msg_a1", SessionID: "ses_usage_1", Role: "assistant",
					ProviderID: "google", ModelID: "gemini-4-argon",
					Time:   MessageTime{Created: now - 3000, Completed: now - 1500},
					Tokens: &MessageTokens{Input: 1000, Output: 200, Reasoning: 50},
				},
				Parts: []MessagePart{
					{ID: "prt_t1", MessageID: "msg_a1", Type: "text", Text: strings.Repeat("large text ", 100)},
					{ID: "prt_tool1", MessageID: "msg_a1", Type: "tool", State: &ToolState{Status: "completed", Output: "ok"}},
					{ID: "prt_tool2", MessageID: "msg_a1", Type: "tool", State: &ToolState{Status: "error", Output: "fail"}},
				},
			},
		},
	}
	if err := s1.Put(rec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	usagePath := filepath.Join(dir, rec.ID, UsageSummaryFile)
	if _, err := os.Stat(usagePath); err != nil {
		t.Fatalf("expected %s written on rewriteTurns: %v", UsageSummaryFile, err)
	}

	// Remove the sidecar to force cold scan of turns.jsonl on a fresh Store.
	if err := os.Remove(usagePath); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(dir)
	var got []TurnUsageSummary
	if !s2.VisitUsageTurns(rec.ID, func(_ SessionRecord, turns []TurnUsageSummary) {
		got = append([]TurnUsageSummary(nil), turns...)
	}) {
		t.Fatal("VisitUsageTurns returned false")
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 assistant turn: %+v", len(got), got)
	}
	if got[0].ID != "msg_a1" || got[0].Tokens == nil || got[0].Tokens.Input != 1000 || got[0].Tokens.Output != 200 {
		t.Fatalf("unexpected usage summary: %+v", got[0])
	}
	if got[0].ToolCallsTotal != 2 || got[0].ToolCallsFailed != 1 {
		t.Fatalf("tools = %d/%d, want 2/1", got[0].ToolCallsTotal, got[0].ToolCallsFailed)
	}
	if _, err := os.Stat(usagePath); err != nil {
		t.Fatalf("expected %s recreated after cold scan: %v", UsageSummaryFile, err)
	}
	if s2.IsTurnsLoaded(rec.ID) {
		t.Fatal("VisitUsageTurns must not hydrate full transcript into LRU cache")
	}

	// Corrupt the sidecar; a fresh store must back it up and rebuild cleanly.
	if err := os.WriteFile(usagePath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	s3 := NewStore(dir)
	got = nil
	if !s3.VisitUsageTurns(rec.ID, func(_ SessionRecord, turns []TurnUsageSummary) {
		got = append([]TurnUsageSummary(nil), turns...)
	}) {
		t.Fatal("VisitUsageTurns after corrupt sidecar failed")
	}
	if len(got) != 1 || got[0].ToolCallsTotal != 2 || got[0].ToolCallsFailed != 1 {
		t.Fatalf("unexpected rebuilt summary: %+v", got)
	}
	entries, err := os.ReadDir(filepath.Join(dir, rec.ID))
	if err != nil {
		t.Fatal(err)
	}
	backedUp := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), UsageSummaryFile+".corrupt-") {
			backedUp = true
			break
		}
	}
	if !backedUp {
		t.Fatalf("expected corrupt %s backup file in %v", UsageSummaryFile, entries)
	}
}

func TestVisitUsageTurnsPreservesImportedAndUpgradesV1WithoutCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	now := time.Now().UnixMilli()
	rec := SessionRecord{
		ID:         "ses_usage_imp",
		Runtime:    string(RuntimeNative),
		ProjectDir: "/repo",
		CreatedAt:  now - 5000,
		UpdatedAt:  now,
		Turns: []MessageTurn{
			{
				Info: MessageInfo{
					ID: "msg_old_imp", SessionID: "ses_usage_imp", Role: "assistant",
					Imported: true,
					Time:     MessageTime{Created: now - 3000, Completed: now - 2500},
					Tokens:   &MessageTokens{Input: 9000, Output: 500},
				},
			},
			{
				Info: MessageInfo{
					ID: "msg_a_new", SessionID: "ses_usage_imp", Role: "assistant",
					Time:   MessageTime{Created: now - 1000, Completed: now - 500},
					Tokens: &MessageTokens{Input: 120, Output: 30},
				},
			},
		},
	}
	if err := s1.Put(rec); err != nil {
		t.Fatal(err)
	}

	// Simulate a legacy v1 turns.usage.json lacking the Imported field.
	usagePath := filepath.Join(dir, rec.ID, UsageSummaryFile)
	turnsPath := filepath.Join(dir, rec.ID, sessionTurnsFile)
	turnsInfo, err := os.Stat(turnsPath)
	if err != nil {
		t.Fatal(err)
	}
	v1Raw := []byte(`{"version":1,"size":` + stringInt64(turnsInfo.Size()) + `,"modTime":` + stringInt64(turnsInfo.ModTime().UnixNano()) + `,"turns":[{"id":"msg_old_imp","role":"assistant"}]}`)
	if err := os.WriteFile(usagePath, v1Raw, 0o600); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore(dir)
	var got []TurnUsageSummary
	if !s2.VisitUsageTurns(rec.ID, func(_ SessionRecord, turns []TurnUsageSummary) {
		got = append([]TurnUsageSummary(nil), turns...)
	}) {
		t.Fatal("VisitUsageTurns returned false")
	}
	if len(got) != 2 || !got[0].Imported || got[1].Imported {
		t.Fatalf("expected [imported=true, imported=false] after v1 upgrade, got %+v", got)
	}
	entries, err := os.ReadDir(filepath.Join(dir, rec.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), UsageSummaryFile+".corrupt-") {
			t.Fatalf("v1 sidecar upgrade must not create corrupt backup: %s", e.Name())
		}
	}
}

func stringInt64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
