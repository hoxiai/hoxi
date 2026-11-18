package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
)

type CommitAdmissionState string

const (
	CommitAdmissionNew      CommitAdmissionState = "new"
	CommitAdmissionInFlight CommitAdmissionState = "admitted"
	CommitAdmissionDone     CommitAdmissionState = "completed"
	CommitAdmissionUnknown  CommitAdmissionState = "outcome_unknown"
	CommitAdmissionConflict CommitAdmissionState = "conflict"
)

type CommitAdmissionDecision struct {
	State        CommitAdmissionState
	HTTPStatus   int
	ResponseBody []byte
	ErrorCode    string
	ErrorMessage string
}

// CommitLedger persists three-state request admission records for worktree
// commit mutations without introducing a package dependency cycle with runner.
type CommitLedger interface {
	AdmitCommit(scope, sessionID, clientRequestID, digest string, meta map[string]string) CommitAdmissionDecision
	CompleteCommit(scope, clientRequestID string, httpStatus int, responseBody []byte)
	AbortUncommittedCommit(scope, clientRequestID string)
	MarkCommitUnknown(scope, clientRequestID, message string)
}

type commitScopeMutex struct {
	mu   sync.Mutex
	refs int
}

// SetCommitLedger registers the cross-restart request ledger shared across all
// directory-scoped worktree managers.
func (m *worktreeManager) SetCommitLedger(ledger CommitLedger) {
	if m == nil || m.roots == nil {
		return
	}
	m.roots.Lock()
	m.roots.commitLedger = ledger
	m.roots.Unlock()
}

func (m *worktreeManager) commitLedger() CommitLedger {
	if m == nil || m.roots == nil {
		return nil
	}
	m.roots.Lock()
	ledger := m.roots.commitLedger
	m.roots.Unlock()
	return ledger
}

func (m *worktreeManager) lockCommitScope(scope string) func() {
	if m == nil || m.roots == nil || scope == "" {
		return func() {}
	}
	m.roots.Lock()
	if m.roots.commitLocks == nil {
		m.roots.commitLocks = make(map[string]*commitScopeMutex)
	}
	entry := m.roots.commitLocks[scope]
	if entry == nil {
		entry = &commitScopeMutex{}
		m.roots.commitLocks[scope] = entry
	}
	entry.refs++
	m.roots.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		m.roots.Lock()
		entry.refs--
		if entry.refs <= 0 {
			delete(m.roots.commitLocks, scope)
		}
		m.roots.Unlock()
	}
}

// CommitRequestScope computes the canonical ledger scope key for a commit request.
func CommitRequestScope(sessionID, workDir string) string {
	sid := sanitizeSessionID(sessionID)
	if sid != "" && sid != "__root__" {
		return sid
	}
	dir := strings.TrimSpace(workDir)
	if dir == "" {
		return "dir:."
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != "" {
		dir = resolved
	}
	if abs, err := filepath.Abs(dir); err == nil && abs != "" {
		dir = abs
	}
	return "dir:" + filepath.ToSlash(filepath.Clean(dir))
}

type canonicalCommitDigestInput struct {
	Scope        string        `json:"scope"`
	Message      string        `json:"message"`
	Push         bool          `json:"push"`
	HasDiffs     bool          `json:"hasDiffs,omitempty"`
	SessionDiffs []SessionDiff `json:"sessionDiffs,omitempty"`
}

// CommitRequestDigest computes the deterministic SHA-256 hex digest of a commit request's semantic payload.
func CommitRequestDigest(scope, message string, push bool, sessionDiffs *[]SessionDiff) string {
	payload := canonicalCommitDigestInput{
		Scope:   strings.TrimSpace(scope),
		Message: strings.TrimSpace(message),
		Push:    push,
	}
	if sessionDiffs != nil {
		payload.HasDiffs = true
		payload.SessionDiffs = *sessionDiffs
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ObserveGitHead returns the current full 40-char HEAD commit hash for workDir, or empty if unavailable.
func ObserveGitHead(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	head, err := runGit(workDir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(head)
}

// ObserveCommitEvidence inspects read-only Git state for an interrupted commit request.
func ObserveCommitEvidence(workDir, preHead string) map[string]any {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return nil
	}
	curHead := ObserveGitHead(workDir)
	shortHead, _ := runGit(workDir, "rev-parse", "--short", "HEAD")
	branch, _ := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	porcelain, statusErr := runGitRaw(workDir, "status", "--porcelain")
	ev := map[string]any{
		"preHead":      strings.TrimSpace(preHead),
		"currentHead":  curHead,
		"shortCommit":  strings.TrimSpace(shortHead),
		"branch":       strings.TrimSpace(branch),
		"headAdvanced": preHead != "" && curHead != "" && curHead != preHead,
	}
	if statusErr == nil {
		ev["workingTreeClean"] = strings.TrimSpace(porcelain) == ""
	}
	return ev
}

func injectCommitDeduplicated(raw []byte) []byte {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return raw
	}
	obj["deduplicated"] = true
	encoded, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return append(encoded, '\n')
}
