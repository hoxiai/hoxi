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
	maxTurnRetryAttempts   = 3
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

func parseSessionStatus(raw interface{}) (SessionStatus, bool) {
	switch v := raw.(type) {
	case SessionStatus:
		return v, v.Type != ""
	case *SessionStatus:
		if v == nil {
			return SessionStatus{}, false
		}
		return *v, v.Type != ""
	case map[string]interface{}:
		data, err := json.Marshal(v)
		if err != nil {
			return SessionStatus{}, false
		}
		var st SessionStatus
		if json.Unmarshal(data, &st) != nil {
			return SessionStatus{}, false
		}
		return st, st.Type != ""
	default:
		return SessionStatus{}, false
	}
}

// shouldCutOffTurnRetry reports whether a runtime `session.status` retry event
// exceeds Hoxi's retry bounds (wait > 15s, attempt > 3, or a permanent/quota
// failure) and must be aborted immediately instead of sleeping.
func shouldCutOffTurnRetry(st SessionStatus, now time.Time) (*MessageError, bool) {
	if st.Type != "retry" {
		return nil, false
	}
	var waitMs int64
	if st.Next > 0 {
		if delta := st.Next - now.UnixMilli(); delta > 0 {
			waitMs = delta
		}
	}
	msg := strings.TrimSpace(st.Message)
	if hinted := llm.ParseRetryDelay(msg, now, 0).Milliseconds(); hinted > waitMs {
		waitMs = hinted
	}
	cutOff := st.Attempt > maxTurnRetryAttempts ||
		waitMs > maxTurnRetryDelay.Milliseconds() ||
		(msg != "" && llm.IsPermanentTurnError(msg))
	if !cutOff {
		return nil, false
	}
	return buildRetryCutoffError(st, waitMs, now), true
}

func buildRetryCutoffError(st SessionStatus, waitMs int64, now time.Time) *MessageError {
	msg := boundEngineText(st.Message)
	if msg == "" {
		msg = "Model request failed after retry limit"
	}
	fault := llm.ClassifyFault("APIError", msg, 0, now)
	retryAfterMs := fault.RetryAfterMs
	if waitMs > retryAfterMs {
		retryAfterMs = min(waitMs, llm.MaxResetWait.Milliseconds())
	}
	kind := fault.Kind
	statusCode := 0
	isRetryable := fault.IsRetryable
	switch {
	case retryAfterMs >= llm.QuotaResetPromoteAfter.Milliseconds():
		kind = llm.FaultKindQuota
		statusCode = 429
		isRetryable = false
	case retryAfterMs > maxTurnRetryDelay.Milliseconds() && (kind == "" || kind == llm.FaultKindServer || kind == llm.FaultKindRateLimit):
		kind = llm.FaultKindRateLimit
		statusCode = 429
		isRetryable = false
	case kind == llm.FaultKindQuota || kind == llm.FaultKindRateLimit:
		statusCode = 429
	}
	if st.Attempt > maxTurnRetryAttempts {
		isRetryable = false
	}
	return &MessageError{
		Name: "APIError",
		Data: &MessageErrorData{
			Message:      msg,
			StatusCode:   statusCode,
			IsRetryable:  isRetryable,
			Kind:         string(kind),
			RetryAfterMs: retryAfterMs,
		},
	}
}

func (s *Service) abortTurnAsync(ap *activeProcess, err *MessageError) {
	if ap == nil {
		return
	}
	ap.stall.mu.Lock()
	if ap.stall.abortErr == nil {
		ap.stall.abortErr = err
	}
	ap.stall.mu.Unlock()
	ap.notifyIdle()
	go s.abortActiveProcess(ap.sessionID)
}

func (s *Service) stampAbortedTurnErrorOnStore(sessionID string, abortErr *MessageError) {
	if s == nil || sessionID == "" || abortErr == nil {
		return
	}
	rec, ok := s.store.Get(sessionID)
	if !ok || len(rec.Turns) == 0 {
		return
	}
	last := rec.Turns[len(rec.Turns)-1].Info
	if last.Role != "assistant" {
		return
	}
	if last.Error != nil && !strings.Contains(strings.ToLower(last.Error.Name), "abort") {
		return
	}
	last.Error = abortErr
	if last.Finish == "" || last.Finish == "aborted" {
		last.Finish = "error"
	}
	if last.Time.Completed == 0 {
		last.Time.Completed = time.Now().UnixMilli()
	}
	s.broadcast(sessionID, SSEEvent{
		Type:       "message.updated",
		Properties: map[string]interface{}{"info": last},
	})
}
