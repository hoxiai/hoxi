package llm

import (
	"strings"
	"time"
)

// FaultKind is the canonical protocol-level error classification shared across
// the Go runner, provider circuit breaker, and frontend auto-failover store.
type FaultKind string

const (
	FaultKindNone      FaultKind = ""
	FaultKindAborted   FaultKind = "aborted"
	FaultKindStalled   FaultKind = "stalled"
	FaultKindContext   FaultKind = "context"
	FaultKindPolicy    FaultKind = "policy"
	FaultKindModel     FaultKind = "model"
	FaultKindQuota     FaultKind = "quota"
	FaultKindRateLimit FaultKind = "rateLimit"
	FaultKindAuth      FaultKind = "auth"
	FaultKindServer    FaultKind = "server"
	FaultKindNetwork   FaultKind = "network"
	FaultKindBadReq    FaultKind = "badRequest"
)

// FaultDetails describes the structured classification of a turn or upstream
// LLM failure so clients do not need to re-parse free-form error text.
type FaultDetails struct {
	Kind         FaultKind `json:"kind,omitempty"`
	IsRetryable  bool      `json:"isRetryable,omitempty"`
	RetryAfterMs int64     `json:"retryAfterMs,omitempty"`
}

var (
	abortedFaultMarkers = []string{
		"interrupted",
		"aborted",
		"cancelled",
		"canceled",
		"context canceled",
		"operation was canceled",
	}
	contextFaultMarkers = []string{
		"context length",
		"context_length_exceeded",
		"context window",
		"context limit",
		"maximum context",
		"too many tokens",
		"token limit exceeded",
		"prompt is too long",
		"input is too long",
		"reduce the length",
		"上下文过长",
		"上下文超",
	}
	policyFaultMarkers = []string{
		"content policy",
		"content_policy_violation",
		"content filter",
		"content_filter",
		"content management",
		"safety system",
		"safety policy",
		"内容安全",
		"敏感内容",
		"违规",
	}
	modelFaultMarkers = []string{
		"model not found",
		"model_not_found",
		"unknown model",
		"is not loaded in the running engine",
		"does not expose model",
		"is not available for this account",
	}
	rateLimitFaultMarkers = []string{
		"rate limit",
		"rate_limit",
		"rate-limit",
		"too many requests",
		"resource_exhausted",
		"频率超限",
		"请求过于频繁",
		"触发限流",
		"限流",
	}
	authFaultMarkers = []string{
		"invalid api key",
		"invalid_api_key",
		"incorrect api key",
		"unauthorized",
		"authentication_error",
		"authentication failed",
		"permission_error",
		"permission denied",
		"permission check failed",
		"user denied permission",
		"access denied",
		"forbidden",
		"鉴权",
		"认证失败",
		"无效 key",
		"无效令牌",
	}
	networkFaultMarkers = []string{
		"timeout",
		"timed out",
		"etimedout",
		"i/o timeout",
		"tls handshake",
		"context deadline exceeded",
		"deadline exceeded",
		"connection reset",
		"econnreset",
		"connection refused",
		"econnrefused",
		"connection aborted",
		"econnaborted",
		"software caused connection abort",
		"connection closed",
		"connection terminated",
		"closed network connection",
		"closed idle connection",
		"broken pipe",
		"epipe",
		"enetunreach",
		"ehostunreach",
		"eai_again",
		"temporary failure in name resolution",
		"unexpected eof",
		"early eof",
		"read: eof",
		"write: eof",
		"socket hang up",
		"fetch failed",
		"network error",
		"stream was interrupted",
		"stream interrupted",
		"connection interrupted",
		"network interrupted",
		"unexpectedly interrupted",
		"client connection lost",
		"server sent goaway",
		"read tcp",
		"write tcp",
		"dial tcp",
		"请求超时",
		"连接超时",
		"响应超时",
		"连接失败",
		"连接中断",
		"连接重置",
		"网络异常",
		"网络错误",
	}
	serverFaultMarkers = []string{
		"overloaded",
		"overloaded_error",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
		"internal server error",
		"server_error",
		"server error",
		"temporarily unavailable",
		"upstream error",
		"服务繁忙",
		"服务暂不可用",
		"服务不可用",
		"服务器繁忙",
	}
	badReqFaultMarkers = []string{
		"invalid_request_error",
		"bad request",
	}
)

