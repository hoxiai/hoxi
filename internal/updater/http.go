package updater

import (
	"encoding/json"
	"net/http"
	"strings"
)

type HTTPHandler struct {
	checker *Checker
}

func NewHTTPHandler(checker *Checker) *HTTPHandler {
	return &HTTPHandler{checker: checker}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/hoxi/updater")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch {
	case (path == "/check" || path == "") && r.Method == http.MethodGet:
		h.handleCheck(w, r)
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func (h *HTTPHandler) handleCheck(w http.ResponseWriter, r *http.Request) {
	res, err := h.checker.Check(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(res)
}
