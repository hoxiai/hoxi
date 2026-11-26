package sessionstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestColdCleanupAndIndexesPreserveLargeTurn(t *testing.T) {
	root := t.TempDir()
	const id = "ses_large"
	const textSize = 64 << 20
	turns := []MessageTurn{
		{Info: MessageInfo{ID: "first", Role: "user"}, Parts: []MessagePart{
			{ID: "large", MessageID: "first", Type: "text", Text: strings.Repeat("a", textSize)},
		}},
		{Info: MessageInfo{ID: "last", Role: "assistant", Tokens: &MessageTokens{Input: 123}}},
	}
	seed := NewStore(root)
	if err := seed.Put(SessionRecord{ID: id, Turns: turns}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{usageSummaryFile, historyIndexFile} {
		if err := os.Remove(filepath.Join(seed.SessionDir(id), name)); err != nil {
			t.Fatal(err)
		}
	}
	cold := NewStore(root)
	if compacted, _, _ := cold.CleanStorage(time.Hour); compacted != 0 {
		t.Fatal("cleanup rewrote a complete canonical transcript")
	}
	got, err := cold.readIndexedHistoryPage(id, HistoryPage{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Info.ID != "last" || len(got[0].Parts) != 1 || len(got[0].Parts[0].Text) != textSize {
		t.Fatal("indexed replay lost a turn or part")
	}
	var input int
	if err := cold.VisitUsageTurns(id, func(_ SessionRecord, usage []TurnUsageSummary) {
		if len(usage) == 1 && usage[0].Tokens != nil {
			input = int(usage[0].Tokens.Input)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if input != 123 {
		t.Fatalf("usage after large record = %d", input)
	}
}

func TestCleanupPreservesUnrecognizedLogEntry(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	const id = "ses_unknown"
	if err := s.Put(SessionRecord{ID: id, Turns: []MessageTurn{{Info: MessageInfo{ID: "a"}}}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.SessionDir(id), sessionTurnsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Include a duplicate valid entry that would normally trigger compaction.
	log := string(data) + string(data) + "{\"futureEntry\":true}\n"
	if err = os.WriteFile(path, []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	for _, warm := range []bool{false, true} {
		cold := NewStore(root)
		if warm {
			cold.Get(id)
		}
		cold.CleanStorage(time.Hour)
		data, err = os.ReadFile(path)
		if err != nil || string(data) != log {
			t.Fatalf("cleanup changed unrecognized entry (warm=%v, err=%v)", warm, err)
		}
	}
}
