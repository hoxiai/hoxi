package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
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

func TestOpenAIResponsesMultiMessageBackfillAndPostTerminalDisconnectTolerance(t *testing.T) {
	drv := newOpenAIDriver(true)
	// 1. Two message items in response.completed: msg_1 streamed with item_id,
	// msg_2 omitted from deltas -> msg_2 must be backfilled without dropping it.
	sseMulti := strings.Join([]string{
		`data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"Part 1. "}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[` +
			`{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"Part 1. "}]},` +
			`{"type":"message","id":"msg_2","content":[{"type":"output_text","text":"Part 2."}]}` +
			`],"usage":{"input_tokens":50,"output_tokens":10}}}`,
	}, "\n") + "\n"
	bodyAfterTerm := &failAfterBodyReader{
		payload: []byte(sseMulti),
		err:     io.ErrUnexpectedEOF, // trailing disconnect AFTER response.completed must be tolerated
	}
	var gotText strings.Builder
	var gotFinish string
	if err := drv.ParseSSE(context.Background(), bodyAfterTerm, func(se StreamEvent) {
		switch se.Kind {
		case EventTextDelta:
			gotText.WriteString(se.Delta)
		case EventFinish:
			gotFinish = se.Finish
		}
	}); err != nil {
		t.Fatalf("expected multi-message backfill + post-terminal EOF to succeed, got: %v", err)
	}
	if gotText.String() != "Part 1. Part 2." || gotFinish != "stop" {
		t.Fatalf("unexpected text=%q finish=%q", gotText.String(), gotFinish)
	}

	// Negative: disconnect BEFORE response.completed must still fail.
	preTerm := &failAfterBodyReader{
		payload: []byte(`data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"Half"}` + "\n\n"),
		err:     io.ErrUnexpectedEOF,
	}
	if err := drv.ParseSSE(context.Background(), preTerm, func(StreamEvent) {}); err == nil {
		t.Fatal("expected pre-terminal EOF on Responses SSE to fail")
	}

	// 2. Anthropic post-terminal disconnect after message_stop must survive; pre-terminal EOF must fail.
	anth := newAnthropicDriver()
	anthOK := &failAfterBodyReader{
		payload: []byte(strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done."}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			`data: {"type":"message_stop"}`,
		}, "\n") + "\n"),
		err: io.ErrUnexpectedEOF,
	}
	var anthText, anthFinish string
	if err := anth.ParseSSE(context.Background(), anthOK, func(se StreamEvent) {
		if se.Kind == EventTextDelta {
			anthText += se.Delta
		}
		if se.Kind == EventFinish {
			anthFinish = se.Finish
		}
	}); err != nil {
		t.Fatalf("expected Anthropic post-terminal EOF to succeed, got: %v", err)
	}
	if anthText != "Done." || anthFinish != "end_turn" {
		t.Fatalf("unexpected anthropic text=%q finish=%q", anthText, anthFinish)
	}
	anthTrunc := &failAfterBodyReader{
		payload: []byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Cut"}}` + "\n\n"),
		err:     io.ErrUnexpectedEOF,
	}
	if err := anth.ParseSSE(context.Background(), anthTrunc, func(StreamEvent) {}); err == nil {
		t.Fatal("expected pre-terminal EOF on Anthropic SSE to fail")
	}
}

func TestInPlaceCompactionPromptAnchorsRetainedTail(t *testing.T) {
	tail := []Message{{
		Role:    RoleUser,
		Content: []ContentBlock{{Type: ContentText, Text: "Please run \"go test\" on internal/agent\nnext"}},
	}}
	prompt := buildInPlaceCompactionPrompt(tail)
	if !strings.Contains(prompt, `from the user message beginning "Please run 'go test' on internal/agent next" onward`) {
		t.Fatalf("expected retained tail anchor quote in prompt, got:\n%s", prompt)
	}
}

func TestDisambiguateToolCallIDsAcrossHistoryAndParallelBatch(t *testing.T) {
	history := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentToolUse, ID: "call_0", Name: "glob"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentToolResult, ToolUseID: "call_0", Text: "a.go"}}},
	}
	asst := []ContentBlock{
		{Type: ContentText, Text: "checking"},
		{Type: ContentToolUse, ID: "call_0", Name: "read"},
		{Type: ContentToolUse, ID: "call_0", Name: "read"},
		{Type: ContentToolUse, ID: "unique_1", Name: "glob"},
	}
	calls := []ToolCall{
		{ID: "call_0", Name: "read"},
		{ID: "call_0", Name: "read"},
		{ID: "unique_1", Name: "glob"},
	}
	gotBlocks, gotCalls := disambiguateToolCallIDs(history, asst, calls)
	if gotCalls[0].ID != "call_0__2" || gotCalls[1].ID != "call_0__3" || gotCalls[2].ID != "unique_1" {
		t.Fatalf("unexpected disambiguated call IDs: %+v", gotCalls)
	}
	if gotBlocks[1].ID != "call_0__2" || gotBlocks[2].ID != "call_0__3" || gotBlocks[3].ID != "unique_1" {
		t.Fatalf("expected assistant ContentToolUse IDs to stay aligned, got: %+v", gotBlocks)
	}
}

