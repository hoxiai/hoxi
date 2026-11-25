package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestOpenAIResponsesReasoningEmptySummaryAndTerminalBackfill(t *testing.T) {
	// 1. Replaying signature-only encrypted reasoning must include explicit empty "summary":[].
	drv := newOpenAIDriver(true)
	req, err := drv.BuildRequest(ModelRequest{
		SessionID: "ses_resp_sum",
		Target:    llm.StreamTarget{ModelID: "gpt-6.2-sol", BaseURL: "https://api.openai.com/v1", Protocol: llm.ProtocolOpenAIResponses},
		Messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "go"}}},
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: ContentThinking, Text: "", Signature: "enc_sig_only"},
				{Type: ContentText, Text: "ok"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}
	if !strings.Contains(string(req.Body), `"encrypted_content":"enc_sig_only"`) || !strings.Contains(string(req.Body), `"summary":[]`) {
		t.Fatalf("expected explicit empty summary array on signature-only reasoning replay, got: %s", req.Body)
	}

	// 2. Terminal response.completed backfills output_text, reasoning, and function_call
	// when a compatible gateway omits incremental delta/output_item.done frames, without duplicating streamed deltas.
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"Hel"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[` +
			`{"type":"reasoning","id":"rs_1","encrypted_content":"enc_backfill","summary":[{"type":"summary_text","text":"thought"}]},` +
			`{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"Hello world"}]},` +
			`{"type":"function_call","id":"fc_1","call_id":"call_bf_1","name":"glob","arguments":"{\"pattern\":\"*.go\"}"}` +
			`],"usage":{"input_tokens":100,"output_tokens":20}}}`,
	}, "\n") + "\n"

	var text strings.Builder
	var sigs []string
	var calls []ToolCall
	if err := drv.ParseSSE(context.Background(), strings.NewReader(sse), func(se StreamEvent) {
		switch se.Kind {
		case EventTextDelta:
			text.WriteString(se.Delta)
		case EventSignatureDelta:
			sigs = append(sigs, se.Delta)
		case EventToolCallReady:
			if se.ToolCall != nil {
				calls = append(calls, *se.ToolCall)
			}
		}
	}); err != nil {
		t.Fatalf("ParseSSE failed: %v", err)
	}
	if text.String() != "Hello world" {
		t.Fatalf("expected exact deduplicated backfilled text 'Hello world', got %q", text.String())
	}
	if len(sigs) != 1 || sigs[0] != "enc_backfill" {
		t.Fatalf("expected backfilled reasoning encrypted_content, got %v", sigs)
	}
	if len(calls) != 1 || calls[0].ID != "call_bf_1" || calls[0].Name != "glob" {
		t.Fatalf("expected backfilled function_call, got %+v", calls)
	}
}

type failAfterBodyReader struct {
	payload []byte
	pos     int
	err     error
}

func (r *failAfterBodyReader) Read(p []byte) (int, error) {
	if r.pos < len(r.payload) {
		n := copy(p, r.payload[r.pos:])
		r.pos += n
		return n, nil
	}
	return 0, r.err
}

func TestOpenAIChatToleratesLostUsageTrailerAfterFinishReason(t *testing.T) {
	drv := newOpenAIDriver(false)
	// Positive: valid finish_reason received, then stream drops with io.ErrUnexpectedEOF before [DONE]/usage chunk.
	bodyOK := &failAfterBodyReader{
		payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"All fixed.\"},\"finish_reason\":\"stop\"}]}\n\n"),
		err:     io.ErrUnexpectedEOF,
	}
	var gotText, gotFinish string
	if err := drv.ParseSSE(context.Background(), bodyOK, func(se StreamEvent) {
		if se.Kind == EventTextDelta {
			gotText += se.Delta
		}
		if se.Kind == EventFinish {
			gotFinish = se.Finish
		}
	}); err != nil {
		t.Fatalf("expected completed chat choice to survive lost usage trailer, got err: %v", err)
	}
	if gotText != "All fixed." || gotFinish != "stop" {
		t.Fatalf("unexpected output text=%q finish=%q", gotText, gotFinish)
	}

	// Negative: EOF BEFORE finish_reason must still fail!
	bodyTrunc := &failAfterBodyReader{
		payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"Halfway\"}}]}\n\n"),
		err:     io.ErrUnexpectedEOF,
	}
	if err := drv.ParseSSE(context.Background(), bodyTrunc, func(StreamEvent) {}); err == nil {
		t.Fatal("expected stream error when connection drops before finish_reason")
	}
}

func TestSummarizeStandaloneFallbackAndAdaptiveChunkHalving(t *testing.T) {
	// 1. Standalone SummarizeWithUsage falls back from promoted Responses 404 to Chat Completions.
	var protos []llm.StreamProtocol
	fbClient := &protocolCaptureClient{
		target: llm.StreamTarget{
			BaseURL:        "http://127.0.0.1:5431/v1",
			Protocol:       llm.ProtocolOpenAIResponses,
			FallbackOpenAI: true,
			HasEndpoint:    true,
			ContextLimit:   128_000,
			OutputLimit:    4096,
		},
		openFn: func(call int, req llm.StreamHTTPRequest) (*http.Response, error) {
			protos = append(protos, req.Target.Protocol)
			if call == 1 {
				return nil, &llm.StreamHTTPError{StatusCode: http.StatusNotFound, Message: "404 page not found"}
			}
			sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Compact fallback summary\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
		},
	}
	got, err := NewDefaultEngine(fbClient).Summarize(context.Background(), "subapi", "openai-codex-gpt-6.2-sol", "short transcript")
	if err != nil || got != "Compact fallback summary" {
		t.Fatalf("standalone summary fallback failed: got=%q err=%v", got, err)
	}
	if len(protos) != 2 || protos[0] != llm.ProtocolOpenAIResponses || protos[1] != llm.ProtocolOpenAI {
		t.Fatalf("expected [openai-responses openai] fallback, got %v", protos)
	}

	// 2. Adaptive chunk halving when an upstream rejects a chunk as context_length_exceeded.
	var chunkLens []int
	halveClient := &protocolCaptureClient{
		target: llm.StreamTarget{
			BaseURL:      "http://127.0.0.1:5431/v1",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 128_000, // advertises 128K -> initial chunkBytes = 100,000
			OutputLimit:  4096,
		},
		openFn: func(_ int, req llm.StreamHTTPRequest) (*http.Response, error) {
			chunkLens = append(chunkLens, len(req.Body))
			if len(req.Body) > 30_000 {
				return nil, &llm.StreamHTTPError{StatusCode: http.StatusBadRequest, Message: "context_length_exceeded: max 16384"}
			}
			sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Condensed segment\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
		},
	}
	longTranscript := strings.Repeat("engineering transcript line with file path and test output\n", 1000) // ~59KB
	sum, err := NewDefaultEngine(halveClient).Summarize(context.Background(), "subapi", "gpt-5", longTranscript)
	if err != nil || sum != "Condensed segment" {
		t.Fatalf("adaptive chunk halving failed: sum=%q err=%v lens=%v", sum, err, chunkLens)
	}
	if len(chunkLens) < 3 || chunkLens[0] <= chunkLens[1] {
		t.Fatalf("expected chunk halving sequence, got body lengths %v", chunkLens)
	}
}

func TestPruneOldToolResultsPreservesBashForegroundLogHandle(t *testing.T) {
	const contextLimit = 8000
	logHandle := "[Foreground log: bash action=output procId=log_abc123 offset=0; retained 65536/65536 bytes for this turn]"
	bashOut := strings.Repeat("=== RUN   TestSuite\n--- PASS: TestSuite (0.01s)\n", 70) +
		"ok  \tgithub.com/hoxiai/hoxi/internal/agent\t1.20s\n" + logHandle

	var msgs []Message
	msgs = append(msgs,
		Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "b1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`)}}},
		Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "b1", Text: bashOut}}},
	)
	for i := 0; i < 8; i++ {
		body := "ok"
		if i == 5 {
			body = strings.Repeat("recent tail line to push over trigger\n", 700)
		}
		msgs = append(msgs,
			Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: fmt.Sprintf("r%d", i), Name: "read", Input: json.RawMessage(`{"filePath":"x.go"}`)}}},
			Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: fmt.Sprintf("r%d", i), Text: body}}},
		)
	}
	pruned := PruneOldToolResults(msgs, "sys", contextLimit)
	got := pruned[1].Content[0].Text
	if !strings.HasPrefix(got, "[tool output pruned:") {
		t.Fatalf("expected bash output to be pruned, got len=%d", len(got))
	}
	if !strings.Contains(got, logHandle) {
		t.Fatalf("expected pruned bash output to preserve foreground log paging handle, got:\n%s", got)
	}
}

