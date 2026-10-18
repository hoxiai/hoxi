package leaderboard

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type HTTPHandler struct {
	svc *Service
}

func NewHTTPHandler(svc *Service) *HTTPHandler {
	return &HTTPHandler{svc: svc}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch r.URL.Path {
	case "/api/hoxi/models/leaderboard", "/api/hoxi/models/leaderboard/":
		h.handleLeaderboard(w, r)
	case "/api/hoxi/gateways/connectable", "/api/hoxi/gateways/connectable/":
		h.handleGateways(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    404,
			"message": "not found",
		})
	}
}

func (h *HTTPHandler) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	dimension := r.URL.Query().Get("sort")
	if dimension == "" {
		dimension = r.URL.Query().Get("dimension")
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	res, err := h.svc.GetLeaderboard(dimension, limit)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func (h *HTTPHandler) handleGateways(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	res, err := h.svc.GetConnectableGateways()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    500,
			"message": err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}
