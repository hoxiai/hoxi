package runner

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

var defaultTurnRetryDelays = []time.Duration{
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
}

const (
	maxTurnRetryDelay      = 15 * time.Second
	maxRetryStatusMsgRunes = 300
	turnResumePrompt       = "Continue from where you left off."
)

func (s *Service) cliTurnRetryDelays() []time.Duration {
	if s != nil && s.turnRetryDelays != nil {
		return s.turnRetryDelays
	}
	return defaultTurnRetryDelays
}

// isRetryableTurnError reports whether a turn error message indicates a
// transient network, timeout, rate-limit, or upstream server failure that
// should be retried automatically.
func isRetryableTurnError(errText string) bool {
	return llm.IsTransientTurnError(errText)
}

func extractFailedStreamResultError(line, fallback string) string {
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(line), &env) != nil {
		return strings.TrimSpace(fallback)
	}
	rawResult := strings.TrimSpace(string(env.Result))
	if strings.HasPrefix(rawResult, "\"") {
		var s string
		if json.Unmarshal(env.Result, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	} else if strings.HasPrefix(rawResult, "{") {
		var nested struct {
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
		}
		if json.Unmarshal(env.Result, &nested) == nil {
			if msg := firstNonEmptyStr(geminiErrorText(nested.Error), nested.Message); msg != "" {
				return msg
			}
		}
	}
	return firstNonEmptyStr(geminiErrorText(env.Error), env.Message, fallback)
}

func summarizeTurnRetryReason(errText string) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(errText)), " ")
	if clean == "" {
		return "Transient API request failure"
	}
	runes := []rune(clean)
	if len(runes) > maxRetryStatusMsgRunes {
		return string(runes[:maxRetryStatusMsgRunes]) + "..."
	}
	return clean
}

func parseTurnRetryDelay(errText string) time.Duration {
	return llm.ParseRetryDelay(errText, time.Now(), maxTurnRetryDelay)
}

func waitTurnRetryBackoff(ap *activeProcess, delay time.Duration) bool {
	if ap == nil || ap.wasAborted() || ap.stall.abortError() != nil {
		return false
	}
	if delay <= 0 {
		return !ap.wasAborted() && ap.stall.abortError() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ap.abortCh:
		return false
	case <-timer.C:
		return !ap.wasAborted() && ap.stall.abortError() == nil
	}
}

func isStreamResultLine(line string) bool {
	if !strings.Contains(line, `"result"`) {
		return false
	}
	var env struct {
		Type  string `json:"type"`
		Event string `json:"event"`
	}
	return json.Unmarshal([]byte(line), &env) == nil && (env.Type == "result" || env.Event == "result")
}

func isFailedStreamResultLine(line string) bool {
	if !isStreamResultLine(line) {
		return false
	}
	var env struct {
		Status  string          `json:"status"`
		IsError bool            `json:"is_error"`
		Error   json.RawMessage `json:"error"`
		Result  json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(line), &env) != nil {
		return false
	}
	if env.IsError || strings.EqualFold(env.Status, "error") || strings.EqualFold(env.Status, "failed") {
		return true
	}
	if len(env.Error) > 0 && string(env.Error) != "null" && string(env.Error) != `""` {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(string(env.Result)), "{") {
		var nested struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(env.Result, &nested) == nil {
			if strings.EqualFold(nested.Status, "error") || strings.EqualFold(nested.Status, "failed") {
				return true
			}
			if len(nested.Error) > 0 && string(nested.Error) != "null" && string(nested.Error) != `""` {
				return true
			}
		}
	}
	return false
}
