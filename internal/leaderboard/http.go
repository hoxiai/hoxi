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
	case "/api/hoxi/models/pricing", "/api/hoxi/models/pricing/":
		h.handleModelPricing(w, r)
	case "/api/hoxi/models/free-tiers", "/api/hoxi/models/free-tiers/":
		h.handleFreeTiers(w, r)
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

func (h *HTTPHandler) handleModelPricing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	forceRefresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	res, err := h.svc.GetModelPricing(forceRefresh)
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
	lang := r.URL.Query().Get("lang")

	res, err := h.svc.GetLeaderboard(dimension, limit, lang)
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

	lang := r.URL.Query().Get("lang")
	res, err := h.svc.GetConnectableGateways(lang)
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

func (h *HTTPHandler) handleFreeTiers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = r.Header.Get("Accept-Language")
	}

	res, err := h.svc.GetFreeTiers(lang)
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
