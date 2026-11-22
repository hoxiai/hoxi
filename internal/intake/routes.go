package intake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// IsPortalAPIPath reports whether requestPath is an exact public tester portal API endpoint.
func IsPortalAPIPath(requestPath string) bool {
	if requestPath == "" || path.Clean(requestPath) != requestPath {
		return false
	}
	switch requestPath {
	case "/api/hoxi/intake/portal/info",
		"/api/hoxi/intake/portal/tasks",
		"/api/hoxi/intake/portal/tasks/feedback",
		"/api/hoxi/intake/portal/attachments",
		"/api/hoxi/intake/portal/timeline",
		"/api/hoxi/intake/portal/stream":
		return true
	}
	if strings.HasPrefix(requestPath, "/api/hoxi/intake/portal/attachments/") {
		rest := strings.TrimPrefix(requestPath, "/api/hoxi/intake/portal/attachments/")
		return rest != "" && !strings.Contains(rest, "/")
	}
	return false
}

func extractPortalToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if hdr := strings.TrimSpace(r.Header.Get("X-Hoxi-Intake-Token")); hdr != "" {
		return hdr
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

// RegisterRoutes mounts desktop-only management routes and portal routes on mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux, isRemote func(*http.Request) bool) {
	if s == nil || mux == nil {
		return
	}
	rejectRemote := func(w http.ResponseWriter, r *http.Request) bool {
		if isRemote != nil && isRemote(r) {
			writeJSONError(w, http.StatusForbidden, "intake management is only accessible on desktop")
			return true
		}
		return false
	}

	mux.HandleFunc("/api/hoxi/intake/links", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		s.handleLinks(w, r)
	})
	mux.HandleFunc("/api/hoxi/intake/tasks", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		s.handleDesktopTasks(w, r)
	})
	mux.HandleFunc("/api/hoxi/intake/tasks/approve", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		s.handleApproveTask(w, r)
	})
	mux.HandleFunc("/api/hoxi/intake/tasks/status", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		s.handleUpdateTaskStatus(w, r)
	})
	mux.HandleFunc("/api/hoxi/intake/portal/", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemote(w, r) {
			return
		}
		s.ServePortalHTTP(w, r)
	})
}

func (s *Service) handleLinks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if err := s.LoadError(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dir := strings.TrimSpace(r.URL.Query().Get("directory"))
		writeJSON(w, http.StatusOK, map[string]any{"links": s.ListLinks(dir)})
	case http.MethodPost:
		var in CreateLinkInput
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
			return
		}
		if in.Directory == "" {
			in.Directory = r.URL.Query().Get("directory")
		}
		pub, err := s.CreateLink(in)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, pub)
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if err := s.DeleteLink(id); err != nil {
			if errors.Is(err, ErrLinkNotFound) {
				writeJSONError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) handleDesktopTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	if err := s.LoadError(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.ListDesktopTasks(dir)})
}

func (s *Service) handleApproveTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var in ApproveTaskInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	item, err := s.ApproveAndRun(r.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, ErrTaskNotFound):
			writeJSONError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrTaskBusy):
			writeJSONError(w, http.StatusConflict, err.Error())
		default:
			writeJSONError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Service) handleUpdateTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var in UpdateTaskStatusInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	item, err := s.UpdateTaskStatus(in)
	if err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// ServePortalHTTP serves strictly authenticated, project-scoped tester portal requests.
func (s *Service) ServePortalHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")

	if r.URL.RawPath != "" || !IsPortalAPIPath(r.URL.Path) {
		writeJSONError(w, http.StatusNotFound, "unknown portal route")
		return
	}
	link, err := s.AuthorizePortalToken(extractPortalToken(r))
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}

	switch {
	case r.URL.Path == "/api/hoxi/intake/portal/info" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"projectName": link.ProjectName,
			"label":       link.Label,
			"tasks":       s.ListPortalTasks(link),
		})
	case r.URL.Path == "/api/hoxi/intake/portal/tasks" && r.Method == http.MethodPost:
		var in SubmitTaskInput
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
			return
		}
		created, sErr := s.SubmitPortalTask(link, in)
		if sErr != nil {
			status := http.StatusBadRequest
			if errors.Is(sErr, ErrTooManyPending) {
				status = http.StatusTooManyRequests
			}
			writeJSONError(w, status, sErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, created)
	case r.URL.Path == "/api/hoxi/intake/portal/tasks/feedback" && r.Method == http.MethodPost:
		var in PortalFeedbackInput
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON payload")
			return
		}
		updated, fErr := s.SubmitPortalFeedback(link, in)
		if fErr != nil {
			status := http.StatusBadRequest
			if errors.Is(fErr, ErrTaskNotFound) {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, fErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case r.URL.Path == "/api/hoxi/intake/portal/attachments" && r.Method == http.MethodPost:
		s.handlePortalUpload(w, r, link)
	case strings.HasPrefix(r.URL.Path, "/api/hoxi/intake/portal/attachments/") && r.Method == http.MethodGet:
		attID := strings.TrimPrefix(r.URL.Path, "/api/hoxi/intake/portal/attachments/")
		s.handlePortalAttachmentRead(w, link, attID)
	case r.URL.Path == "/api/hoxi/intake/portal/timeline" && r.Method == http.MethodGet:
		taskID := strings.TrimSpace(r.URL.Query().Get("taskId"))
		tl, tErr := s.PortalTimeline(link, taskID)
		if tErr != nil {
			writeJSONError(w, http.StatusNotFound, tErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, tl)
	case r.URL.Path == "/api/hoxi/intake/portal/stream" && r.Method == http.MethodGet:
		s.handlePortalSSE(w, r, link)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) handlePortalUpload(w http.ResponseWriter, r *http.Request, link Link) {
	maxBytes := DefaultMaxAttachmentBytes
	if s.attachments != nil && s.attachments.MaxBytes() > 0 {
		maxBytes = s.attachments.MaxBytes()
	}
	if err := r.ParseMultipartForm(int64(maxBytes) + (1 << 20)); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref, err := s.SavePortalAttachment(link, hdr.Filename, hdr.Header.Get("Content-Type"), data)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrAttachmentQuotaExceeded) || errors.Is(err, ErrUploadRateLimited) {
			status = http.StatusTooManyRequests
		} else if errors.Is(err, ErrUnauthorizedToken) {
			status = http.StatusUnauthorized
		}
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ref)
}

func safeInlinePortalMime(mime string) bool {
	lower := strings.ToLower(strings.TrimSpace(mime))
	if strings.Contains(lower, "svg") || strings.Contains(lower, "html") || strings.Contains(lower, "xml") {
		return false
	}
	return strings.HasPrefix(lower, "image/") || lower == "application/pdf" || strings.HasPrefix(lower, "video/")
}

func (s *Service) handlePortalAttachmentRead(w http.ResponseWriter, link Link, attID string) {
	meta, raw, err := s.ReadPortalAttachment(link, attID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "attachment not found")
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Content-Type", meta.Mime)
	disposition := "attachment"
	if safeInlinePortalMime(meta.Mime) {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, path.Base(meta.Filename)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Service) handlePortalSSE(w http.ResponseWriter, r *http.Request, link Link) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "data: {\"type\":\"connected\"}\n\n"); err != nil {
		return
	}
	flusher.Flush()

	ch, unsub := s.SubscribePortal(link.ID)
	defer unsub()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, "data: {\"type\":\"heartbeat\"}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case evt, ok := <-ch:
			if !ok {
				return
			}
			encoded, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
				return
			}
			flusher.Flush()
		}
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
