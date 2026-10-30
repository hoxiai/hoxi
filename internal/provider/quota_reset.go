package provider

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Upstreams often say exactly when a rate limit or quota resets, e.g.
// "将在 2026-10-30 22:48:16 UTC+8 重置" or "Please try again in 20s". Keeping
// the candidate open until then avoids re-dispatching to it every few minutes
// and waiting for the same 429 again.

const (
	// quotaResetMaxWait caps a stated reset so a typo or far-future date never
	// benches a model for longer than a day.
	quotaResetMaxWait = 24 * time.Hour
	// quotaResetQuotaAfter treats a reset this far away as an exhausted quota
	// rather than a short rate limit.
	quotaResetQuotaAfter = 10 * time.Minute
)

var (
	quotaResetAbsoluteRe = regexp.MustCompile(
		`(\d{4}-\d{2}-\d{2})[ t](\d{2}:\d{2}(?::\d{2})?)(?:\.\d+)?\s*(z|(?:utc|gmt)?\s*[+-]\d{1,2}(?::?\d{2})?|utc|gmt)?`)
	quotaResetRelativeRe = regexp.MustCompile(
		`(?:in|after)\s+(\d+(?:\.\d+)?)\s*(ms|milliseconds?|s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?)\b`)
	quotaResetRelativeZhRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(秒|分钟|小时)\s*后`)
	quotaResetWordRe       = regexp.MustCompile(`重置|重试|恢复|reset|retry|try again|available|until`)
)

// parseQuotaReset returns when an upstream says the limit lifts. It only
// trusts a time that is in the future; anything past or unparsable is ignored.
func parseQuotaReset(reason string, now time.Time) (time.Time, bool) {
	text := strings.ToLower(reason)
	if !quotaResetWordRe.MatchString(text) {
		return time.Time{}, false
	}
	if m := quotaResetAbsoluteRe.FindStringSubmatch(text); m != nil {
		if at, ok := parseQuotaResetAbsolute(m[1], m[2], m[3], now); ok && at.After(now) {
			return at, true
		}
	}
	if m := quotaResetRelativeRe.FindStringSubmatch(text); m != nil {
		if d, ok := quotaResetDuration(m[1], m[2]); ok {
			return now.Add(d), true
		}
	}
	if m := quotaResetRelativeZhRe.FindStringSubmatch(text); m != nil {
		if d, ok := quotaResetDuration(m[1], m[2]); ok {
			return now.Add(d), true
		}
	}
	return time.Time{}, false
}

func parseQuotaResetAbsolute(date, clock, zone string, now time.Time) (time.Time, bool) {
	layout := "2006-01-02 15:04"
	if strings.Count(clock, ":") == 2 {
		layout = "2006-01-02 15:04:05"
	}
	loc := now.Location()
	zone = strings.TrimSpace(zone)
	if zone != "" {
		offset, ok := parseQuotaResetOffset(zone)
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

// parseQuotaResetOffset turns "z", "utc", "utc+8", "+08:00" or "-0530" into
// seconds east of UTC.
func parseQuotaResetOffset(zone string) (int, bool) {
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

func quotaResetDuration(value, unit string) (time.Duration, bool) {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	var base time.Duration
	switch {
	case unit == "ms" || strings.HasPrefix(unit, "milli"):
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

// quotaResetCooldown applies a stated reset time to a rate-limit or quota
// failure. A reset far enough away means the quota is used up, so the class is
// promoted to quota; the wait is capped at a day.
func quotaResetCooldown(class breakerClass, reason string, now time.Time) (breakerClass, time.Time, bool) {
	if class != breakerClassRateLimit && class != breakerClassQuota {
		return class, time.Time{}, false
	}
	at, ok := parseQuotaReset(reason, now)
	if !ok {
		return class, time.Time{}, false
	}
	if limit := now.Add(quotaResetMaxWait); at.After(limit) {
		at = limit
	}
	if at.Sub(now) >= quotaResetQuotaAfter {
		class = breakerClassQuota
	}
	return class, at, true
}
