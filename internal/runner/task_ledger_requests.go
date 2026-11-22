package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/runner/taskledger"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	RequestKindPrompt          = taskledger.RequestKindPrompt
	RequestKindPermissionReply = taskledger.RequestKindPermissionReply
	RequestKindWorktreeCommit  = taskledger.RequestKindWorktreeCommit

	RequestStatusNotAdmitted    = taskledger.RequestStatusNotAdmitted
	RequestStatusAdmitted       = taskledger.RequestStatusAdmitted
	RequestStatusCompleted      = taskledger.RequestStatusCompleted
	RequestStatusOutcomeUnknown = taskledger.RequestStatusOutcomeUnknown

	ErrCodeRequestConflict           = taskledger.ErrCodeRequestConflict
	ErrCodeOutcomeUnknown            = taskledger.ErrCodeOutcomeUnknown
	ErrCodeAlreadyAnswered           = taskledger.ErrCodeAlreadyAnswered
	ErrCodeRequestInProgress         = taskledger.ErrCodeRequestInProgress
	ErrCodeRequestStorageUnavailable = taskledger.ErrCodeRequestStorageUnavailable

	RequestAdmissionNew         = taskledger.RequestAdmissionNew
	RequestAdmissionInFlight    = taskledger.RequestAdmissionInFlight
	RequestAdmissionDone        = taskledger.RequestAdmissionDone
	RequestAdmissionUnknown     = taskledger.RequestAdmissionUnknown
	RequestAdmissionConflict    = taskledger.RequestAdmissionConflict
	RequestAdmissionUnavailable = taskledger.RequestAdmissionUnavailable
)

type (
	PersistedRequestRecord  = taskledger.PersistedRequestRecord
	RequestAdmissionState   = taskledger.RequestAdmissionState
	RequestAdmissionOutcome = taskledger.RequestAdmissionOutcome
	RequestInspectResponse  = taskledger.RequestInspectResponse
	CodedRequestError       = taskledger.CodedRequestError
)

func PermissionReplyDigest(permissionID, reply, message string, grantClass bool) string {
	return taskledger.PermissionReplyDigest(permissionID, reply, message, grantClass)
}

func isValidHexDigest(d string) bool { return taskledger.IsValidHexDigest(d) }

func writeRunnerRequestError(w http.ResponseWriter, status int, err error) {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	var coded *CodedRequestError
	if errors.As(err, &coded) && coded != nil {
		if coded.HTTPStatus > 0 {
			status = coded.HTTPStatus
		}
		payload := map[string]any{
			"error": coded.Message,
			"code":  coded.Code,
		}
		if coded.ClientRequestID != "" {
			payload["clientRequestId"] = coded.ClientRequestID
		}
		writeJSON(w, status, payload)
		return
	}
	msg := "request failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		msg = err.Error()
	}
	writeJSONError(w, status, msg)
}

func writeCodedRunnerError(w http.ResponseWriter, status int, code, msg, clientReqID string) {
	writeRunnerRequestError(w, status, &CodedRequestError{
		HTTPStatus:      status,
		Code:            code,
		Message:         msg,
		ClientRequestID: strings.TrimSpace(clientReqID),
	})
}

type canonicalPromptDigestPayload struct {
	SessionID      string              `json:"sessionID"`
	Text           string              `json:"text"`
	Runtime        string              `json:"runtime,omitempty"`
	Agent          string              `json:"agent,omitempty"`
	Variant        string              `json:"variant,omitempty"`
	FastMode       bool                `json:"fastMode,omitempty"`
	System         string              `json:"system,omitempty"`
	Model          *PromptModel        `json:"model,omitempty"`
	ImageOptions   *PromptImageOptions `json:"imageOptions,omitempty"`
	Parts          []MessagePart       `json:"parts,omitempty"`
	SkillSelection *SkillSelection     `json:"skillSelection,omitempty"`
	ContinueTask   bool                `json:"continueTask,omitempty"`
}

