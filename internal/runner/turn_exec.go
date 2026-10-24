package runner

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/attachment"
)

func (s *Service) prepareTurnAttachments(
	rec SessionRecord,
	now int64,
	seq int64,
	userMsgID string,
	promptText string,
	fileParts []MessagePart,
) (storedParts []MessagePart, deliveryParts []MessagePart, cliPrompt string) {
	textPartID := fmt.Sprintf("prt_u_%d_%d", now, seq)
	var storedFiles []MessagePart
	var deliveryFiles []MessagePart
	var docBlocks []string
	var cliImageNotes []string

	for index, part := range fileParts {
		if part.Type != "file" || strings.TrimSpace(part.URL) == "" {
			continue
		}
		partID := fmt.Sprintf("prt_f_%d_%d_%d", now, seq, index)
		var meta attachment.Metadata
		var hasMeta bool

		if s.attachments != nil {
			if refID := attachment.ResolveRefID(part.URL); refID != "" {
				if m, _, err := s.attachments.Get(refID); err == nil {
					meta = m
					hasMeta = true
				}
			} else if strings.HasPrefix(strings.TrimSpace(part.URL), "data:") {
				if m, err := s.attachments.Save(attachment.SaveRequest{
					SessionID: rec.ID,
					Directory: rec.WorktreePath,
					Filename:  part.Filename,
					Mime:      part.Mime,
					DataURL:   part.URL,
				}); err == nil {
					meta = m
					hasMeta = true
				}
			}
		}

		if hasMeta {
			storedFiles = append(storedFiles, MessagePart{
				ID:        partID,
				SessionID: rec.ID,
				MessageID: userMsgID,
				Type:      "file",
				Mime:      meta.Mime,
				Filename:  meta.Filename,
				URL:       meta.URL,
			})
			var localPath string
			if rec.WorktreePath != "" {
				_, localPath, _ = s.attachments.MaterializeToDir(meta.ID, rec.WorktreePath)
			}
			if meta.Kind == attachment.KindImage {
				_, dataURL, err := s.attachments.ReadAsDataURL(meta.ID)
				if err != nil {
					dataURL = part.URL
				}
				deliveryFiles = append(deliveryFiles, MessagePart{
					ID:        partID,
					SessionID: rec.ID,
					MessageID: userMsgID,
					Type:      "file",
					Mime:      meta.Mime,
					Filename:  meta.Filename,
					URL:       dataURL,
				})
				if localPath != "" {
					cliImageNotes = append(cliImageNotes, fmt.Sprintf("[Attached Image: %s]", localPath))
				}
			} else {
				extracted := s.attachments.ExtractText(meta.ID, 48000)
				header := fmt.Sprintf(`<attached_file name=%q mime=%q`, meta.Filename, meta.Mime)
				if localPath != "" {
					header += fmt.Sprintf(` path=%q`, localPath)
				}
				header += ">"
				if extracted != "" {
					docBlocks = append(docBlocks, header+"\n"+extracted+"\n</attached_file>")
				} else {
					docBlocks = append(docBlocks, header+"\n</attached_file>")
				}
			}
			continue
		}

		// Fallback if attachment store is unavailable or URL is external
		storedPart := part
		storedPart.ID = partID
		storedPart.SessionID, storedPart.MessageID = rec.ID, userMsgID
		storedFiles = append(storedFiles, storedPart)
		deliveryFiles = append(deliveryFiles, storedPart)
		if saved := materializeTurnAttachments(rec.WorktreePath, now, seq, []MessagePart{part}); len(saved) > 0 {
			cliImageNotes = append(cliImageNotes, fmt.Sprintf("[Attached Image: %s]", saved[0]))
		}
	}

	effectiveText := promptText
	if len(docBlocks) > 0 {
		joinedDocs := strings.Join(docBlocks, "\n\n")
		if effectiveText == "" {
			effectiveText = "Please analyze the attached file(s):\n\n" + joinedDocs
		} else {
			effectiveText = effectiveText + "\n\n" + joinedDocs
		}
	}

	cliPrompt = effectiveText
	if len(cliImageNotes) > 0 {
		joinedImgs := strings.Join(cliImageNotes, "\n")
		if cliPrompt == "" {
			cliPrompt = "Please analyze the attached image(s):\n" + joinedImgs
		} else {
			cliPrompt = cliPrompt + "\n\n" + joinedImgs
		}
	}

	if promptText != "" || len(storedFiles) == 0 {
		storedParts = append(storedParts, MessagePart{
			ID:        textPartID,
			SessionID: rec.ID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      promptText,
		})
	}
	storedParts = append(storedParts, storedFiles...)

	if effectiveText != "" || len(deliveryFiles) == 0 {
		deliveryParts = append(deliveryParts, MessagePart{
			ID:        textPartID,
			SessionID: rec.ID,
			MessageID: userMsgID,
			Type:      "text",
			Text:      effectiveText,
		})
	}
	deliveryParts = append(deliveryParts, deliveryFiles...)

	return storedParts, deliveryParts, cliPrompt
}

