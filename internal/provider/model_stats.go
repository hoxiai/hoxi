package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Model usage and quality statistics per provider/model (turn outcomes,
// latency samples, and probe results), persisted to
// ~/.hoxi/provider-telemetry.json and served at /api/hoxi/provider/telemetry.
// Not to be confused with internal/telemetry, the anonymous launch beacon.

const maxTelemetrySamples = 30
const maxTelemetryIncidents = 10

type providerIncident struct {
	Timestamp int64  `json:"timestamp"`
	Type      string `json:"type"` // "timeout" | "api_error" | "retry" | "tool_error" | "hallucination_loop" | "user_revert"
	ModelID   string `json:"modelID,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type modelTelemetryStats struct {
	ModelID            string    `json:"modelID"`
	TotalTurns         int       `json:"totalTurns"`
	SuccessTurns       int       `json:"successTurns"`
	ErrorTurns         int       `json:"errorTurns"`
	TimeoutTurns       int       `json:"timeoutTurns"`
	RetryCount         int       `json:"retryCount"`
	ToolCallsTotal     int       `json:"toolCallsTotal"`
	ToolCallsFailed    int       `json:"toolCallsFailed"`
	HallucinationFlags int       `json:"hallucinationFlags"`
	RevertCount        int       `json:"revertCount"`
	CacheHitTurns      int       `json:"cacheHitTurns"`
	CacheEligibleTurns int       `json:"cacheEligibleTurns,omitempty"`
	ReasoningTurns     int       `json:"reasoningTurns"`
	TtftSamplesMs      []int64   `json:"ttftSamplesMs,omitempty"`
	TpsSamples         []float64 `json:"tpsSamples,omitempty"`
	LastProbeLatencyMs int64     `json:"lastProbeLatencyMs,omitempty"`
	LastProbeOk        *bool     `json:"lastProbeOk,omitempty"`
	UpdatedAt          int64     `json:"updatedAt"`
}

type providerTelemetryStats struct {
	ProviderID         string                          `json:"providerID"`
	TotalTurns         int                             `json:"totalTurns"`
	SuccessTurns       int                             `json:"successTurns"`
	ErrorTurns         int                             `json:"errorTurns"`
	TimeoutTurns       int                             `json:"timeoutTurns"`
	RetryCount         int                             `json:"retryCount"`
	ToolCallsTotal     int                             `json:"toolCallsTotal"`
	ToolCallsFailed    int                             `json:"toolCallsFailed"`
	HallucinationFlags int                             `json:"hallucinationFlags"`
	RevertCount        int                             `json:"revertCount"`
	CacheHitTurns      int                             `json:"cacheHitTurns"`
	CacheEligibleTurns int                             `json:"cacheEligibleTurns,omitempty"`
	ReasoningTurns     int                             `json:"reasoningTurns"`
	TtftSamplesMs      []int64                         `json:"ttftSamplesMs,omitempty"`
	TpsSamples         []float64                       `json:"tpsSamples,omitempty"`
	LastProbeLatencyMs int64                           `json:"lastProbeLatencyMs,omitempty"`
	LastProbeOk        *bool                           `json:"lastProbeOk,omitempty"`
	Models             map[string]*modelTelemetryStats `json:"models,omitempty"`
	RecentIncidents    []providerIncident              `json:"recentIncidents,omitempty"`
	UpdatedAt          int64                           `json:"updatedAt"`
}

type modelStatsStore struct {
	mu               sync.Mutex
	filePath         string
	records          map[string]*providerTelemetryStats
	consecutiveFail  map[string]int
	degraded         map[string]bool
	onDegradedChange func(providerID string, degraded bool)
}

const llmDegradedThreshold = 3

func (s *modelStatsStore) setOnDegradedChange(fn func(providerID string, degraded bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDegradedChange = fn
}

// recordLLMRequest folds Hoxi-internal LLM retries into existing provider
// telemetry records and flags providers that fail repeatedly so the status bar
// can surface them. It never creates records, so probe/audit quality telemetry
// stays untouched by one-off internal calls.
func (s *modelStatsStore) recordLLMRequest(providerID, modelID string, attempts int, err error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return
	}
	s.mu.Lock()
	if s.consecutiveFail == nil {
		s.consecutiveFail = make(map[string]int)
	}
	if s.degraded == nil {
		s.degraded = make(map[string]bool)
	}
	rec := s.records[providerID]
	now := time.Now().UnixMilli()
	if rec != nil && attempts > 1 {
		rec.RetryCount += attempts - 1
		rec.UpdatedAt = now
		if m := ensureModelRecordLocked(rec, modelID); m != nil {
			m.RetryCount += attempts - 1
			m.UpdatedAt = now
		}
		appendIncidentLocked(rec, providerIncident{
			Timestamp: now,
			Type:      "retry",
			ModelID:   modelID,
			Detail:    "internal request retried after transient failure",
		})
		s.saveToDiskLocked()
	}
	var notify func(string, bool)
	if err != nil {
		s.consecutiveFail[providerID]++
		if s.consecutiveFail[providerID] >= llmDegradedThreshold && !s.degraded[providerID] {
			s.degraded[providerID] = true
			notify = s.onDegradedChange
		}
	} else {
		s.consecutiveFail[providerID] = 0
		if s.degraded[providerID] {
			delete(s.degraded, providerID)
			notify = s.onDegradedChange
		}
	}
	degraded := s.degraded[providerID]
	s.mu.Unlock()
	if notify != nil {
		notify(providerID, degraded)
	}
}

func newModelStatsStore(rootDir string) *modelStatsStore {
	storePath := resolveModelStatsFilePath(rootDir)
	s := &modelStatsStore{
		filePath:        storePath,
		records:         make(map[string]*providerTelemetryStats),
		consecutiveFail: make(map[string]int),
		degraded:        make(map[string]bool),
	}
	s.loadFromDisk()
	return s
}

func resolveModelStatsFilePath(rootDir string) string {
	if dir := hoxipaths.Home(); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return filepath.Join(dir, "provider-telemetry.json")
		}
	}
	fallbackDir := filepath.Join(rootDir, ".hoxi")
	_ = os.MkdirAll(fallbackDir, 0o700)
	return filepath.Join(fallbackDir, "provider-telemetry.json")
}

func (s *modelStatsStore) loadFromDisk() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return
	}
	var loaded map[string]*providerTelemetryStats
	if err := json.Unmarshal(data, &loaded); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", s.filePath, time.Now().UnixNano())
		_ = os.Rename(s.filePath, backup)
		return
	}
	for pid, rec := range loaded {
		if rec == nil || len(rec.Models) == 0 {
			continue
		}
		s.records[pid] = rec
	}
}

func (s *modelStatsStore) saveToDiskLocked() {
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return
	}
	_ = hoxipaths.WriteFileAtomic(s.filePath, data, 0o600)
}

func cloneProviderTelemetryStats(v *providerTelemetryStats) providerTelemetryStats {
	cp := *v
	if v.LastProbeOk != nil {
		ok := *v.LastProbeOk
		cp.LastProbeOk = &ok
	}
	if len(v.TtftSamplesMs) > 0 {
		cp.TtftSamplesMs = append([]int64(nil), v.TtftSamplesMs...)
	}
	if len(v.TpsSamples) > 0 {
		cp.TpsSamples = append([]float64(nil), v.TpsSamples...)
	}
	if v.Models != nil {
		cp.Models = make(map[string]*modelTelemetryStats, len(v.Models))
		for mk, mv := range v.Models {
			if mv != nil {
				mCopy := *mv
				if mv.LastProbeOk != nil {
					ok := *mv.LastProbeOk
					mCopy.LastProbeOk = &ok
				}
				if len(mv.TtftSamplesMs) > 0 {
					mCopy.TtftSamplesMs = append([]int64(nil), mv.TtftSamplesMs...)
				}
				if len(mv.TpsSamples) > 0 {
					mCopy.TpsSamples = append([]float64(nil), mv.TpsSamples...)
				}
				cp.Models[mk] = &mCopy
			}
		}
	}
	if len(v.RecentIncidents) > 0 {
		cp.RecentIncidents = append([]providerIncident(nil), v.RecentIncidents...)
	}
	return cp
}

func (s *modelStatsStore) ensureRecordLocked(providerID string) *providerTelemetryStats {
	rec, ok := s.records[providerID]
	if !ok || rec == nil {
		rec = &providerTelemetryStats{
			ProviderID: providerID,
			Models:     make(map[string]*modelTelemetryStats),
			UpdatedAt:  time.Now().UnixMilli(),
		}
		s.records[providerID] = rec
	}
	if rec.Models == nil {
		rec.Models = make(map[string]*modelTelemetryStats)
	}
	return rec
}

func ensureModelRecordLocked(rec *providerTelemetryStats, modelID string) *modelTelemetryStats {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	if rec.Models == nil {
		rec.Models = make(map[string]*modelTelemetryStats)
	}
	m, ok := rec.Models[modelID]
	if !ok || m == nil {
		m = &modelTelemetryStats{
			ModelID:   modelID,
			UpdatedAt: time.Now().UnixMilli(),
		}
		rec.Models[modelID] = m
	}
	return m
}

func (s *modelStatsStore) recordProbeResult(providerID string, ok bool, latencyMs int64, errMsg string, modelID string) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.ensureRecordLocked(providerID)
	rec.LastProbeLatencyMs = latencyMs
	rec.LastProbeOk = &ok
	rec.UpdatedAt = time.Now().UnixMilli()
	if m := ensureModelRecordLocked(rec, modelID); m != nil {
		m.LastProbeLatencyMs = latencyMs
		m.LastProbeOk = &ok
		m.UpdatedAt = rec.UpdatedAt
	}

	if !ok && errMsg != "" {
		incType := "api_error"
		lower := strings.ToLower(errMsg)
		if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") || strings.Contains(lower, "504") || strings.Contains(lower, "502") {
			incType = "timeout"
		}
		appendIncidentLocked(rec, providerIncident{
			Timestamp: rec.UpdatedAt,
			Type:      incType,
			ModelID:   modelID,
			Detail:    errMsg,
		})
	}
	s.saveToDiskLocked()
}

func appendIncidentLocked(rec *providerTelemetryStats, inc providerIncident) {
	rec.RecentIncidents = append([]providerIncident{inc}, rec.RecentIncidents...)
	if len(rec.RecentIncidents) > maxTelemetryIncidents {
		rec.RecentIncidents = rec.RecentIncidents[:maxTelemetryIncidents]
	}
}

func (s *modelStatsStore) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/provider/telemetry", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.mu.Lock()
			snapshot := make(map[string]providerTelemetryStats, len(s.records))
			for k, v := range s.records {
				if v != nil {
					snapshot[k] = cloneProviderTelemetryStats(v)
				}
			}
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"providers": snapshot})

		case http.MethodPost:
			var payload struct {
				Providers map[string]*providerTelemetryStats `json:"providers"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&payload); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid telemetry payload")
				return
			}
			s.mu.Lock()
			for pid, incoming := range payload.Providers {
				pid = strings.TrimSpace(pid)
				if pid == "" || incoming == nil {
					continue
				}
				incoming.ProviderID = pid
				if len(incoming.TtftSamplesMs) > maxTelemetrySamples {
					incoming.TtftSamplesMs = incoming.TtftSamplesMs[len(incoming.TtftSamplesMs)-maxTelemetrySamples:]
				}
				if len(incoming.TpsSamples) > maxTelemetrySamples {
					incoming.TpsSamples = incoming.TpsSamples[len(incoming.TpsSamples)-maxTelemetrySamples:]
				}
				if len(incoming.RecentIncidents) > maxTelemetryIncidents {
					incoming.RecentIncidents = incoming.RecentIncidents[:maxTelemetryIncidents]
				}
				s.records[pid] = incoming
			}
			s.saveToDiskLocked()
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})

		case http.MethodDelete:
			pid := strings.TrimSpace(r.URL.Query().Get("providerID"))
			var clearedDegraded []string
			s.mu.Lock()
			onChange := s.onDegradedChange
			if pid != "" {
				delete(s.records, pid)
				delete(s.consecutiveFail, pid)
				if s.degraded[pid] {
					delete(s.degraded, pid)
					clearedDegraded = append(clearedDegraded, pid)
				}
			} else {
				s.records = make(map[string]*providerTelemetryStats)
				s.consecutiveFail = make(map[string]int)
				for k, wasDegraded := range s.degraded {
					if wasDegraded {
						clearedDegraded = append(clearedDegraded, k)
					}
				}
				s.degraded = make(map[string]bool)
			}
			s.saveToDiskLocked()
			s.mu.Unlock()
			if onChange != nil {
				for _, id := range clearedDegraded {
					onChange(id, false)
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})

		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}
