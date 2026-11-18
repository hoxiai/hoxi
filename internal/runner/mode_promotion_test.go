package runner

import (
	"fmt"
	"testing"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

func TestClassifyNativeAgentFailureRetainsRedactedUpstreamDetails(t *testing.T) {
	streamErr := &llm.StreamHTTPError{
		StatusCode: 429,
		RetryAfter: 9 * time.Second,
		Message:    "upstream rate limit exceeded",
		Upstream: &llm.UpstreamResponseDetails{
			StatusCode: 429,
			Protocol:   llm.ProtocolAnthropic,
			Endpoint:   "https://gateway.example.invalid/v1/messages",
			RequestID:  "req_9911",
			LatencyMs:  215,
			Headers: map[string]string{
				"x-request-id": "req_9911",
				"retry-after":  "9",
			},
			BodySnippet: `{"error":{"message":"rate limit"}}`,
		},
	}

	msgErr := classifyNativeAgentFailureError(fmt.Errorf("stream failed: %w", streamErr), false)
	if msgErr == nil || msgErr.Data == nil {
		t.Fatalf("expected classified MessageErrorData")
	}
	if msgErr.Data.StatusCode != 429 || msgErr.Data.RetryAfterMs != 9000 {
		t.Fatalf("expected status 429 and retryAfterMs 9000, got status=%d retry=%d", msgErr.Data.StatusCode, msgErr.Data.RetryAfterMs)
	}
	if msgErr.Data.Upstream == nil || msgErr.Data.Upstream.RequestID != "req_9911" {
		t.Fatalf("expected Upstream.RequestID req_9911, got %+v", msgErr.Data.Upstream)
	}

	mutatingErr := classifyNativeAgentFailureError(streamErr, true)
	if mutatingErr.Data.IsRetryable || mutatingErr.Data.Kind != ToolOutcomeUnknown {
		t.Fatalf("expected mutating side effect to disable retryability and mark outcome_unknown, got %+v", mutatingErr.Data)
	}
}

func TestApplyNativeModePromotionUpdatesStoreAndTranslator(t *testing.T) {
	workDir := t.TempDir()
	svc := NewService(workDir, nil, nil, t.TempDir())
	rec := SessionRecord{
		ID:           "ses_nat_promo_1",
		Runtime:      string(RuntimeNative),
		Mode:         "plan",
		ProjectDir:   workDir,
		WorktreePath: workDir,
		CreatedAt:    100,
		UpdatedAt:    100,
	}
	if err := svc.store.Put(rec); err != nil {
		t.Fatalf("store.Put: %v", err)
	}
	tr := NewTurnTranslator(rec.ID, string(RuntimeNative), "msg_a_1", "msg_u_1", "openai", "gpt-5", "plan", "")
	svc.applyNativeModePromotion(rec.ID, "build", tr)

	got, ok := svc.store.Meta(rec.ID)
	if !ok || got.Mode != "build" {
		t.Fatalf("expected persisted session Mode='build', got ok=%v mode=%q", ok, got.Mode)
	}
	if tr.MsgInfo.Mode != "build" || tr.MsgInfo.Agent != "build" {
		t.Fatalf("expected translator Mode/Agent='build', got mode=%q agent=%q", tr.MsgInfo.Mode, tr.MsgInfo.Agent)
	}
}
