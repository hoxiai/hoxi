package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxTelemetrySamples = 30
const maxTelemetryIncidents = 10

type providerIncident struct {
	Timestamp int64  `json:"timestamp"`
	Type      string `json:"type"` // "timeout" | "api_error" | "retry" | "tool_error" | "hallucination_loop" | "user_revert"
	ModelID   string `json:"modelID,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type providerTelemetryStats struct {
	ProviderID         string             `json:"providerID"`
	TotalTurns         int                `json:"totalTurns"`
	SuccessTurns       int                `json:"successTurns"`
	ErrorTurns         int                `json:"errorTurns"`
	TimeoutTurns       int                `json:"timeoutTurns"`
	RetryCount         int                `json:"retryCount"`
	ToolCallsTotal     int                `json:"toolCallsTotal"`
	ToolCallsFailed    int                `json:"toolCallsFailed"`
	HallucinationFlags int                `json:"hallucinationFlags"`
	RevertCount        int                `json:"revertCount"`
	CacheHitTurns      int                `json:"cacheHitTurns"`
	ReasoningTurns     int                `json:"reasoningTurns"`
	TtftSamplesMs      []int64            `json:"ttftSamplesMs,omitempty"`
	TpsSamples         []float64          `json:"tpsSamples,omitempty"`
	LastProbeLatencyMs int64              `json:"lastProbeLatencyMs,omitempty"`
	LastProbeOk        *bool              `json:"lastProbeOk,omitempty"`
	RecentIncidents    []providerIncident `json:"recentIncidents,omitempty"`
	UpdatedAt          int64              `json:"updatedAt"`
}

type providerTelemetryStore struct {
	mu       sync.Mutex
	filePath string
	records  map[string]*providerTelemetryStats
}

func newProviderTelemetryStore(rootDir string) *providerTelemetryStore {
	storePath := resolveTelemetryFilePath(rootDir)
	s := &providerTelemetryStore{
		filePath: storePath,
		records:  make(map[string]*providerTelemetryStats),
	}
	s.loadFromDisk()
	return s
}

func resolveTelemetryFilePath(rootDir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dir := filepath.Join(home, ".hoxi")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return filepath.Join(dir, "provider-telemetry.json")
		}
	}
	fallbackDir := filepath.Join(rootDir, ".hoxi")
	_ = os.MkdirAll(fallbackDir, 0o755)
	return filepath.Join(fallbackDir, "provider-telemetry.json")
}

func (s *providerTelemetryStore) loadFromDisk() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil || len(data) == 0 {
		return
	}
	var loaded map[string]*providerTelemetryStats
	if err := json.Unmarshal(data, &loaded); err == nil && loaded != nil {
		s.records = loaded
	}
}

func (s *providerTelemetryStore) saveToDiskLocked() {
	if s.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.filePath), 0o755)
	_ = os.WriteFile(s.filePath, data, 0o644)
}

func (s *providerTelemetryStore) ensureRecordLocked(providerID string) *providerTelemetryStats {
	rec, ok := s.records[providerID]
	if !ok || rec == nil {
		rec = &providerTelemetryStats{
			ProviderID: providerID,
			UpdatedAt:  time.Now().UnixMilli(),
		}
		s.records[providerID] = rec
	}
	return rec
}

func (s *providerTelemetryStore) recordProbeResult(providerID string, ok bool, latencyMs int64, errMsg string, modelID string) {
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

func appendInt64Sample(list []int64, val int64) []int64 {
	if val <= 0 {
		return list
	}
	next := append(list, val)
	if len(next) > maxTelemetrySamples {
		next = next[len(next)-maxTelemetrySamples:]
	}
	return next
}

func appendFloat64Sample(list []float64, val float64) []float64 {
	if val <= 0 {
		return list
	}
	next := append(list, val)
	if len(next) > maxTelemetrySamples {
		next = next[len(next)-maxTelemetrySamples:]
	}
	return next
}

func (s *providerTelemetryStore) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/provider/telemetry", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.mu.Lock()
			snapshot := make(map[string]providerTelemetryStats, len(s.records))
			for k, v := range s.records {
				if v != nil {
					snapshot[k] = *v
				}
			}
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"providers": snapshot})

		case http.MethodPost:
			var payload struct {
				Providers map[string]*providerTelemetryStats `json:"providers"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
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
			s.mu.Lock()
			if pid != "" {
				delete(s.records, pid)
			} else {
				s.records = make(map[string]*providerTelemetryStats)
			}
			s.saveToDiskLocked()
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})

		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}
