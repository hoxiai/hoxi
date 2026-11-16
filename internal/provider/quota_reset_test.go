package provider

import (
	"testing"
	"time"
)

func TestParseQuotaReset(t *testing.T) {
	shanghai := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 30, 13, 24, 0, 0, shanghai)
	cases := []struct {
		name   string
		reason string
		want   time.Time
		ok     bool
	}{
		{
			name:   "chinese absolute with utc offset",
			reason: "上游返回 429: 您的使用量已超出频率限制，将在 2026-10-30 22:48:16 UTC+8 重置，您也可以切换其他模型继续使用。",
			want:   time.Date(2026, 10, 30, 22, 48, 16, 0, shanghai),
			ok:     true,
		},
		{
			name:   "rfc3339 zulu",
			reason: "Quota exceeded, resets at 2026-10-30T06:00:00Z",
			want:   time.Date(2026, 10, 30, 6, 0, 0, 0, time.UTC),
			ok:     true,
		},
		{
			name:   "offset with colon",
			reason: "limit reached, available again 2026-10-31 01:30 +08:00",
			want:   time.Date(2026, 10, 31, 1, 30, 0, 0, shanghai),
			ok:     true,
		},
		{
			name:   "no zone uses local",
			reason: "将在 2026-10-30 15:00:00 重置",
			want:   time.Date(2026, 10, 30, 15, 0, 0, 0, shanghai),
			ok:     true,
		},
		{name: "relative seconds", reason: "Rate limit reached. Please try again in 20s.", want: now.Add(20 * time.Second), ok: true},
		{name: "relative fractional", reason: "Please retry after 1.5 minutes", want: now.Add(90 * time.Second), ok: true},
		{name: "relative chinese", reason: "请求过于频繁，请 30 分钟后重试", want: now.Add(30 * time.Minute), ok: true},
		{name: "reset in the past is ignored", reason: "将在 2026-10-29 22:48:16 UTC+8 重置", ok: false},
		{name: "date without reset wording is ignored", reason: "request 2026-10-30 22:48:16 failed", ok: false},
		{name: "plain rate limit", reason: "429 Too Many Requests", ok: false},
		{name: "bad offset", reason: "reset at 2026-10-30 22:48:16 UTC+99", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseQuotaReset(tc.reason, now)
			if ok != tc.ok {
				t.Fatalf("ok=%v, want %v (got %v)", ok, tc.ok, got)
			}
			if ok && !got.Equal(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBreakerHonorsStatedResetTime(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	store := newBreakerStore(dir)
	shanghai := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 30, 13, 24, 0, 0, shanghai)
	store.nowFn = func() time.Time { return now }

	reason := "上游返回 429: 您的使用量已超出频率限制，将在 2026-10-30 22:48:16 UTC+8 重置"
	class := classifyCandidateFailure(429, nil, []byte(reason))
	store.RecordFailure("subapi", "workbuddy", class, 429, 0, reason)
	rec := store.items[breakerKey("subapi", "workbuddy")]
	want := time.Date(2026, 10, 30, 22, 48, 16, 0, shanghai).UnixMilli()
	if rec.OpenUntil != want {
		t.Fatalf("openUntil=%d, want the stated reset %d", rec.OpenUntil, want)
	}
	if rec.LastClass != breakerClassQuota {
		t.Fatalf("a reset hours away is an exhausted quota, got %q", rec.LastClass)
	}

	store.nowFn = func() time.Time { return now.Add(3 * time.Minute) }
	if allowed, _ := store.Evaluate("subapi", "workbuddy"); allowed {
		t.Fatal("the candidate must stay open until the stated reset, not the 3-minute backoff")
	}
}

func TestBreakerResetCappedAndShortResetStaysRateLimit(t *testing.T) {
	_, dir := setupIsolatedEnv(t)
	store := newBreakerStore(dir)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	store.nowFn = func() time.Time { return now }

	store.RecordFailure("p", "far", breakerClassQuota, 429, 0, "quota resets at 2027-01-01T00:00:00Z")
	if got := store.items[breakerKey("p", "far")].OpenUntil; got != now.Add(quotaResetMaxWait).UnixMilli() {
		t.Fatalf("a far reset must be capped at a day, got %d", got)
	}

	store.RecordFailure("p", "short", breakerClassRateLimit, 429, 0, "Please try again in 20s")
	rec := store.items[breakerKey("p", "short")]
	if rec.LastClass != breakerClassRateLimit || rec.OpenUntil != now.Add(20*time.Second).UnixMilli() {
		t.Fatalf("short reset: class=%q openUntil=%d", rec.LastClass, rec.OpenUntil)
	}

	// A stated time on a transient failure is not a quota signal.
	store.RecordFailure("p", "flaky", breakerClassTransient, 503, 0, "retry after 2 hours")
	if store.items[breakerKey("p", "flaky")].Phase == breakerOpen {
		t.Fatal("one transient failure must not open the circuit")
	}
}

func TestClassifyChineseQuotaExhausted(t *testing.T) {
	body := []byte("上游返回 429: 您的使用量已超出频率限制（所有候选账号对模型 X 均已限额或禁用）")
	if got := classifyCandidateFailure(429, nil, body); got != breakerClassQuota {
		t.Fatalf("got %q, want quota", got)
	}
	if got := classifyCandidateFailure(429, nil, []byte("Too Many Requests")); got != breakerClassRateLimit {
		t.Fatalf("plain 429 must stay a rate limit, got %q", got)
	}
}
