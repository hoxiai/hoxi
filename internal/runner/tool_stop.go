package runner

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// toolStopMarkTTL bounds how long a stopped-tool mark may decorate events when
// the session has no runner-owned process to wait on (OpenCode turns).
const toolStopMarkTTL = 2 * time.Minute

// stopMarkerPrefix prefixes the shell comment the sandbox plugin prepends to
// every bash command. It is platform independent because both the stop path and
// the event relay read it on every OS.
const stopMarkerPrefix = "# hoxi-stop:"

// openCodeStopMarker is the shell comment the sandbox plugin prepends to every
// bash command. It names the engine session and tool call, so a stop can find
// exactly one command inside the shared daemon instead of guessing from the
// command text (macOS hides other processes' environments, and `ps` escapes
// multi-line commands).
func openCodeStopMarker(engineSessionID, callID string) string {
	engineSessionID = strings.TrimSpace(engineSessionID)
	callID = strings.TrimSpace(callID)
	if engineSessionID == "" || callID == "" {
		return ""
	}
	return stopMarkerPrefix + engineSessionID + ":" + callID
}

// openCodeStopTrap is the line the sandbox plugin puts right after the stop
// marker. OpenCode's bash tool only returns early when the shell exits with a
// code: a shell killed by a signal fails the tool's exit-code wait, which then
// lasts until the tool's own timeout. So a stop sends the shell SIGUSR1 and
// terminates only its descendants, and this trap prints the stop notice and
// exits 130 once the running command is gone. The EXIT trap keeps sh and bash
// from exec-ing the last command in place of the shell, which would drop both
// the marker and the trap. It must match the plugin's STOP_TRAP exactly.
const openCodeStopTrap = `trap 'printf "\n` + toolStoppedNotice + `\n"; exit 130' USR1; trap : EXIT`
const openCodeStopTrapUSR1 = `trap 'printf "\n` + toolStoppedNotice + `\n"; exit 130' USR1`

// toolStoppedNotice tells the user and the model that the command was stopped
// on purpose rather than failing or timing out.
const toolStoppedNotice = "[Command stopped by user]"

// StopToolRequest identifies a running tool invocation inside a session turn
// that the user wants to terminate without aborting the parent session turn.
type StopToolRequest struct {
	CallID  string `json:"callID,omitempty"`
	PartID  string `json:"partID,omitempty"`
	Command string `json:"command,omitempty"`
}

func (s *Service) handleSessionStopTool(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body StopToolRequest
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
	}
	stopped, err := s.StopToolCommand(sessionID, body.CallID, body.PartID, body.Command)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errSessionNotRunning):
			status = http.StatusConflict
		case errors.Is(err, errToolProcessNotFound):
			status = http.StatusNotFound
		}
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"stopped": stopped,
	})
}

var (
	// errSessionNotRunning: the session has no runner-owned CLI process.
	// OpenCode sessions run inside the shared `opencode serve` daemon and are
	// stopped through OpenCode's own abort instead.
	errSessionNotRunning = errors.New("session not running in runner")
	// errToolProcessNotFound: no child process of the session matched the tool.
	errToolProcessNotFound = errors.New("no running process matched the tool command")
)

// StopToolCommand terminates the running child tool/shell process for a tool
// invocation while keeping the parent Agent CLI turn alive so the conversation
// can continue with the interrupted tool output. Only processes inside the
// session's own CLI process tree are candidates.
func (s *Service) StopToolCommand(sessionID, callID, partID, command string) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	callID = strings.TrimSpace(callID)
	partID = strings.TrimSpace(partID)
	command = strings.TrimSpace(command)

	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	rec, hasRec := s.store.Get(sessionID)
	openCodeSession := hasRec && rec.Runtime == "opencode"
	// OpenCode turns run inside the shared daemon, so their tool commands are
	// reachable without a runner-owned CLI process.
	if ap == nil && !openCodeSession {
		return 0, errSessionNotRunning
	}

	var matchedPart *MessagePart
	if hasRec {
		matchedPart = findTargetRunningToolPart(rec.Turns, partID, callID)
		if matchedPart != nil {
			if partID == "" {
				partID = strings.TrimSpace(matchedPart.ID)
			}
			if callID == "" {
				callID = strings.TrimSpace(matchedPart.CallID)
			}
			if command == "" && matchedPart.State != nil {
				command = firstString(
					matchedPart.State.Input["command"],
					matchedPart.State.Input["CommandLine"],
				)
			}
		}
	}

	// Mark before killing so the tool result produced by the kill is
	// decorated; the marks are dropped again if nothing was stopped.
	s.markToolStopped(sessionID, partID, callID)
	if ap != nil {
		s.noteActivity(ap)
	}
	stopped := 0
	switch {
	case ap != nil && ap.cmd != nil && ap.cmd.Process != nil:
		stopped = stopChildToolProcesses(ap.cmd.Process.Pid, command)
	case openCodeSession && s.openCode != nil && isRunningBashPart(matchedPart):
		// OpenCode tool commands are children of the shared `opencode serve`
		// daemon. The sandbox plugin tags each bash command with its engine
		// session and call ID, so the exact subtree is located by that marker
		// instead of guessing from the command text. The marker is built only
		// from a bash call this session knows is running, never read back from
		// command text, so tags a model forges cannot redirect the stop.
		if daemonPID := openCodeDaemonPID(s.openCode.BaseURL()); daemonPID > 1 {
			marker := openCodeStopMarker(rec.ExternalID, matchedPart.CallID)
			if marker == "" && matchedPart.CallID != "" {
				marker = stopMarkerPrefix + ":" + matchedPart.CallID
			}
			if marker != "" {
				stopped = stopOpenCodeToolProcessByMarker(daemonPID, marker)
			}
		}
	}
	if stopped == 0 {
		s.clearToolStopped(sessionID, partID, callID)
		return 0, errToolProcessNotFound
	}
	if ap != nil {
		go func() {
			<-ap.done
			s.clearToolStopped(sessionID, partID, callID)
		}()
	} else {
		go func() {
			time.Sleep(toolStopMarkTTL)
			s.clearToolStopped(sessionID, partID, callID)
		}()
	}

	// The stopped command may already have reported its result: OpenCode's
	// shell exits through the stop trap within milliseconds. Re-read the part
	// so a stale running copy never overwrites that result; a finished part
	// was already flagged by decorateStoppedToolEvent when it was relayed.
	if current, ok := s.store.Get(sessionID); ok && matchedPart != nil {
		matchedPart = findTargetRunningToolPart(current.Turns, partID, callID)
	}
	if matchedPart != nil && matchedPart.State != nil &&
		(matchedPart.State.Status == "running" || matchedPart.State.Status == "pending") {
		updated := clonePart(*matchedPart)
		if updated.State.Metadata == nil {
			updated.State.Metadata = make(map[string]interface{})
		}
		updated.State.Metadata["stoppedByUser"] = true
		s.broadcast(sessionID, SSEEvent{
			Type:       "message.part.updated",
			Properties: map[string]interface{}{"part": updated},
		})
	}

	return stopped, nil
}

