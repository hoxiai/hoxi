package llm

import (
	"testing"
	"time"
)

func TestClassifyFault(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 30, 13, 24, 0, 0, time.FixedZone("CST", 8*3600))

	cases := []struct {
		name      string
		errName   string
		message   string
		status    int
		wantKind  FaultKind
		wantRetry bool
		wantMinMs int64
	}{
		{
			name:     "stalled turn",
			errName:  "RunnerStalledError",
			message:  "turn stalled after 90s",
			wantKind: FaultKindStalled,
		},
		{
			name:     "aborted turn",
			errName:  "MessageAbortedError",
			message:  "Aborted",
			wantKind: FaultKindAborted,
		},
		{
			name:     "context overflow",
			errName:  "APIError",
			message:  "This model's maximum context length is 128000 tokens",
			status:   400,
			wantKind: FaultKindContext,
		},
		{
			name:     "content policy",
			errName:  "APIError",
			message:  "Request blocked by content policy filter",
			status:   400,
			wantKind: FaultKindPolicy,
		},
		{
			name:     "model not found",
			errName:  "ProviderModelNotFoundError",
			message:  "Model not found: foo/bar",
			status:   500,
			wantKind: FaultKindModel,
		},
		{
			name:      "short rate limit 429",
			errName:   "APIError",
			message:   "Too Many Requests, please try again in 20s",
			status:    429,
			wantKind:  FaultKindRateLimit,
			wantRetry: true,
			wantMinMs: 20_000,
		},
		{
			name:      "long reset 429 promoted to quota",
			errName:   "APIError",
			message:   "429: 您的使用量已超出频率限制，将在 2026-10-30 22:48:16 UTC+8 重置",
			status:    429,
			wantKind:  FaultKindQuota,
			wantRetry: false,
			wantMinMs: 3600_000,
		},
		{
			name:     "auth error 401",
			errName:  "APIError",
			message:  "Invalid API key provided",
			status:   401,
			wantKind: FaultKindAuth,
		},
		{
			name:      "network reset",
			errName:   "RunnerError",
			message:   "read tcp 10.0.0.1:443: read: connection reset by peer",
			wantKind:  FaultKindNetwork,
			wantRetry: true,
		},
		{
			name:      "server 502",
			errName:   "APIError",
			message:   "502 Bad Gateway",
			status:    502,
			wantKind:  FaultKindServer,
			wantRetry: true,
		},
		{
			name:     "missing session 404 is not model fault",
			errName:  "NotFoundError",
			message:  "Session not found: ses_123",
			status:   404,
			wantKind: FaultKindNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFault(tc.errName, tc.message, tc.status, now)
			if got.Kind != tc.wantKind {
				t.Fatalf("Kind = %q, want %q (got %+v)", got.Kind, tc.wantKind, got)
			}
			if got.IsRetryable != tc.wantRetry {
				t.Fatalf("IsRetryable = %v, want %v (got %+v)", got.IsRetryable, tc.wantRetry, got)
			}
			if tc.wantMinMs > 0 && got.RetryAfterMs < tc.wantMinMs {
				t.Fatalf("RetryAfterMs = %d, want >= %d", got.RetryAfterMs, tc.wantMinMs)
			}
		})
	}
}
