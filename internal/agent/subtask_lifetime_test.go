package agent

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

type subtaskLifetimeClient struct {
	onStream func(context.Context)
}

func (*subtaskLifetimeClient) ResolveStreamTarget(p, m string) llm.StreamTarget {
	return llm.StreamTarget{ProviderID: p, ModelID: m, Protocol: llm.ProtocolOpenAI, HasEndpoint: true}
}
func (*subtaskLifetimeClient) ResolveEconomyModel(_, _ string) (string, bool) {
	return "economy-model", true
}
func (c *subtaskLifetimeClient) OpenStream(ctx context.Context, _ llm.StreamHTTPRequest) (*http.Response, error) {
	c.onStream(ctx)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSubtaskInheritsCancellationWithoutNewDeadline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOXI_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &subtaskLifetimeClient{onStream: func(child context.Context) {
		if d, ok := child.Deadline(); ok {
			t.Errorf("unexpected child deadline: %v", d)
		}
		cancel()
	}}
	eng := NewDefaultEngine(c)
	_, agents := llm.LoadAgents(t.TempDir(), nil)
	_, err := eng.runSubtask(ctx, TurnRequest{WorktreeDir: t.TempDir(), ProviderID: "chosen", ModelID: "large", Effort: "high"},
		"build", agents, nil, SubtaskRequest{SubagentType: "explore", Prompt: "investigate"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop did not cancel subtask: %v", err)
	}
	// A canceled child must release its concurrency slot.
	checkCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	for i := 0; i < defaultMaxConcurrentSubtasks; i++ {
		release, err := eng.acquireSubtaskSlot(checkCtx)
		if err != nil {
			t.Fatal("subtask slot leaked", err)
		}
		defer release()
	}
}

func TestSubtaskEconomyIsExplicitOptIn(t *testing.T) {
	eng := NewDefaultEngine(&subtaskLifetimeClient{})
	parent := TurnRequest{ProviderID: "chosen", ModelID: "large"}
	for _, tc := range []struct{ override, wantModel, wantRoute string }{
		{"", "large", "inherit_parent"},
		{"economy", "economy-model", "call_economy"},
		{"fast", "economy-model", "call_economy"},
	} {
		p, m, route := eng.resolveSubtaskModel(parent, "explore", llm.AgentSpec{}, false, tc.override, nil)
		if p != "chosen" || m != tc.wantModel || route != tc.wantRoute {
			t.Fatalf("override %q: %s/%s (%s)", tc.override, p, m, route)
		}
	}
}