func TestStepRetryPolicyCaps524AndDialTimeoutsAndSilencesFastFirstNetworkRetry(t *testing.T) {
	eng := NewDefaultEngine(nil)
	now := time.Now()

	// HTTP 524 origin timeout: 1 retry allowed, 2nd attempt rejected.
	err524 := &llm.StreamHTTPError{StatusCode: 524, Message: "HTTP 524: A timeout occurred"}
	if d1 := eng.planStepRetry(err524, 1, 0, now); !d1.shouldRetry || d1.silentUI {
		t.Fatalf("expected 524 attempt 1 to retry visibly, got %+v", d1)
	}
	if d2 := eng.planStepRetry(err524, 2, 0, now); d2.shouldRetry {
		t.Fatalf("expected 524 attempt 2 to stop retrying, got %+v", d2)
	}

	// Pre-send TCP dial timeout: capped to 3 retries, attempt 1 silent fast resend (<5s), attempt 2 visible.
	dialErr := fmt.Errorf("Post \"https://api.example.com/v1/responses\": dial tcp 1.2.3.4:443: i/o timeout")
	if d1 := eng.planStepRetry(dialErr, 1, 0, now); !d1.shouldRetry || !d1.silentUI || d1.delay > time.Second {
		t.Fatalf("expected dial attempt 1 to be fast silent resend, got %+v", d1)
	}
	if d2 := eng.planStepRetry(dialErr, 2, 0, now); !d2.shouldRetry || d2.silentUI {
		t.Fatalf("expected dial attempt 2 to show UI status, got %+v", d2)
	}
	if d4 := eng.planStepRetry(dialErr, 4, 0, now); d4.shouldRetry {
		t.Fatalf("expected dial timeout to cap at 3 retries, got %+v", d4)
	}
}

func TestForegroundBashTimeoutPromotesToBackgroundWhileCancelStillKills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell timing test")
	}
	sessID := "ses_timeout_promote_1"
	defer DeleteSessionBackgroundProcs(sessID)

	tool := &bashTool{}
	ws := t.TempDir()

	// 1. Timeout promotes to background with reason=timeout and preserves pre/post output.
	timeoutIn, _ := json.Marshal(map[string]any{
		"command":   "printf 'timeout-before\\n'; sleep 0.25; printf 'timeout-after\\n'",
		"timeoutMs": 60,
	})
	promOut, err := tool.Execute(context.Background(), ToolContext{
		SessionID:   sessID,
		WorktreeDir: ws,
	}, timeoutIn)
	if err != nil {
		t.Fatalf("expected timeout promotion without error, got: %v", err)
	}
	procID, _ := promOut.Metadata["procId"].(string)
	if prom, _ := promOut.Metadata["promoted"].(bool); !prom || procID == "" || promOut.Metadata["reason"] != "timeout" {
		t.Fatalf("expected timeout promotion metadata, got %+v", promOut.Metadata)
	}
	if !strings.Contains(promOut.Output, "reached its timeout") {
		t.Fatalf("expected timeout promotion notice, got:\n%s", promOut.Output)
	}
	var after strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pollIn, _ := json.Marshal(map[string]any{"action": "output", "procId": procID, "waitMs": 200})
		pollOut, pErr := tool.Execute(context.Background(), ToolContext{SessionID: sessID, WorktreeDir: ws}, pollIn)
		if pErr != nil {
			t.Fatalf("poll failed: %v", pErr)
		}
		after.WriteString(pollOut.Output)
		if running, _ := pollOut.Metadata["running"].(bool); !running {
			break
		}
	}
	if full := promOut.Output + "\n" + after.String(); !strings.Contains(full, "timeout-before") || !strings.Contains(full, "timeout-after") {
		t.Fatalf("expected full pre/post output across timeout promotion, got:\n%s", full)
	}

	// 2. Rejection path: explicit user cancellation must KILL, never promote.
	cancelCtx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	cancelIn, _ := json.Marshal(map[string]any{
		"command":   "sleep 5",
		"timeoutMs": 5000,
	})
	cancelOut, cancelErr := tool.Execute(cancelCtx, ToolContext{SessionID: sessID, WorktreeDir: ws}, cancelIn)
	if cancelErr == nil || !cancelOut.IsError {
		t.Fatalf("expected user cancellation to terminate command with error, got out=%+v err=%v", cancelOut, cancelErr)
	}
	if prom, _ := cancelOut.Metadata["promoted"].(bool); prom {
		t.Fatalf("cancelled command must never promote to background: %+v", cancelOut)
	}
}

func TestClassifyPrefixCacheChangePinpointsExactDivergence(t *testing.T) {
	specs := []ToolSpec{{Name: "read", Description: "read file"}}
	m1 := []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "hi"}}}}
	m2 := append(append([]Message(nil), m1...), Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "hello"}}})
	mMut := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "mutated"}}},
		m2[1],
	}

	fp1 := computePrefixFingerprint("k1", "sys1", specs, m1, 10000)
	fpSame := computePrefixFingerprint("k1", "sys1", specs, m2, 500)
	if cause, shared := classifyPrefixCacheChange(fp1, fpSame); cause != "upstream_evicted_prefix_unchanged" || shared != 1 {
		t.Fatalf("expected upstream eviction with unchanged prefix, got cause=%q shared=%d", cause, shared)
	}

	fpSys := computePrefixFingerprint("k1", "sys2", specs, m2, 500)
	if cause, _ := classifyPrefixCacheChange(fp1, fpSys); cause != "system_changed" {
		t.Fatalf("expected system_changed, got %q", cause)
	}

	fpTools := computePrefixFingerprint("k1", "sys1", append(specs, ToolSpec{Name: "glob"}), m2, 500)
	if cause, _ := classifyPrefixCacheChange(fp1, fpTools); cause != "tools_changed" {
		t.Fatalf("expected tools_changed, got %q", cause)
	}

	fpHist := computePrefixFingerprint("k1", "sys1", specs, mMut, 500)
	if cause, shared := classifyPrefixCacheChange(fp1, fpHist); cause != "history_mutated_at=0/1" || shared != 0 {
		t.Fatalf("expected history_mutated_at=0/1, got cause=%q shared=%d", cause, shared)
	}
}
