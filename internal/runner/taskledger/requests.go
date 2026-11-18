package taskledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	RequestKindPrompt          = "prompt"
	RequestKindPermissionReply = "permission_reply"
	RequestKindWorktreeCommit  = "worktree_commit"

	RequestStatusNotAdmitted    = "not_admitted"
	RequestStatusAdmitted       = "admitted"
	RequestStatusCompleted      = "completed"
	RequestStatusOutcomeUnknown = "outcome_unknown"

	ErrCodeRequestConflict           = "request_conflict"
	ErrCodeOutcomeUnknown            = "outcome_unknown"
	ErrCodeAlreadyAnswered           = "already_answered"
	ErrCodeRequestInProgress         = "request_in_progress"
	ErrCodeRequestStorageUnavailable = "request_storage_unavailable"

	RequestLedgerRetention = 24 * time.Hour
	MaxPersistedRequests   = 1024
)

// PersistedRequestRecord tracks one client-identified mutating operation across
// network disconnects and host restarts.
type PersistedRequestRecord struct {
	Kind            string            `json:"kind"`
	Scope           string            `json:"scope"`
	SessionID       string            `json:"sessionID,omitempty"`
	ClientRequestID string            `json:"clientRequestId"`
	Digest          string            `json:"digest,omitempty"`
	Status          string            `json:"status"`
	HTTPStatus      int               `json:"httpStatus,omitempty"`
	ResponseBody    []byte            `json:"responseBody,omitempty"`
	ErrorCode       string            `json:"errorCode,omitempty"`
	ErrorMessage    string            `json:"errorMessage,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`
	CreatedAtMs     int64             `json:"createdAtMs"`
	UpdatedAtMs     int64             `json:"updatedAtMs"`
	ExpiresAtMs     int64             `json:"expiresAtMs"`
}

type RequestAdmissionState string

const (
	RequestAdmissionNew         RequestAdmissionState = "new"
	RequestAdmissionInFlight    RequestAdmissionState = "admitted"
	RequestAdmissionDone        RequestAdmissionState = "completed"
	RequestAdmissionUnknown     RequestAdmissionState = "outcome_unknown"
	RequestAdmissionConflict    RequestAdmissionState = "conflict"
	RequestAdmissionUnavailable RequestAdmissionState = "unavailable"
)

type RequestAdmissionOutcome struct {
	State        RequestAdmissionState
	HTTPStatus   int
	ResponseBody []byte
	ErrorCode    string
	ErrorMessage string
}

