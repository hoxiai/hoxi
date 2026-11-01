package provider

import (
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	// quotaResetMaxWait caps a stated reset so a typo or far-future date never
	// benches a model for longer than a day.
	quotaResetMaxWait = llm.MaxResetWait
	// quotaResetQuotaAfter treats a reset this far away as an exhausted quota
	// rather than a short rate limit.
	quotaResetQuotaAfter = llm.QuotaResetPromoteAfter
)

// parseQuotaReset returns when an upstream says the limit lifts. It only
// trusts a time that is in the future; anything past or unparsable is ignored.
func parseQuotaReset(reason string, now time.Time) (time.Time, bool) {
	return llm.ParseResetTime(reason, now)
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
