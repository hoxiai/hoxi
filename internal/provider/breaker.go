package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Candidate circuit breaker.
//
// The auto model pool must not keep dispatching to a model that is rate
// limited, out of quota, misconfigured, or repeatedly failing, and it should
// prefer the faster model when several are healthy. This store is the shared
// source of truth for both planes: the internal/relay plane (consulted by
// resolveBackendAutoCandidate) and the session plane (the desktop frontend,
// which reads the snapshot and reports turn outcomes back through
// /api/hoxi/provider/candidate-outcome).
//
// State machine per candidate (provider/model):
//
//	closed -- normal, counts failures, opens after the class threshold
//	open   -- excluded from rotation until openUntil elapses
//
// An expired open circuit is eligible again until an outcome re-opens or closes
// it, so there is no separate half-open latch to leak. A cooldown is always
// bounded and self-healing; there is no permanent removal. The only durable
// "unhealthy" signal is a config-class failure, which still expires and can be
// cleared by a probe or by fixing the credentials.

const breakerStateVersion = 2

const (
	// breakerTransientThreshold opens the circuit only after a short burst, so a
	// single flaky 5xx or socket hang-up does not bench a healthy model.
	breakerTransientThreshold = 2
	// breakerMaxEntries bounds the persisted file so a huge catalog cannot grow
	// the state unbounded.
	breakerMaxEntries = 512
	// breakerCounterDecayAt halves the success/failure counters once they grow
	// past this, giving the score a moving window instead of an all-time sum.
	breakerCounterDecayAt = 60
	// breakerMinPersistInterval throttles disk writes; phase changes force a
	// write immediately regardless of this interval.
	breakerMinPersistInterval = 1500 * time.Millisecond
)

type breakerPhase string

const (
	breakerClosed breakerPhase = "closed"
	breakerOpen   breakerPhase = "open"
)

type breakerClass string

const (
	breakerClassNone      breakerClass = ""
	breakerClassTransient breakerClass = "transient"
	breakerClassRateLimit breakerClass = "rate_limit"
	breakerClassQuota     breakerClass = "quota"
	breakerClassConfig    breakerClass = "config"
	breakerClassBadReq    breakerClass = "bad_request"
)

// candidateBreaker is the persisted per-candidate state.
type candidateBreaker struct {
	ProviderID          string       `json:"providerID"`
	ModelID             string       `json:"modelID"`
	Phase               breakerPhase `json:"phase"`
	ConsecutiveFailures int          `json:"consecutiveFailures"`
	Successes           int          `json:"successes"`
	Failures            int          `json:"failures"`
	LatencyEwmaMs       float64      `json:"latencyEwmaMs,omitempty"`
	LatencySamples      int          `json:"latencySamples,omitempty"`
	OpenUntil           int64        `json:"openUntil,omitempty"`
	LastClass           breakerClass `json:"lastClass,omitempty"`
	LastReason          string       `json:"lastReason,omitempty"`
	UpdatedAt           int64        `json:"updatedAt"`
}

// BreakerSnapshot is the read model handed to the frontend and diagnostics.
type BreakerSnapshot struct {
	ProviderID          string  `json:"providerID"`
	ModelID             string  `json:"modelID"`
	Phase               string  `json:"phase"`
	Open                bool    `json:"open"`
	OpenUntil           int64   `json:"openUntil,omitempty"`
	Score               float64 `json:"score"`
	LatencyEwmaMs       float64 `json:"latencyEwmaMs,omitempty"`
	ConsecutiveFailures int     `json:"consecutiveFailures"`
	LastClass           string  `json:"lastClass,omitempty"`
	UpdatedAt           int64   `json:"updatedAt"`
}

type breakerStore struct {
	mu          sync.Mutex
	filePath    string
	items       map[string]*candidateBreaker
	nowFn       func() time.Time
	lastSavedAt time.Time
}

func newBreakerStore(rootDir string) *breakerStore {
	s := &breakerStore{
		filePath: resolveBreakerFilePath(rootDir),
		items:    make(map[string]*candidateBreaker),
		nowFn:    time.Now,
	}
	s.loadFromDisk()
	return s
}

