package runner

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

func TestOpenCodeEngineErrorFromHTTPError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		status    int
		body      string
		wantName  string
		wantMsg   string
		wantState int
	}{
		{
			name:      "generic failure keeps its own code",
			status:    500,
			body:      `{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_dfccf5fa"}}`,
			wantName:  "UnknownError",
			wantMsg:   "Unexpected server error. Check server logs for details.",
			wantState: 500,
		},
		{
			name:      "structured provider model not found",
			status:    500,
			body:      `{"name":"ProviderModelNotFoundError","data":{"message":"Model not found: subapi/QoderCN-DeepSeek-Flash."}}`,
			wantName:  "ProviderModelNotFoundError",
			wantMsg:   "Model not found: subapi/QoderCN-DeepSeek-Flash.",
			wantState: 500,
		},
		{
			name:      "generic code carries the real reason",
			status:    500,
			body:      `{"name":"UnknownError","data":{"message":"Model not found: subapi/Qoder-DeepSeek-Flash. Did you mean: DeepSeek-Flash?"}}`,
			wantName:  "ProviderModelNotFoundError",
			wantMsg:   "Model not found: subapi/Qoder-DeepSeek-Flash. Did you mean: DeepSeek-Flash?",
			wantState: 500,
		},
		{
			name:      "missing session",
			status:    404,
			body:      `{"name":"NotFoundError","data":{"message":"Session not found: ses_x"}}`,
			wantName:  "NotFoundError",
			wantMsg:   "Session not found: ses_x",
			wantState: 404,
		},
		{
			name:      "non json body keeps status only",
			status:    502,
			body:      "<html>bad gateway</html>",
			wantName:  "OpenCodeError",
			wantMsg:   "",
			wantState: 502,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := openCodeEngineError(&ocapi.HTTPError{Status: tc.status, Body: tc.body})
			if got == nil || got.Name != tc.wantName {
				t.Fatalf("%+v", got)
			}
			if got.Data == nil || got.Data.Message != tc.wantMsg || got.Data.StatusCode != tc.wantState {
				t.Fatalf("%+v", got.Data)
			}
		})
	}
}

func TestOpenCodeEngineErrorKeepsClassificationForDiagnostics(t *testing.T) {
	t.Parallel()
	failure := openCodeEngineError(&ocapi.HTTPError{
		Status: 500,
		Body:   `{"name":"UnknownError","data":{"message":"Model not found: subapi/QoderCN-DeepSeek-Flash."}}`,
	})
	e := sessionErrorDiagnostic("ses_x", failure)
	if e == nil || e.Status != 500 || e.Message != "model is not in the runtime model catalog" {
		t.Fatalf("%+v", e)
	}
}

func TestOpenCodeEngineErrorWithoutHTTPStatus(t *testing.T) {
	t.Parallel()
	got := openCodeEngineError(errors.New("dial tcp 127.0.0.1:4096: connect: connection refused"))
	if got == nil || got.Name != "OpenCodeError" || got.Data == nil || got.Data.StatusCode != 0 {
		t.Fatalf("%+v", got)
	}
	if got.Data.Kind != "network" || !got.Data.IsRetryable {
		t.Fatalf("expected structured network fault, got %+v", got.Data)
	}
	if !strings.Contains(got.Data.Message, "connection refused") {
		t.Fatalf("reason lost: %+v", got.Data)
	}
	if openCodeEngineError(nil) != nil {
		t.Fatal("nil error produced a failure")
	}
}

func TestNewClassifiedMessageErrorStructuredFault(t *testing.T) {
	t.Parallel()
	rateErr := newClassifiedMessageError("APIError", "Rate limit exceeded, please retry in 30s", 429)
	if rateErr.Data == nil || rateErr.Data.Kind != "rateLimit" || !rateErr.Data.IsRetryable || rateErr.Data.RetryAfterMs != 30_000 {
		t.Fatalf("unexpected rate limit fault details: %+v", rateErr.Data)
	}
	stallErr := newClassifiedMessageError(stallErrorName, "No output for 90s", 0)
	if stallErr.Data == nil || stallErr.Data.Kind != "stalled" || stallErr.Data.IsRetryable {
		t.Fatalf("unexpected stall fault details: %+v", stallErr.Data)
	}
}

func TestOpenCodeEngineErrorBoundsReason(t *testing.T) {
	t.Parallel()
	long := "Model not found: " + strings.Repeat("x", engineErrorMessageMax*2)
	got := openCodeEngineError(&ocapi.HTTPError{Status: 500, Body: fmt.Sprintf(`{"name":"UnknownError","data":{"message":%q}}`, long)})
	if got == nil || got.Data == nil || len(got.Data.Message) > engineErrorMessageMax {
		t.Fatalf("unbounded: %d", len(got.Data.Message))
	}
}

func TestIsMissingEngineSession(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&ocapi.HTTPError{Status: 404, Body: `{"name":"NotFoundError","data":{"message":"Session not found: ses_x"}}`}, true},
		{&ocapi.HTTPError{Status: 404, Body: `{"name":"NotFoundError","data":{"message":"Provider not found"}}`}, false},
		{&ocapi.HTTPError{Status: 500, Body: `{"name":"NotFoundError","data":{"message":"Session not found: ses_x"}}`}, false},
		{&ocapi.HTTPError{Status: 404, Body: `{"name":"SomethingElse","data":{"message":"no such route"}}`}, false},
		{errors.New("OpenCode HTTP 404: Session not found"), false},
		{nil, false},
	} {
		if got := isMissingEngineSession(tc.err); got != tc.want {
			t.Fatalf("%v: got %v want %v", tc.err, got, tc.want)
		}
	}
}

func TestIsEngineSessionPostServerFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "masked v1.19.3 hono unknown error 500",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_d43c8cf9"}}`},
			want: true,
		},
		{
			name: "explicit event seq unique constraint 500",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"EffectDrizzleQueryError","data":{"message":"SQLiteError: UNIQUE constraint failed: event.aggregate_id, event.seq"}}`},
			want: true,
		},
		{
			name: "model not found 500 must not rotate session",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"UnknownError","data":{"message":"Model not found: openai/gpt-missing."}}`},
			want: false,
		},
		{
			name: "structured provider model not found 500",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"ProviderModelNotFoundError","data":{"message":"Model not found: subapi/m"}}`},
			want: false,
		},
		{
			name: "auth failure 500",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"APIError","data":{"message":"Invalid API Key"}}`},
			want: false,
		},
		{
			name: "rate limit 500",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"APIError","data":{"message":"Rate limit exceeded"}}`},
			want: false,
		},
		{
			name: "quota 500",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"APIError","data":{"message":"Insufficient quota"}}`},
			want: false,
		},
		{
			name: "context overflow 500",
			err:  &ocapi.HTTPError{Status: 500, Body: `{"name":"APIError","data":{"message":"maximum context length is 128k"}}`},
			want: false,
		},
		{
			name: "404 session not found handled separately",
			err:  &ocapi.HTTPError{Status: 404, Body: `{"name":"NotFoundError","data":{"message":"Session not found: ses_x"}}`},
			want: false,
		},
		{
			name: "502 bad gateway is upstream proxy error",
			err:  &ocapi.HTTPError{Status: 502, Body: "<html>bad gateway</html>"},
			want: false,
		},
		{
			name: "network error without status",
			err:  errors.New("dial tcp 127.0.0.1:4096: connect: connection refused"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEngineSessionPostServerFailure(tc.err); got != tc.want {
				t.Fatalf("isEngineSessionPostServerFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
