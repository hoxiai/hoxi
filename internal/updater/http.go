package updater

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
)

var activeChecker atomic.Pointer[Checker]

type HTTPHandler struct {
	checker  *Checker
	draining func() bool
}

func NewHTTPHandler(checker *Checker) *HTTPHandler {
	if checker != nil {
		activeChecker.Store(checker)
	}
	return &HTTPHandler{checker: checker}
}

func (h *HTTPHandler) SetDraining(fn func() bool) { h.draining = fn }

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/hoxi/updater")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch {
	case path == "/native" && r.Method == http.MethodGet:
		draining := h.draining != nil && h.draining()
		_ = json.NewEncoder(w).Encode(struct {
			Available bool             `json:"available"`
			Draining  bool             `json:"draining"`
			Progress  *InstallProgress `json:"progress,omitempty"`
		}{NativeAvailable(), draining, installProgress.Load()})
	case path == "/install" && r.Method == http.MethodPost:
		if !NativeAvailable() {
			http.Error(w, `{"error":"native updater is not configured"}`, http.StatusServiceUnavailable)
			return
		}
		if err := CheckNative(); err != nil {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"started": true})
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
