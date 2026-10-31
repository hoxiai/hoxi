package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
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

var (
	permanentStatusRe = regexp.MustCompile(`\b(?:http|status|statuscode|code)[:\s=]+(?:400|401|402|403|404|413|422)\b`)
	transientStatusRe = regexp.MustCompile(`\b(?:(?:http|status|statuscode|error|code)[:\s=]+(?:408|429|500|502|503|504|520|522|524|529)|(?:502|503|504|529)\s+(?:bad gateway|service unavailable|gateway timeout|overloaded)|429\s+too many requests)\b`)
	relativeRetryRe   = regexp.MustCompile(`(?:in|after)\s+(\d+(?:\.\d+)?)\s*(ms|milliseconds?|s|secs?|seconds?)\b`)
)

var nonRetryableMarkers = []string{
	"runnerstallederror",
	"repeatedtoolfailureerror",
	"context canceled",
	"operation was canceled",
	"context length",
	"context window",
	"context limit",
	"maximum context",
	"too many tokens",
	"prompt is too long",
	"reduce the length",
	"上下文过长",
	"上下文超",
	"content policy",
	"content filter",
	"content management",
	"safety system",
	"内容安全",
	"敏感内容",
	"invalid api key",
	"incorrect api key",
	"unauthorized",
	"authentication failed",
	"permission check failed",
	"user denied permission",
	"action blocked by",
	"model not found",
	"model_not_found",
	"unknown model",
	"is not available for this account",
	"does not support",
	"insufficient_quota",
	"insufficient quota",
	"exceeded your current quota",
	"insufficient balance",
	"insufficient_balance",
	"insufficient credits",
	"insufficient_credits",
	"insufficient credit",
	"insufficient_credit",
	"insufficient funds",
	"insufficient_funds",
	"credit balance",
	"out of credits",
	"payment required",
	"billing_hard_limit",
	"freeusagelimiterror",
	"均已限额",
	"额度已用完",
	"额度不足",
	"余额不足",
	"积分不足",
	"欠费",
	"使用量已超出",
}

var retryableTurnMarkers = []string{
	"timeout",
	"timed out",
	"etimedout",
	"i/o timeout",
	"tls handshake timeout",
	"context deadline exceeded",
	"deadline exceeded",
	"connection reset",
	"econnreset",
	"connection refused",
	"econnrefused",
	"connection aborted",
	"econnaborted",
	"connection closed",
	"use of closed network connection",
	"server closed idle connection",
	"broken pipe",
	"epipe",
	"enetunreach",
	"ehostunreach",
	"eai_again",
	"temporary failure in name resolution",
	"unexpected eof",
	"socket hang up",
	"fetch failed",
	"network error",
	"stream was interrupted",
	"stream error",
	"http2: client connection lost",
	"http2: server sent goaway",
	"read tcp",
	"write tcp",
	"dial tcp",
	"request failed: post ",
	"request failed: get ",
	"overloaded",
	"service unavailable",
	"bad gateway",
	"gateway timeout",
	"internal server error",
	"server error",
	"temporarily unavailable",
	"upstream error",
	"try again",
	"rate limit",
	"rate_limit",
	"too many requests",
	"请求超时",
	"连接超时",
	"响应超时",
	"连接失败",
	"连接中断",
	"连接重置",
	"网络异常",
	"网络错误",
	"服务繁忙",
	"服务不可用",
	"服务器繁忙",
	"请求过于频繁",
	"触发限流",
}

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
	trimmed := strings.TrimSpace(errText)
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, "interrupted") || strings.EqualFold(trimmed, "aborted") {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range nonRetryableMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	if permanentStatusRe.MatchString(lower) {
		return false
	}
	for _, marker := range retryableTurnMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.HasSuffix(lower, ": eof") || strings.Contains(lower, ": eof ") || lower == "eof" {
		return true
	}
	return transientStatusRe.MatchString(lower)
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
	m := relativeRetryRe.FindStringSubmatch(strings.ToLower(errText))
	if len(m) != 3 {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n <= 0 {
		return 0
	}
	unit := m[2]
	var d time.Duration
	if unit == "ms" || strings.HasPrefix(unit, "milli") {
		d = time.Duration(n * float64(time.Millisecond))
	} else {
		d = time.Duration(n * float64(time.Second))
	}
	if d > maxTurnRetryDelay {
		return maxTurnRetryDelay
	}
	return d
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

func buildTurnCLIArgs(runtime, mode, modelID, effort, externalID string, geminiUsesAgy bool, claudeHookArgs []string) []string {
	switch runtime {
	case string(RuntimeClaudeCode):
		return append(BuildClaudeArgs(mode, modelID, effort, externalID), claudeHookArgs...)
	case string(RuntimeGeminiCLI):
		if geminiUsesAgy {
			agyModel, agyEffort := ResolveGeminiAgyModelAndEffort(modelID, effort)
			return BuildAgyArgs(mode, agyModel, agyEffort, externalID)
		}
		return BuildGeminiArgs(mode, modelID, effort, externalID)
	case string(RuntimeCodeBuddy):
		return BuildCodeBuddyArgs(mode, modelID, effort, externalID)
	case string(RuntimeQoder):
		return BuildQoderArgs(mode, modelID, effort, externalID)
	default:
		return BuildAgyArgs(mode, modelID, effort, externalID)
	}
}

func (t *TurnTranslator) HasToolParts() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.partsByID {
		if p != nil && p.Type == "tool" {
			return true
		}
	}
	return len(t.subagentOrder) > 0 || len(t.claudeTasks) > 0 || len(t.childOrder) > 0
}

func (t *TurnTranslator) LastErrorText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.geminiLastErrMsg
}

