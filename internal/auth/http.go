package auth

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
	path := strings.TrimPrefix(r.URL.Path, "/api/hoxi/auth")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodPost {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, `{"error":"application/json required"}`, http.StatusUnsupportedMediaType)
			return
		}
	}

	switch {
	case (path == "/status" || path == "") && r.Method == http.MethodGet:
		h.handleStatus(w, r)
	case path == "/start" && r.Method == http.MethodPost:
		h.handleStart(w, r)
	case path == "/exchange" && r.Method == http.MethodPost:
		h.handleExchange(w, r)
	case path == "/logout" && r.Method == http.MethodPost:
		h.handleLogout(w, r)
	case path == "/member" && r.Method == http.MethodGet:
		h.handleMember(w, r)
	case path == "/checkin" && r.Method == http.MethodPost:
		h.handleCheckin(w, r)
	case path == "/open-url" && r.Method == http.MethodPost:
		h.handleOpenURL(w, r)
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func (h *HTTPHandler) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := h.service.Snapshot()
	_ = json.NewEncoder(w).Encode(snap)
}

func (h *HTTPHandler) handleStart(w http.ResponseWriter, r *http.Request) {
	res, err := h.service.StartAuthorization(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(res)
}

func (h *HTTPHandler) handleExchange(w http.ResponseWriter, r *http.Request) {
	var req ExchangeTokenRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
		return
	}

	auth, err := h.service.ExchangeToken(r.Context(), req.HandoffToken, req.State)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"user":    auth.User,
	})
}

func (h *HTTPHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Logout(r.Context()); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *HTTPHandler) handleMember(w http.ResponseWriter, r *http.Request) {
	data, err := h.service.FetchMemberStatus(r.Context())
	if err != nil {
		h.writeMemberError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(data)
}

func (h *HTTPHandler) handleCheckin(w http.ResponseWriter, r *http.Request) {
	data, err := h.service.PerformCheckin(r.Context())
	if err != nil {
		h.writeMemberError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(data)
}

func (h *HTTPHandler) handleOpenURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json body"})
		return
	}
	targetURL, err := h.service.OpenPortalPage(req.Target)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"url":     targetURL,
	})
}

func (h *HTTPHandler) writeMemberError(w http.ResponseWriter, err error) {
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
