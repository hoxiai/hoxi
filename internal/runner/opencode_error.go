package runner

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/opencode/ocapi"
)

const (
	engineErrorNameMax    = 64
	engineErrorMessageMax = 400
)

// engineBodyError mirrors the engine's structured error envelope. Only the
// stable code and the human-readable reason are read; anything else (refs,
// stacks, echoed content) is ignored.
type engineBodyError struct {
	Name string `json:"name"`
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
}

// openCodeEngineError turns a failed engine call into structured metadata: the
// engine's own error code plus its HTTP status. Diagnostics keep only that
// classification with provider/model; the raw body is never persisted.
func openCodeEngineError(err error) *MessageError {
	if err == nil {
		return nil
	}
	var httpErr *ocapi.HTTPError
	if !errors.As(err, &httpErr) {
		return &MessageError{Name: "OpenCodeError", Data: &MessageErrorData{Message: boundEngineText(err.Error())}}
	}
	name, message := parseEngineBodyError(httpErr.Body)
	if name == "" {
		name = "OpenCodeError"
	}
	return &MessageError{
		Name: name,
		Data: &MessageErrorData{Message: message, StatusCode: httpErr.Status},
	}
}

// parseEngineBodyError reads the stable code and bounded reason from the engine
// error body. Engine builds disagree on the code for a missing model (some emit
// ProviderModelNotFoundError, others a generic UnknownError), so the name is
// normalized through the same classifier diagnostics uses.
func parseEngineBodyError(body string) (string, string) {
	var parsed engineBodyError
	if json.Unmarshal([]byte(body), &parsed) != nil {
		return "", ""
	}
	message := boundEngineText(parsed.Data.Message)
	name := diagnostics.ClassifyErrorName(parsed.Name, message)
	if !isEngineErrorName(name) {
		name = ""
	}
	return name, message
}

// isMissingEngineSession reports whether a turn failed because the backing
// engine session vanished (it was deleted, or the daemon lost it). Only a
// structured NotFoundError on the session-scoped route counts; free-form text
// never triggers a rebuild.
func isMissingEngineSession(err error) bool {
	var httpErr *ocapi.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.Status != 404 {
		return false
	}
	name, message := parseEngineBodyError(httpErr.Body)
	if name != "NotFoundError" {
		return false
	}
	message = strings.ToLower(strings.TrimSpace(message))
	return message == "" || strings.Contains(message, "session")
}

func isEngineErrorName(name string) bool {
	if name == "" || len(name) > engineErrorNameMax || !strings.HasSuffix(name, "Error") {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// boundEngineText keeps engine text displayable without letting a single error
// field grow unbounded; control characters collapse to spaces.
func boundEngineText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, text)
	if len(text) > engineErrorMessageMax {
		text = text[:engineErrorMessageMax]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return strings.TrimSpace(text)
}
