package runner

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Antigravity subagents run as their own conversations inside the parent's
// agy process and inherit its hook environment, so their tool calls reach the
// hook with the parent turn's credential but their own conversationId. A call
// is accepted only from the session's own conversation or from a subagent
// child session recorded under it; any other conversation is still denied.

const (
	// agySubagentHookWait bounds how long a call from a not-yet-recorded
	// conversation waits for its child session: the record is written when
	// the dispatch step completes, which a subagent's first call can race.
	agySubagentHookWait = 5 * time.Second
	agySubagentHookPoll = 100 * time.Millisecond
)

// agyHookOrigin is the conversation a hook call came from.
type agyHookOrigin struct {
	convID  string // conversation the call belongs to ("" when unknown)
	childID string // subagent child session, empty for the session itself
}

// guardKey scopes per-stream doom-loop state, so parallel subagents do not
// interleave their call histories with each other or with the parent.
func (o agyHookOrigin) guardKey(sessionID string) string {
	if o.childID != "" {
		return o.childID
	}
	return sessionID
}

// callID keys approvals per call; step indexes restart in every subagent
// conversation, so a subagent's ID must not collide with the parent's.
func (o agyHookOrigin) callID(messageID string, stepIdx int) string {
	if o.childID != "" {
		return fmt.Sprintf("call_%s_%s_%d", messageID, o.childID, stepIdx)
	}
	return fmt.Sprintf("call_%s_%d", messageID, stepIdx)
}

// resolveAgyHookOrigin reports which conversation of rec a hook call belongs
// to, or false when it belongs to neither the session nor its subagents.
func (s *Service) resolveAgyHookOrigin(r *http.Request, rec SessionRecord, convID string) (agyHookOrigin, bool) {
	convID = strings.TrimSpace(convID)
	ext := strings.TrimSpace(rec.ExternalID)
	if convID == "" || ext == "" || convID == ext {
		return agyHookOrigin{convID: firstNonEmptyStr(convID, ext)}, true
	}
	wait := s.gate.subagentWaitLimit()
	deadline := time.Now().Add(wait)
	for {
		if childID, ok := s.store.SubagentChildByExternalID(rec.ID, convID); ok {
			return agyHookOrigin{convID: convID, childID: childID}, true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return agyHookOrigin{}, false
		}
		timer := time.NewTimer(min(remaining, agySubagentHookPoll))
		select {
		case <-r.Context().Done():
			timer.Stop()
			return agyHookOrigin{}, false
		case <-timer.C:
		}
	}
}

func (g *approvalGate) subagentWaitLimit() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.subagentWait
}
