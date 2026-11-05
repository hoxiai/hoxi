package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/subproc"
)

// Subagents run silently for minutes; their transcripts are checked at this
// pace so the running task card keeps showing progress.
const subagentPollInterval = 3 * time.Second

// orphanDrainGrace is how long stdout/stderr may stay open after the CLI
// exits before leftover holders are treated as orphans.
var orphanDrainGrace = 2 * time.Second

func (s *Service) startTurnProcess(
	rec SessionRecord,
	binPath, mode, modelID, effort, promptText string,
	userInfo MessageInfo,
	storedParts []MessagePart,
	deliveryParts []MessagePart,
	asstMsgID string,
) error {
	geminiUsesAgy := rec.Runtime == string(RuntimeGeminiCLI) &&
		(ShouldGeminiCLIUseAgy(modelID) || isAgyBinary(binPath))
	rawBinPath := binPath
	priorTurns := append([]MessageTurn(nil), rec.Turns...)
	initialExternalID := strings.TrimSpace(rec.ExternalID)

	var claudeHookArgs []string
	var hookEnv []string
	revokeHook := func() {}
	if rec.Runtime == string(RuntimeClaudeCode) {
		claudeHookArgs, hookEnv, revokeHook = s.issueClaudeHook(mode, rec.ID, asstMsgID)
	} else if rec.Runtime == string(RuntimeGeminiCLI) {
		if geminiUsesAgy {
			s.ensureAgyHookInstalled()
			hookEnv, revokeHook = s.issueHookEnv(rec.ID, asstMsgID)
		} else {
			hookEnv, revokeHook = s.issueGeminiHook(mode, rec.ID, asstMsgID)
		}
	} else if rec.Runtime == string(RuntimeAntigravity) {
		s.ensureAgyHookInstalled()
		hookEnv, revokeHook = s.issueHookEnv(rec.ID, asstMsgID)
	}

	args := buildTurnCLIArgs(rec.Runtime, mode, modelID, effort, rec.ExternalID, geminiUsesAgy, claudeHookArgs)
	binPath, args, err := s.confineRuntimeCommand(rec, rawBinPath, args)
	if err != nil {
		revokeHook()
		return err
	}
	if base := strings.TrimSpace(s.hookBaseURL()); base != "" {
		hookEnv = append(hookEnv, "HOXI_GATEWAY_URL="+base, "HOXI_BRIDGE_URL="+base)
	}
	procEnv := runtimeProcessEnv(rec, os.Environ(), hookEnv)
	cmd := subproc.Command(binPath, args...)
	cmd.Dir = rec.WorktreePath
	cmd.Env = procEnv

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
		abortCh:   make(chan struct{}),
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
	s.broadcast(rec.ID, tr.InitialEvents(userInfo, storedParts)...)

	inputRuntime := rec.Runtime
	if geminiUsesAgy {
		inputRuntime = string(RuntimeAntigravity)
	}
	inputBytes, _ := EncodeStreamInputTurnWithParts(inputRuntime, promptText, deliveryParts)
	_, _ = stdinPipe.Write(inputBytes)
	_ = stdinPipe.Close()

	go func() {
		var terminalEvts []SSEEvent
		defer func() {
			for _, id := range tr.ChildSessionIDs() {
				s.setChildBusy(id, false)
			}
			revokeHook()
			s.cancelSessionApprovals(rec.ID, "the CLI process exited")
			s.syncSessionMemoryFile(rec.ID)
			s.store.FlushSession(rec.ID)
			s.releaseActiveProcess(rec.ID, ap)
			if len(terminalEvts) > 0 {
				s.broadcastRouted(rec.ID, terminalEvts)
			}
			close(ap.done)
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

		retryDelays := s.cliTurnRetryDelays()
		retryAttempt := 0
		var heldFailedResultLine string
		canRetryWithoutResume := rec.Runtime == string(RuntimeGeminiCLI) && strings.TrimSpace(rec.ExternalID) != ""
		feedStdout := func(r *os.File) <-chan struct{} {
			done := make(chan struct{})
			go func() {
				defer close(done)
				readStreamLines(r, func(line string) {
					emitMu.Lock()
					defer emitMu.Unlock()
					// A stopped CLI reports its interruption as a failed result;
					// Hoxi finishes the turn itself as stopped (or stalled).
					if ap.wasAborted() && isStreamResultLine(line) {
						return
					}
					if isFailedStreamResultLine(line) && !tr.IsStaleAgyResultError(line) && !ap.wasAborted() && ap.stall.abortError() == nil {
						resErr := extractFailedStreamResultError(line, tr.LastErrorText())
						canResume := !tr.HasToolParts() || strings.TrimSpace(firstNonEmptyStr(tr.ExternalID(), rec.ExternalID)) != ""
						if (retryAttempt < len(retryDelays) && isRetryableTurnError(resErr) && canResume) ||
							(canRetryWithoutResume && len(tr.partsByID) == 0) {
							heldFailedResultLine = line
							return
						}
					}
					evts := tr.FeedLine(line)
					s.noteToolEvents(rec.ID, evts)
					// Only output the user can see counts as progress: CLIs keep
					// writing heartbeats (Claude Code's `tool_progress`) while a
					// tool hangs.
					if len(evts) > 0 {
						s.noteActivity(ap)
					}
					if extID := tr.ExternalID(); extID != "" && extID != rec.ExternalID {
						rec.ExternalID = extID
						_, _, _ = s.store.Update(rec.ID, func(sr *SessionRecord) {
							sr.ExternalID = extID
						})
					}
					var liveEvts []SSEEvent
					for _, evt := range evts {
						if evt.Type == "session.status" {
							if st, ok := parseSessionStatus(evt.Properties["status"]); ok && st.Type == "retry" {
								if retryErr, cut := shouldCutOffTurnRetry(st, time.Now()); cut {
									s.abortTurnAsync(ap, retryErr)
									continue
								}
							}
						}
						if evt.Type == "session.error" || evt.Type == "session.idle" || (evt.Type == "session.status" && isOpenCodeIdleStatus(evt.Properties["status"])) {
							terminalEvts = append(terminalEvts, evt)
						} else {
							liveEvts = append(liveEvts, evt)
						}
					}
					s.broadcastRouted(rec.ID, liveEvts)
				})
			}()
			return done
		}

		readerDone := feedStdout(stdoutR)
		waitErr := cmd.Wait()
		reapOrphanedOutput(cmd.Process, stdoutR, readerDone)

		for !tr.IsTurnFinished() && !ap.wasAborted() && ap.stall.abortError() == nil {
			transientErrText := ""
			if heldFailedResultLine != "" {
				transientErrText = extractFailedStreamResultError(heldFailedResultLine, tr.LastErrorText())
			}
			if transientErrText == "" {
				transientErrText = firstNonEmptyStr(tr.LastErrorText(), subproc.DecodeOutputBytes(stderrBuf.Bytes()))
				if transientErrText == "" && waitErr != nil {
					transientErrText = waitErr.Error()
				}
			}
			hasTools := tr.HasToolParts()
			resumeExtID := strings.TrimSpace(firstNonEmptyStr(tr.ExternalID(), rec.ExternalID))

			if retryAttempt < len(retryDelays) && isRetryableTurnError(transientErrText) && (!hasTools || resumeExtID != "") {
				retryAttempt++
				delay := retryDelays[retryAttempt-1]
				if hinted := parseTurnRetryDelay(transientErrText); hinted > delay {
					delay = hinted
				}
				heldFailedResultLine = ""
				terminalEvts = nil
				nextMs := time.Now().Add(delay).UnixMilli()
				s.broadcast(rec.ID, SSEEvent{
					Type: "session.status",
					Properties: map[string]interface{}{
						"sessionID": rec.ID,
						"status": SessionStatus{
							Type:    "retry",
							Attempt: retryAttempt,
							Message: summarizeTurnRetryReason(transientErrText),
							Next:    nextMs,
						},
					},
				})
				s.noteActivity(ap)
				if !waitTurnRetryBackoff(ap, delay) {
					break
				}

				var retryArgs []string
				var retryInput []byte
				emitMu.Lock()
				if !hasTools {
					resetEvts := tr.ResetUncommittedTurnForRetry(initialExternalID)
					rec.ExternalID = initialExternalID
					_, _, _ = s.store.Update(rec.ID, func(sr *SessionRecord) {
						sr.ExternalID = initialExternalID
					})
					if len(resetEvts) > 0 {
						s.broadcastRouted(rec.ID, resetEvts)
					}
					retryArgs = buildTurnCLIArgs(rec.Runtime, mode, modelID, effort, initialExternalID, geminiUsesAgy, claudeHookArgs)
					retryInput = inputBytes
				} else {
					settleEvts := tr.PrepareForResumeRetry(time.Now().UnixMilli())
					rec.ExternalID = resumeExtID
					_, _, _ = s.store.Update(rec.ID, func(sr *SessionRecord) {
						sr.ExternalID = resumeExtID
					})
					if len(settleEvts) > 0 {
						s.broadcastRouted(rec.ID, settleEvts)
					}
					retryArgs = buildTurnCLIArgs(rec.Runtime, mode, modelID, effort, resumeExtID, geminiUsesAgy, claudeHookArgs)
					retryInput, _ = EncodeStreamInputTurnWithParts(inputRuntime, turnResumePrompt, nil)
				}
				emitMu.Unlock()

				started, retryWaitErr := s.runRetrySubprocess(rec, rawBinPath, retryArgs, procEnv, retryInput, ap, &stderrBuf, true, feedStdout)
				if !started {
					break
				}
				waitErr = retryWaitErr
				continue
			}

			if canRetryWithoutResume && len(tr.partsByID) == 0 {
				canRetryWithoutResume = false
				heldFailedResultLine = ""
				rec.ExternalID = ""
				_, _, _ = s.store.Update(rec.ID, func(sr *SessionRecord) {
					sr.ExternalID = ""
				})
				tr.ResetForFreshRetry()
				terminalEvts = nil

				histRec := rec
				histRec.ExternalID = ""
				histRec.ContextPending = len(priorTurns) > 0
				histRec.ContextTurnCount = len(priorTurns)
				histRec.Turns = priorTurns
				retryPrompt := promptText
				sysPrefix := ""
				if !strings.Contains(retryPrompt, commandExecutionGuidance) {
					if sys := s.fallbackCLISystemForFreshRetry(rec.ID, rec.Runtime); sys != "" {
						sysPrefix = sys + "\n\n"
					}
				}
				ctxPrefix := runtimeMemoryContext(histRec) + runtimeContext(histRec)
				if !strings.Contains(promptText, "Continue the existing conversation") {
					retryPrompt = ctxPrefix + sysPrefix + promptText
				} else if sysPrefix != "" {
					retryPrompt = sysPrefix + promptText
				}
				retryInput, _ := EncodeStreamInputTurnWithParts(inputRuntime, retryPrompt, deliveryParts)
				retryArgs := buildTurnCLIArgs(rec.Runtime, mode, modelID, effort, "", geminiUsesAgy, claudeHookArgs)
				if started, retryWaitErr := s.runRetrySubprocess(rec, rawBinPath, retryArgs, procEnv, retryInput, ap, &stderrBuf, false, feedStdout); started {
					waitErr = retryWaitErr
					continue
				}
			}
			break
		}

		if !tr.IsTurnFinished() && heldFailedResultLine != "" && !ap.wasAborted() && ap.stall.abortError() == nil {
			emitMu.Lock()
			evts := tr.FeedLine(heldFailedResultLine)
			s.noteToolEvents(rec.ID, evts)
			var liveEvts []SSEEvent
			for _, evt := range evts {
				if evt.Type == "session.error" || evt.Type == "session.idle" || (evt.Type == "session.status" && isOpenCodeIdleStatus(evt.Properties["status"])) {
					terminalEvts = append(terminalEvts, evt)
				} else {
					liveEvts = append(liveEvts, evt)
				}
			}
			s.broadcastRouted(rec.ID, liveEvts)
			emitMu.Unlock()
		}

		close(stopPoll)
		<-pollDone
		if !tr.IsTurnFinished() {
			s.broadcastRouted(rec.ID, tr.AbandonTurn(time.Now().UnixMilli()))
			stallErr := ap.stall.abortError()
			if ap.wasAborted() && stallErr == nil {
				tr.msgInfo.Time.Completed = time.Now().UnixMilli()
				tr.msgInfo.Finish = "aborted"
				s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.msgInfo}})
				terminalEvts = append(terminalEvts, SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}})
				return
			}
			errText := strings.TrimSpace(subproc.DecodeOutputBytes(stderrBuf.Bytes()))
			if errText == "" && waitErr != nil {
				errText = waitErr.Error()
			}
			if errText == "" {
				errText = "CLI process exited unexpectedly without a result event"
			}
			msgErr := newClassifiedMessageError("RunnerProcessError", errText, 0)
			if stallErr != nil {
				msgErr = stallErr
			}
			tr.msgInfo.Time.Completed = time.Now().UnixMilli()
			tr.msgInfo.Error = msgErr
			tr.msgInfo.Finish = "error"
			s.broadcast(rec.ID, SSEEvent{Type: "message.updated", Properties: map[string]interface{}{"info": tr.msgInfo}})
			terminalEvts = append(terminalEvts,
				SSEEvent{Type: "session.error", Properties: map[string]interface{}{"sessionID": rec.ID, "error": msgErr}},
				SSEEvent{Type: "session.status", Properties: map[string]interface{}{"sessionID": rec.ID, "status": SessionStatus{Type: "idle"}}},
			)
		}
	}()

	return nil
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
	s.AbortSession(sessionID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
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

func buildTurnCLIArgs(runtime, mode, modelID, effort, externalID string, geminiUsesAgy bool, claudeHookArgs []string) []string {
	switch runtime {
	case string(RuntimeClaudeCode):
		return append(BuildClaudeArgs(mode, modelID, effort, externalID), claudeHookArgs...)
	case string(RuntimeGeminiCLI):
		if geminiUsesAgy {
			agyModel, agyEffort := ResolveGeminiAgyModelAndEffort(modelID, effort)
			return BuildAgyArgs(mode, agyModel, agyEffort, externalID)
		}
		return BuildGeminiArgs(mode, modelID, effort, externalID)
	case string(RuntimeCodeBuddy):
		return BuildCodeBuddyArgs(mode, modelID, effort, externalID)
	case string(RuntimeQoder):
		return BuildQoderArgs(mode, modelID, effort, externalID)
	default:
		return BuildAgyArgs(mode, modelID, effort, externalID)
	}
}

func (s *Service) runRetrySubprocess(
	rec SessionRecord,
	rawBinPath string,
	retryArgs []string,
	procEnv []string,
	retryInput []byte,
	ap *activeProcess,
	stderrBuf *bytes.Buffer,
	emitBusy bool,
	feedStdout func(*os.File) <-chan struct{},
) (bool, error) {
	retryBin, wrappedArgs, wrapErr := s.confineRuntimeCommand(rec, rawBinPath, retryArgs)
	if wrapErr != nil {
		return false, nil
	}
	retryCmd := subproc.Command(retryBin, wrappedArgs...)
	retryCmd.Dir = rec.WorktreePath
	retryCmd.Env = procEnv
	retryStdin, inErr := retryCmd.StdinPipe()
	if inErr != nil {
		return false, nil
	}
	retryOutR, retryOutW, pipeErr := os.Pipe()
	if pipeErr != nil {
		_ = retryStdin.Close()
		return false, nil
	}
	var retryStderr bytes.Buffer
	retryCmd.Stdout = retryOutW
	retryCmd.Stderr = &retryStderr
	retryCmd.WaitDelay = orphanDrainGrace
	setProcessGroup(retryCmd)
	if startErr := retryCmd.Start(); startErr != nil {
		_ = retryStdin.Close()
		_ = retryOutR.Close()
		_ = retryOutW.Close()
		return false, nil
	}
	_ = retryOutW.Close()
	stderrBuf.Reset()
	ap.mu.Lock()
	ap.cmd = retryCmd
	ap.stdin = retryStdin
	ap.mu.Unlock()
	s.noteActivity(ap)
	if emitBusy {
		s.broadcast(rec.ID, SSEEvent{
			Type: "session.status",
			Properties: map[string]interface{}{
				"sessionID": rec.ID,
				"status":    SessionStatus{Type: "busy"},
			},
		})
	}
	_, _ = retryStdin.Write(retryInput)
	_ = retryStdin.Close()

	retryReaderDone := feedStdout(retryOutR)
	waitErr := retryCmd.Wait()
	reapOrphanedOutput(retryCmd.Process, retryOutR, retryReaderDone)
	if retryStderr.Len() > 0 {
		*stderrBuf = retryStderr
	}
	return true, waitErr
}
