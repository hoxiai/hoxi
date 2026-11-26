package runner

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

const (
	sandboxRoutePath      = "/api/hoxi/runner/sandbox/route"
	approvalSourceSandbox = "sandbox"
)

func (s *Service) handleSandboxRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeJSONError(w, http.StatusForbidden, "loopback only")
		return
	}
	var body struct {
		SessionID string `json:"sessionID"`
		Directory string `json:"directory"`
		Command   string `json:"command"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"profile": s.onDemandSandboxProfile(body.SessionID, body.Directory, body.Command)})
}

// onDemandSandboxProfile returns the profile to wrap command in, or "" when
// the command should run as it is: the policy or a session grant already
// allows it, it asks for a reason other than opacity, or wrapping would not
// change the answer.
func (s *Service) onDemandSandboxProfile(sessionID, directory, command string) string {
	mode := s.ApprovalPolicy().Mode
	if mode != ApprovalModeAuto || !sandbox.Available() ||
		strings.TrimSpace(command) == "" || strings.TrimSpace(directory) == "" ||
		optsOutOfOnDemandSandbox(command) {
		return ""
	}
	plain, hoxiID, ok := s.openCodeRouteBashItem(sessionID, directory, command)
	if !ok {
		return ""
	}
	if s.gate.hasAlways(hoxiID, command) ||
		s.policyApproves(plain, directory) ||
		s.classGrantApproves(plain, hoxiID, directory) ||
		s.openCodeAlwaysApproves(plain, hoxiID) ||
		!isOpaquePermissionRequestWith(plain, directory, scriptTrustFor(mode)) {
		return ""
	}
	if isDestructivePermissionRequest(plain) || !sandboxContainable(plain, directory) {
		return ""
	}
	var scope sandbox.PathScope
	if rec, ok := s.effectiveSessionRecord(hoxiID); ok {
		scope = sessionPathScope(rec)
	}
	profile, err := sandbox.NewManager("").WriteOnDemandWithScope(directory, s.sandboxControlPorts(), scope)
	if err != nil {
		return ""
	}
	wrapped, _, _ := s.openCodeRouteBashItem(sessionID, directory, sandboxWrappedCommand(profile, command))
	sanitizeOpenCodePermissionItem(wrapped)
	if !s.policyApproves(wrapped, directory) {
		return ""
	}
	return profile
}

func (s *Service) openCodeRouteBashItem(rawSessionID, directory, command string) (map[string]interface{}, string, bool) {
	raw := strings.TrimSpace(rawSessionID)
	hoxiID, sessionDir := s.resolveOpenCodePermissionTarget(raw, "", nil)
	if sessionDir != "" && normalizeComparePath(sessionDir) != normalizeComparePath(directory) {
		return nil, "", false
	}
	sid := firstNonEmptyStr(hoxiID, raw)
	item := openCodeBashItem(sid, command)
	if raw != "" && sid != raw {
		if meta, _ := item["metadata"].(map[string]interface{}); meta != nil {
			meta["externalID"] = raw
		}
	}
	return item, sid, true
}

func openCodeBashItem(sessionID, command string) map[string]interface{} {
	return map[string]interface{}{
		"permission": "bash",
		"sessionID":  sessionID,
		"patterns":   []interface{}{command},
		"metadata":   map[string]interface{}{"command": command},
	}
}

func sandboxWrappedCommand(profile, command string) string {
	return shellQuote(sandboxExecPath) + " -f " + shellQuote(profile) + " -- /bin/sh -c " + shellQuote(command)
}

func (s *Service) sandboxControlPorts() []int {
	var ports []int
	if p := sandbox.PortOf(s.hookBaseURL()); p > 0 {
		ports = append(ports, p)
	}
	if s.openCode != nil {
		if p := sandbox.PortOf(s.openCode.BaseURL()); p > 0 {
			ports = append(ports, p)
		}
	}
	return ports
}
