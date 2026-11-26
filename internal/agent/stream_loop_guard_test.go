package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestStreamChantingDetectorCatchesChantingAndIgnoresCodeAndDividers(t *testing.T) {
	// 1. Repetitive English prose chanting triggers detection.
	d := newStreamChantingDetector()
	phrase := "Let me check this file again carefully before making any changes. "
	hit := false
	for i := 0; i < 15; i++ {
		if ok, _ := d.Observe(phrase); ok {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatal("expected repetitive English chanting to trigger loop detection")
	}

	// 2. Repetitive CJK prose chanting triggers detection cleanly on rune boundaries.
	dCJK := newStreamChantingDetector()
	cjkPhrase := "让我再仔细检查一遍当前这个文件的实现逻辑和所有边界条件，确保没有任何遗漏。"
	hit = false
	for i := 0; i < 15; i++ {
		if ok, _ := dCJK.Observe(cjkPhrase); ok {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatal("expected repetitive CJK chanting to trigger loop detection")
	}

	// 3. Repetitive code inside fenced blocks (including fences split across deltas) never false-positives.
	dCode := newStreamChantingDetector()
	for _, chunk := range []string{"Here is the generated table:\n``", "`go\n"} {
		if ok, _ := dCode.Observe(chunk); ok {
			t.Fatal("unexpected trigger while opening code fence")
		}
	}
	for i := 0; i < 25; i++ {
		if ok, _ := dCode.Observe("assert.Equal(t, expectedValue, actualValue, \"row must match baseline\")\n"); ok {
			t.Fatalf("code inside fenced block falsely triggered loop detection at iteration %d", i)
		}
	}
	if ok, _ := dCode.Observe("```\nDone.\n"); ok {
		t.Fatal("closing fence falsely triggered loop detection")
	}

	// 4. Markdown dividers and low-diversity padding never false-positive.
	dDiv := newStreamChantingDetector()
	for i := 0; i < 20; i++ {
		if ok, _ := dDiv.Observe("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n"); ok {
			t.Fatal("markdown table divider falsely triggered loop detection")
		}
	}
}

type chantingStreamClient struct {
	proto       llm.StreamProtocol
	calls       int
	streamCtxOK []bool
	onCall      func(ctx context.Context, call int) (*http.Response, error)
}

func (c *chantingStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		Protocol:     c.proto,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  8192,
	}
}

func (c *chantingStreamClient) OpenStream(ctx context.Context, _ llm.StreamHTTPRequest) (*http.Response, error) {
	c.calls++
	return c.onCall(ctx, c.calls)
}

func TestEngineAbortsVisibleTextChantingLoopImmediately(t *testing.T) {
	phrase := "Let me check this file again carefully before making any edits. "
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", phrase))
	}
	lines = append(lines, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")

	var streamCancelled atomic.Bool
	var written atomic.Int32
	writerDone := make(chan struct{})
	client := &chantingStreamClient{
		proto: llm.ProtocolOpenAI,
		onCall: func(ctx context.Context, _ int) (*http.Response, error) {
			pr, pw := io.Pipe()
			go func() {
				defer close(writerDone)
				defer pw.Close()
				for _, frame := range lines {
					select {
					case <-ctx.Done():
						streamCancelled.Store(true)
						return
					default:
					}
					if _, err := pw.Write([]byte(frame)); err != nil {
						streamCancelled.Store(true)
						return
					}
					written.Add(1)
				}
			}()
			return &http.Response{StatusCode: http.StatusOK, Body: pr, Header: make(http.Header)}, nil
		},
	}

	err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_chant_text",
		WorktreeDir: t.TempDir(),
		UserPrompt:  "explain",
	})
	<-writerDone
	if !errors.Is(err, errStreamChantingLoop) {
		t.Fatalf("expected errStreamChantingLoop, got %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("visible chanting loop must not retry blindly, got %d calls", client.calls)
	}
	if !streamCancelled.Load() || int(written.Load()) >= len(lines) {
		t.Fatalf("expected per-step stream context/body to be cancelled mid-stream (cancelled=%v written=%d/%d)", streamCancelled.Load(), written.Load(), len(lines))
	}
}

func TestEngineRecoversOnceFromThinkingOnlyChantingLoop(t *testing.T) {
	phrase := "Wait, let me double check every single assumption carefully before I act. "
	var thinkSSE strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&thinkSSE, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%q}}]}\n\n", phrase)
	}
	thinkSSE.WriteString("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")

	goodSSE := "data: {\"choices\":[{\"delta\":{\"content\":\"All checks pass.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

	client := &chantingStreamClient{
		proto: llm.ProtocolOpenAI,
		onCall: func(_ context.Context, call int) (*http.Response, error) {
			body := goodSSE
			if call == 1 {
				body = thinkSSE.String()
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		},
	}

	var finalText string
	err := NewDefaultEngine(client).RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_chant_think",
		WorktreeDir: t.TempDir(),
		UserPrompt:  "summarize",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineTextEnd {
				finalText = evt.Text
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected clean single recovery from thinking-only chant, got %v", err)
	}
	if client.calls != 2 || finalText != "All checks pass." {
		t.Fatalf("calls=%d finalText=%q", client.calls, finalText)
	}
}
