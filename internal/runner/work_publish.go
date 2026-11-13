package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// RequestWorkPublishApproval sends the exact immutable product diff through the
// shared permission plane. Publication cannot be covered by a session/class
// grant, unattended mode, or a remote controller's reply.
func (s *Service) RequestWorkPublishApproval(ctx context.Context, sessionID, directory, storeID string, draft any) error {
	if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
		return errors.New("store publication is unavailable in unattended mode")
	}
	if _, err := s.ResolveComputerSession(sessionID, directory); err != nil {
		return err
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	id := runnerPermissionID + randomHex(12)
	request := map[string]interface{}{
		"id": id, "sessionID": sessionID, "directory": directory,
		"permission": "hoxi_work_publish", "patterns": []string{"Publish product draft to 1688 store " + storeID}, "always": []string{},
		"metadata": map[string]interface{}{"tool": "hoxi_work_publish", "storeId": storeID,
			"args": string(raw), "destructive": true, "desktopOnly": true,
			"description": "Publish the displayed product changes to the seller's 1688 store"},
	}
	incoming, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/api/hoxi/work/mcp", nil)
	if err != nil {
		return err
	}
	answer := s.awaitReply(incoming, hookCredential{runtime: "work", sessionID: sessionID}, id, request)
	if answer.Reply != "once" || isRemoteApprovalSource(answer.Source) {
		return errors.New("store publication was not approved on the desktop")
	}
	if s.ApprovalPolicy().Mode == ApprovalModeUnattended {
		return errors.New("store publication is unavailable in unattended mode")
	}
	return ctx.Err()
}
