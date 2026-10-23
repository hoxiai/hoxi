package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

// A deferred entry is a command unattended mode refused, or an approval that
// timed out, kept so a person can run it later. The list is desktop-only: it
// is not on the tunnel allow-list, and nothing here executes the command.

const deferredFile = "approval-deferred.json"

// DeferredEntry is one refused or timed-out action waiting for a person.
type DeferredEntry struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	RequestID string `json:"requestID,omitempty"`
	Category  string `json:"category,omitempty"`
	Command   string `json:"command"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	Resolved  bool   `json:"resolved,omitempty"`
}

type deferredFileDoc struct {
	Version int             `json:"version"`
	Entries []DeferredEntry `json:"entries"`
}

type deferredStore struct {
	mu   sync.Mutex
	path string
	rows []DeferredEntry
}

func newDeferredStore(customStorePath string) *deferredStore {
	path := hoxipaths.Join(deferredFile)
	if trimmed := strings.TrimSpace(customStorePath); trimmed != "" {
		path = filepath.Join(filepath.Dir(filepath.Clean(trimmed)), deferredFile)
	}
	s := &deferredStore{path: path}
	s.load()
	return s
}

func (s *deferredStore) load() {
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var doc deferredFileDoc
	if json.Unmarshal(raw, &doc) != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		_ = os.Rename(s.path, backup)
		return
	}
	s.rows = doc.Entries
}

func (s *deferredStore) add(e DeferredEntry) {
	if strings.TrimSpace(e.Command) == "" && strings.TrimSpace(e.Reason) == "" {
		return
	}
	e.Command = diagnostics.RedactSecrets(e.Command)
	e.Reason = diagnostics.RedactSecrets(e.Reason)
	if e.CreatedAt <= 0 {
		e.CreatedAt = time.Now().UnixMilli()
	}
	if e.ID == "" {
		e.ID = "def_" + randomHex(8)
	}
	s.mu.Lock()
	s.rows = append([]DeferredEntry{e}, s.rows...)
	if len(s.rows) > maxApprovalAuditEntries {
		s.rows = s.rows[:maxApprovalAuditEntries]
	}
	snapshot := append([]DeferredEntry(nil), s.rows...)
	path := s.path
	s.mu.Unlock()
	s.write(path, snapshot)
}

func (s *deferredStore) list() []DeferredEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeferredEntry, len(s.rows))
	copy(out, s.rows)
	return out
}

func (s *deferredStore) resolve(id string) bool {
	s.mu.Lock()
	found := false
	for i := range s.rows {
		if s.rows[i].ID == id {
			s.rows[i].Resolved = true
			found = true
			break
		}
	}
	snapshot := append([]DeferredEntry(nil), s.rows...)
	path := s.path
	s.mu.Unlock()
	if found {
		s.write(path, snapshot)
	}
	return found
}

func (s *deferredStore) write(path string, rows []DeferredEntry) {
	if path == "" {
		return
	}
	raw, err := json.MarshalIndent(deferredFileDoc{Version: 1, Entries: rows}, "", "  ")
	if err != nil {
		return
	}
	_ = writeApprovalAuditAtomic(path, append(raw, '\n'))
}

// turnCounts tracks one parent session's unattended decisions so the
// end-of-turn summary can report numbers without any command text.
type turnCounts struct {
	allowed  int
	denied   int
	timedOut int
}

func (s *Service) noteTurn(sessionID, kind string) {
	if sessionID == "" {
		return
	}
	v, _ := s.turnStats.LoadOrStore(sessionID, &turnCounts{})
	c, _ := v.(*turnCounts)
	if c == nil {
		return
	}
	switch kind {
	case "allow":
		c.allowed++
	case "deny":
		c.denied++
	case "timeout":
		c.timedOut++
	}
}

// finishTurnSummary emits one summary when a parent session's turn ends, and
// only when unattended mode is on or this turn timed out a request. Child
// sessions never emit their own. The notice carries counts and ids only.
func (s *Service) finishTurnSummary(sessionID, status string) {
	rec, ok := s.store.Meta(sessionID)
	if !ok || rec.Subagent || rec.ParentID != "" {
		return
	}
	v, loaded := s.turnStats.LoadAndDelete(sessionID)
	c, _ := v.(*turnCounts)
	if c == nil {
		c = &turnCounts{}
	}
	if s.ApprovalPolicy().Mode != ApprovalModeUnattended && c.timedOut == 0 && !loaded {
		return
	}
	deferred := 0
	if s.deferred != nil {
		for _, row := range s.deferred.list() {
			if row.SessionID == sessionID && !row.Resolved {
				deferred++
			}
		}
	}
	if s.status != nil {
		s.status.Announce(statusbar.ApprovalNotice{
			Kind: "summary", SessionID: sessionID, Status: status,
			Allowed: c.allowed, Denied: c.denied, TimedOut: c.timedOut, Deferred: deferred,
		})
	}
	if s.onSummary != nil {
		// Counts only. The session title, commands and paths stay out of this text.
		s.onSummary(sessionID, fmt.Sprintf("自动放行 %d，拒绝 %d，待处理 %d", c.allowed, c.denied, deferred))
	}
}

func (s *Service) rememberDeferred(sessionID, requestID, category, command string) {
	if s.deferred == nil {
		return
	}
	s.deferred.add(DeferredEntry{
		SessionID: sessionID,
		RequestID: requestID,
		Category:  category,
		Command:   command,
		Reason:    unattendedDenyReason(category),
	})
}

func (s *Service) handleDeferred(w http.ResponseWriter, r *http.Request) {
	if isRemoteApprovalSource(requestApprovalSource(r)) {
		writeJSONError(w, http.StatusForbidden, "the deferred list is desktop-only")
		return
	}
	if s.deferred == nil {
		writeJSON(w, http.StatusOK, []DeferredEntry{})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.deferred.list())
	case http.MethodPost:
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
			writeJSONError(w, http.StatusBadRequest, "id is required")
			return
		}
		if !s.deferred.resolve(body.ID) {
			writeJSONError(w, http.StatusNotFound, "deferred entry not found")
			return
		}
		writeJSON(w, http.StatusOK, true)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

// unattendedSessionShape checks a new session against the approval mode.
// Unattended mode refuses a local checkout or a disabled command sandbox
// instead of silently rewriting the request. Other modes keep the request.
func (s *Service) unattendedSessionShape(sandboxMode string, commandSandbox bool) (string, bool, error) {
	mode, err := normalizeSandboxMode(sandboxMode)
	if err != nil {
		return "", false, err
	}
	if commandSandbox && !sandbox.Available() {
		return "", false, errors.New(sandbox.UnavailableReason())
	}
	if s.ApprovalPolicy().Mode != ApprovalModeUnattended {
		return mode, commandSandbox, nil
	}
	if !sandbox.Available() {
		return "", false, errors.New(sandbox.UnavailableReason())
	}
	if mode != "worktree" || !commandSandbox {
		return "", false, errUnattendedRequiresIsolation
	}
	return mode, commandSandbox, nil
}

// errUnattendedRequiresIsolation is returned when a session created while
// unattended mode is on asks for a local checkout or no command sandbox.
var errUnattendedRequiresIsolation = errors.New("unattended mode requires an isolated worktree and the command sandbox")
