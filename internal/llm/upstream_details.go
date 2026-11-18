package llm

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxUpstreamSnippetBytes = 768

// Safe Diagnostic Headers allowlisted for retention on upstream errors/degradations.
// Auth, cookie, session, and account-identifying headers are never included.
var safeUpstreamHeaderAllowlist = []string{
	"x-request-id",
	"request-id",
	"anthropic-request-id",
	"cf-ray",
	"openai-processing-ms",
	"retry-after",
	"retry-after-ms",
	"x-ratelimit-limit-requests",
	"x-ratelimit-remaining-requests",
	"x-ratelimit-reset-requests",
	"x-ratelimit-limit-tokens",
	"x-ratelimit-remaining-tokens",
	"x-ratelimit-reset-tokens",
	"x-error-code",
	"x-upstream-status",
	"server",
}

var inlineTokenScrubRe = regexp.MustCompile(`(?i)(?:bearer\s+[A-Za-z0-9._\-+/=]{8,}|(?:sk|hk|rk|pk|ghp|gho|AIza)[-_A-Za-z0-9]{12,}|([?&](?:key|api_key|token|access_token)=)[^&\s"']+)`)

// UpstreamResponseDetails retains redacted diagnostic metadata from an upstream
// model gateway or relay response without exposing credentials or raw prompts.
type UpstreamResponseDetails struct {
	StatusCode  int               `json:"statusCode,omitempty"`
	Protocol    StreamProtocol    `json:"protocol,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	RequestID   string            `json:"requestId,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	LatencyMs   int64             `json:"latencyMs,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodySnippet string            `json:"bodySnippet,omitempty"`
}

// SanitizeEndpointForDiagnostics strips userinfo, query parameters, and fragments
// from rawURL so URL-embedded keys (e.g. ?key=...) never enter logs or transcripts.
func SanitizeEndpointForDiagnostics(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// ScrubDiagnosticText redacts explicit caller secrets and common bearer/API key
// patterns from upstream error snippets or header values.
func ScrubDiagnosticText(s string, secrets ...string) string {
	out := strings.TrimSpace(s)
	if out == "" {
		return ""
	}
	for _, sec := range secrets {
		trimmed := strings.TrimSpace(sec)
		if len(trimmed) >= 4 {
			out = strings.ReplaceAll(out, trimmed, "[REDACTED]")
		}
	}
	out = inlineTokenScrubRe.ReplaceAllStringFunc(out, func(m string) string {
		if idx := strings.IndexByte(m, '='); idx >= 0 && (m[0] == '?' || m[0] == '&') {
			return m[:idx+1] + "[REDACTED]"
		}
		return "[REDACTED]"
	})
	return out
}

// BuildRedactedUpstreamDetails constructs a sanitized UpstreamResponseDetails
// snapshot from an HTTP response status, headers, raw body, and latency.
func BuildRedactedUpstreamDetails(
	statusCode int,
	proto StreamProtocol,
	endpointURL string,
	hdr http.Header,
	rawBody []byte,
	latencyMs int64,
	secrets ...string,
) *UpstreamResponseDetails {
	det := &UpstreamResponseDetails{
		StatusCode: statusCode,
		Protocol:   proto,
		Endpoint:   SanitizeEndpointForDiagnostics(endpointURL),
		LatencyMs:  latencyMs,
	}
	if hdr != nil {
		if ct := strings.TrimSpace(hdr.Get("Content-Type")); ct != "" {
			det.ContentType = ScrubDiagnosticText(ct, secrets...)
		}
		safeHdrs := make(map[string]string)
		for _, k := range safeUpstreamHeaderAllowlist {
			if val := strings.TrimSpace(hdr.Get(k)); val != "" {
				scrubbed := ScrubDiagnosticText(val, secrets...)
				if len(scrubbed) > 160 {
					scrubbed = scrubbed[:160]
				}
				safeHdrs[k] = scrubbed
			}
		}
		for _, reqKey := range []string{"x-request-id", "request-id", "anthropic-request-id", "cf-ray"} {
			if v := safeHdrs[reqKey]; v != "" {
				det.RequestID = v
				break
			}
		}
		if len(safeHdrs) > 0 {
			det.Headers = safeHdrs
		}
	}
	if len(rawBody) > 0 {
		snippet := ScrubDiagnosticText(string(rawBody), secrets...)
		if len(snippet) > maxUpstreamSnippetBytes {
			cut := maxUpstreamSnippetBytes
			for cut > 0 && !utf8.RuneStart(snippet[cut]) {
				cut--
			}
			snippet = snippet[:cut] + "...[truncated]"
		}
		det.BodySnippet = snippet
	}
	return det
}