func TestEngineRetriesMidStreamHTTP2ProtocolErrorWithForceHTTP1(t *testing.T) {
	var forceFlags []bool
	client := &protocolCaptureClient{
		target: llm.StreamTarget{
			BaseURL:      "https://api.openai.com/v1",
			Protocol:     llm.ProtocolOpenAI,
			HasEndpoint:  true,
			ContextLimit: 128_000,
			OutputLimit:  4096,
		},
		openFn: func(call int, req llm.StreamHTTPRequest) (*http.Response, error) {
			forceFlags = append(forceFlags, req.ForceHTTP1)
			if call == 1 {
				body := &failAfterBodyReader{
					payload: nil,
					err:     fmt.Errorf("stream error: stream ID 3; PROTOCOL_ERROR; received from peer"),
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: make(http.Header)}, nil
			}
			sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Recovered over HTTP/1.1\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}, nil
		},
	}
	eng := NewDefaultEngine(client)
	eng.SetStepRetryDelays([]time.Duration{0})
	var reply string
	if err := eng.RunTurn(context.Background(), TurnRequest{
		SessionID:   "ses_h2_retry",
		WorktreeDir: t.TempDir(),
		Mode:        "build",
		ProviderID:  "openai",
		ModelID:     "gpt-5",
		UserPrompt:  "ping",
		Sink: EventSinkFunc(func(evt EngineEvent) {
			if evt.Kind == EngineTextEnd {
				reply = evt.Text
			}
		}),
	}); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if reply != "Recovered over HTTP/1.1" {
		t.Fatalf("unexpected reply %q", reply)
	}
	if len(forceFlags) != 2 || forceFlags[0] || !forceFlags[1] {
		t.Fatalf("expected [false true] ForceHTTP1 retry sequence, got %v", forceFlags)
	}
}
