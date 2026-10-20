package handoff

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgyModelsOutputAndNormalize(t *testing.T) {
	sample := `Available models (default: claude-opus-4-6-thinking):
  claude-opus-4-6-thinking        Claude Opus 4.6 (Thinking)
  claude-sonnet-4-6               Claude Sonnet 4.6
  gemini-3.1-pro-high             Gemini 3.1 Pro (High)
  gemini-3-flash                  Gemini 3 Flash
`
	models, displayBySlug, slugByDisplay := parseAgyModelsOutput(sample)
	if len(models) != 4 {
		t.Fatalf("expected 4 models, got %v", models)
	}
	if displayBySlug["claude-opus-4-6-thinking"] != "Claude Opus 4.6 (Thinking)" {
		t.Fatalf("unexpected display map: %v", displayBySlug)
	}
	if slugByDisplay["claude opus 4.6 (thinking)"] != "claude-opus-4-6-thinking" {
		t.Fatalf("unexpected reverse slug map: %v", slugByDisplay)
	}

	cases := map[string]string{
		"Claude Opus 4.6 (Thinking)": "claude-opus-4-6-thinking",
		"Claude Sonnet 4.6":          "claude-sonnet-4-6",
		"Gemini 3.1 Pro (High)":      "gemini-3.1-pro-high",
		"Gemini 3 Flash":             "gemini-3-flash",
		"GPT-OSS 120B (Medium)":      "gpt-oss-120b-medium",
	}
	for input, want := range cases {
		if got := normalizeAntigravityModelID(input); got != want {
			t.Errorf("normalizeAntigravityModelID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExtractAntigravityUserPromptDecimalModelNames(t *testing.T) {
	raw := "The user changed setting `Model Selection` to Claude Opus 5.5 (High).\n<USER_REQUEST>\nCheck why the session cannot continue\n</USER_REQUEST>"
	text, model := extractAntigravityUserPrompt(raw)
	if model != "claude-opus-5-5-high" {
		t.Fatalf("expected model 'claude-opus-5-5-high', got %q", model)
	}
	if text != "Check why the session cannot continue" {
		t.Fatalf("unexpected text: %q", text)
	}

	raw2 := "The user changed setting `Model Selection` to Gemini 3.8 Flash (High).\n<USER_REQUEST>Hello</USER_REQUEST>"
	_, model2 := extractAntigravityUserPrompt(raw2)
	if model2 != "gemini-3.8-flash-high" {
		t.Fatalf("expected model 'gemini-3.8-flash-high', got %q", model2)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGrokCLIBridgeHandlesNullBodyWithoutPanic(t *testing.T) {
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	if err := os.WriteFile(filepath.Join(grokHome, "auth.json"), []byte(`{"default":{"key":"grok-test-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	origTransport := grokBridgeTransport
	grokBridgeTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"invalid body"}`)),
		}, nil
	})
	t.Cleanup(func() {
		grokBridgeTransport = origTransport
	})

	svc := NewService(0, "", t.TempDir(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hoxi/bridge/grok-cli/v1/chat/completions", strings.NewReader("null"))
	svc.handleGrokCLIBridge(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 from stub upstream, got %d: %s", rec.Code, rec.Body.String())
	}
}
