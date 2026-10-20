package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Doom-loop guard for CLI runtimes, mirroring OpenCode's built-in check: a
// model that issues the same tool call with identical input several times in
// a row is usually stuck, so the next identical call waits for the user
// (permission "doom_loop") instead of burning tokens. Rejecting it tells the
// model why, which is often enough to break the loop.

const doomLoopThreshold = 3

// Status tools are polled with the same input on purpose.
var doomLoopPollingTools = map[string]bool{
	"BashOutput": true,
	"TaskOutput": true,
	"TaskGet":    true,
	"TaskList":   true,
}

type doomLoopGuard struct {
	mu     sync.Mutex
	recent map[string][]string        // session → fingerprints of its latest calls
	exempt map[string]map[string]bool // session → tools the user always allows
}

func newDoomLoopGuard() *doomLoopGuard {
	return &doomLoopGuard{recent: make(map[string][]string), exempt: make(map[string]map[string]bool)}
}

// observe records a call and reports whether it completes a run of
// doomLoopThreshold identical calls.
func (g *doomLoopGuard) observe(sessionID, tool string, input json.RawMessage) bool {
	if doomLoopPollingTools[tool] {
		return false
	}
	fp := toolCallFingerprint(tool, input)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.exempt[sessionID][tool] {
		return false
	}
	calls := append(g.recent[sessionID], fp)
	if len(calls) > doomLoopThreshold {
		calls = calls[len(calls)-doomLoopThreshold:]
	}
	g.recent[sessionID] = calls
	if len(calls) < doomLoopThreshold {
		return false
	}
	for _, c := range calls {
		if c != fp {
			return false
		}
	}
	return true
}

// settle clears the run after the user answered, so allowing a call once
// does not re-ask on every following identical call.
func (g *doomLoopGuard) settle(sessionID, tool string, always bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.recent, sessionID)
	if !always {
		return
	}
	if g.exempt[sessionID] == nil {
		g.exempt[sessionID] = make(map[string]bool)
	}
	g.exempt[sessionID][tool] = true
}

// forget drops a session's guard state once it is deleted.
func (g *doomLoopGuard) forget(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.recent, sessionID)
	delete(g.exempt, sessionID)
}

// toolCallFingerprint hashes the tool name and its input with keys sorted,
// so formatting differences do not hide a repeat.
func toolCallFingerprint(tool string, input json.RawMessage) string {
	canonical := []byte(strings.TrimSpace(string(input)))
	var v interface{}
	if json.Unmarshal(input, &v) == nil {
		if raw, err := json.Marshal(v); err == nil {
			canonical = raw
		}
	}
	sum := sha256.Sum256(append([]byte(tool+"\x00"), canonical...))
	return hex.EncodeToString(sum[:])
}

// gateDoomLoop asks the user before a repeated call runs. It reports whether
// the call may proceed to the normal policy, and the reason when it may not.
func (s *Service) gateDoomLoop(r *http.Request, cred hookCredential, tool string, input json.RawMessage, callID string) (bool, string) {
	if !s.doomLoop.observe(cred.sessionID, tool, input) {
		return true, ""
	}
	var shown interface{}
	if json.Unmarshal(input, &shown) != nil {
		shown = string(input)
	}
	id := runnerPermissionID + randomHex(12)
	req := map[string]interface{}{
		"id":         id,
		"sessionID":  cred.sessionID,
		"permission": "doom_loop",
		"patterns":   []string{tool},
		"metadata":   map[string]interface{}{"tool": tool, "input": shown},
		"always":     []string{tool},
		"tool": map[string]interface{}{
			"messageID": cred.messageID,
			"callID":    callID,
		},
	}
	answer := s.awaitReply(r, cred, id, req)
	s.doomLoop.settle(cred.sessionID, tool, answer.Reply == "always")
	if answer.Reply == "once" || answer.Reply == "always" {
		return true, ""
	}
	reason := "Hoxi stopped a repeated tool call: " + tool + " was called " +
		"several times in a row with identical input. Try a different approach."
	if msg := strings.TrimSpace(answer.Message); msg != "" {
		reason += " User note: " + msg
	}
	return false, reason
}
