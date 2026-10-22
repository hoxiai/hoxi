package diagnostics

import "strings"

// ClassifyErrorName maps engine errors whose own code is too generic onto a
// stable classification. reason is only inspected, never persisted, so
// free-form provider text never reaches the log.
func ClassifyErrorName(name, reason string) string {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if strings.HasPrefix(lower, "model not found") || strings.Contains(lower, "is not loaded in the running engine") {
		return "ProviderModelNotFoundError"
	}
	return strings.TrimSpace(name)
}

// SessionError classifies structured runtime error metadata, never free-form
// error text. Unknown names are not retained. Cancellations produce no entry.
func SessionError(name, reason string, status int) *Entry {
	name = ClassifyErrorName(name, reason)
	if name == "MessageAbortedError" || name == "AbortError" {
		return nil
	}
	message := "session execution failed"
	// Codes whose meaning is already precise; a coarse HTTP bucket would only
	// blur it (a missing model is not a provider outage).
	precise := false
	switch name {
	case "APIError":
		message = "model API request failed"
	case "ProviderAuthError":
		message = "model provider authentication failed"
	case "ProviderModelNotFoundError":
		message = "model is not in the runtime model catalog"
		precise = true
	case "NotFoundError":
		message = "runtime session not found"
		precise = true
	case "ContextOverflowError":
		message = "model context limit exceeded"
	case "MessageOutputLengthError":
		message = "model output limit exceeded"
	case "OpenCodeError":
		message = "OpenCode execution failed"
		precise = true
	case "UnknownError", "EngineRequestError":
		message = "runtime engine request failed"
		precise = true
	case "ProcessError":
		message = "runtime process failed"
		precise = true
	}
	if status < 100 || status > 599 {
		status = 0
	}
	if precise {
		return &Entry{Kind: "request", Level: "ERROR", Message: message, Status: status}
	}
	switch {
	case status == 401 || status == 403:
		message = "model provider authentication failed"
	case status == 429:
		message = "model provider rate limited"
	case status >= 500:
		message = "model provider service failed"
	case status >= 400:
		message = "model API request rejected"
	}
	return &Entry{Kind: "request", Level: "ERROR", Message: message, Status: status}
}
