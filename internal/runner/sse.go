package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Service) handleGlobalSSE(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, "")
}

func (s *Service) handleUnifiedSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, unsub := s.Subscribe("")
	defer unsub()

	upstreamLines := make(chan string, 128)
	directory := strings.TrimSpace(r.URL.Query().Get("directory"))
	upstreamPath := "/global/event"
	if directory != "" {
		upstreamPath = "/event"
	}
	if s.openCodeClient != nil && strings.TrimSpace(s.openCodeURL) != "" {
		if resp, err := s.openCodeRequest(r.Context(), http.MethodGet, upstreamPath, directory, nil); err == nil {
			if resp.StatusCode == http.StatusOK {
				go func() {
					defer resp.Body.Close()
					defer close(upstreamLines)
					reader := bufio.NewReaderSize(resp.Body, 64*1024)
					skipBlank := false
					for {
						line, readErr := reader.ReadString('\n')
						if len(line) > 0 {
							if strings.HasPrefix(line, "data:") && !s.AllowOpenCodeSSELine(line) {
								skipBlank = true
							} else if strings.TrimSpace(line) == "" && skipBlank {
								skipBlank = false
							} else {
								skipBlank = false
								if s.status != nil {
									s.status.InspectSSELine(strings.TrimRight(line, "\r\n"))
								}
								select {
								case upstreamLines <- line:
								case <-r.Context().Done():
									return
								}
							}
						}
						if readErr != nil {
							return
						}
					}
				}()
			} else {
				_ = resp.Body.Close()
				close(upstreamLines)
			}
		} else {
			close(upstreamLines)
		}
	} else {
		close(upstreamLines)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case evt := <-ch:
			raw, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(raw))
			flusher.Flush()
		case line, ok := <-upstreamLines:
			if !ok {
				upstreamLines = nil
				continue
			}
			_, _ = io.WriteString(w, line)
			flusher.Flush()
		}
	}
}

func (s *Service) serveSSE(w http.ResponseWriter, r *http.Request, sessionID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// Subscribe before flushing headers so clients cannot dispatch a prompt before we listen.
	ch, unsub := s.Subscribe(sessionID)
	defer unsub()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case evt := <-ch:
			raw, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(raw))
			flusher.Flush()
		}
	}
}
