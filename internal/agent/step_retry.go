package agent

import (
	"context"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/llm"
)

const (
	defaultMaxNetworkRetries      = 4
	defaultMaxRateOrServerRetries = 5
	defaultMaxNetworkBackoff      = 30 * time.Second
	defaultMaxRateBackoff         = 60 * time.Second
	defaultMaxTurnRetryBudget     = 3 * time.Minute
	retryActivityPulseInterval    = 15 * time.Second
	maxRetryMessageRunes          = 240
)

var (
	defaultNetworkRetrySchedule = []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	}
	defaultRateOrServerSchedule = []time.Duration{
		2 * time.Second,
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
	}
)

type stepRetryDecision struct {
	shouldRetry bool
	delay       time.Duration
	reason      string
}

func summarizeRetryReason(err error) string {
	if err == nil {
		return "Transient upstream failure"
	}
	clean := strings.Join(strings.Fields(strings.TrimSpace(err.Error())), " ")
	if clean == "" {
		return "Transient upstream failure"
	}
	runes := []rune(clean)
	if len(runes) > maxRetryMessageRunes {
		return string(runes[:maxRetryMessageRunes]) + "..."
	}
	return clean
}

func addJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	window := int64(base) / 5
	if window <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(window+1))
}

func classifyRetryableStepFailure(err error, now time.Time) (retryable bool, rateOrServer bool, retryAfter time.Duration) {
	if err == nil {
		return false, false, 0
	}
	if IsIncompleteStreamError(err) {
		return true, false, 0
	}
	fault := llm.ClassifyError(err, now)
	if fault.RetryAfterMs > 0 {
		retryAfter = time.Duration(fault.RetryAfterMs) * time.Millisecond
	}
	switch fault.Kind {
	case llm.FaultKindQuota, llm.FaultKindAuth, llm.FaultKindBadReq, llm.FaultKindPolicy, llm.FaultKindModel, llm.FaultKindContext, llm.FaultKindAborted, llm.FaultKindStalled:
		return false, false, retryAfter
	case llm.FaultKindRateLimit, llm.FaultKindServer:
		if retryAfter >= llm.QuotaResetPromoteAfter {
			return false, true, retryAfter
		}
		return true, true, retryAfter
	case llm.FaultKindNetwork:
		return true, false, retryAfter
	default:
		if llm.IsTransientTurnError(err.Error()) {
			return true, false, retryAfter
		}
		return false, false, retryAfter
	}
}

func (e *Engine) planStepRetry(err error, attempt int, spentWait time.Duration, now time.Time) stepRetryDecision {
	retryable, rateOrServer, retryAfter := classifyRetryableStepFailure(err, now)
	if !retryable {
		return stepRetryDecision{}
	}

	budget := e.retryBudget
	if budget <= 0 {
		budget = defaultMaxTurnRetryBudget
	}
	remainingBudget := budget - spentWait

	if e.customRetry {
		delays := e.retryDelay
		maxAttempts := maxStepTransientRetries
		if len(delays) > 0 {
			maxAttempts = len(delays)
		}
		if attempt > maxAttempts {
			return stepRetryDecision{}
		}
		var d time.Duration
		if len(delays) > 0 {
			idx := min(attempt-1, len(delays)-1)
			d = max(0, delays[idx])
			if retryAfter > d {
				d = retryAfter
			}
		} else if retryAfter > 0 {
			d = retryAfter
		}
		if d > remainingBudget {
			return stepRetryDecision{}
		}
		return stepRetryDecision{
			shouldRetry: true,
			delay:       d,
			reason:      summarizeRetryReason(err),
		}
	}

	maxAttempts := defaultMaxNetworkRetries
	schedule := defaultNetworkRetrySchedule
	maxCap := defaultMaxNetworkBackoff
	if rateOrServer {
		maxAttempts = defaultMaxRateOrServerRetries
		schedule = defaultRateOrServerSchedule
		maxCap = defaultMaxRateBackoff
	}
	if attempt > maxAttempts {
		return stepRetryDecision{}
	}

	idx := min(attempt-1, len(schedule)-1)
	wait := addJitter(schedule[idx])
	if wait > maxCap {
		wait = maxCap
	}
	// Provider Retry-After hints are minimum wait floors, never shortened.
	if retryAfter > wait {
		wait = retryAfter
	}
	if remainingBudget <= 0 || wait > remainingBudget {
		return stepRetryDecision{}
	}
	return stepRetryDecision{
		shouldRetry: true,
		delay:       wait,
		reason:      summarizeRetryReason(err),
	}
}

func waitContextWithPulse(ctx context.Context, delay time.Duration, onPulse func()) bool {
	if ctx.Err() != nil {
		return false
	}
	if delay <= 0 {
		return ctx.Err() == nil
	}
	deadlineTimer := time.NewTimer(delay)
	defer deadlineTimer.Stop()

	if onPulse == nil || delay <= retryActivityPulseInterval {
		select {
		case <-ctx.Done():
			return false
		case <-deadlineTimer.C:
			return ctx.Err() == nil
		}
	}

	pulseTicker := time.NewTicker(retryActivityPulseInterval)
	defer pulseTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadlineTimer.C:
			return ctx.Err() == nil
		case <-pulseTicker.C:
			onPulse()
		}
	}
}

func (e *Engine) waitForStepRetry(
	ctx context.Context,
	step int,
	subtaskDepth int,
	attempt int,
	dec stepRetryDecision,
	emit func(EngineEvent),
) bool {
	if ctx.Err() != nil {
		return false
	}
	nextUnixMs := time.Now().Add(dec.delay).UnixMilli()
	emitRetry := func() {
		if subtaskDepth == 0 && dec.delay > 0 && emit != nil {
			emit(EngineEvent{
				Kind:      EngineStepRetry,
				Step:      step,
				ErrorText: dec.reason,
				Metadata: map[string]any{
					"attempt": attempt,
					"delayMs": dec.delay.Milliseconds(),
					"next":    nextUnixMs,
				},
			})
		}
	}
	emitRetry()
	if !waitContextWithPulse(ctx, dec.delay, emitRetry) {
		return false
	}
	if subtaskDepth == 0 && dec.delay > 0 && emit != nil {
		emit(EngineEvent{Kind: EngineStepRetryDone, Step: step})
	}
	return true
}
