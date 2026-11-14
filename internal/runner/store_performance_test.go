package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

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

func BenchmarkRunnerHistoryPage(b *testing.B) {
	dir := b.TempDir()
	s := NewStore(dir)
	if err := s.Put(SessionRecord{ID: "bench", Turns: performanceTurns(5000)}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := NewStore(dir).MarshalTurnsPage("bench", historyPage{limit: 200}); !ok {
			b.Fatal("missing history")
		}
	}
}

func BenchmarkRunnerEditedFiles(b *testing.B) {
	svc := NewService("/repo", nil, nil, filepath.Join(b.TempDir(), "sessions"))
	if err := svc.store.Put(SessionRecord{ID: "bench", ProjectDir: "/repo", Turns: performanceTurns(3000)}); err != nil {
		b.Fatal(err)
	}
	svc.store.SetActive("bench", true)
	_ = svc.sessionEditedFileSet("bench")
	b.Cleanup(svc.store.Flush)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.store.AppendPartDelta("bench", "msg_002999", "text", " token")
		if len(svc.sessionEditedFileSet("bench")) != 3000 {
			b.Fatal("lost edited files")
		}
	}
}

func BenchmarkRunnerStreamPersist(b *testing.B) {
	rec := SessionRecord{ID: "bench", Turns: []MessageTurn{{Info: MessageInfo{ID: "msg", SessionID: "bench"}, Parts: []MessagePart{{
		ID: "tool", MessageID: "msg", Type: "tool", Tool: "bash", State: &ToolState{Status: "completed", Output: strings.Repeat("output\n", 150000)},
	}}}}}
	info := MessageInfo{ID: "msg", SessionID: "bench", Role: "assistant"}
	for _, inline := range []bool{true, false} {
		name := "queued-stream"
		if inline {
			name = "inline-baseline"
		}
		b.Run(name, func(b *testing.B) {
			svc := NewService("/repo", nil, nil, filepath.Join(b.TempDir(), "sessions"))
			svc.store.StartPersistence(context.Background())
			b.Cleanup(svc.store.ClosePersistence)
			if err := svc.store.Put(rec); err != nil {
				b.Fatal(err)
			}
			svc.store.SetActive("bench", true)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if inline {
					svc.store.UpsertTurnInfo("bench", info)
				} else {
					svc.broadcast("bench", SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": info}})
				}
			}
			b.StopTimer()
			svc.store.FlushSession("bench")
		})
	}
}