func resolveBreakerFilePath(rootDir string) string {
	if dir := hoxipaths.Home(); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return filepath.Join(dir, "provider-breakers.json")
		}
	}
	fallbackDir := filepath.Join(rootDir, ".hoxi")
	_ = os.MkdirAll(fallbackDir, 0o700)
	return filepath.Join(fallbackDir, "provider-breakers.json")
}

func (s *breakerStore) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func breakerKey(providerID, modelID string) string {
	return strings.TrimSpace(providerID) + "/" + strings.TrimSpace(modelID)
}

func (s *breakerStore) loadFromDisk() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filePath == "" {
		return
	}
	doc := readJSONMapFile(s.filePath)
	if doc == nil {
		return
	}
	rawCandidates, ok := doc["candidates"].(map[string]interface{})
	if !ok {
		return
	}
	for key, raw := range rawCandidates {
		blob, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var cb candidateBreaker
		if json.Unmarshal(blob, &cb) != nil {
			continue
		}
		if strings.TrimSpace(cb.ProviderID) == "" || strings.TrimSpace(cb.ModelID) == "" {
			continue
		}
		if cb.Phase == "" {
			cb.Phase = breakerClosed
		}
		s.items[key] = &cb
	}
}

func (s *breakerStore) saveLocked(force bool) {
	if s.filePath == "" {
		return
	}
	now := s.now()
	if !force && now.Sub(s.lastSavedAt) < breakerMinPersistInterval {
		return
	}
	s.evictLocked()
	doc := map[string]interface{}{
		"version":    breakerStateVersion,
		"updatedAt":  now.UnixMilli(),
		"candidates": s.items,
	}
	if err := writeJSONMapFile(s.filePath, doc); err != nil {
		return
	}
	s.lastSavedAt = now
}

