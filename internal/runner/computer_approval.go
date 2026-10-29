package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// RequestComputerApproval uses the shared permission plane. Desktop input has
// effects outside a worktree, so it never receives automatic or stored grants.
func (s *Service) RequestComputerApproval(ctx context.Context, sessionID, directory, app string, action any) (bool, error) {
	if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
		return false, errors.New("desktop_input_unavailable_in_unattended_mode")
	}
	if _, err := s.ResolveComputerSession(sessionID, directory); err != nil {
		return false, err
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		return false, err
	}
	id := runnerPermissionID + randomHex(12)
	request := map[string]interface{}{"id": id, "sessionID": sessionID, "permission": "hoxi_computer", "patterns": []string{app + " " + string(encoded)}, "always": []string{}, "metadata": map[string]interface{}{"tool": "hoxi_computer", "app": app, "args": string(encoded), "destructive": true, "description": "Desktop input outside the session worktree", "desktopOnly": true}}
	incoming, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/api/hoxi/computer/action", nil)
	answer := s.awaitReply(incoming, hookCredential{runtime: "computer", sessionID: sessionID}, id, request)
	return answer.Reply == "once", nil
}

func (s *Service) ResolveComputerSession(sessionID, directory string) (string, error) {
	resolved, err := s.ResolveBrowserSession(sessionID, directory)
	if err != nil {
		return "", err
	}
	if _, ok := s.store.Meta(resolved); !ok {
		return "", errors.New("computer_use_requires_registered_session")
	}
	return resolved, nil
}

func (s *Service) CancelComputerApprovals(sessionID string) {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	for _, pending := range s.gate.pending {
		if permissionName(pending.req) != "hoxi_computer" || sessionID != "" && pending.sid != sessionID {
			continue
		}
		select {
		case pending.reply <- approvalReply{Reply: "reject", Message: "computer control was stopped or revoked", Source: "system"}:
		default:
		}
	}
}