func parseBase64DataURL(dataURL, fallbackMime string) (mimeType, b64Data string, rawBytes []byte, ok bool) {
	trimmed := strings.TrimSpace(dataURL)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", "", nil, false
	}
	comma := strings.IndexByte(trimmed, ',')
	if comma < 0 {
		return "", "", nil, false
	}
	header := trimmed[5:comma]
	b64Data = strings.TrimSpace(trimmed[comma+1:])
	if b64Data == "" {
		return "", "", nil, false
	}
	mimeType = strings.TrimSpace(strings.Split(header, ";")[0])
	if mimeType == "" {
		mimeType = strings.TrimSpace(fallbackMime)
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	decoded, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil || len(decoded) == 0 {
		return "", "", nil, false
	}
	return mimeType, b64Data, decoded, true
}

func attachmentExtForMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func sanitizeAttachmentFilename(name, mimeType string, index int) string {
	base := filepath.Base(strings.TrimSpace(name))
	var clean strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			clean.WriteRune(r)
		}
	}
	s := strings.Trim(clean.String(), "._-")
	ext := attachmentExtForMime(mimeType)
	if s == "" {
		return fmt.Sprintf("image_%d%s", index+1, ext)
	}
	if filepath.Ext(s) == "" {
		s += ext
	}
	return s
}

func materializeTurnAttachments(worktreePath string, now int64, seq int64, parts []MessagePart) []string {
	wt := strings.TrimSpace(worktreePath)
	if wt == "" || len(parts) == 0 {
		return nil
	}
	attachDir := filepath.Join(wt, ".hoxi", "attachments")
	if err := os.MkdirAll(attachDir, 0o700); err != nil {
		return nil
	}
	var saved []string
	for idx, p := range parts {
		if p.Type != "file" {
			continue
		}
		mimeType, _, raw, ok := parseBase64DataURL(p.URL, p.Mime)
		if !ok {
			continue
		}
		safeName := sanitizeAttachmentFilename(p.Filename, mimeType, idx)
		target := filepath.Join(attachDir, fmt.Sprintf("%d_%d_%d_%s", now, seq, idx+1, safeName))
		if err := os.WriteFile(target, raw, 0o600); err == nil {
			saved = append(saved, target)
		}
	}
	return saved
}

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
	var args []string
	switch rec.Runtime {
	case string(RuntimeClaudeCode):
		args = BuildClaudeArgs(mode, modelID, effort, rec.ExternalID)
	case string(RuntimeGeminiCLI):
		args = BuildGeminiArgs(mode, modelID, effort, rec.ExternalID)
	case string(RuntimeCodeBuddy):
		args = BuildCodeBuddyArgs(mode, modelID, effort, rec.ExternalID)
	case string(RuntimeQoder):
		args = BuildQoderArgs(mode, modelID, effort, rec.ExternalID)
	default:
		args = BuildAgyArgs(mode, modelID, effort, rec.ExternalID)
	}

	var hookEnv []string
	revokeHook := func() {}
	if rec.Runtime == string(RuntimeClaudeCode) {
		var hookArgs []string
		hookArgs, hookEnv, revokeHook = s.issueClaudeHook(mode, rec.ID, asstMsgID)
		args = append(args, hookArgs...)
	} else if rec.Runtime == string(RuntimeGeminiCLI) {
		hookEnv, revokeHook = s.issueGeminiHook(mode, rec.ID, asstMsgID)
	} else if rec.Runtime == string(RuntimeAntigravity) {
		s.ensureAgyHookInstalled()
		hookEnv, revokeHook = s.issueHookEnv(rec.ID, asstMsgID)
	} else if rec.Runtime == string(RuntimeCodeBuddy) || rec.Runtime == string(RuntimeQoder) {
		// Every approval mode keeps the mandatory floor (secret reads,
		// destructive commands, opaque execution), and these runtimes have no
		// PreToolUse gate to ask. Only verifiable read-only modes may run.
		if !isReadOnlyAgentMode(mode) {
			return fmt.Errorf("runtime %s does not support PreToolUse approval gate; refusing to run in non-read-only mode %q", rec.Runtime, mode)
		}
	}

	binPath, args, err := s.confineRuntimeCommand(rec, binPath, args)
	if err != nil {
		revokeHook()
		return err
	}
	cmd := exec.Command(binPath, args...)
	cmd.Dir = rec.WorktreePath
	cmd.Env = runtimeProcessEnv(rec, os.Environ(), hookEnv)

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
	s.broadcast(rec.ID, tr.InitialEvents(userInfo, storedParts)...)

	inputBytes, _ := EncodeStreamInputTurnWithParts(rec.Runtime, promptText, deliveryParts)
	_, _ = stdinPipe.Write(inputBytes)
	_ = stdinPipe.Close()

	go func() {
		defer func() {
			revokeHook()
			s.cancelSessionApprovals(rec.ID, "the CLI process exited")
			s.syncSessionMemoryFile(rec.ID)
			s.store.FlushSession(rec.ID)
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
				s.noteToolEvents(rec.ID, evts)
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
