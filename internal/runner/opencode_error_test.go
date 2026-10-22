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
	if !strings.Contains(got.Data.Message, "connection refused") {
		t.Fatalf("reason lost: %+v", got.Data)
	}
	if openCodeEngineError(nil) != nil {
		t.Fatal("nil error produced a failure")
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
