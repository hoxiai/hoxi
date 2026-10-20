package runner

import (
	"context"
	"errors"
	"strings"
	"time"
)

// abortSettleTimeout bounds how long RunPromptAndWait waits for an aborted
// turn to wind down before returning.
const abortSettleTimeout = 10 * time.Second

// RunPromptAndWait submits one turn to a Hoxi conversation and blocks until the
// turn ends, so in-process callers (convoys) get the same session plane as the
// UI: store persistence, status bar, approval gates and stall watchdog. It
// returns the final assistant text, or the turn's error. Cancelling ctx stops
// the turn.
func (s *Service) RunPromptAndWait(ctx context.Context, sessionID string, req PromptRequest) (string, error) {
	if _, err := s.startPrompt(ctx, sessionID, "", req); err != nil {
		return "", err
	}
	s.mu.Lock()
	ap := s.procs[sessionID]
	s.mu.Unlock()
	if ap != nil {
		select {
		case <-ap.done:
		case <-ctx.Done():
			s.AbortSession(sessionID)
			select {
			case <-ap.done:
			case <-time.After(abortSettleTimeout):
			}
			return "", ctx.Err()
		}
	}
	return s.lastTurnOutcome(sessionID)
}

// AbortSession stops a conversation's running turn, if any.
func (s *Service) AbortSession(sessionID string) {
	s.abortActiveProcess(sessionID)
	s.broadcast(sessionID, SSEEvent{
		Type:       "session.status",
		Properties: map[string]interface{}{"sessionID": sessionID, "status": SessionStatus{Type: "idle"}},
	})
}

// lastTurnOutcome reads the latest assistant message of a finished turn.
func (s *Service) lastTurnOutcome(sessionID string) (string, error) {
	var (
		text    string
		turnErr error
	)
	found := s.store.VisitTurns(sessionID, func(rec SessionRecord) {
		for i := len(rec.Turns) - 1; i >= 0; i-- {
			turn := rec.Turns[i]
			if turn.Info.Role == "user" {
				return
			}
			if turn.Info.Role != "assistant" {
				continue
			}
			if turn.Info.Error != nil && turnErr == nil {
				turnErr = messageErrorValue(turn.Info.Error)
			}
			var texts []string
			for _, part := range turn.Parts {
				if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
					texts = append(texts, part.Text)
				}
			}
			if len(texts) > 0 && text == "" {
				text = strings.Join(texts, "\n")
			}
			if text != "" {
				return
			}
		}
	})
	if !found {
		return "", errors.New("runner session not found")
	}
	if turnErr != nil {
		return text, turnErr
	}
	return text, nil
}

func messageErrorValue(e *MessageError) error {
	if e.Data != nil && strings.TrimSpace(e.Data.Message) != "" {
		return errors.New(e.Name + ": " + e.Data.Message)
	}
	return errors.New(e.Name)
}
