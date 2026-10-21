package runner

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

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
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"stopped": stopped,
	})
}

// StopToolCommand terminates the running child tool/shell process for a tool
// invocation while keeping the parent Agent CLI / OpenCode turn alive so the
// conversation can continue with the interrupted tool output.
func (s *Service) StopToolCommand(sessionID, callID, partID, command string) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	callID = strings.TrimSpace(callID)
	partID = strings.TrimSpace(partID)
	command = strings.TrimSpace(command)

	var matchedPart *MessagePart
	if rec, ok := s.store.Get(sessionID); ok {
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

	s.markToolStopped(sessionID, partID, callID)

	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap != nil {
		s.noteActivity(ap)
	}

	cliPID := 0
	if ap != nil && ap.cmd != nil && ap.cmd.Process != nil {
		cliPID = ap.cmd.Process.Pid
	}
	stopped := stopChildToolProcesses(cliPID, command)

	if matchedPart != nil && matchedPart.State != nil {
		updated := clonePart(*matchedPart)
		if updated.State.Metadata == nil {
			updated.State.Metadata = make(map[string]interface{})
		}
		updated.State.Metadata["stoppedByUser"] = true
		if ap == nil && (updated.State.Status == "running" || updated.State.Status == "pending") {
			now := time.Now().UnixMilli()
			start := now
			if updated.State.Time != nil && updated.State.Time.Start > 0 {
				start = updated.State.Time.Start
			}
			updated.State.Status = "completed"
			updated.State.Time = &PartTime{Start: start, End: now}
			appendStoppedNotice(&updated)
		}
		s.broadcast(sessionID, SSEEvent{
			Type:       "message.part.updated",
			Properties: map[string]interface{}{"part": updated},
		})
	}

	return stopped, nil
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
	const notice = "[Command stopped by user]"
	if strings.Contains(part.State.Output, notice) || strings.Contains(part.State.Error, notice) {
		return
	}
	if strings.TrimSpace(part.State.Output) != "" {
		part.State.Output = strings.TrimRight(part.State.Output, "\n") + "\n" + notice
	} else if strings.TrimSpace(part.State.Error) == "" {
		part.State.Output = notice
	}
}
