package runner

import "strings"

// nativeCLISystemForTurn avoids repeating commandExecutionGuidance and unchanged
// caller system instructions inside every user message of a resumed native CLI
// conversation (--resume / --conversation), where Turn 1 already placed them in
// the CLI's persistent conversation history.
func (s *Service) nativeCLISystemForTurn(rec SessionRecord, callerSystem, fullSystem string) string {
	if strings.TrimSpace(rec.ExternalID) == "" || rec.ContextPending {
		return fullSystem
	}
	prevCaller, ok := s.cliSystemDelivered.Load(rec.ID + "\x00" + rec.Runtime)
	if !ok {
		return fullSystem
	}
	if callerSystem == "" || callerSystem == prevCaller {
		return ""
	}
	return callerSystem
}

func (s *Service) recordCLISystemDelivered(sessionID, runtime, callerSystem string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(runtime) == "" {
		return
	}
	s.cliSystemDelivered.Store(sessionID+"\x00"+runtime, callerSystem)
}

func (s *Service) fallbackCLISystemForFreshRetry(sessionID, runtime string) string {
	sys := effectiveCommandExecutionGuidance()
	if caller, ok := s.cliSystemDelivered.Load(sessionID + "\x00" + runtime); ok {
		if strings.TrimSpace(caller) != "" {
			sys += "\n\n" + strings.TrimSpace(caller)
		}
	}
	return sys
}
