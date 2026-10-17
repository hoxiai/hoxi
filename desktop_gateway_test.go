package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsAllowedLocalOrigin(t *testing.T) {
	allowed := []string{
		"wails://wails",
		"wails://wails.localhost",
		"http://localhost:5173",
		"http://127.0.0.1:8080",
		"http://[::1]:3000",
		"https://wails.localhost",
		"http://app.wails.localhost",
	}
	for _, origin := range allowed {
		if !isAllowedLocalOrigin(origin) {
			t.Errorf("expected origin %q to be allowed", origin)
		}
	}

	rejected := []string{
		"",
		"https://evil.com",
		"http://127.0.0.1.evil.com",
		"http://localhost.attacker.org",
		"https://notwails.localhost.evil.com",
		"javascript:alert(1)",
	}
	for _, origin := range rejected {
		if isAllowedLocalOrigin(origin) {
			t.Errorf("expected origin %q to be rejected", origin)
		}
	}
}

func TestGatewayCORSWhitelistEnforcement(t *testing.T) {
	gw := newDesktopGateway(0, "test-pass", "/tmp", nil, nil)

	// Disallowed origin preflight should receive 403 Forbidden and no CORS reflection
	reqEvil := httptest.NewRequest(http.MethodOptions, "/global/health", nil)
	reqEvil.Header.Set("Origin", "https://evil.example.com")
	recEvil := httptest.NewRecorder()
	gw.ServeHTTP(recEvil, reqEvil)

	if recEvil.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed origin preflight, got %d", recEvil.Code)
	}
	if got := recEvil.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected empty Access-Control-Allow-Origin for evil origin, got %q", got)
	}

	// Allowed local origin preflight should receive 204 and reflected local origin
	reqLocal := httptest.NewRequest(http.MethodOptions, "/global/health", nil)
	reqLocal.Header.Set("Origin", "http://localhost:5173")
	recLocal := httptest.NewRecorder()
	gw.ServeHTTP(recLocal, reqLocal)

	if recLocal.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for allowed origin preflight, got %d", recLocal.Code)
	}
	if got := recLocal.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected Access-Control-Allow-Origin to be http://localhost:5173, got %q", got)
	}
}

type chunkedStringReader struct {
	chunks []string
	idx    int
}

func (r *chunkedStringReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.idx])
	r.chunks[r.idx] = r.chunks[r.idx][n:]
	if len(r.chunks[r.idx]) == 0 {
		r.idx++
	}
	return n, nil
}

func (r *chunkedStringReader) Close() error { return nil }

func TestSSETapReadCloserReassemblesLinesWithoutAllocationScanner(t *testing.T) {
	src := &chunkedStringReader{
		chunks: []string{
			"event: message\r\nda",
			"ta: {\"type\":\"session.status\"}\n",
			"\n",
			"data: {\"type\":\"session.idle\"}\r\n",
		},
	}

	var lines []string
	tap := &sseTapReadCloser{
		rc: src,
		onLine: func(line string) {
			lines = append(lines, line)
		},
	}

	fullBytes, err := io.ReadAll(tap)
	if err != nil {
		t.Fatalf("unexpected ReadAll error: %v", err)
	}
	if !strings.Contains(string(fullBytes), "session.status") {
		t.Fatalf("expected full stream bytes to be preserved, got %q", string(fullBytes))
	}

	want := []string{
		"event: message",
		`data: {"type":"session.status"}`,
		"",
		`data: {"type":"session.idle"}`,
	}
	if len(lines) != len(want) {
		t.Fatalf("expected %d lines, got %d (%v)", len(want), len(lines), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestParseOpenAIModelsPayloadAndExtractAPIError(t *testing.T) {
	raw := []byte(`{"data":[{"id":"gpt-4o","name":"GPT-4o"},{"id":"claude-3-7-sonnet"},{"id":"gpt-4o"}]}`)
	models := parseOpenAIModelsPayload(raw)
	if len(models) != 2 {
		t.Fatalf("expected 2 deduplicated models, got %d", len(models))
	}
	if models[0].ID != "claude-3-7-sonnet" || models[1].ID != "gpt-4o" {
		t.Errorf("unexpected sorted models: %+v", models)
	}

	errMsg := extractAPIErrorMessage(401, []byte(`{"error":{"message":"Invalid API key provided"}}`))
	if errMsg != "HTTP 401: Invalid API key provided" {
		t.Errorf("unexpected error message: %q", errMsg)
	}
}
