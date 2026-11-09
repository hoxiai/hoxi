package runner

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

// On-demand sandbox. In auto and trusted mode a shell command that Hoxi would
// ask about only because it cannot read it runs inside a narrow OS sandbox
// instead (sandbox.OnDemandProfile): writes stay in the workspace, temp
// directories and build caches, .git is read-only, secrets are unreadable and
// only local network is reachable.
//
// The OpenCode sandbox plugin asks sandboxRoutePath before each bash command.
// Hoxi answers with a profile only when wrapping turns an ask into an allow.
// The wrapped command then comes back as a permission request;
// sanitizeOpenCodePermissionItem strips the wrapper for display and leaves a
// sandboxProof, a Go value JSON input can never produce. The decision lets
// the opacity through when the profile is still the one this process wrote
// for that directory. Destructive commands, the hard-refuse classes and tools
// that only work outside the sandbox (ssh, gh, cloud CLIs) still ask.

const (
	sandboxRoutePath      = "/api/hoxi/runner/sandbox/route"
	approvalSourceSandbox = "sandbox"
	sandboxProofKey       = "hoxiSandboxed"
	sandboxExecPath       = "/usr/bin/sandbox-exec"
)

type sandboxProof struct{ profile string }

// MarshalJSON keeps the proof readable in relayed events and audit entries;
// a decoded `true` is not a proof.
func (sandboxProof) MarshalJSON() ([]byte, error) { return []byte("true"), nil }

var (
	// strictSandboxWrapRe is exactly what the plugin writes: one single-quoted
	// script, so nothing can run outside the wrapper.
	strictSandboxWrapRe = regexp.MustCompile(`^(?:'/usr/bin/sandbox-exec'|/usr/bin/sandbox-exec) -f '([^']+)' -- /bin/sh -c '((?:[^']|'\\'')*)'$`)
	// hostOnlyToolRe names tools that need SSH keys, the gh token, cloud
	// credentials or the keychain. Under the sandbox they would only fail.
	hostOnlyToolRe = regexp.MustCompile(`(?:^|[\s;&|()/` + "`" + `'"])(?:ssh|scp|sftp|rsync|mosh|gh|aws|gcloud|az|kubectl|helm|doctl|flyctl|vercel|netlify|wrangler|heroku|security)(?:$|[\s;&|()'"` + "`" + `])`)
)

// recordSandboxProof runs before the wrapper is stripped for display. A proof
// from an earlier pass over the same item is kept; any other value under the
// key is dropped.
func recordSandboxProof(item map[string]interface{}) {
	if item == nil {
		return
	}
	if _, ok := item[sandboxProofKey].(sandboxProof); ok {
		return
	}
	delete(item, sandboxProofKey)
	if profile, ok := sandboxWrappedProfile(item); ok {
		item[sandboxProofKey] = sandboxProof{profile: profile}
	}
}

// sandboxWrappedProfile returns the profile when every command in a bash
// request is the plugin's wrapper around one profile. Stop-marker and trap
// pieces run outside it but are fixed text.
func sandboxWrappedProfile(item map[string]interface{}) (string, bool) {
	if permissionCategory(item) != permissionCategoryBash {
		return "", false
	}
	meta := itemMetadata(item)
	var raw []string
	for _, v := range []interface{}{item["patterns"], item["pattern"], meta["command"], meta["cmd"], meta["commands"]} {
		raw = append(raw, stringList(v)...)
	}
	profile := ""
	for _, cmd := range raw {
		rest := strings.TrimSpace(stripOpenCodeStopMarkerText(cmd))
		if rest == "" {
			continue
		}
		m := strictSandboxWrapRe.FindStringSubmatch(rest)
		if m == nil || (profile != "" && m[1] != profile) {
			return "", false
		}
		profile = m[1]
	}
	return profile, profile != ""
}

// sandboxCoversOpacity reports whether an opaque request may still run
// unasked because it runs under this process's on-demand profile for its
// directory and names nothing the sandbox cannot contain.
func sandboxCoversOpacity(item map[string]interface{}, mode, worktree string) bool {
	if mode != ApprovalModeAuto && mode != ApprovalModeTrusted {
		return false
	}
	proof, ok := item[sandboxProofKey].(sandboxProof)
	worktree = parsePermissionRequest(item).resolveWorktree(worktree)
	if !ok || strings.TrimSpace(worktree) == "" {
		return false
	}
	if !sandbox.NewManager("").OnDemandTrusted(proof.profile, worktree) {
		return false
	}
	return sandboxContainable(item, worktree)
}

// sandboxContainable is false for the hard-refuse classes (secret, publish,
// privilege, credential egress, dynamic code) and host-only tools.
func sandboxContainable(item map[string]interface{}, worktree string) bool {
	if unattendedHardDeny(item, worktree) != "" {
		return false
	}
	for _, cmd := range append(opacityCandidates(item), commandCandidates(item)...) {
		if hostOnlyToolRe.MatchString(cmd) {
			return false
		}
	}
	return true
}

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
// the command should run as it is: the policy already allows it, it asks for
// a reason other than opacity, or wrapping would not change the answer.
func (s *Service) onDemandSandboxProfile(sessionID, directory, command string) string {
	mode := s.ApprovalPolicy().Mode
	if (mode != ApprovalModeAuto && mode != ApprovalModeTrusted) || !sandbox.Available() ||
		strings.TrimSpace(command) == "" || strings.TrimSpace(directory) == "" {
		return ""
	}
	plain := openCodeBashItem(sessionID, command)
	if s.policyApproves(plain, directory) || !isOpaquePermissionRequestWith(plain, directory, scriptTrustFor(mode)) {
		return ""
	}
	if isDestructivePermissionRequest(plain) || !sandboxContainable(plain, directory) {
		return ""
	}
	profile, err := sandbox.NewManager("").WriteOnDemand(directory, s.sandboxControlPorts())
	if err != nil {
		return ""
	}
	wrapped := openCodeBashItem(sessionID, sandboxWrappedCommand(profile, command))
	sanitizeOpenCodePermissionItem(wrapped)
	if !s.policyApproves(wrapped, directory) {
		return ""
	}
	return profile
}

// openCodeBashItem is the permission request OpenCode sends for command.
func openCodeBashItem(sessionID, command string) map[string]interface{} {
	return map[string]interface{}{
		"permission": "bash",
		"sessionID":  sessionID,
		"patterns":   []interface{}{command},
		"metadata":   map[string]interface{}{"command": command},
	}
}

// sandboxWrappedCommand matches the plugin's wrapper byte for byte.
func sandboxWrappedCommand(profile, command string) string {
	return shellQuote(sandboxExecPath) + " -f " + shellQuote(profile) + " -- /bin/sh -c " + shellQuote(command)
}

// sandboxControlPorts are the local APIs a confined command must not call:
// Hoxi's gateway (approval replies, policy) and the OpenCode daemon.
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
