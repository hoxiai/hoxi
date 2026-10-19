package diagnostics

// SessionError classifies structured runtime error metadata, never free-form
// error text. Unknown names are not retained. Cancellations produce no entry.
func SessionError(name string, status int) *Entry {
	if name == "MessageAbortedError" || name == "AbortError" {
		return nil
	}
	message := "session execution failed"
	switch name {
	case "APIError":
		message = "model API request failed"
	case "ProviderAuthError":
		message = "model provider authentication failed"
	case "ContextOverflowError":
		message = "model context limit exceeded"
	case "MessageOutputLengthError":
		message = "model output limit exceeded"
	case "OpenCodeError":
		message = "OpenCode execution failed"
	case "ProcessError":
		message = "runtime process failed"
	}
	if status < 100 || status > 599 {
		status = 0
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
