package agent_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/llm"
)

func TestDriversRejectTruncatedStreamsWithoutTerminalFrame(t *testing.T) {
	cases := []struct {
		name  string
		proto llm.StreamProtocol
		sse   string
	}{
		{
			name:  "openai_chat_missing_done_and_finish",
			proto: llm.ProtocolOpenAI,
			sse:   "data: {\"choices\":[{\"delta\":{\"content\":\"half sentence\"}}]}\n\n",
		},
		{
			name:  "openai_responses_missing_completed",
			proto: llm.ProtocolOpenAIResponses,
			sse:   "data: {\"type\":\"response.output_text.delta\",\"delta\":\"half\"}\n\n",
		},
		{
			name:  "anthropic_missing_message_stop",
			proto: llm.ProtocolAnthropic,
			sse:   "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"half\"}}\n\n",
		},
		{
			name:  "gemini_missing_finish_reason",
			proto: llm.ProtocolGoogle,
			sse:   "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"half\"}]}}]}\n\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &stubStreamClient{proto: tc.proto, responses: []string{tc.sse}}
			eng := agent.NewDefaultEngine(sc)
			eng.SetStepRetryDelays(nil)

			var textEnded string
			err := eng.RunTurn(context.Background(), agent.TurnRequest{
				SessionID:   "ses_trunc",
				WorktreeDir: t.TempDir(),
				Mode:        "code",
				ProviderID:  "test",
				ModelID:     "test-model",
				UserPrompt:  "hi",
				Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
					if evt.Kind == agent.EngineTextEnd {
						textEnded = evt.Text
					}
				}),
			})
			if !agent.IsIncompleteStreamError(err) {
				t.Fatalf("expected ErrIncompleteStream for %s, got: %v", tc.name, err)
			}
			if textEnded == "" {
				t.Fatalf("expected partial text to be sealed before returning ErrIncompleteStream")
			}
			if got := sc.callCount.Load(); got != 1 {
				t.Fatalf("expected 1 attempt after visible text was emitted, got %d", got)
			}
		})
	}
}

func TestDriversDropLengthTruncatedToolJSONAndFailOnCorruptToolJSON(t *testing.T) {
	dir := t.TempDir()
	sseLengthCut := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Checking files...","tool_calls":[{"index":0,"id":"ok1","function":{"name":"glob","arguments":"{\"pattern\":\"*.go\"}"}},{"index":1,"id":"cut2","function":{"name":"bash","arguments":"{\"command\":\"rm -rf"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
	sseFinish := "data: {\"choices\":[{\"delta\":{\"content\":\"Completed safely.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

	sc := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseLengthCut, sseFinish}}
	eng := agent.NewDefaultEngine(sc)
	eng.SetStepRetryDelays(nil)

	var ranTools []string
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_len_cut",
		WorktreeDir: dir,
		Mode:        "code",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "list files",
		CommandRunner: func(_ context.Context, _, cmd string) (string, int, error) {
			ranTools = append(ranTools, "bash:"+cmd)
			return "", 0, nil
		},
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			if evt.Kind == agent.EngineToolCompleted {
				ranTools = append(ranTools, evt.ToolName)
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected length-truncated tool call to be safely dropped, got err: %v", err)
	}
	if len(ranTools) != 1 || ranTools[0] != "glob" {
		t.Fatalf("expected only complete 'glob' tool to run and truncated 'bash' to be dropped, got: %v", ranTools)
	}

	sseCorrupt := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"bad1","function":{"name":"bash","arguments":"{\"command\":\"broken"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
	scBad := &stubStreamClient{proto: llm.ProtocolOpenAI, responses: []string{sseCorrupt, sseCorrupt, sseCorrupt}}
	engBad := agent.NewDefaultEngine(scBad)
	engBad.SetStepRetryDelays(nil)
	badErr := engBad.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_bad_json",
		WorktreeDir: dir,
		Mode:        "code",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "run",
	})
	if !agent.IsIncompleteStreamError(badErr) {
		t.Fatalf("expected ErrIncompleteStream on corrupt non-length tool JSON, got: %v", badErr)
	}
}

func TestRelayInlineThinkFilterCacheUsageAndPreDeltaAutoRetry(t *testing.T) {
	var attempts atomic.Int32
	relaySSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"<thi"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"nk>Analyzing edge cases</thi"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"nk>\nFinal answer keeps <think> literal in code."}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":80}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	flakyClient := &flakyStreamClient{
		onOpen: func() (*http.Response, error) {
			n := attempts.Add(1)
			if n == 1 {
				return nil, errors.New("connection reset by peer")
			}
			if n == 2 {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(": keepalive\n\n")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(relaySSE)),
				Header:     make(http.Header),
			}, nil
		},
	}

	eng := agent.NewDefaultEngine(flakyClient)
	eng.SetStepRetryDelays(nil)

	var gotThink, gotText string
	var gotCacheRead int
	err := eng.RunTurn(context.Background(), agent.TurnRequest{
		SessionID:   "ses_relay",
		WorktreeDir: t.TempDir(),
		Mode:        "code",
		ProviderID:  "deepseek",
		ModelID:     "deepseek-reasoner",
		UserPrompt:  "test inline think",
		Sink: agent.EventSinkFunc(func(evt agent.EngineEvent) {
			switch evt.Kind {
			case agent.EngineThinkingEnd:
				gotThink = evt.Text
			case agent.EngineTextEnd:
				gotText = evt.Text
			case agent.EngineTurnUsage:
				if evt.Usage != nil {
					gotCacheRead = evt.Usage.CacheRead
				}
			}
		}),
	})
	if err != nil {
		t.Fatalf("expected pre-delta transient failures to recover cleanly, got: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts (2 transient pre-delta retries + 1 success), got %d", attempts.Load())
	}
	if gotThink != "Analyzing edge cases" {
		t.Fatalf("unexpected inline thinking extraction: %q", gotThink)
	}
	if gotText != "Final answer keeps <think> literal in code." {
		t.Fatalf("unexpected visible text extraction: %q", gotText)
	}
	if gotCacheRead != 80 {
		t.Fatalf("expected prompt_cache_hit_tokens=80, got %d", gotCacheRead)
	}
}

type flakyStreamClient struct {
	onOpen func() (*http.Response, error)
}

func (f *flakyStreamClient) ResolveStreamTarget(providerID, modelID string) llm.StreamTarget {
	return llm.StreamTarget{
		ProviderID:   providerID,
		ModelID:      modelID,
		BaseURL:      "https://api.example.com/v1",
		Protocol:     llm.ProtocolOpenAI,
		HasEndpoint:  true,
		ContextLimit: 128000,
		OutputLimit:  4096,
	}
}

func (f *flakyStreamClient) OpenStream(_ context.Context, _ llm.StreamHTTPRequest) (*http.Response, error) {
	return f.onOpen()
}
