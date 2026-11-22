package runner

import (
	"strings"
	"unicode/utf8"
)

// Read-only projection of ONE execution. Do not return tool, reasoning, error
// bodies or sibling messages. A completed submission receipt is not a result.
func (s *Service) inspectPromptTurn(req PersistedRequestRecord) map[string]any {
	out := map[string]any{"status": "unknown"}
	user, assistant := req.Meta["userMsgID"], req.Meta["assistantMsgID"]
	if s.store == nil || req.SessionID == "" || req.Scope != req.SessionID || user == "" || assistant == "" {
		return out
	}
	s.store.VisitTurnsTransient(req.SessionID, func(rec SessionRecord) {
		for _, turn := range rec.Turns {
			m := turn.Info
			if m.ID != assistant || m.ParentID != user || m.Role != "assistant" || m.SessionID != req.SessionID {
				continue
			}
			switch {
			case m.Error != nil && m.Error.Data != nil && m.Error.Data.Kind == ToolOutcomeUnknown:
				return
			case m.Error != nil || m.Finish == "aborted" || m.Finish == "error":
				out["status"] = "turn_failed"
			case m.Time.Completed == 0:
				out["status"] = "running"
			case m.Finish == "stop" || m.Finish == "end_turn":
				out["status"] = "turn_completed"
				const maxBytes = 64 << 10
				var b strings.Builder
				for _, p := range turn.Parts {
					if p.Type != "text" || p.MessageID != assistant || p.SessionID != req.SessionID || p.Text == "" {
						continue
					}
					remaining := maxBytes - b.Len()
					if len(p.Text) > remaining {
						for remaining > 0 && !utf8.RuneStart(p.Text[remaining]) {
							remaining--
						}
						b.WriteString(p.Text[:remaining])
						out["truncated"] = true
						break
					}
					b.WriteString(p.Text)
				}
				out["text"] = b.String()
			}
			return
		}
	})
	return out
}
