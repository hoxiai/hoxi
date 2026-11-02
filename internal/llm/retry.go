package llm

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxResetWait caps a stated reset time so a typo or far-future date never
	// benches a model for longer than a day.
	MaxResetWait = 24 * time.Hour
	// QuotaResetPromoteAfter treats a reset this far away as an exhausted quota
	// rather than a short rate limit.
	QuotaResetPromoteAfter = 10 * time.Minute
)

var (
	resetAbsoluteRe = regexp.MustCompile(
		`(\d{4}-\d{2}-\d{2})[ t](\d{2}:\d{2}(?::\d{2})?)(?:\.\d+)?\s*(z|(?:utc|gmt)?\s*[+-]\d{1,2}(?::?\d{2})?|utc|gmt)?`)
	resetRelativeRe = regexp.MustCompile(
		`(?:retry[-_\s]+after|try\s+again\s+in|reset(?:s|\s+in)?|in|after)\s*[:=]?\s*(\d+(?:\.\d+)?)\s*(ms|milliseconds?|s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?)\b`)
	resetRelativeZhRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(毫秒|秒|分钟|小时)\s*后`)
	resetWordRe       = regexp.MustCompile(`重置|重试|恢复|reset|retry|try again|available|until`)

	permanentStatusRe = regexp.MustCompile(`\b(?:http|status|statuscode|code)[:\s=]+(?:400|401|402|403|404|413|422)\b`)
	transientStatusRe = regexp.MustCompile(`\b(?:(?:http|status|statuscode|error|code)[:\s=]+(?:408|429|500|502|503|504|520|522|524|529)|(?:502|503|504|529)\s+(?:bad gateway|service unavailable|gateway timeout|overloaded)|429\s+too many requests)\b`)
)

// ParseResetTime returns when an upstream error says a rate limit or quota
// lifts. Only future timestamps or positive relative durations accompanied by
// reset/retry wording are trusted.
func ParseResetTime(reason string, now time.Time) (time.Time, bool) {
	text := strings.ToLower(reason)
	if !resetWordRe.MatchString(text) {
		return time.Time{}, false
	}
	if m := resetAbsoluteRe.FindStringSubmatch(text); m != nil {
		if at, ok := parseResetAbsolute(m[1], m[2], m[3], now); ok && at.After(now) {
			return at, true
		}
	}
	if m := resetRelativeRe.FindStringSubmatch(text); m != nil {
		if d, ok := parseResetDuration(m[1], m[2]); ok {
			return now.Add(d), true
		}
	}
	if m := resetRelativeZhRe.FindStringSubmatch(text); m != nil {
		if d, ok := parseResetDuration(m[1], m[2]); ok {
			return now.Add(d), true
		}
	}
	return time.Time{}, false
}

// ParseRetryDelay extracts a relative or absolute retry delay from an error
// message, capped at maxWait when maxWait > 0.
func ParseRetryDelay(reason string, now time.Time, maxWait time.Duration) time.Duration {
	text := strings.ToLower(reason)
	if m := resetRelativeRe.FindStringSubmatch(text); m != nil {
		if d, ok := parseResetDuration(m[1], m[2]); ok {
			if maxWait > 0 && d > maxWait {
				return maxWait
			}
			return d
		}
	}
	if m := resetRelativeZhRe.FindStringSubmatch(text); m != nil {
		if d, ok := parseResetDuration(m[1], m[2]); ok {
			if maxWait > 0 && d > maxWait {
				return maxWait
			}
			return d
		}
	}
	at, ok := ParseResetTime(reason, now)
	if !ok {
		return 0
	}
	d := at.Sub(now)
	if d <= 0 {
		return 0
	}
	if maxWait > 0 && d > maxWait {
		return maxWait
	}
	return d
}

func parseResetAbsolute(date, clock, zone string, now time.Time) (time.Time, bool) {
	layout := "2006-01-02 15:04"
	if strings.Count(clock, ":") == 2 {
		layout = "2006-01-02 15:04:05"
	}
	loc := now.Location()
	zone = strings.TrimSpace(zone)
	if zone != "" {
		offset, ok := parseResetZoneOffset(zone)
		if !ok {
			return time.Time{}, false
		}
		loc = time.FixedZone("", offset)
	}
	at, err := time.ParseInLocation(layout, date+" "+clock, loc)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

func parseResetZoneOffset(zone string) (int, bool) {
	zone = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(zone, "utc"), "gmt"))
	if zone == "" || zone == "z" {
		return 0, true
	}
	sign := 1
	switch zone[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	zone = strings.ReplaceAll(zone[1:], ":", "")
	hours, minutes := zone, ""
	if len(zone) > 2 {
		hours, minutes = zone[:len(zone)-2], zone[len(zone)-2:]
	}
	h, err := strconv.Atoi(hours)
	if err != nil || h > 14 {
		return 0, false
	}
	m := 0
	if minutes != "" {
		if m, err = strconv.Atoi(minutes); err != nil || m > 59 {
			return 0, false
		}
	}
	return sign * (h*3600 + m*60), true
}

func parseResetDuration(value, unit string) (time.Duration, bool) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	var base time.Duration
	switch {
	case unit == "ms" || unit == "毫秒" || strings.HasPrefix(unit, "milli"):
		base = time.Millisecond
	case unit == "秒" || strings.HasPrefix(unit, "s"):
		base = time.Second
	case unit == "分钟" || strings.HasPrefix(unit, "m"):
		base = time.Minute
	case unit == "小时" || strings.HasPrefix(unit, "h"):
		base = time.Hour
	default:
		return 0, false
	}
	return time.Duration(n * float64(base)), true
}

var quotaExhaustedMarkers = []string{
	"insufficient_quota",
	"insufficient quota",
	"exceeded your current quota",
	"quota exceeded for",
	"insufficient balance",
	"insufficient_balance",
	"insufficient credits",
	"insufficient_credits",
	"insufficient credit",
	"insufficient_credit",
	"insufficient funds",
	"insufficient_funds",
	"credit balance is too low",
	"credit balance",
	"out of credits",
	"payment required",
	"billing_hard_limit",
	"billing hard limit",
	"monthly spend limit",
	"budget exceeded",
	"plan limit reached",
	"daily usage limit",
	"usage limit reached",
	"freeusagelimiterror",
	"均已限额",
	"额度已用完",
	"额度不足",
	"余额不足",
	"积分不足",
	"欠费",
	"配额已用尽",
	"调用次数已达上限",
}

// IsQuotaExhausted reports whether an upstream error message indicates an
// exhausted account balance, billing cap, or hard quota rather than a short
// burst rate limit.
func IsQuotaExhausted(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range quotaExhaustedMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

var permanentTurnErrorMarkers = []string{
	"runnerstallederror",
	"repeatedtoolfailureerror",
	"interrupted",
	"aborted",
	"cancelled",
	"canceled",
	"context canceled",
	"operation was canceled",
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
	"content policy",
	"content_policy_violation",
	"content filter",
	"content_filter",
	"content management",
	"safety system",
	"safety policy",
	"内容安全",
	"敏感内容",
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
	"action blocked by",
	"blocked by",
	"access denied",
	"forbidden",
	"payment_required",
	"model not found",
	"model_not_found",
	"unknown model",
	"does not exist",
	"is not available for this account",
	"does not support",
	"invalid_request_error",
	"bad request",
	"使用量已超出",
}

var transientTurnErrorMarkers = []string{
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
	"software caused connection abort",
	"connection closed",
	"connection terminated",
	"use of closed network connection",
	"server closed idle connection",
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
	"stream error",
	"http2: client connection lost",
	"client connection lost",
	"http2: server sent goaway",
	"read tcp",
	"write tcp",
	"dial tcp",
	"request failed: post ",
	"request failed: get ",
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
	"try again",
	"retryable",
	"rate limit",
	"rate_limit",
	"too many requests",
	"resource_exhausted",
	"请求超时",
	"连接超时",
	"响应超时",
	"连接失败",
	"连接中断",
	"连接重置",
	"网络异常",
	"网络错误",
	"服务繁忙",
	"服务暂不可用",
	"服务不可用",
	"服务器繁忙",
	"稍后重试",
	"频率超限",
	"请求过于频繁",
	"触发限流",
}

// IsPermanentTurnError reports whether an error text represents user
// cancellation, auth/config failure, quota exhaustion, context overflow, or
// safety rejection that must not be retried on the same model.
func IsPermanentTurnError(msg string) bool {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	if IsQuotaExhausted(lower) {
		return true
	}
	isNet := isNetworkFault(lower, 0)
	for _, marker := range permanentTurnErrorMarkers {
		if isNet && (marker == "interrupted" || marker == "aborted") {
			continue
		}
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return permanentStatusRe.MatchString(lower)
}

// IsTransientTurnError reports whether an error text came from a transient
// network drop, rate limit, or 5xx/overload condition worth auto-retrying.
func IsTransientTurnError(msg string) bool {
	if IsPermanentTurnError(msg) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(msg))
	for _, marker := range transientTurnErrorMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.HasSuffix(lower, ": eof") || strings.Contains(lower, ": eof ") || lower == "eof" {
		return true
	}
	return transientStatusRe.MatchString(lower)
}