// RequestInspectResponse is returned by GET /api/hoxi/requests/{clientRequestId}.
type RequestInspectResponse struct {
	OK              bool            `json:"ok"`
	Kind            string          `json:"kind"`
	Scope           string          `json:"scope"`
	ClientRequestID string          `json:"clientRequestId"`
	Digest          string          `json:"digest,omitempty"`
	Status          string          `json:"status"`
	HTTPStatus      int             `json:"httpStatus,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	ErrorCode       string          `json:"errorCode,omitempty"`
	ErrorMessage    string          `json:"errorMessage,omitempty"`
	Evidence        map[string]any  `json:"evidence,omitempty"`
	UpdatedAtMs     int64           `json:"updatedAtMs,omitempty"`
}

// CodedRequestError carries an HTTP status and a stable machine-readable code.
type CodedRequestError struct {
	HTTPStatus      int
	Code            string
	Message         string
	ClientRequestID string
}

func (e *CodedRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func requestLedgerKey(kind, scope, clientRequestID string) string {
	return strings.TrimSpace(kind) + "\x00" + strings.TrimSpace(scope) + "\x00" + strings.TrimSpace(clientRequestID)
}

func cloneNonEmptyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if strings.TrimSpace(k) != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func IsValidHexDigest(d string) bool {
	if len(d) != 64 {
		return false
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

type canonicalPermissionDigestPayload struct {
	PermissionID string `json:"permissionID"`
	Reply        string `json:"reply"`
	Message      string `json:"message,omitempty"`
	GrantClass   bool   `json:"grantClass,omitempty"`
}

// PermissionReplyDigest computes a deterministic SHA-256 hex digest of a permission reply.
func PermissionReplyDigest(permissionID, reply, message string, grantClass bool) string {
	raw, _ := json.Marshal(canonicalPermissionDigestPayload{
		PermissionID: strings.TrimSpace(permissionID),
		Reply:        strings.TrimSpace(reply),
		Message:      strings.TrimSpace(message),
		GrantClass:   grantClass,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func evaluateExistingRecord(existing PersistedRequestRecord, incomingDigest string, incomingMeta map[string]string) RequestAdmissionOutcome {
	if existing.Digest != "" && incomingDigest != "" && existing.Digest != incomingDigest {
		return RequestAdmissionOutcome{
			State:        RequestAdmissionConflict,
			HTTPStatus:   http.StatusConflict,
			ErrorCode:    ErrCodeRequestConflict,
			ErrorMessage: "request identifier was already used for different content",
		}
	}
	if existing.Digest == "" && existing.Kind == RequestKindPermissionReply {
		if prevReply := existing.Meta["reply"]; prevReply != "" && incomingMeta["reply"] != "" && prevReply != incomingMeta["reply"] {
			return RequestAdmissionOutcome{
				State:        RequestAdmissionConflict,
				HTTPStatus:   http.StatusConflict,
				ErrorCode:    ErrCodeRequestConflict,
				ErrorMessage: "request identifier was already used for different content",
			}
		}
	}
	switch existing.Status {
	case RequestStatusCompleted:
		status := existing.HTTPStatus
		if status <= 0 {
			status = http.StatusOK
		}
		return RequestAdmissionOutcome{
			State:        RequestAdmissionDone,
			HTTPStatus:   status,
			ResponseBody: append([]byte(nil), existing.ResponseBody...),
		}
	case RequestStatusOutcomeUnknown:
		msg := strings.TrimSpace(existing.ErrorMessage)
		if msg == "" {
			msg = "request was interrupted before completion; inspect its outcome before retrying"
		}
		return RequestAdmissionOutcome{
			State:        RequestAdmissionUnknown,
			HTTPStatus:   http.StatusConflict,
			ErrorCode:    ErrCodeOutcomeUnknown,
			ErrorMessage: msg,
		}
	default:
		return RequestAdmissionOutcome{
			State:        RequestAdmissionInFlight,
			HTTPStatus:   http.StatusConflict,
			ErrorCode:    ErrCodeRequestInProgress,
			ErrorMessage: "request is still in progress; inspect using the same clientRequestId",
		}
	}
}

func (s *Store) PeekRequest(kind, scope, clientRequestID, digest string, meta map[string]string) (RequestAdmissionOutcome, bool) {
	if s == nil {
		return RequestAdmissionOutcome{}, false
	}
	kind = strings.TrimSpace(kind)
	scope = strings.TrimSpace(scope)
	cid := strings.TrimSpace(clientRequestID)
	if kind == "" || scope == "" || cid == "" {
		return RequestAdmissionOutcome{}, false
	}
	nowMs := time.Now().UnixMilli()
	key := requestLedgerKey(kind, scope, cid)
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.requests[key]
	if !ok {
		return RequestAdmissionOutcome{}, false
	}
	if existing.Status != RequestStatusAdmitted && existing.ExpiresAtMs > 0 && existing.ExpiresAtMs <= nowMs {
		return RequestAdmissionOutcome{}, false
	}
	return evaluateExistingRecord(existing, strings.ToLower(strings.TrimSpace(digest)), meta), true
}

func (s *Store) AdmitRequest(kind, scope, sessionID, clientRequestID, digest string, meta map[string]string) RequestAdmissionOutcome {
	if s == nil {
		return RequestAdmissionOutcome{State: RequestAdmissionNew}
	}
	if s.loadErr != nil {
		return RequestAdmissionOutcome{
			State:        RequestAdmissionUnavailable,
			HTTPStatus:   http.StatusServiceUnavailable,
			ErrorCode:    ErrCodeRequestStorageUnavailable,
			ErrorMessage: "request ledger could not be loaded; no operation was started",
		}
	}
	kind = strings.TrimSpace(kind)
	scope = strings.TrimSpace(scope)
	cid := strings.TrimSpace(clientRequestID)
	digest = strings.ToLower(strings.TrimSpace(digest))
	if kind == "" || scope == "" || cid == "" {
		return RequestAdmissionOutcome{State: RequestAdmissionNew}
	}
	nowMs := time.Now().UnixMilli()
	key := requestLedgerKey(kind, scope, cid)

	s.mu.Lock()
	if existing, ok := s.requests[key]; ok {
		if existing.Status == RequestStatusAdmitted || existing.ExpiresAtMs <= 0 || existing.ExpiresAtMs > nowMs {
			out := evaluateExistingRecord(existing, digest, meta)
			s.mu.Unlock()
			return out
		}
		delete(s.requests, key)
	}
	s.requests[key] = PersistedRequestRecord{
		Kind:            kind,
		Scope:           scope,
		SessionID:       strings.TrimSpace(sessionID),
		ClientRequestID: cid,
		Digest:          digest,
		Status:          RequestStatusAdmitted,
		Meta:            cloneNonEmptyStringMap(meta),
		CreatedAtMs:     nowMs,
		UpdatedAtMs:     nowMs,
		ExpiresAtMs:     nowMs + RequestLedgerRetention.Milliseconds(),
	}
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	if err := s.persist(snap); err != nil {
		s.AbortUncommittedRequest(kind, scope, cid)
		return RequestAdmissionOutcome{
			State:        RequestAdmissionUnavailable,
			HTTPStatus:   http.StatusServiceUnavailable,
			ErrorCode:    ErrCodeRequestStorageUnavailable,
			ErrorMessage: "request could not be durably recorded; no operation was started; retry with the same clientRequestId after storage is available",
		}
	}
	return RequestAdmissionOutcome{State: RequestAdmissionNew}
}

func (s *Store) CompleteRequest(kind, scope, sessionID, clientRequestID, digest string, httpStatus int, responseBody []byte, meta map[string]string) {
	if s == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	scope = strings.TrimSpace(scope)
	cid := strings.TrimSpace(clientRequestID)
	if kind == "" || scope == "" || cid == "" {
		return
	}
	if httpStatus <= 0 {
		httpStatus = http.StatusOK
	}
	nowMs := time.Now().UnixMilli()
	key := requestLedgerKey(kind, scope, cid)

	s.mu.Lock()
	rec, exists := s.requests[key]
	if !exists {
		rec = PersistedRequestRecord{
			Kind:            kind,
			Scope:           scope,
			SessionID:       strings.TrimSpace(sessionID),
			ClientRequestID: cid,
			Digest:          strings.ToLower(strings.TrimSpace(digest)),
			CreatedAtMs:     nowMs,
		}
	} else {
		if rec.SessionID == "" && strings.TrimSpace(sessionID) != "" {
			rec.SessionID = strings.TrimSpace(sessionID)
		}
		if rec.Digest == "" && strings.TrimSpace(digest) != "" {
			rec.Digest = strings.ToLower(strings.TrimSpace(digest))
		}
	}
	if len(meta) > 0 {
		merged := cloneNonEmptyStringMap(rec.Meta)
		if merged == nil {
			merged = make(map[string]string, len(meta))
		}
		for k, v := range meta {
			if k != "" && v != "" {
				merged[k] = v
			}
		}
		rec.Meta = merged
	}
	rec.Status = RequestStatusCompleted
	rec.HTTPStatus = httpStatus
	rec.ResponseBody = append([]byte(nil), responseBody...)
	rec.ErrorCode = ""
	rec.ErrorMessage = ""
	rec.UpdatedAtMs = nowMs
	rec.ExpiresAtMs = nowMs + RequestLedgerRetention.Milliseconds()
	s.requests[key] = rec
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) AbortUncommittedRequest(kind, scope, clientRequestID string) {
	if s == nil {
		return
	}
	key := requestLedgerKey(kind, scope, clientRequestID)
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	existing, ok := s.requests[key]
	if !ok || existing.Status != RequestStatusAdmitted {
		s.mu.Unlock()
		return
	}
	delete(s.requests, key)
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) MarkRequestUnknown(kind, scope, clientRequestID, message string) {
	if s == nil {
		return
	}
	key := requestLedgerKey(kind, scope, clientRequestID)
	nowMs := time.Now().UnixMilli()
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "request was interrupted before completion; inspect its outcome before retrying"
	}
	s.mu.Lock()
	rec, ok := s.requests[key]
	if !ok || rec.Status == RequestStatusCompleted {
		s.mu.Unlock()
		return
	}
	rec.Status = RequestStatusOutcomeUnknown
	rec.HTTPStatus = http.StatusConflict
	rec.ErrorCode = ErrCodeOutcomeUnknown
	rec.ErrorMessage = msg
	rec.UpdatedAtMs = nowMs
	rec.ExpiresAtMs = nowMs + RequestLedgerRetention.Milliseconds()
	s.requests[key] = rec
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

// SettleAdmittedRequestsOnBoot transitions any request left in "admitted" by a
// previous Hoxi process into the terminal "outcome_unknown" state.
func (s *Store) SettleAdmittedRequestsOnBoot() {
	if s == nil {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	changed := false
	for k, rec := range s.requests {
		if rec.Status == RequestStatusAdmitted {
			rec.Status = RequestStatusOutcomeUnknown
			rec.HTTPStatus = http.StatusConflict
			rec.ErrorCode = ErrCodeOutcomeUnknown
			rec.ErrorMessage = "request was interrupted by a Hoxi restart; its outcome is unknown"
			rec.UpdatedAtMs = nowMs
			if rec.ExpiresAtMs <= nowMs {
				rec.ExpiresAtMs = nowMs + RequestLedgerRetention.Milliseconds()
			}
			s.requests[k] = rec
			changed = true
		}
	}
	if !changed {
		s.mu.Unlock()
		return
	}
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) InspectRequest(kind, scope, clientRequestID, queryDigest string) (PersistedRequestRecord, bool, error) {
	if s == nil {
		return PersistedRequestRecord{}, false, nil
	}
	if s.loadErr != nil {
		return PersistedRequestRecord{}, false, &CodedRequestError{
			HTTPStatus:      http.StatusServiceUnavailable,
			Code:            ErrCodeRequestStorageUnavailable,
			Message:         "request ledger could not be loaded; restore the preserved ledger and restart before retrying",
			ClientRequestID: clientRequestID,
		}
	}
	key := requestLedgerKey(kind, scope, clientRequestID)
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	rec, ok := s.requests[key]
	s.mu.Unlock()
	if !ok {
		return PersistedRequestRecord{}, false, nil
	}
	if rec.Status != RequestStatusAdmitted && rec.ExpiresAtMs > 0 && rec.ExpiresAtMs <= nowMs {
		return PersistedRequestRecord{}, false, nil
	}
	if queryDigest != "" && rec.Digest != "" && rec.Digest != queryDigest {
		return PersistedRequestRecord{}, false, &CodedRequestError{
			HTTPStatus:      http.StatusConflict,
			Code:            ErrCodeRequestConflict,
			Message:         "request identifier was already used for different content",
			ClientRequestID: strings.TrimSpace(clientRequestID),
		}
	}
	rec.ResponseBody = append([]byte(nil), rec.ResponseBody...)
	rec.Meta = cloneNonEmptyStringMap(rec.Meta)
	return rec, true, nil
}