// ResetUncommittedTurnForRetry clears any partial text/reasoning parts emitted
// before a retry when no tool calls have executed yet in this turn.
func (t *TurnTranslator) ResetUncommittedTurnForRetry(initialExternalID string) []SSEEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	var evts []SSEEvent
	for _, id := range t.partOrder {
		part := t.partsByID[id]
		if part == nil || (part.Type != "text" && part.Type != "reasoning") {
			continue
		}
		cleared := clonePart(*part)
		cleared.Text = ""
		evts = append(evts, SSEEvent{
			Type:       "message.part.updated",
			Properties: map[string]interface{}{"part": cleared},
		})
	}
	t.partsByID = make(map[string]*MessagePart)
	t.partOrder = nil
	t.stepStartMs = make(map[int]int64)
	t.agyStepUsage = make(map[string]agyUsage)
	t.claudeMsgUsage = make(map[string]claudeUsage)
	t.hasTextPart = false
	t.turnFinished = false
	t.externalID = initialExternalID
	t.convPinned = false
	t.editPayloads = nil
	t.geminiLastErrMsg = ""
	t.claudeMessageID = ""
	t.claudeReasonPartID = ""
	t.claudeTextPartID = ""
	t.claudeRetrying = false
	t.geminiReasonPartID = ""
	t.geminiTextPartID = ""
	return evts
}

