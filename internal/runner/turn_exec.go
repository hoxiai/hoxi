package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

func (s *Service) startTurnProcess(
	rec SessionRecord,
	binPath, mode, modelID, effort, promptText string,
	userInfo MessageInfo,
	userParts []MessagePart,
	asstMsgID string,
) error {
	var args []string
	if rec.Runtime == string(RuntimeClaudeCode) {
		args = BuildClaudeArgs(mode, modelID, effort, rec.ExternalID)
	} else {
		args = BuildAgyArgs(mode, modelID, effort, rec.ExternalID)
	}

	cmd := exec.Command(binPath, args...)
	cmd.Dir = rec.WorktreePath

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return err
	}

	ap := &activeProcess{
		sessionID: rec.ID,
		runtime:   rec.Runtime,
		cmd:       cmd,
		stdin:     stdinPipe,
		done:      make(chan struct{}),
	}
	s.mu.Lock()
	s.procs[rec.ID] = ap
	s.mu.Unlock()

	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}

	tr := NewTurnTranslator(rec.ID, rec.Runtime, asstMsgID, userInfo.ID, rec.ProviderID, modelID, mode, rec.ExternalID)
	s.broadcast(rec.ID, tr.InitialEvents(userInfo, userParts)...)

	inputBytes, _ := EncodeStreamInputTurn(rec.Runtime, promptText)
	_, _ = stdinPipe.Write(inputBytes)
	_ = stdinPipe.Close()

	go func() {
		defer func() {
			s.store.Flush()
			close(ap.done)
			s.mu.Lock()
			if s.procs[rec.ID] == ap {
				delete(s.procs, rec.ID)
			}
			s.mu.Unlock()
			if s.status != nil {
				s.status.SetSessionBusy(rec.ID, false)
			}
		}()

		readStreamLines(stdoutPipe, func(line string) {
			evts := tr.FeedLine(line)
			if extID := tr.ExternalID(); extID != "" && extID != rec.ExternalID {
				rec.ExternalID = extID
				_, _, _ = s.store.Update(rec.ID, func(r *SessionRecord) {
					r.ExternalID = extID
				})
			}
			if len(evts) > 0 {
				s.broadcast(rec.ID, evts...)
			}
		})

		waitErr := cmd.Wait()
		if !tr.IsTurnFinished() {
			if ap.wasAborted() {
				tr.msgInfo.Time.Completed = time.Now().UnixMilli()
				tr.msgInfo.Finish = "aborted"
				s.broadcast(rec.ID,
					SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.msgInfo}},
					SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}},
				)
				return
			}
			errText := strings.TrimSpace(stderrBuf.String())
			if errText == "" && waitErr != nil {
				errText = waitErr.Error()
			}
			if errText == "" {
				errText = "CLI process exited unexpectedly without a result event"
			}
			msgErr := &MessageError{
				Name: "RunnerProcessError",
				Data: &MessageErrorData{Message: errText, IsRetryable: false},
			}
			tr.msgInfo.Time.Completed = time.Now().UnixMilli()
			tr.msgInfo.Error = msgErr
			tr.msgInfo.Finish = "error"
			s.broadcast(rec.ID,
				SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.msgInfo}},
				SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": msgErr}},
				SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}},
			)
		}
	}()

	return nil
}

func (s *Service) abortActiveProcess(sessionID string) {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap != nil {
		ap.Abort()
	}
	if s.status != nil {
		s.status.SetSessionBusy(sessionID, false)
	}
}

func (s *Service) handleSessionAbort(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	s.abortActiveProcess(sessionID)
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.status",
		Properties: map[string]interface{}{"sessionID": sessionID, "status": SessionStatus{Type: "idle"}},
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Service) handleGlobalSSE(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, "")
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

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"error": msg})
}
