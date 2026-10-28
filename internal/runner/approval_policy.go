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

// ApprovalPolicy is the user's approval mode plus per-tool overrides. It lives
// in ~/.hoxi/approval-policy.json so the decision is the same whether a
// request is answered by the desktop, the CLI, the phone, or nobody is
// watching. An empty override follows the mode's preset.
type ApprovalPolicy struct {
	Mode      string `json:"mode"`
	Edit      string `json:"edit,omitempty"`     // "" | "allow" | "ask"
	Webfetch  string `json:"webfetch,omitempty"` // "" | "allow" | "ask"
	Bash      string `json:"bash,omitempty"`     // "" | "allow" | "smart" | "ask"
	// AIReview lets a user-selected lightweight model inspect complex
	// non-destructive bash commands that fail static checks and allow strictly
	// read-only inspection commands without prompting.
	AIReview      bool   `json:"aiReview,omitempty"`
	AIReviewModel string `json:"aiReviewModel,omitempty"` // "providerID/modelID"
	// ApprovalTimeoutSeconds is how long a pending request waits before it is
	// rejected. Zero means the default. A timeout only ever rejects.
	ApprovalTimeoutSeconds int   `json:"approvalTimeoutSeconds,omitempty"`
	UpdatedAt              int64 `json:"updatedAt,omitempty"`
}

const (
	defaultApprovalTimeoutSeconds = 600
	minApprovalTimeoutSeconds     = 60
	maxApprovalTimeoutSeconds     = 3600
)

const approvalPolicyFile = "approval-policy.json"

func defaultApprovalPolicy() ApprovalPolicy {
	return ApprovalPolicy{Mode: ApprovalModeAuto}
}

// approvalWaitLimit is how long a pending request waits. Zero in the file
// means the default.
func (p ApprovalPolicy) approvalWaitLimit() time.Duration {
	seconds := p.ApprovalTimeoutSeconds
	if seconds <= 0 {
		seconds = defaultApprovalTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

// approvalWaitLimit is the wait for a hook request: the configured timeout,
// but never longer than the CLI's own kill minus a few seconds.
func (s *Service) approvalWaitLimit() time.Duration {
	if s.gate != nil && s.gate.waitLimit > 0 && s.gate.waitLimit < time.Minute {
		// Tests inject a short limit directly; a saved policy never does.
		return s.gate.waitLimit
	}
	limit := s.ApprovalPolicy().approvalWaitLimit()
	if cap := agyhook.ClientTimeout - 5*time.Second; limit > cap {
		return cap
	}
	return limit
}

func approvalTimeoutReason(p ApprovalPolicy) string {
	minutes := int(p.approvalWaitLimit() / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("Approval timed out after %d minute(s) with no response and was rejected. Try an approach that does not need this permission, or say so in the summary.", minutes)
}

// unattendedQuestionTimeoutReason is the only wording a question gets back
// while unattended. Other modes keep approvalTimeoutReason unchanged.
func unattendedQuestionTimeoutReason() string {
	return "The user is away. Choose the safest plan that needs no approval, and say what you assumed."
}

func isQuestionRequest(req map[string]interface{}) bool {
	name := strings.ToLower(strings.TrimSpace(permissionName(req)))
	return name == "question" || name == "ask_question" || strings.HasSuffix(name, ".question")
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

// isReadOnlyAgentMode reports whether a mode is verifiably read-only for a
// runtime without an approval gate. Only Plan is accepted: Ask still leaves the
// CLI free to run shell tools that the hard floor cannot gate.
func isReadOnlyAgentMode(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "plan")
}

// validate rejects unknown values instead of silently dropping them.
func (p ApprovalPolicy) validate() error {
	switch p.Mode {
	case ApprovalModeAuto, ApprovalModeSmart, ApprovalModeManual, ApprovalModeTrusted, ApprovalModeUnattended:
	default:
		return fmt.Errorf("mode must be auto, smart, manual, trusted or unattended")
	}
	if p.ApprovalTimeoutSeconds != 0 && (p.ApprovalTimeoutSeconds < minApprovalTimeoutSeconds || p.ApprovalTimeoutSeconds > maxApprovalTimeoutSeconds) {
		return fmt.Errorf("approvalTimeoutSeconds must be between %d and %d", minApprovalTimeoutSeconds, maxApprovalTimeoutSeconds)
	}
	for name, value := range map[string]string{"edit": p.Edit, "webfetch": p.Webfetch} {
		if value != "" && value != "allow" && value != "ask" {
			return fmt.Errorf("%s must be empty, allow or ask", name)
		}
	}
	if p.Bash != "" && p.Bash != "allow" && p.Bash != "smart" && p.Bash != "ask" {
		return fmt.Errorf("bash must be empty, allow, smart or ask")
	}
	if !validAIReviewModelSpec(p.AIReviewModel) {
		return fmt.Errorf("aiReviewModel must be empty or provider/model")
	}
	return nil
}

func validAIReviewModelSpec(spec string) bool {
	if spec == "" {
		return true
	}
	if len(spec) > 200 || spec != strings.TrimSpace(spec) {
		return false
	}
	prov, model, ok := strings.Cut(spec, "/")
	if !ok || prov == "" || model == "" {
		return false
	}
	if strings.Contains(spec, "..") || strings.ContainsRune(spec, '\\') {
		return false
	}
	for _, r := range spec {
		if r <= 32 || r == 127 {
			return false
		}
	}
	return true
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
	if err := json.Unmarshal(raw, &p); err != nil || p.validate() != nil {
		// Keep the bad file for inspection; restore a clean default file on disk.
		backup := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		if renameErr := os.Rename(s.path, backup); renameErr != nil {
			s.blocked = fmt.Errorf("approval policy file is invalid and could not be backed up: %w", renameErr)
			return
		}
		s.seedDefaultLocked()
		return
	}
	p.AIReviewModel = strings.TrimSpace(p.AIReviewModel)
	if p.AIReviewModel == "" {
		p.AIReview = false
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
	p.AIReviewModel = strings.TrimSpace(p.AIReviewModel)
	if err := p.validate(); err != nil {
		return ApprovalPolicy{}, err
	}
	if p.AIReviewModel == "" {
		p.AIReview = false
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
