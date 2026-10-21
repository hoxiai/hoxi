package orchestration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (s *Service) handlePlans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if sessionID := strings.TrimSpace(r.URL.Query().Get("session")); sessionID != "" {
			writeOrchestrationJSON(w, http.StatusOK, s.ListBySession(sessionID))
			return
		}
		writeOrchestrationJSON(w, http.StatusOK, s.List(r.URL.Query().Get("directory")))
	case http.MethodPost:
		var request PlanRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 512<<10)).Decode(&request); err != nil {
			writeOrchestrationError(w, http.StatusBadRequest, "invalid orchestration request")
			return
		}
		plan, err := s.Create(request)
		if err != nil {
			writeOrchestrationError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeOrchestrationJSON(w, http.StatusOK, plan)
	default:
		writeOrchestrationError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) handlePlan(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hoxi/orchestration/plans/"), "/")
	parts := strings.SplitN(sub, "/", 2)
	id := strings.TrimSpace(parts[0])
	action := ""
	if len(parts) > 1 {
		action = strings.TrimSpace(parts[1])
	}
	if id == "" {
		writeOrchestrationError(w, http.StatusBadRequest, "plan id is required")
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		plan, ok := s.Get(id)
		if !ok {
			writeOrchestrationError(w, http.StatusNotFound, "orchestration plan not found")
			return
		}
		writeOrchestrationJSON(w, http.StatusOK, plan)
	case action == "cancel" && r.Method == http.MethodPost:
		plan, err := s.Cancel(id)
		if err != nil {
			writeOrchestrationError(w, http.StatusNotFound, err.Error())
			return
		}
		writeOrchestrationJSON(w, http.StatusOK, plan)
	case action == "retry" && r.Method == http.MethodPost:
		plan, err := s.Retry(id)
		if err != nil {
			switch {
			case errors.Is(err, ErrPlanNotFound):
				writeOrchestrationError(w, http.StatusNotFound, err.Error())
			case errors.Is(err, ErrPlanActive), errors.Is(err, ErrPlanStopping):
				writeOrchestrationError(w, http.StatusConflict, err.Error())
			default:
				writeOrchestrationError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		writeOrchestrationJSON(w, http.StatusOK, plan)
	default:
		writeOrchestrationError(w, http.StatusNotFound, "unknown orchestration endpoint")
	}
}

func writeOrchestrationJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOrchestrationError(w http.ResponseWriter, status int, message string) {
	writeOrchestrationJSON(w, status, map[string]string{"error": message})
}
