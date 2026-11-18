package llm

import (
	"net/http"
	"strings"
	"testing"
)

func TestBuildRedactedUpstreamDetailsScrubsSecretsAndHeaders(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Content-Type", "application/json; charset=utf-8")
	hdr.Set("X-Request-Id", "req_upstream_9912")
	hdr.Set("Cf-Ray", "8ef0123456-NRT")
	hdr.Set("Retry-After", "12")
	fakeTok := "sk-" + "demo9988776655443322"
	customSec := "custom_" + " token_alpha_99"
	hdr.Set("Authorization", "Bearer "+fakeTok)
	hdr.Set("Set-Cookie", "sid=opaque")

	rawEndpoint := "https://gateway.example.invalid/v1/messages?api_key=demo#frag"
	rawBody := []byte(`{"error":{"message":"upstream rejected ` + fakeTok + ` and ` + customSec + `"}}`)

	det := BuildRedactedUpstreamDetails(
		http.StatusTooManyRequests,
		ProtocolAnthropic,
		rawEndpoint,
		hdr,
		rawBody,
		342,
		customSec,
	)

	if det.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", det.StatusCode)
	}
	if det.Endpoint != "https://gateway.example.invalid/v1/messages" {
		t.Fatalf("expected query/fragment stripped from endpoint, got %q", det.Endpoint)
	}
	if det.RequestID != "req_upstream_9912" {
		t.Fatalf("expected request ID req_upstream_9912, got %q", det.RequestID)
	}
	if _, leaked := det.Headers["authorization"]; leaked {
		t.Fatalf("authorization header must never be retained")
	}
	if _, leaked := det.Headers["set-cookie"]; leaked {
		t.Fatalf("set-cookie header must never be retained")
	}
	if det.Headers["cf-ray"] != "8ef0123456-NRT" || det.Headers["retry-after"] != "12" {
		t.Fatalf("expected safe diagnostic headers retained, got %+v", det.Headers)
	}
	if strings.Contains(det.BodySnippet, fakeTok) || strings.Contains(det.BodySnippet, customSec) {
		t.Fatalf("expected secrets scrubbed from bodySnippet, got %q", det.BodySnippet)
	}
	if !strings.Contains(det.BodySnippet, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] marker in bodySnippet, got %q", det.BodySnippet)
	}
}