func (s *breakerStore) evictLocked() {
	if len(s.items) <= breakerMaxEntries {
		return
	}
	type pair struct {
		key string
		at  int64
	}
	all := make([]pair, 0, len(s.items))
	for key, rec := range s.items {
		all = append(all, pair{key: key, at: rec.UpdatedAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
	for _, p := range all[:len(s.items)-breakerMaxEntries] {
		delete(s.items, p.key)
	}
}

// entryLocked returns the mutable breaker for a candidate, creating a closed
// one on first use. Callers must hold s.mu.
func (s *breakerStore) entryLocked(providerID, modelID string) *candidateBreaker {
	key := breakerKey(providerID, modelID)
	if rec, ok := s.items[key]; ok && rec != nil {
		return rec
	}
	rec := &candidateBreaker{
		ProviderID: strings.TrimSpace(providerID),
		ModelID:    strings.TrimSpace(modelID),
		Phase:      breakerClosed,
		UpdatedAt:  s.now().UnixMilli(),
	}
	s.items[key] = rec
	return rec
}

// RecordSuccess closes the circuit and folds the observed latency into the
// moving average so faster candidates rank higher.
func (s *breakerStore) RecordSuccess(providerID, modelID string, latencyMs int64) {
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.entryLocked(providerID, modelID)
	rec.Successes++
	rec.ConsecutiveFailures = 0
	rec.LastClass = breakerClassNone
	rec.LastReason = ""
	rec.OpenUntil = 0
	phaseChanged := rec.Phase != breakerClosed
	rec.Phase = breakerClosed
	if latencyMs > 0 {
		if rec.LatencySamples == 0 || rec.LatencyEwmaMs <= 0 {
			rec.LatencyEwmaMs = float64(latencyMs)
		} else {
			rec.LatencyEwmaMs = 0.7*rec.LatencyEwmaMs + 0.3*float64(latencyMs)
		}
		rec.LatencySamples++
	}
	decayCountersLocked(rec)
	rec.UpdatedAt = s.now().UnixMilli()
	s.saveLocked(phaseChanged)
}

// RecordFailure counts a failure and opens the circuit once the class threshold
// is reached. bad_request and user cancellation never penalize the model.
func (s *breakerStore) RecordFailure(providerID, modelID string, class breakerClass, status int, latencyMs int64, reason string) {
	if class == breakerClassNone || class == breakerClassBadReq {
		return
	}
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.entryLocked(providerID, modelID)
	rec.Failures++
	rec.ConsecutiveFailures++
	rec.LastClass = class
	rec.LastReason = truncateReason(reason)
	if latencyMs > 0 {
		// A timeout carries a latency signal for scoring too, but weight it
		// lightly so a slow failure does not masquerade as a healthy sample.
		if rec.LatencySamples == 0 || rec.LatencyEwmaMs <= 0 {
			rec.LatencyEwmaMs = float64(latencyMs)
		} else {
			rec.LatencyEwmaMs = 0.85*rec.LatencyEwmaMs + 0.15*float64(latencyMs)
		}
		rec.LatencySamples++
	}
	decayCountersLocked(rec)

	threshold := breakerTransientThreshold
	if class == breakerClassRateLimit || class == breakerClassQuota || class == breakerClassConfig {
		threshold = 1
	}
	phaseChanged := false
	if rec.ConsecutiveFailures >= threshold {
		rec.Phase = breakerOpen
		rec.OpenUntil = s.now().Add(breakerCooldown(class, rec.ConsecutiveFailures)).UnixMilli()
		phaseChanged = true
	}
	rec.UpdatedAt = s.now().UnixMilli()
	s.saveLocked(phaseChanged)
}

func decayCountersLocked(rec *candidateBreaker) {
	if rec.Successes+rec.Failures <= breakerCounterDecayAt {
		return
	}
	rec.Successes /= 2
	rec.Failures /= 2
}

// breakerCooldown returns the bounded cooldown for a failure class. The failure
// count drives an exponential backoff so repeated opens stay out longer, but the
// cap keeps every model recoverable within a predictable window.
func breakerCooldown(class breakerClass, consecutive int) time.Duration {
	if consecutive < 1 {
		consecutive = 1
	}
	shift := consecutive - 1
	if shift > 6 {
		shift = 6
	}
	switch class {
	case breakerClassQuota:
		return 2 * time.Hour
	case breakerClassConfig:
		return 30 * time.Minute
	case breakerClassRateLimit:
		d := 90 * time.Second << shift
		if d > 15*time.Minute {
			d = 15 * time.Minute
		}
		return d
	default: // transient
		d := 15 * time.Second << shift
		if d > 3*time.Minute {
			d = 3 * time.Minute
		}
		return d
	}
}

// Score returns a 0..1 health score (higher is better): a blend of reliability
// and observed latency, so a fast, reliable candidate outranks a slow one.
func (s *breakerStore) Score(providerID, modelID string) float64 {
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return 0.5
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[breakerKey(providerID, modelID)]
	if !ok || rec == nil {
		return 0.54
	}
	if rec.Phase == breakerOpen {
		return 0
	}
	reliability := float64(rec.Successes+1) / float64(rec.Successes+rec.Failures+2)
	return 0.6*reliability + 0.4*latencyFactor(rec.LatencyEwmaMs, rec.LatencySamples)
}

// latencyFactor maps an average latency onto 0.15..1.0. Untested candidates sit
// at a neutral 0.6 so they are neither favored nor buried.
func latencyFactor(ewmaMs float64, samples int) float64 {
	if samples <= 0 || ewmaMs <= 0 {
		return 0.6
	}
	const fastMs, slowMs = 900.0, 18000.0
	if ewmaMs <= fastMs {
		return 1.0
	}
	if ewmaMs >= slowMs {
		return 0.15
	}
	return 1.0 - (ewmaMs-fastMs)/(slowMs-fastMs)*0.85
}

func (s *breakerStore) Snapshot() map[string]BreakerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	nowMs := s.now().UnixMilli()
	out := make(map[string]BreakerSnapshot, len(s.items))
	for key, rec := range s.items {
		if rec == nil {
			continue
		}
		open := rec.Phase == breakerOpen && rec.OpenUntil > nowMs
		out[key] = BreakerSnapshot{
			ProviderID:          rec.ProviderID,
			ModelID:             rec.ModelID,
			Phase:               string(rec.Phase),
			Open:                open,
			OpenUntil:           rec.OpenUntil,
			Score:               round3(candidateScore(rec)),
			LatencyEwmaMs:       rec.LatencyEwmaMs,
			ConsecutiveFailures: rec.ConsecutiveFailures,
			LastClass:           string(rec.LastClass),
			UpdatedAt:           rec.UpdatedAt,
		}
	}
	return out
}

// Evaluate reports whether a candidate is dispatchable right now and its health
// score. It does not advance the open -> half-open transition; selection uses it
// as a read, and the dispatched request goes through Allow to claim the probe.
func (s *breakerStore) Evaluate(providerID, modelID string) (allowed bool, score float64) {
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return true, 0.5
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[breakerKey(providerID, modelID)]
	if !ok || rec == nil {
		return true, 0.54
	}
	if rec.Phase == breakerOpen {
		if rec.OpenUntil > 0 && s.now().UnixMilli() >= rec.OpenUntil {
			// Expired cooldown: treat as a recoverable probe candidate.
			return true, 0.3
		}
		return false, 0
	}
	return true, candidateScore(rec)
}

// candidateScore blends reliability and latency into a 0..1 health score.
func candidateScore(rec *candidateBreaker) float64 {
	if rec == nil {
		return 0.54
	}
	reliability := float64(rec.Successes+1) / float64(rec.Successes+rec.Failures+2)
	return 0.6*reliability + 0.4*latencyFactor(rec.LatencyEwmaMs, rec.LatencySamples)
}

// cooldownRemainingMs returns how long until an open candidate can be retried,
// used to pick the least-bad fallback when every candidate is cooling down.
func (s *breakerStore) cooldownRemainingMs(providerID, modelID string) int64 {
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[breakerKey(providerID, modelID)]
	if !ok || rec == nil || rec.Phase != breakerOpen {
		return 0
	}
	remaining := rec.OpenUntil - s.now().UnixMilli()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// LatencyMs returns the observed latency average in milliseconds, or 0 when the
// candidate has no latency sample yet. Selection uses it to skip a candidate
// that is much slower than a healthy peer.
func (s *breakerStore) LatencyMs(providerID, modelID string) float64 {
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[breakerKey(providerID, modelID)]
	if !ok || rec == nil || rec.LatencySamples <= 0 {
		return 0
	}
	return rec.LatencyEwmaMs
}

func round3(v float64) float64 {
	return float64(int(v*1000+0.5)) / 1000
}

func truncateReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 160 {
		return reason[:160]
	}
	return reason
}

// classifyCandidateFailure turns an HTTP status and/or transport error into a
// failure class. Semantics matter: a 400 caused by the caller's bad payload is
// not the model's fault, while an auth or model-not-found error is durable
// misconfiguration that must not be retried silently forever.
func classifyCandidateFailure(status int, err error, body []byte) breakerClass {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return breakerClassNone
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return breakerClassTransient
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return breakerClassTransient
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return breakerClassTransient
		}
	}
	if msg := failureText(err, body); msg != "" {
		switch {
		case strings.Contains(msg, "insufficient_quota"),
			strings.Contains(msg, "insufficient balance"),
			strings.Contains(msg, "insufficient_balance"),
			strings.Contains(msg, "credit balance is too low"),
			strings.Contains(msg, "billing_hard_limit"),
			strings.Contains(msg, "freeusagelimiterror"),
			strings.Contains(msg, "exceeded your current quota"):
			return breakerClassQuota
		case strings.Contains(msg, "model not found"),
			strings.Contains(msg, "model_not_found"),
			strings.Contains(msg, "does not expose model"),
			strings.Contains(msg, "is not loaded in the running engine"),
			strings.Contains(msg, "unknown model"),
			strings.Contains(msg, "unknown provider"):
			return breakerClassConfig
		case strings.Contains(msg, "invalid api key"),
			strings.Contains(msg, "incorrect api key"),
			strings.Contains(msg, "unauthorized"),
			strings.Contains(msg, "authentication"):
			return breakerClassConfig
		}
	}
	switch status {
	case 0:
		return breakerClassTransient
	case 400, 405, 406, 413, 422:
		return breakerClassBadReq
	case 401:
		return breakerClassConfig
	case 403:
		if isQuotaExhausted(body) {
			return breakerClassQuota
		}
		return breakerClassConfig
	case 404:
		return breakerClassConfig
	case 408, 409, 423, 425:
		return breakerClassTransient
	case 429:
		if isQuotaExhausted(body) {
			return breakerClassQuota
		}
		return breakerClassRateLimit
	}
	if status >= 500 {
		return breakerClassTransient
	}
	return breakerClassBadReq
}

