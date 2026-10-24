package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Doom-loop guard for CLI runtimes, mirroring OpenCode's built-in check: a
// model that issues the same tool call with identical input several times in
// a row is usually stuck, so the next identical call waits for the user
// (permission "doom_loop") instead of burning tokens. Rejecting it tells the
// model why, which is often enough to break the loop.

const (
	doomLoopThreshold       = 3
	doomLoopNoEditThreshold = 3
)

// Status tools are polled with the same input on purpose.
var doomLoopPollingTools = map[string]bool{
	"BashOutput":     true,
	"TaskOutput":     true,
	"TaskGet":        true,
	"TaskList":       true,
	"command_status": true,
	"agent_status":   true,
	"agent_wait":     true,
}

type doomLoopGuard struct {
	mu        sync.Mutex
	recent    map[string][]string         // session → fingerprints of its latest consecutive calls
	sinceEdit map[string]map[string]int   // session → fingerprint counts since last file mutation in this turn
	turn      map[string]string           // session → turn the recent calls belong to
	exempt    map[string]map[string]int64 // session → tools the user always allows (createdAt ms)
}

func newDoomLoopGuard() *doomLoopGuard {
	return &doomLoopGuard{
		recent:    make(map[string][]string),
		sinceEdit: make(map[string]map[string]int),
		turn:      make(map[string]string),
		exempt:    make(map[string]map[string]int64),
	}
}

// observe records a call and reports whether it completes a run of
// doomLoopThreshold identical calls within one turn (or repeats the same
// non-mutating call doomLoopNoEditThreshold times in one turn without any file
// modification in between): the user asking for the same check (`go test ./...`)
// in consecutive turns, or re-running a check after editing a file, is not a loop.
func (g *doomLoopGuard) observe(sessionID, turnID, tool string, input json.RawMessage) bool {
	if doomLoopPollingTools[tool] {
		return false
	}
	fp := toolCallFingerprint(tool, input)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.exempt[sessionID][tool] > 0 {
		return false
	}
	if g.turn[sessionID] != turnID {
		g.turn[sessionID] = turnID
		delete(g.recent, sessionID)
		delete(g.sinceEdit, sessionID)
	}
	calls := append(g.recent[sessionID], fp)
	if len(calls) > doomLoopThreshold {
		calls = calls[len(calls)-doomLoopThreshold:]
	}
	g.recent[sessionID] = calls
	consecutiveMatch := len(calls) >= doomLoopThreshold
	if consecutiveMatch {
		for _, c := range calls {
			if c != fp {
				consecutiveMatch = false
				break
			}
		}
	}
	if consecutiveMatch {
		return true
	}

	if isMutatingToolName(tool) {
		// A file mutation changes workspace state, so reset non-consecutive
		// repeat counts while recording this mutation's own fingerprint.
		g.sinceEdit[sessionID] = map[string]int{fp: 1}
		return false
	}
	if g.sinceEdit[sessionID] == nil {
		g.sinceEdit[sessionID] = make(map[string]int)
	}
	g.sinceEdit[sessionID][fp]++
	return g.sinceEdit[sessionID][fp] >= doomLoopNoEditThreshold
}

// settle clears the run after the user answered, so allowing a call once
// does not re-ask on every following identical call.
func (g *doomLoopGuard) settle(sessionID, tool string, always bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.recent, sessionID)
	delete(g.sinceEdit, sessionID)
	if !always {
		return
	}
	if g.exempt[sessionID] == nil {
		g.exempt[sessionID] = make(map[string]int64)
	}
	g.exempt[sessionID][tool] = time.Now().UnixMilli()
}

// forget drops a session's guard state once it is deleted.
func (g *doomLoopGuard) forget(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.recent, sessionID)
	delete(g.sinceEdit, sessionID)
	delete(g.turn, sessionID)
	delete(g.exempt, sessionID)
}

func (g *doomLoopGuard) listExemptGrants() []ApprovalGrant {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ApprovalGrant
	for sid, tools := range g.exempt {
		for tool, createdAt := range tools {
			key := "doom_loop:" + tool
			out = append(out, ApprovalGrant{
				ID:        makeGrantID(sid, "doom_loop", key),
				SessionID: sid,
				Kind:      "doom_loop",
				Tool:      tool,
				Key:       key,
				Pattern:   tool,
				Scope:     "session_memory",
				Source:    "desktop",
				CreatedAt: createdAt,
			})
		}
	}
	return out
}

func (g *doomLoopGuard) revokeExemptGrants(id, sessionID string, all bool) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	revoked := 0
	for sid, tools := range g.exempt {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for tool := range tools {
			key := "doom_loop:" + tool
			gid := makeGrantID(sid, "doom_loop", key)
			if all || (sessionID != "" && id == "") || (id != "" && gid == id) {
				delete(tools, tool)
				revoked++
			}
		}
		if len(tools) == 0 {
			delete(g.exempt, sid)
		}
	}
	return revoked
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
	if !s.doomLoop.observe(cred.sessionID, cred.messageID, tool, input) {
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
