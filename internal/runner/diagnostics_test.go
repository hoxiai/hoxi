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
	}{
		{"APIError", 429, "model provider rate limited"},
		{"ProviderAuthError", 0, "model provider authentication failed"},
		{"ContextOverflowError", 0, "model context limit exceeded"},
		{"MessageOutputLengthError", 0, "model output limit exceeded"},
		{"OpenCodeError", 0, "OpenCode execution failed"},
		{"ProcessError", 0, "runtime process failed"},
		{"APIError", 503, "model provider service failed"},
		{"APIError", 400, "model API request rejected"},
		{"private runtime output", 401, "model provider authentication failed"},
		{"private runtime output", 0, "session execution failed"},
	} {
		t.Run(tc.name+tc.message, func(t *testing.T) {
			for _, value := range []interface{}{
				&MessageError{Name: tc.name, Data: &MessageErrorData{Message: "private prompt", StatusCode: tc.code}},
				MessageError{Name: tc.name, Data: &MessageErrorData{Message: "private prompt", StatusCode: tc.code}},
				map[string]interface{}{"name": tc.name, "data": map[string]interface{}{"message": "private prompt", "statusCode": float64(tc.code)}},
			} {
				e := sessionErrorDiagnostic("s1", value)
				if e == nil || e.Kind != "request" || e.Level != "ERROR" || e.Message != tc.message || e.Status != tc.code {
					t.Fatalf("%+v", e)
				}
				b, _ := json.Marshal(e)
				if strings.Contains(string(b), "private") {
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
