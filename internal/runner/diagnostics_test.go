package runner

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionErrorDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		message string
		want    string
	}{
		{"APIError", 429, "private prompt", "model provider rate limited"},
		{"ProviderAuthError", 0, "private prompt", "model provider authentication failed"},
		{"ProviderModelNotFoundError", 500, "private prompt", "model is not in the runtime model catalog"},
		{"NotFoundError", 404, "private prompt", "runtime session not found"},
		{"ContextOverflowError", 0, "private prompt", "model context limit exceeded"},
		{"MessageOutputLengthError", 0, "private prompt", "model output limit exceeded"},
		{"OpenCodeError", 0, "private prompt", "OpenCode execution failed"},
		{"ProcessError", 0, "private prompt", "runtime process failed"},
		{"APIError", 503, "private prompt", "model provider service failed"},
		{"APIError", 400, "private prompt", "model API request rejected"},
		{"private runtime output", 401, "private prompt", "model provider authentication failed"},
		{"private runtime output", 0, "private prompt", "session execution failed"},
		// Engine builds disagree on the code for a missing model; the reason is
		// classified, never persisted.
		{"UnknownError", 500, "Model not found: subapi/QoderCN-DeepSeek-Flash.", "model is not in the runtime model catalog"},
	} {
		t.Run(tc.name+tc.message, func(t *testing.T) {
			for _, value := range []interface{}{
				&MessageError{Name: tc.name, Data: &MessageErrorData{Message: tc.message, StatusCode: tc.code}},
				MessageError{Name: tc.name, Data: &MessageErrorData{Message: tc.message, StatusCode: tc.code}},
				map[string]interface{}{"name": tc.name, "data": map[string]interface{}{"message": tc.message, "statusCode": float64(tc.code)}},
			} {
				e := sessionErrorDiagnostic("s1", value)
				if e == nil || e.Kind != "request" || e.Level != "ERROR" || e.Message != tc.want || e.Status != tc.code {
					t.Fatalf("%+v", e)
				}
				b, _ := json.Marshal(e)
				if strings.Contains(string(b), "private") || strings.Contains(string(b), "QoderCN") {
					t.Fatalf("leak: %s", b)
				}
			}
		})
	}
	for _, name := range []string{"AbortError", "MessageAbortedError"} {
		if sessionErrorDiagnostic("s", &MessageError{Name: name}) != nil {
			t.Fatal("abort recorded")
		}
		if sessionErrorDiagnostic("s", map[string]interface{}{"name": name}) != nil {
			t.Fatal("abort recorded")
		}
	}
}