func hasAnyMarker(lower string, markers []string) bool {
	if lower == "" {
		return false
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func isNetworkFault(lowerMsg string, statusCode int) bool {
	return statusCode == 408 || hasAnyMarker(lowerMsg, networkFaultMarkers) ||
		strings.HasSuffix(lowerMsg, ": eof") || strings.Contains(lowerMsg, ": eof ") || lowerMsg == "eof"
}

// ClassifyFault derives a structured FaultDetails classification from an error
// code/name, free-form message, and optional HTTP status code.
func ClassifyFault(name, message string, statusCode int, now time.Time) FaultDetails {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	lowerMsg := strings.ToLower(strings.TrimSpace(message))

	var retryAfterMs int64
	if d := ParseRetryDelay(message, now, MaxResetWait); d > 0 {
		retryAfterMs = d.Milliseconds()
	}

	if lowerName == "runnerstallederror" || lowerName == "repeatedtoolfailureerror" ||
		strings.Contains(lowerMsg, "runnerstallederror") || strings.Contains(lowerMsg, "repeatedtoolfailureerror") {
		return FaultDetails{Kind: FaultKindStalled}
	}
	isNet := isNetworkFault(lowerMsg, statusCode)
	if lowerName == "messageabortederror" || lowerName == "aborterror" || (!isNet && hasAnyMarker(lowerMsg, abortedFaultMarkers)) {
		return FaultDetails{Kind: FaultKindAborted}
	}
	if lowerName == "contextoverflowerror" || lowerName == "messageoutputlengtherror" || hasAnyMarker(lowerMsg, contextFaultMarkers) {
		return FaultDetails{Kind: FaultKindContext}
	}
	if hasAnyMarker(lowerMsg, policyFaultMarkers) {
		return FaultDetails{Kind: FaultKindPolicy}
	}
	if lowerName == "providermodelnotfounderror" || hasAnyMarker(lowerMsg, modelFaultMarkers) {
		return FaultDetails{Kind: FaultKindModel}
	}

	isRate := statusCode == 429 || hasAnyMarker(lowerMsg, rateLimitFaultMarkers)
	if statusCode == 402 || IsQuotaExhausted(lowerMsg) || (isRate && retryAfterMs >= QuotaResetPromoteAfter.Milliseconds()) {
		return FaultDetails{Kind: FaultKindQuota, RetryAfterMs: retryAfterMs}
	}
	if isRate {
		return FaultDetails{Kind: FaultKindRateLimit, IsRetryable: true, RetryAfterMs: retryAfterMs}
	}
	if lowerName == "providerautherror" || statusCode == 401 || statusCode == 403 || hasAnyMarker(lowerMsg, authFaultMarkers) {
		return FaultDetails{Kind: FaultKindAuth}
	}
	if statusCode == 404 && lowerName != "notfounderror" {
		return FaultDetails{Kind: FaultKindModel}
	}
	if isNet {
		return FaultDetails{Kind: FaultKindNetwork, IsRetryable: true, RetryAfterMs: retryAfterMs}
	}
	if statusCode >= 500 || hasAnyMarker(lowerMsg, serverFaultMarkers) || transientStatusRe.MatchString(lowerMsg) {
		return FaultDetails{Kind: FaultKindServer, IsRetryable: true, RetryAfterMs: retryAfterMs}
	}
	if statusCode == 400 || statusCode == 413 || statusCode == 422 ||
		hasAnyMarker(lowerMsg, badReqFaultMarkers) || permanentStatusRe.MatchString(lowerMsg) {
		return FaultDetails{Kind: FaultKindBadReq}
	}
	if IsTransientTurnError(message) {
		return FaultDetails{Kind: FaultKindServer, IsRetryable: true, RetryAfterMs: retryAfterMs}
	}
	return FaultDetails{RetryAfterMs: retryAfterMs}
}
