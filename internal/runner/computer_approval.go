package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// RequestComputerApproval uses the shared permission plane. Desktop input acts
// outside the worktree and its sandbox (a keystroke can do anything the target
// app can), so no approval mode, stored "always" grant, AI review or remote
// reply approves it: only a one-time "allow" on this desktop does.
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
	if _, ok := s.store.Meta(resolved); ok {
		return resolved, nil
	}
	if s.worktrees != nil {
		if binding, bound, bErr := s.worktrees.BindingForSession(resolved); bErr == nil && bound && binding.Directory != "" {
			if normalizeComparePath(directory) != normalizeComparePath(binding.Directory) {
				return "", errors.New("browser directory does not match its session sandbox")
			}
			return resolved, nil
		}
	}
	if s.openCode != nil && s.openCode.Available() {
		var info SessionInfo
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.openCodeJSON(ctx, http.MethodGet, "/session/"+resolved, directory, nil, &info); err == nil && info.ID == resolved {
			dir := info.Directory
			if dir == "" {
				dir = s.rootDir
			}
			if dir != "" && normalizeComparePath(directory) == normalizeComparePath(dir) {
				return resolved, nil
			}
		}
	}
	return "", errors.New("computer_use_requires_registered_session")
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