func failureText(err error, body []byte) string {
	var b strings.Builder
	if err != nil {
		b.WriteString(err.Error())
		b.WriteByte(' ')
	}
	if len(body) > 0 {
		snippet := body
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		b.Write(snippet)
	}
	return strings.ToLower(b.String())
}

// candidateOutcomeRequest is the payload the desktop frontend posts for every
// auto-pool turn so the shared breaker reflects session-plane results too.
type candidateOutcomeRequest struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode,omitempty"`
	Message    string `json:"message,omitempty"`
	LatencyMs  int64  `json:"latencyMs,omitempty"`
	Class      string `json:"class,omitempty"`
}

func (s *breakerStore) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/provider/candidates", s.handleSnapshot)
	mux.HandleFunc("/api/hoxi/provider/candidate-outcome", s.handleOutcome)
}

func (s *breakerStore) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "breakers": s.Snapshot()})
	case http.MethodDelete:
		pid := strings.TrimSpace(r.URL.Query().Get("providerID"))
		mid := strings.TrimSpace(r.URL.Query().Get("modelID"))
		s.clear(pid, mid)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "breakers": s.Snapshot()})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *breakerStore) handleOutcome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req candidateOutcomeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid candidate outcome payload")
		return
	}
	pid, mid := strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.ModelID)
	if pid == "" || mid == "" {
		writeJSONError(w, http.StatusBadRequest, "providerID and modelID are required")
		return
	}
	if req.OK {
		s.RecordSuccess(pid, mid, req.LatencyMs)
	} else {
		class := breakerClass(strings.TrimSpace(req.Class))
		if class == breakerClassNone {
			class = classifyCandidateFailure(req.StatusCode, nil, []byte(req.Message))
		}
		s.RecordFailure(pid, mid, class, req.StatusCode, req.LatencyMs, req.Message)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// clear deletes breaker state for one candidate, or all candidates when both
// ids are empty. It mirrors the "clear cooldown" affordance in the UI.
func (s *breakerStore) clear(providerID, modelID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if providerID == "" && modelID == "" {
		s.items = make(map[string]*candidateBreaker)
	} else if providerID != "" && modelID != "" {
		delete(s.items, breakerKey(providerID, modelID))
	} else {
		prefix := providerID + "/"
		for key := range s.items {
			if strings.HasPrefix(key, prefix) {
				delete(s.items, key)
			}
		}
	}
	s.saveLocked(true)
}

// recordBreakerSuccess and recordBreakerFailure are nil-safe Service helpers so
// the internal, relay and probe planes can all feed the shared breaker.
func (s *Service) recordBreakerSuccess(providerID, modelID string, latencyMs int64) {
	if s.breaker != nil {
		s.breaker.RecordSuccess(providerID, modelID, latencyMs)
	}
}

func (s *Service) recordBreakerFailure(providerID, modelID string, class breakerClass, status int, latencyMs int64, reason string) {
	if s.breaker != nil {
		s.breaker.RecordFailure(providerID, modelID, class, status, latencyMs, reason)
	}
}
