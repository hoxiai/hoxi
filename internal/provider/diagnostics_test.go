package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

func TestResponseReadFailureDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("private partial output"))
	}))
	defer server.Close()
	id := "read-failure-" + t.Name()
	text, err := callSimpleLLM(context.Background(), llmCompletionRequest{BaseURL: server.URL, ProviderID: id, ModelID: "test-model", Prompt: "private prompt"})
	if err == nil || text != "" {
		t.Fatalf("partial response accepted: %q %v", text, err)
	}
	w := httptest.NewRecorder()
	diagnostics.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/hoxi/diagnostics?providerID="+id+"&limit=1", nil))
	var body struct {
		Entries []diagnostics.Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Entries) != 1 {
		t.Fatal(w.Body.String())
	}
	e := body.Entries[0]
	if e.Level != "ERROR" || e.Message != "internal request response read failed" || e.Status != 200 {
		t.Fatalf("%+v", e)
	}
	if strings.Contains(w.Body.String(), "private") {
		t.Fatal("response content leaked")
	}
}
