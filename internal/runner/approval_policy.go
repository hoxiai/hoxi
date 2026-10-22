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

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// ApprovalPolicy is the user's approval mode plus per-tool overrides. It lives
// in ~/.hoxi/approval-policy.json so the decision is the same whether a
// request is answered by the desktop, the CLI, the phone, or nobody is
// watching. An empty override follows the mode's preset.
type ApprovalPolicy struct {
	Mode      string `json:"mode"`
	Edit      string `json:"edit,omitempty"`     // "" | "allow" | "ask"
	Webfetch  string `json:"webfetch,omitempty"` // "" | "allow" | "ask"
	Bash      string `json:"bash,omitempty"`     // "" | "allow" | "smart" | "ask"
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

const approvalPolicyFile = "approval-policy.json"

func defaultApprovalPolicy() ApprovalPolicy {
	return ApprovalPolicy{Mode: ApprovalModeAuto}
}

// effectiveRule resolves a category's rule from its override or the mode preset.
func (p ApprovalPolicy) effectiveRule(category string) string {
	switch category {
	case permissionCategoryEdit:
		if p.Edit != "" {
			return p.Edit
		}
	case permissionCategoryWebfetch:
		if p.Webfetch != "" {
			return p.Webfetch
		}
	case permissionCategoryBash:
		if p.Bash != "" {
			return p.Bash
		}
	}
	switch p.Mode {
	case ApprovalModeManual:
		return "ask"
	case ApprovalModeSmart:
		if category == permissionCategoryBash {
			return "smart"
		}
	}
	return "allow"
}

// validate rejects unknown values instead of silently dropping them.
func (p ApprovalPolicy) validate() error {
	switch p.Mode {
	case ApprovalModeAuto, ApprovalModeSmart, ApprovalModeManual:
	default:
		return fmt.Errorf("mode must be auto, smart or manual")
	}
	for name, value := range map[string]string{"edit": p.Edit, "webfetch": p.Webfetch} {
		if value != "" && value != "allow" && value != "ask" {
			return fmt.Errorf("%s must be empty, allow or ask", name)
		}
	}
	if p.Bash != "" && p.Bash != "allow" && p.Bash != "smart" && p.Bash != "ask" {
		return fmt.Errorf("bash must be empty, allow, smart or ask")
	}
	return nil
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

func (s *approvalPolicyStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.blocked = fmt.Errorf("approval policy file is unreadable: %w", err)
		}
		return
	}
	var p ApprovalPolicy
	if err := json.Unmarshal(raw, &p); err != nil || p.validate() != nil {
		// Keep the bad file for inspection; the next explicit save replaces it.
		backup := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		if renameErr := os.Rename(s.path, backup); renameErr != nil {
			s.blocked = fmt.Errorf("approval policy file is invalid and could not be backed up: %w", renameErr)
		}
		return
	}
	s.policy = p
	s.exists = true
}

func (s *approvalPolicyStore) get() (ApprovalPolicy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy, s.exists
}

func (s *approvalPolicyStore) set(p ApprovalPolicy) (ApprovalPolicy, error) {
	if err := p.validate(); err != nil {
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
		if err := body.validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
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
