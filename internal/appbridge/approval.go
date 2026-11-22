package appbridge

import (
	"errors"
	"maps"
	"sort"
	"strings"
	"time"
)

const approvalAskTTL = 10 * time.Minute

// ApprovalRequest carries a one-time decision for an app, not a runner authorization.
type ApprovalRequest struct {
	ApprovalID string            `json:"approvalId"`
	AppID      string            `json:"appId"`
	AppName    string            `json:"appName"`
	Title      string            `json:"title"`
	Summary    string            `json:"summary"`
	RiskLevel  string            `json:"riskLevel"`
	Details    map[string]string `json:"details,omitempty"`
	Status     string            `json:"status"`
	DecisionID string            `json:"decisionId,omitempty"`
	CreatedAt  int64             `json:"createdAt"`
	DecidedAt  int64             `json:"decidedAt,omitempty"`
	ExpiresAt  int64             `json:"expiresAt"`
}

type approvalState struct {
	grantID string
	req     ApprovalRequest
	ch      chan ApprovalRequest
}

func cloneApproval(req ApprovalRequest) ApprovalRequest {
	req.Details = maps.Clone(req.Details)
	return req
}

// EnqueueApproval returns a snapshot and a one-shot wait channel.
func (s *Service) EnqueueApproval(grant GrantInfo, title, summary, riskLevel string, details map[string]string) (ApprovalRequest, <-chan ApprovalRequest, error) {
	title, summary = strings.TrimSpace(title), strings.TrimSpace(summary)
	if title == "" || len(title) > 160 || len(summary) > 1000 || len(details) > 20 {
		return ApprovalRequest{}, nil, errors.New("invalid approval title, summary, or details")
	}
	for key, value := range details {
		if len(key) == 0 || len(key) > 64 || len(value) > 1000 {
			return ApprovalRequest{}, nil, errors.New("invalid approval detail")
		}
	}
	risk := strings.ToLower(strings.TrimSpace(riskLevel))
	switch risk {
	case "":
		risk = "medium"
	case "low", "medium", "high":
	default:
		return ApprovalRequest{}, nil, errors.New("riskLevel must be low, medium, or high")
	}
	id, err := randomHex(8)
	if err != nil {
		return ApprovalRequest{}, nil, err
	}
	now := time.Now()
	req := ApprovalRequest{ApprovalID: "apr_" + id, AppID: grant.AppID, AppName: grant.Name, Title: title,
		Summary: summary, RiskLevel: risk, Details: maps.Clone(details), Status: "pending", CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(approvalAskTTL).UnixMilli()}
	ch := make(chan ApprovalRequest, 1)
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	s.pruneApprovalsLocked(now)
	if len(s.approvals) >= maxPendingRequests {
		return ApprovalRequest{}, nil, ErrQueueFull
	}
	s.approvals[req.ApprovalID] = &approvalState{grantID: grant.ID, req: req, ch: ch}
	return cloneApproval(req), ch, nil
}

func (s *Service) ListPendingApprovals() []ApprovalRequest {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	s.pruneApprovalsLocked(time.Now())
	out := make([]ApprovalRequest, 0)
	for _, st := range s.approvals {
		if st.req.Status == "pending" {
			out = append(out, cloneApproval(st.req))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func (s *Service) DecideApproval(approvalID string, approve bool) (ApprovalRequest, error) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	now := time.Now()
	s.pruneApprovalsLocked(now)
	st, ok := s.approvals[strings.TrimSpace(approvalID)]
	if !ok || st.req.Status != "pending" {
		return ApprovalRequest{}, errors.New("approval is not pending")
	}
	id, err := randomHex(6)
	if err != nil {
		return ApprovalRequest{}, err
	}
	st.req.Status = "denied"
	if approve {
		st.req.Status = "approved"
	}
	st.req.DecidedAt = now.UnixMilli()
	st.req.DecisionID = "dec_" + id
	st.ch <- cloneApproval(st.req)
	close(st.ch)
	return cloneApproval(st.req), nil
}

func (s *Service) pruneApprovalsLocked(now time.Time) {
	for id, st := range s.approvals {
		if st.req.ExpiresAt > now.UnixMilli() {
			continue
		}
		if st.req.Status == "pending" {
			st.req.Status = "expired"
			st.ch <- cloneApproval(st.req)
			close(st.ch)
		}
		delete(s.approvals, id)
	}
}

// ApprovalResult exposes only requests created with this exact grant.
func (s *Service) ApprovalResult(grantID, approvalID string) (ApprovalRequest, bool) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	s.pruneApprovalsLocked(time.Now())
	st, ok := s.approvals[approvalID]
	if !ok || st.grantID != grantID {
		return ApprovalRequest{}, false
	}
	return cloneApproval(st.req), true
}

func (s *Service) CancelApproval(grantID, approvalID string) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if st, ok := s.approvals[approvalID]; ok && st.grantID == grantID && st.req.Status == "pending" {
		close(st.ch)
		delete(s.approvals, approvalID)
	}
}

// cancelGrantApprovals closes pending waits when their credential loses the
// ability to request approval. A closed channel without a decision is denial.
func (s *Service) cancelGrantApprovals(grantID string) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	for id, st := range s.approvals {
		if st.grantID == grantID {
			if st.req.Status == "pending" {
				close(st.ch)
			}
			delete(s.approvals, id)
		}
	}
}
