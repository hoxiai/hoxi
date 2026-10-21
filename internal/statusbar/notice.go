package statusbar

// ApprovalNotice announces that an agent newly needs the user. It carries no
// command text or other session content on purpose: notices leave the machine
// (Hoxi → GoPanel → APNs) and are shown on a lock screen. The phone fetches
// the details over the authenticated tunnel after the user opens it.
type ApprovalNotice struct {
	Kind       string `json:"kind"` // "approval" or "question"
	RequestID  string `json:"requestId"`
	SessionID  string `json:"sessionId"`
	Permission string `json:"permission,omitempty"`
	Pending    int    `json:"pending"`
}

// SetApprovalListener registers fn for newly pending approvals and questions.
// Each request is announced once; fn runs on its own goroutine.
func (t *Tracker) SetApprovalListener(fn func(ApprovalNotice)) {
	t.mu.Lock()
	t.approvalListener = fn
	t.mu.Unlock()
}

// addPendingLocked records a pending request and returns the announcement to
// fire after unlocking, or nil when it was already pending or nobody listens.
func (t *Tracker) addPendingLocked(kind, requestID, sessionID, permission string) func() {
	if _, exists := t.pendingApprovals[requestID]; exists {
		t.pendingApprovals[requestID] = sessionID
		return nil
	}
	t.pendingApprovals[requestID] = sessionID
	listener := t.approvalListener
	if listener == nil {
		return nil
	}
	notice := ApprovalNotice{
		Kind: kind, RequestID: requestID, SessionID: sessionID,
		Permission: permission, Pending: len(t.pendingApprovals),
	}
	return func() { go listener(notice) }
}
