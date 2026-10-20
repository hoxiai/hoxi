package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Subagents run silently for minutes; their transcripts are checked at this
// pace so the running task card keeps showing progress.
const subagentPollInterval = 3 * time.Second

// orphanDrainGrace is how long stdout/stderr may stay open after the CLI
// exits before leftover holders are treated as orphans.
const orphanDrainGrace = 2 * time.Second

func (s *Service) startTurnProcess(
	rec SessionRecord,
	binPath, mode, modelID, effort, promptText string,
	userInfo MessageInfo,
	userParts []MessagePart,
	asstMsgID string,
) error {
	var args []string
	switch rec.Runtime {
	case string(RuntimeClaudeCode):
		args = BuildClaudeArgs(mode, modelID, effort, rec.ExternalID)
	case string(RuntimeCodeBuddy):
		args = BuildCodeBuddyArgs(mode, modelID, effort, rec.ExternalID)
	case string(RuntimeQoder):
		args = BuildQoderArgs(mode, modelID, effort, rec.ExternalID)
	default:
		args = BuildAgyArgs(mode, modelID, effort, rec.ExternalID)
	}

	cmd := exec.Command(binPath, args...)
	cmd.Dir = rec.WorktreePath

	var hookEnv []string
	revokeHook := func() {}
	if rec.Runtime == string(RuntimeClaudeCode) {
		var hookArgs []string
		hookArgs, hookEnv, revokeHook = s.issueClaudeHook(mode, rec.ID, asstMsgID)
		cmd.Args = append(cmd.Args, hookArgs...)
	} else if rec.Runtime == string(RuntimeAntigravity) {
		s.ensureAgyHookInstalled()
		hookEnv, revokeHook = s.issueHookEnv(rec.ID, asstMsgID)
	}
	if len(hookEnv) > 0 {
		cmd.Env = append(os.Environ(), hookEnv...)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		revokeHook()
		return err
	}
	// Hoxi owns the stdout pipe (instead of StdoutPipe) so Wait returns when
	// the CLI exits even if an orphaned grandchild still holds the write end.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		revokeHook()
		return err
	}
	cmd.Stdout = stdoutW
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	cmd.WaitDelay = orphanDrainGrace
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		revokeHook()
		return err
	}
	_ = stdoutW.Close()
	s.markContextDelivered(rec.ID)

	ap := &activeProcess{
		sessionID: rec.ID,
		runtime:   rec.Runtime,
		cmd:       cmd,
		stdin:     stdinPipe,
		done:      make(chan struct{}),
	}
	ap.stall.touch(time.Now())
	s.mu.Lock()
	s.procs[rec.ID] = ap
	s.mu.Unlock()

	if s.status != nil {
		s.status.SetSessionBusy(rec.ID, true)
	}
	go s.watchStall(ap)

	tr := NewTurnTranslator(rec.ID, rec.Runtime, asstMsgID, userInfo.ID, rec.ProviderID, modelID, mode, rec.ExternalID)
	tr.createChild = s.childSessionFactory(rec, modelID, mode)
	s.broadcast(rec.ID, tr.InitialEvents(userInfo, userParts)...)

	inputBytes, _ := EncodeStreamInputTurn(rec.Runtime, promptText)
	_, _ = stdinPipe.Write(inputBytes)
	_ = stdinPipe.Close()

	go func() {
		defer func() {
			revokeHook()
			s.cancelSessionApprovals(rec.ID, "the CLI process exited")
			s.syncSessionMemoryFile(rec.ID)
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

		// emitMu keeps translate+broadcast atomic so a poll snapshot can never
		// land after (and overwrite) a newer stdout snapshot of the same part.
		var emitMu sync.Mutex
		stopPoll := make(chan struct{})
		pollDone := make(chan struct{})
		go func() {
			defer close(pollDone)
			ticker := time.NewTicker(subagentPollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stopPoll:
					return
				case <-ticker.C:
					emitMu.Lock()
					if evts := tr.PollSubagentActivity(); len(evts) > 0 {
						s.noteActivity(ap)
						s.broadcastRouted(rec.ID, evts)
					}
					emitMu.Unlock()
				}
			}
		}()

		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			readStreamLines(stdoutR, func(line string) {
				emitMu.Lock()
				defer emitMu.Unlock()
			// A stopped CLI reports its interruption as a failed result;
			// Hoxi finishes the turn itself as stopped (or stalled).
			if ap.wasAborted() && isStreamResultLine(line) {
				return
			}
			evts := tr.FeedLine(line)
			// Only output the user can see counts as progress: CLIs keep
			// writing heartbeats (Claude Code's `tool_progress`) while a
			// tool hangs.
			if len(evts) > 0 {
				s.noteActivity(ap)
			}
				if extID := tr.ExternalID(); extID != "" && extID != rec.ExternalID {
					rec.ExternalID = extID
					_, _, _ = s.store.Update(rec.ID, func(r *SessionRecord) {
						r.ExternalID = extID
					})
				}
				s.broadcastRouted(rec.ID, evts)
			})
		}()

		waitErr := cmd.Wait()
		reapOrphanedOutput(cmd.Process, stdoutR, readerDone)
		close(stopPoll)
		<-pollDone
		defer func() {
			for _, id := range tr.ChildSessionIDs() {
				s.setChildBusy(id, false)
			}
		}()
		if !tr.IsTurnFinished() {
			s.broadcastRouted(rec.ID, tr.AbandonTurn(time.Now().UnixMilli()))
			stallErr := ap.stall.abortError()
			if ap.wasAborted() && stallErr == nil {
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
			if stallErr != nil {
				msgErr = stallErr
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

func isStreamResultLine(line string) bool {
	if !strings.Contains(line, `"result"`) {
		return false
	}
	var env struct {
		Type string `json:"type"`
	}
	return json.Unmarshal([]byte(line), &env) == nil && env.Type == "result"
}

// reapOrphanedOutput runs once the CLI has exited. A reader still blocked
// after the grace period means a grandchild (dev server, watch-mode test,
// `tail -f`) inherited stdout and would keep the turn busy forever: its group
// is killed, and the pipe is closed as a last resort for setsid escapees.
// Background jobs that redirected their output elsewhere are left running.
func reapOrphanedOutput(proc *os.Process, stdout *os.File, readerDone <-chan struct{}) {
	defer stdout.Close()
	select {
	case <-readerDone:
		return
	case <-time.After(orphanDrainGrace):
	}
	_ = signalProcessGroup(proc, os.Kill)
	select {
	case <-readerDone:
	case <-time.After(orphanDrainGrace):
		_ = stdout.Close()
		<-readerDone
	}
}

func (s *Service) abortActiveProcess(sessionID string) {
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	s.cancelSessionApprovals(sessionID, "the session was stopped")
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
