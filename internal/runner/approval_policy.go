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

	"github.com/hoxiai/hoxi/internal/agyhook"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/sandbox"
)

const approvalPolicyFile = "approval-policy.json"

// approvalWaitLimit is the wait for a hook request: the configured timeout,
// but never longer than the CLI's own kill minus a few seconds.
func (s *Service) approvalWaitLimit() time.Duration {
	if s.gate != nil && s.gate.waitLimit > 0 && s.gate.waitLimit < time.Minute {
		// Tests inject a short limit directly; a saved policy never does.
		return s.gate.waitLimit
	}
	limit := s.ApprovalPolicy().ApprovalWaitLimit()
	if cap := agyhook.ClientTimeout - 5*time.Second; limit > cap {
		return cap
	}
	return limit
}

type approvalPolicyStore struct {
	mu     sync.Mutex
	path   string
	policy ApprovalPolicy
	exists bool
	// blocked keeps an unreadable file from being overwritten silently.
	blocked error
}

func newApprovalPolicyStore(customStorePath string) *approvalPolicyStore {
	path := hoxipaths.ApprovalPolicyPath()
	if trimmed := strings.TrimSpace(customStorePath); trimmed != "" {
		path = filepath.Join(filepath.Dir(filepath.Clean(trimmed)), approvalPolicyFile)
	}
	s := &approvalPolicyStore{path: path, policy: defaultApprovalPolicy()}
	s.load()
	return s
}

func (s *approvalPolicyStore) seedDefaultLocked() {
	if s.path == "" {
		return
	}
	raw, err := json.MarshalIndent(defaultApprovalPolicy(), "", "  ")
	if err != nil {
		return
	}
	_ = writeApprovalAuditAtomic(s.path, append(raw, '\n'))
}

func (s *approvalPolicyStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.seedDefaultLocked()
		} else {
			s.blocked = fmt.Errorf("approval policy file is unreadable: %w", err)
		}
		return
	}
	var p ApprovalPolicy
	if err := json.Unmarshal(raw, &p); err != nil || p.Validate() != nil {
		// Keep the bad file for inspection; restore a clean default file on disk.
		backup := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		if renameErr := os.Rename(s.path, backup); renameErr != nil {
			s.blocked = fmt.Errorf("approval policy file is invalid and could not be backed up: %w", renameErr)
			return
		}
		s.seedDefaultLocked()
		return
	}
	s.policy = p
	s.exists = p.UpdatedAt > 0 || p != defaultApprovalPolicy()
}

func (s *approvalPolicyStore) get() (ApprovalPolicy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy, s.exists
}

func (s *approvalPolicyStore) set(p ApprovalPolicy) (ApprovalPolicy, error) {
	if err := p.Validate(); err != nil {
		return ApprovalPolicy{}, err
	}
	p.UpdatedAt = time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blocked != nil {
		return ApprovalPolicy{}, s.blocked
	}
	if s.path != "" {
		raw, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return ApprovalPolicy{}, err
		}
		if err := writeApprovalAuditAtomic(s.path, append(raw, '\n')); err != nil {
			return ApprovalPolicy{}, err
		}
	}
	s.policy = p
	s.exists = true
	return p, nil
}

// ApprovalPolicy returns the active policy.
func (s *Service) ApprovalPolicy() ApprovalPolicy {
	p, _ := s.policy.get()
	return p
}

// UpdateApprovalPolicy validates, persists, and reconciles a new approval policy.
func (s *Service) UpdateApprovalPolicy(p ApprovalPolicy) (ApprovalPolicy, error) {
	saved, err := s.policy.set(p)
	if err != nil {
		return ApprovalPolicy{}, err
	}
	s.mu.Lock()
	hook := s.onPolicySaved
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	s.reconcilePendingApprovals()
	return saved, nil
}

// SetPermissionRulesSource wires the OpenCode `permission` block (pattern
// rules saved from approval cards) into every runtime's approval decision.
func (s *Service) SetPermissionRulesSource(fn func() map[string]interface{}) {
	s.mu.Lock()
	s.permissionRules = fn
	s.mu.Unlock()
}

// SetApprovalPolicyHook runs after the policy is saved, so the gateway can
// keep OpenCode asking Hoxi instead of deciding on its own.
func (s *Service) SetApprovalPolicyHook(fn func()) {
	s.mu.Lock()
	s.onPolicySaved = fn
	s.mu.Unlock()
}

type approvalPolicyResponse struct {
	Policy ApprovalPolicy `json:"policy"`
	Exists bool           `json:"exists"`
}

func (s *Service) handleApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	if isRemoteApprovalSource(requestApprovalSource(r)) {
		writeJSONError(w, http.StatusForbidden, "the approval policy cannot be changed by a remote controller")
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, exists := s.policy.get()
		writeJSON(w, http.StatusOK, approvalPolicyResponse{Policy: p, Exists: exists})
	case http.MethodPut:
		var body ApprovalPolicy
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid approval policy")
			return
		}
		if err := body.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Mode == ApprovalModeUnattended && !sandbox.Available() {
			writeJSONError(w, http.StatusBadRequest, sandbox.UnavailableReason())
			return
		}
		saved, err := s.policy.set(body)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.mu.Lock()
		hook := s.onPolicySaved
		s.mu.Unlock()
		if hook != nil {
			hook()
		}
		s.reconcilePendingApprovals()
		writeJSON(w, http.StatusOK, approvalPolicyResponse{Policy: saved, Exists: true})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or PUT required")
	}
}
