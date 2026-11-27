package runner

import (
	"context"

	"github.com/hoxiai/hoxi/internal/llm"
	"path/filepath"
	"testing"
	"time"
)

type usageNativeEngine struct{}

func (*usageNativeEngine) RunTurn(_ context.Context, req NativeTurnRequest) error {
	req.Emit(NativeEvent{Kind: NativeEventTurnUsage, Usage: &NativeTokenUsage{
		Input: 12, Output: 3, Reasoning: 5, CacheRead: 20, CacheWrite: 7, ReportedCacheInput: 39, CacheDrop: &llm.PromptCacheDrop{Cause: "local_prefix_unchanged", Before: 4000, After: 20},
	}})
	req.Emit(NativeEvent{Kind: NativeEventTurnUsage, ParentCallID: "child", Usage: &NativeTokenUsage{
		Input: 2, Output: 1, Reasoning: 1, CacheRead: 3, ReportedCacheInput: 5, CacheDrop: &llm.PromptCacheDrop{Cause: "tools_changed"},
	}})
	req.Emit(NativeEvent{Kind: NativeEventSummaryUsage, Usage: &NativeTokenUsage{Input: 7, Output: 3, CostUSD: 0.05}})
	req.Emit(NativeEvent{Kind: NativeEventSummaryUsage, ParentCallID: "child", Usage: &NativeTokenUsage{Input: 2, Output: 1, CostUSD: 0.01}})
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
				// Billing buckets accumulate parent, child and summary once;
				// Total & Context reflect the parent's single-step window occupancy (12+3+5+20+7 = 47).
				if u == nil || u.ReportedCacheInput != 44 || u.CacheDrop == nil || u.CacheDrop.Cause != "local_prefix_unchanged" || u.Summary == nil || u.Summary.Input != 9 || u.Summary.Output != 4 || u.Input != 23 || u.Output != 8 || u.Reasoning != 6 || u.Cache.Read != 23 || u.Cache.Write != 7 || u.Total != 47 || u.Context != 47 {
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
