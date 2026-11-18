package sessionstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "hoxi-sessionstore-test-*")
	if err == nil {
		_ = os.Setenv("HOXI_HOME", tmpHome)
	}
	code := m.Run()
	if tmpHome != "" {
		_ = os.RemoveAll(tmpHome)
	}
	os.Exit(code)
}

func seedStore(t *testing.T, dir string) *Store {
	t.Helper()
	store := NewStore(dir)
	if err := store.Put(SessionRecord{ID: "ses_cld_1", Runtime: "claude-code", ProjectDir: "/repo", Title: "first"}); err != nil {
		t.Fatal(err)
	}
	store.UpsertTurnInfo("ses_cld_1", MessageInfo{ID: "msg_1", SessionID: "ses_cld_1", Role: "assistant"})
	store.UpsertTurnPart("ses_cld_1", MessagePart{ID: "prt_1", SessionID: "ses_cld_1", MessageID: "msg_1", Type: "text"})
	store.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", "hello ")
	store.AppendPartDelta("ses_cld_1", "msg_1", "prt_1", "world")
	return store
}

func performanceTurns(count int) []MessageTurn {
	turns := make([]MessageTurn, count)
	for i := range turns {
		id := fmt.Sprintf("msg_%06d", i)
		turns[i] = MessageTurn{Info: MessageInfo{ID: id, SessionID: "bench", Role: "assistant"}, Parts: []MessagePart{
			{ID: "text", MessageID: id, SessionID: "bench", Type: "text", Text: strings.Repeat("x", 4096)},
			{ID: "edit", MessageID: id, SessionID: "bench", Type: "tool", Tool: "write", State: &ToolState{
				Status: "completed", Input: map[string]interface{}{"filePath": fmt.Sprintf("src/file_%d.go", i)},
			}},
		}}
	}
	return turns
}

func historyTestTurns(n int) []MessageTurn {
	turns := make([]MessageTurn, n)
	for i := range turns {
		role := "assistant"
		if i%3 == 0 {
			role = "user"
		}
		id := fmt.Sprintf("msg_%03d", i)
		turns[i] = MessageTurn{
			Info:  MessageInfo{ID: id, SessionID: "ses_hoxi_page", Role: role},
			Parts: []MessagePart{{ID: "prt_" + id, SessionID: "ses_hoxi_page", MessageID: id, Type: "text", Text: id}},
		}
	}
	return turns
}

func turnIDs(turns []MessageTurn) []string {
	ids := make([]string, len(turns))
	for i, turn := range turns {
		ids[i] = turn.Info.ID
	}
	return ids
}

func enableStreamPersistence(t testing.TB, s *Store) {
	t.Helper()
	s.StartPersistence(context.Background())
	t.Cleanup(s.ClosePersistence)
}

func occupyPersistWorkers(s *Store) func() {
	return s.OccupyPersistWorkers()
}

func waitPersistSettled(t *testing.T, s *Store, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ss := s.lockSession(id, false)
		if ss == nil {
			return
		}
		settled := ss.persistJob == nil
		ss.mu.Unlock()
		if settled {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("persistence job did not settle")
}