func isRunningBashPart(p *MessagePart) bool {
	if p == nil || p.Type != "tool" || p.Tool != "bash" || p.State == nil {
		return false
	}
	return p.State.Status == "running" || p.State.Status == "pending"
}

func findTargetRunningToolPart(turns []MessageTurn, partID, callID string) *MessagePart {
	for i := len(turns) - 1; i >= 0; i-- {
		parts := turns[i].Parts
		for j := len(parts) - 1; j >= 0; j-- {
			p := parts[j]
			if p.Type != "tool" || p.State == nil {
				continue
			}
			if partID != "" && p.ID == partID {
				cp := clonePart(p)
				return &cp
			}
			if callID != "" && p.CallID == callID {
				cp := clonePart(p)
				return &cp
			}
		}
	}
	if partID != "" || callID != "" {
		return nil
	}
	for i := len(turns) - 1; i >= 0; i-- {
		parts := turns[i].Parts
		for j := len(parts) - 1; j >= 0; j-- {
			p := parts[j]
			if p.Type != "tool" || p.State == nil {
				continue
			}
			if (p.State.Status == "running" || p.State.Status == "pending") && p.Tool == "bash" {
				cp := clonePart(p)
				return &cp
			}
		}
	}
	return nil
}

func (s *Service) markToolStopped(sessionID, partID, callID string) {
	if sessionID == "" {
		return
	}
	if partID != "" {
		s.stoppedTools.Store(sessionID+"\x00part:"+partID, true)
	}
	if callID != "" {
		s.stoppedTools.Store(sessionID+"\x00call:"+callID, true)
	}
}

func (s *Service) clearToolStopped(sessionID, partID, callID string) {
	if partID != "" {
		s.stoppedTools.Delete(sessionID + "\x00part:" + partID)
	}
	if callID != "" {
		s.stoppedTools.Delete(sessionID + "\x00call:" + callID)
	}
}

func (s *Service) isToolMarkedStopped(sessionID, partID, callID string) bool {
	if sessionID == "" {
		return false
	}
	if partID != "" {
		if _, ok := s.stoppedTools.Load(sessionID + "\x00part:" + partID); ok {
			return true
		}
	}
	if callID != "" {
		if _, ok := s.stoppedTools.Load(sessionID + "\x00call:" + callID); ok {
			return true
		}
	}
	return false
}

func (s *Service) decorateStoppedToolEvent(sessionID string, evt *SSEEvent) {
	if evt == nil || evt.Type != "message.part.updated" || evt.Properties == nil {
		return
	}
	part, ok := evt.Properties["part"].(MessagePart)
	if !ok || part.Type != "tool" || part.State == nil {
		return
	}
	if !s.isToolMarkedStopped(sessionID, part.ID, part.CallID) {
		return
	}
	part = clonePart(part)
	if part.State.Metadata == nil {
		part.State.Metadata = make(map[string]interface{})
	}
	part.State.Metadata["stoppedByUser"] = true
	if part.State.Status == "completed" || part.State.Status == "error" {
		appendStoppedNotice(&part)
	}
	evt.Properties["part"] = part
}

func appendStoppedNotice(part *MessagePart) {
	if part == nil || part.State == nil {
		return
	}
	if strings.Contains(part.State.Output, toolStoppedNotice) || strings.Contains(part.State.Error, toolStoppedNotice) {
		return
	}
	if strings.TrimSpace(part.State.Output) != "" {
		part.State.Output = strings.TrimRight(part.State.Output, "\n") + "\n" + toolStoppedNotice
	} else if strings.TrimSpace(part.State.Error) == "" {
		part.State.Output = toolStoppedNotice
	}
}
