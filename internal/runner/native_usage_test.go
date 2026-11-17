package runner

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type usageNativeEngine struct{}

func (*usageNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	req.Emit(NativeEvent{Kind: NativeEventTurnUsage, Usage: &NativeTokenUsage{
		Input: 12, Output: 3, Reasoning: 5, CacheRead: 20, CacheWrite: 7,
	}})
	req.Emit(NativeEvent{Kind: NativeEventTurnUsage, ParentCallID: "child", Usage: &NativeTokenUsage{
		Input: 2, Output: 1, Reasoning: 1, CacheRead: 3,
	}})
	return nil
}

func (*usageNativeEngine) Summarize(context.Context, string, string, string) (string, error) {
	return "summary", nil
}

func TestNativeTurnTotalIncludesCacheAndChildUsageOnce(t *testing.T) {
	root := t.TempDir()
	svc := NewService(root, nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	defer svc.CloseAll()
	svc.SetNativeEngine(&usageNativeEngine{})
	rec := SessionRecord{ID: "ses_nat_usage", Runtime: "native", ProjectDir: root, WorktreePath: root}
	if err := svc.store.Put(rec); err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe := svc.Subscribe(rec.ID)
	defer unsubscribe()
	if err := svc.startNativeTurn(rec, "build", "model", "", "", "hello",
		MessageInfo{ID: "msg_usage_user", SessionID: rec.ID, Role: "user"}, nil, "msg_usage_assistant"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type != "session.idle" {
				continue
			}
			stored, _ := svc.store.Get(rec.ID)
			for _, turn := range stored.Turns {
				if turn.Info.ID != "msg_usage_assistant" {
					continue
				}
				u := turn.Info.Tokens
				// Billing buckets accumulate parent + child once (14/4/6/23/7);
				// Total & Context reflect the parent's single-step window occupancy (12+3+5+20+7 = 47).
				if u == nil || u.Input != 14 || u.Output != 4 || u.Reasoning != 6 || u.Cache.Read != 23 || u.Cache.Write != 7 || u.Total != 47 || u.Context != 47 {
					t.Fatalf("incorrect native usage: %+v", u)
				}
				return
			}
			t.Fatal("assistant usage was not persisted")
		case <-deadline:
			t.Fatal("native turn did not finish")
		}
	}
}
