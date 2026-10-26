package tools

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

type HTTPHandler struct {
	service *Service
}

func NewHTTPHandler(service *Service) *HTTPHandler {
	return &HTTPHandler{service: service}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, `{"error":"application/json required"}`, http.StatusUnsupportedMediaType)
		return
	}

	switch r.URL.Path {
	case "/api/hoxi/tools/discover":
		body, err := readBoundedJSONBody(r, 64<<10)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
			return
		}
		data, err := h.service.Discover(r.Context(), body)
		if err != nil {
			writeToolsError(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(data)
	case "/api/hoxi/tools/inspect":
		body, err := readBoundedJSONBody(r, 64<<10)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
			return
		}
		data, err := h.service.Inspect(r.Context(), body)
		if err != nil {
			writeToolsError(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(data)
	case "/api/hoxi/tools/run":
		body, err := readBoundedJSONBody(r, 512<<10)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
			return
		}
		data, err := h.service.Run(r.Context(), body)
		if err != nil {
			writeToolsError(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(data)
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func readBoundedJSONBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return []byte(`{}`), nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []byte(`{}`), nil
	}
	var check map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		return nil, err
	}
	return raw, nil
}

func writeToolsError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotLoggedIn) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	var upErr *UpstreamAPIError
	if errors.As(err, &upErr) {
		status := upErr.StatusCode
		if status < 400 || status >= 600 {
			status = http.StatusBadGateway
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": upErr.Error()})
		return
	}
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