// PromptRequestDigest computes a deterministic SHA-256 hex digest of a prompt's semantic inputs.
func PromptRequestDigest(sessionID string, body PromptRequest) string {
	payload := canonicalPromptDigestPayload{
		SessionID:      strings.TrimSpace(sessionID),
		Text:           strings.TrimSpace(body.Text),
		Runtime:        strings.TrimSpace(body.Runtime),
		Agent:          strings.TrimSpace(body.Agent),
		Variant:        strings.TrimSpace(body.Variant),
		FastMode:       body.FastMode,
		System:         strings.TrimSpace(body.System),
		Model:          body.Model,
		ImageOptions:   body.ImageOptions,
		Parts:          body.Parts,
		SkillSelection: body.SkillSelection,
		ContinueTask:   body.ContinueTask,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CommitLedger implementation bridging worktree/commit into taskLedgerStore.
func (s *Service) AdmitCommit(scope, sessionID, clientRequestID, digest string, meta map[string]string) worktree.CommitAdmissionDecision {
	if s == nil || s.ledger == nil {
		return worktree.CommitAdmissionDecision{State: worktree.CommitAdmissionNew}
	}
	out := s.ledger.AdmitRequest(RequestKindWorktreeCommit, scope, sessionID, clientRequestID, digest, meta)
	return worktree.CommitAdmissionDecision{
		State:        worktree.CommitAdmissionState(out.State),
		HTTPStatus:   out.HTTPStatus,
		ResponseBody: out.ResponseBody,
		ErrorCode:    out.ErrorCode,
		ErrorMessage: out.ErrorMessage,
	}
}

func (s *Service) CompleteCommit(scope, clientRequestID string, httpStatus int, responseBody []byte) {
	if s == nil || s.ledger == nil {
		return
	}
	s.ledger.CompleteRequest(RequestKindWorktreeCommit, scope, "", clientRequestID, "", httpStatus, responseBody, nil)
}

func (s *Service) AbortUncommittedCommit(scope, clientRequestID string) {
	if s == nil || s.ledger == nil {
		return
	}
	s.ledger.AbortUncommittedRequest(RequestKindWorktreeCommit, scope, clientRequestID)
}

func (s *Service) MarkCommitUnknown(scope, clientRequestID, message string) {
	if s == nil || s.ledger == nil {
		return
	}
	s.ledger.MarkRequestUnknown(RequestKindWorktreeCommit, scope, clientRequestID, message)
}

func (s *Service) handleInspectRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/hoxi/requests/"))
	if id == "" || strings.Contains(id, "/") {
		writeJSONError(w, http.StatusNotFound, "unknown request endpoint")
		return
	}
	q := r.URL.Query()
	kind := strings.TrimSpace(q.Get("kind"))
	switch kind {
	case RequestKindPrompt, RequestKindPermissionReply, RequestKindWorktreeCommit:
	default:
		writeJSONError(w, http.StatusBadRequest, "kind must be prompt, permission_reply or worktree_commit")
		return
	}
	scope := strings.TrimSpace(q.Get("scope"))
	if scope == "" && (kind == RequestKindPrompt || kind == RequestKindWorktreeCommit) {
		scope = strings.TrimSpace(q.Get("sessionID"))
	}
	if scope == "" && kind == RequestKindWorktreeCommit && strings.TrimSpace(q.Get("directory")) != "" {
		scope = worktree.CommitRequestScope("", q.Get("directory"))
	}
	if scope == "" {
		writeJSONError(w, http.StatusBadRequest, "scope is required")
		return
	}
	if kind == RequestKindWorktreeCommit && strings.HasPrefix(scope, "dir:") {
		scope = worktree.CommitRequestScope("", strings.TrimPrefix(scope, "dir:"))
	}
	digest := strings.ToLower(strings.TrimSpace(q.Get("digest")))
	if digest != "" && !isValidHexDigest(digest) {
		writeJSONError(w, http.StatusBadRequest, "invalid request digest")
		return
	}

	reqDir := strings.TrimSpace(q.Get("directory"))
	if kind == RequestKindPrompt || (kind == RequestKindWorktreeCommit && !strings.HasPrefix(scope, "dir:")) {
		if !s.matchesQuestionOrApprovalDirectory(scope, "", reqDir) {
			writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
			return
		}
	}

	rec, found, err := s.ledger.InspectRequest(kind, scope, id, digest)
	if err != nil {
		writeRunnerRequestError(w, http.StatusConflict, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, RequestInspectResponse{
			OK:              true,
			Kind:            kind,
			Scope:           scope,
			ClientRequestID: id,
			Status:          RequestStatusNotAdmitted,
		})
		return
	}
	if rec.SessionID != "" && !s.matchesQuestionOrApprovalDirectory(rec.SessionID, "", reqDir) {
		writeJSONError(w, http.StatusForbidden, "session directory does not match its sandbox")
		return
	}

	resp := RequestInspectResponse{
		OK:              true,
		Kind:            rec.Kind,
		Scope:           rec.Scope,
		ClientRequestID: rec.ClientRequestID,
		Digest:          rec.Digest,
		Status:          rec.Status,
		HTTPStatus:      rec.HTTPStatus,
		ErrorCode:       rec.ErrorCode,
		ErrorMessage:    rec.ErrorMessage,
		UpdatedAtMs:     rec.UpdatedAtMs,
	}
	if rec.Status == RequestStatusCompleted && len(rec.ResponseBody) > 0 && json.Valid(rec.ResponseBody) {
		resp.Result = append(json.RawMessage(nil), rec.ResponseBody...)
		if kind == RequestKindPrompt && q.Get("turnResult") == "1" {
			resp.Evidence = s.inspectPromptTurn(rec)
		}
	}
	if rec.Status == RequestStatusOutcomeUnknown || rec.Status == RequestStatusAdmitted {
		resp.Evidence = s.collectRequestEvidence(rec)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) collectRequestEvidence(rec PersistedRequestRecord) map[string]any {
	switch rec.Kind {
	case RequestKindWorktreeCommit:
		workDir := strings.TrimSpace(rec.Meta["workDir"])
		if workDir == "" && rec.SessionID != "" {
			workDir = s.sessionWorktree(rec.SessionID)
		}
		return worktree.ObserveCommitEvidence(workDir, rec.Meta["preHead"])

	case RequestKindPrompt:
		sid := firstNonEmptyStr(rec.SessionID, rec.Scope)
		userMsgID := strings.TrimSpace(rec.Meta["userMsgID"])
		turnRecorded := false
		if sid != "" && userMsgID != "" && s.store != nil {
			s.store.VisitTurns(sid, func(sr SessionRecord) {
				for _, t := range sr.Turns {
					if t.Info.ID == userMsgID {
						turnRecorded = true
						return
					}
				}
			})
		}
		return map[string]any{
			"userMessageId": userMsgID,
			"turnRecorded":  turnRecorded,
			"sessionBusy":   s.isSessionBusy(sid),
		}

	case RequestKindPermissionReply:
		permID := rec.Scope
		stillPending := false
		if s.gate != nil {
			s.gate.mu.Lock()
			stillPending = s.gate.pending[permID] != nil
			s.gate.mu.Unlock()
			if !stillPending {
				_, stillPending = s.gate.openCodeAskFor(permID)
			}
		}
		auditRecorded := false
		auditDecision := ""
		if s.audit != nil {
			for _, entry := range s.audit.list(rec.SessionID, 200) {
				if entry.RequestID == permID {
					auditRecorded = true
					auditDecision = entry.Decision
					break
				}
			}
		}
		ev := map[string]any{
			"stillPending":  stillPending,
			"auditRecorded": auditRecorded,
		}
		if auditDecision != "" {
			ev["decision"] = auditDecision
		}
		return ev
	}
	return nil
}
