package scheduler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const maxScheduleBodyBytes = 128 << 10

// RegisterRoutes mounts /api/hoxi/schedules endpoints. All routes remain
// strictly desktop/local-host only and reject remote tunnel callers with 403.
func (s *Service) RegisterRoutes(mux *http.ServeMux, isRemote func(*http.Request) bool) {
	if s == nil || mux == nil {
		return
	}
	denyRemote := func(w http.ResponseWriter, r *http.Request) bool {
		if isRemote != nil && isRemote(r) {
			writeJSONError(w, http.StatusForbidden, "scheduled tasks are only available locally")
			return true
		}
		return false
	}

	mux.HandleFunc("/api/hoxi/schedules/preview-cron", func(w http.ResponseWriter, r *http.Request) {
		if denyRemote(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		var body struct {
			Trigger TriggerSpec `json:"trigger"`
			Count   int         `json:"count,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxScheduleBodyBytes)).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid trigger payload")
			return
		}
		if body.Trigger.Kind == "" {
			if strings.TrimSpace(body.Trigger.Cron) != "" {
				body.Trigger.Kind = TriggerKindCron
			} else if body.Trigger.IntervalMinutes > 0 {
				body.Trigger.Kind = TriggerKindInterval
			}
		}
		writeJSON(w, http.StatusOK, PreviewNextRuns(body.Trigger, s.now(), body.Count))
	})

	mux.HandleFunc("/api/hoxi/schedules", func(w http.ResponseWriter, r *http.Request) {
		if denyRemote(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query()
			writeJSON(w, http.StatusOK, s.List(q.Get("projectID"), q.Get("directory")))
		case http.MethodPost:
			var body UpsertInput
			if err := json.NewDecoder(io.LimitReader(r.Body, maxScheduleBodyBytes)).Decode(&body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid schedule payload")
				return
			}
			created, err := s.Create(body)
			if err != nil {
				writeScheduleError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, created)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
	})

	mux.HandleFunc("/api/hoxi/schedules/", func(w http.ResponseWriter, r *http.Request) {
		if denyRemote(w, r) {
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/hoxi/schedules/")
		rest = strings.Trim(rest, "/")
		if rest == "" {
			writeJSONError(w, http.StatusNotFound, "schedule id is required")
			return
		}
		parts := strings.Split(rest, "/")
		id := strings.TrimSpace(parts[0])
		if id == "" {
			writeJSONError(w, http.StatusNotFound, "schedule id is required")
			return
		}

		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				sch, ok := s.Get(id)
				if !ok {
					writeJSONError(w, http.StatusNotFound, ErrScheduleNotFound.Error())
					return
				}
				writeJSON(w, http.StatusOK, sch)
			case http.MethodPatch:
				var patch PatchInput
				if err := json.NewDecoder(io.LimitReader(r.Body, maxScheduleBodyBytes)).Decode(&patch); err != nil {
					writeJSONError(w, http.StatusBadRequest, "invalid schedule update payload")
					return
				}
				updated, err := s.Update(id, patch)
				if err != nil {
					writeScheduleError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, updated)
			case http.MethodDelete:
				if err := s.Delete(id); err != nil {
					writeScheduleError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
			default:
				writeJSONError(w, http.StatusMethodNotAllowed, "GET, PATCH, or DELETE required")
			}
			return
		}

		if len(parts) != 2 {
			writeJSONError(w, http.StatusNotFound, "unknown schedule route")
			return
		}
		action := parts[1]
		switch action {
		case "run":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
				return
			}
			runRec, err := s.RunNow(id)
			if err != nil {
				writeScheduleError(w, err)
				return
			}
			writeJSON(w, http.StatusAccepted, runRec)
		case "stop":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
				return
			}
			if err := s.StopRun(id); err != nil {
				writeScheduleError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"stopped": true, "id": id})
		case "ack":
			if r.Method != http.MethodPost {
				writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
				return
			}
			updated, err := s.AcknowledgeAlert(id)
			if err != nil {
				writeScheduleError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, updated)
		case "runs":
			if r.Method != http.MethodGet {
				writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
				return
			}
			runs, err := s.History(id)
			if err != nil {
				writeScheduleError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, runs)
		default:
			writeJSONError(w, http.StatusNotFound, "unknown schedule action")
		}
	})
}

func writeScheduleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrScheduleNotFound), errors.Is(err, ErrBoundSessionMissing):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrScheduleBusy):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrStorageUnavailable):
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrTooManySchedules):
		writeJSONError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, ErrInvalidSchedule), errors.Is(err, ErrModelRequired):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