// PrepareForResumeRetry settles any in-flight parts and resets active streaming
// cursors so a resumed retry CLI appends new steps to the same assistant turn.
func (t *TurnTranslator) PrepareForResumeRetry(nowMs int64) []SSEEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	evts := t.finishAgySubagents(true, nowMs)
	evts = append(evts, t.finishClaudeSubagents(true, nowMs)...)
	evts = append(evts, t.closeAllChildren(true, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.geminiReasonPartID, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.geminiTextPartID, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.claudeReasonPartID, nowMs)...)
	evts = append(evts, t.closeActiveGeminiPart(t.claudeTextPartID, nowMs)...)
	for _, id := range t.partOrder {
		part := t.partsByID[id]
		if part == nil {
			continue
		}
		if (part.Type == "text" || part.Type == "reasoning") && (part.Time == nil || part.Time.End == 0) {
			start := nowMs
			if part.Time != nil && part.Time.Start > 0 {
				start = part.Time.Start
			}
			part.Time = &PartTime{Start: start, End: nowMs}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		} else if part.Type == "tool" && part.Tool != "task" && part.State != nil &&
			(part.State.Status == "pending" || part.State.Status == "running") {
			start := nowMs
			if part.State.Time != nil && part.State.Time.Start > 0 {
				start = part.State.Time.Start
			}
			part.State.Status = "error"
			if strings.TrimSpace(part.State.Error) == "" {
				part.State.Error = "Interrupted before completion"
			}
			part.State.Time = &PartTime{Start: start, End: nowMs}
			evts = append(evts, SSEEvent{Type: "message.part.updated", Properties: map[string]interface{}{"part": clonePart(*part)}})
		}
	}
	t.turnFinished = false
	t.geminiStepIdx++
	t.geminiReasonPartID = ""
	t.geminiTextPartID = ""
	t.geminiLastErrMsg = ""
	t.claudeMessageID = ""
	t.claudeReasonPartID = ""
	t.claudeTextPartID = ""
	t.claudeRetrying = false
	return evts
}

func (ap *activeProcess) shouldRelayOpenCodeStatus(raw interface{}) bool {
	if ap == nil {
		return false
	}
	var stType string
	switch v := raw.(type) {
	case SessionStatus:
		stType = v.Type
	case map[string]interface{}:
		stType, _ = v["type"].(string)
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if stType == "retry" {
		ap.retrying = true
		return true
	}
	if stType == "busy" && ap.retrying {
		ap.retrying = false
		return true
	}
	return false
}

func (s *Service) runRetrySubprocess(
	rec SessionRecord,
	rawBinPath string,
	retryArgs []string,
	procEnv []string,
	retryInput []byte,
	ap *activeProcess,
	stderrBuf *bytes.Buffer,
	emitBusy bool,
	feedStdout func(*os.File) <-chan struct{},
) (bool, error) {
	retryBin, wrappedArgs, wrapErr := s.confineRuntimeCommand(rec, rawBinPath, retryArgs)
	if wrapErr != nil {
		return false, nil
	}
	retryCmd := subproc.Command(retryBin, wrappedArgs...)
	retryCmd.Dir = rec.WorktreePath
	retryCmd.Env = procEnv
	retryStdin, inErr := retryCmd.StdinPipe()
	if inErr != nil {
		return false, nil
	}
	retryOutR, retryOutW, pipeErr := os.Pipe()
	if pipeErr != nil {
		_ = retryStdin.Close()
		return false, nil
	}
	var retryStderr bytes.Buffer
	retryCmd.Stdout = retryOutW
	retryCmd.Stderr = &retryStderr
	retryCmd.WaitDelay = orphanDrainGrace
	setProcessGroup(retryCmd)
	if startErr := retryCmd.Start(); startErr != nil {
		_ = retryStdin.Close()
		_ = retryOutR.Close()
		_ = retryOutW.Close()
		return false, nil
	}
	_ = retryOutW.Close()
	stderrBuf.Reset()
	ap.mu.Lock()
	ap.cmd = retryCmd
	ap.stdin = retryStdin
	ap.mu.Unlock()
	s.noteActivity(ap)
	if emitBusy {
		s.broadcast(rec.ID, SSEEvent{
			Type: "session.status",
			Properties: map[string]interface{}{
				"sessionID": rec.ID,
				"status":    SessionStatus{Type: "busy"},
			},
		})
	}
	_, _ = retryStdin.Write(retryInput)
	_ = retryStdin.Close()

	retryReaderDone := feedStdout(retryOutR)
	waitErr := retryCmd.Wait()
	reapOrphanedOutput(retryCmd.Process, retryOutR, retryReaderDone)
	if retryStderr.Len() > 0 {
		*stderrBuf = retryStderr
	}
	return true, waitErr
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
