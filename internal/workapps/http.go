package workapps

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

type HTTPHandler struct {
	service *Service
}

func NewHTTPHandler(service *Service) *HTTPHandler {
	return &HTTPHandler{service: service}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch r.URL.Path {
	case "/api/hoxi/work/apps":
		h.handleApps(w, r)
	case "/api/hoxi/work/stores":
		h.handleStores(w, r)
	case "/api/hoxi/work/proxy-check":
		h.handleProxyCheck(w, r)
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func (h *HTTPHandler) handleApps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := h.service.FetchApps(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(data)
	case http.MethodPut, http.MethodPost:
		if !requireJSONContentType(w, r) {
			return
		}
		var state map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&state); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
			return
		}
		if err := h.service.SaveState(state); err != nil {
			if errors.Is(err, ErrInvalidStoreConfig) {
				w.WriteHeader(http.StatusBadRequest)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *HTTPHandler) handleStores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	stores := h.service.ListStores()
	if stores == nil {
		stores = []StoreProfile{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"stores": stores})
}

func (h *HTTPHandler) handleProxyCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !requireJSONContentType(w, r) {
		return
	}
	var req struct {
		StoreID  string `json:"storeId"`
		ProxyURL string `json:"proxyURL"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
		return
	}
	rawProxy := strings.TrimSpace(req.ProxyURL)
	storeID := strings.TrimSpace(req.StoreID)
	if storeID != "" && (rawProxy == "" || isRedactedProxyPlaceholder(rawProxy)) {
		prof, ok := h.service.GetStore(storeID)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "store not found"})
			return
		}
		if rawProxy != "" && RedactProxyURL(prof.ProxyURL) != rawProxy {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "proxy credentials must be re-entered when changing proxy host"})
			return
		}
		rawProxy = strings.TrimSpace(prof.ProxyURL)
	}
	if rawProxy == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "proxyURL or configured storeId is required"})
		return
	}
	if isRedactedProxyPlaceholder(rawProxy) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "proxy credentials are redacted; supply full proxyURL or storeId"})
		return
	}
	if _, err := ValidateProxyURL(rawProxy); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ProxyProbeResult{OK: false, Error: err.Error()})
		return
	}
	res, _ := ProbeProxy(r.Context(), rawProxy)
	_ = json.NewEncoder(w).Encode(res)
}

func requireJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, `{"error":"application/json required"}`, http.StatusUnsupportedMediaType)
		return false
	}
	return true
}
