package provider

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	llmMaxAttempts   = 3
	llmRetryBaseWait = 300 * time.Millisecond
	llmRetryMaxWait  = 1200 * time.Millisecond
)

func isTransientLLMError(err error, status int) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return true
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}
		return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
	}
	switch {
	case status == http.StatusRequestTimeout,
		status == http.StatusTooEarly,
		status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	}
	return false
}

func llmRetryWait(attempt int, retryAfter time.Duration) time.Duration {
	wait := llmRetryBaseWait << (attempt - 1)
	if wait > llmRetryMaxWait {
		wait = llmRetryMaxWait
	}
	if retryAfter > wait {
		wait = retryAfter
	}
	return wait + time.Duration(rand.Int64N(int64(wait)/4+1))
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

func llmAttemptWindow(remaining time.Duration, attemptsLeft int) time.Duration {
	if remaining <= 0 {
		return 0
	}
	if attemptsLeft <= 1 {
		return remaining
	}
	return remaining * 2 / 3
}
