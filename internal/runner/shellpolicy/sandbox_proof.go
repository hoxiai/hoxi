package shellpolicy

import (
	"regexp"
	"strings"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

const (
	SandboxProofKey   = "hoxiSandboxed"
	SandboxExecPath   = "/usr/bin/sandbox-exec"
	UnsandboxedMarker = "# hoxi:unsandboxed"

	sandboxProofKey   = SandboxProofKey
	sandboxExecPath   = SandboxExecPath
	unsandboxedMarker = UnsandboxedMarker
)

const OnDemandSandboxNote = "\n\n[Hoxi] This command ran in a sandbox because it could not be approved automatically as written. " +
	"In the sandbox it can only write inside the project, temp and cache directories, cannot change .git, " +
	"cannot read .env, key or agent sign-in files, and can only reach servers it starts itself " +
	"(no internet, no local services that were already running, no sockets outside the project). If it failed because of that, " +
	"run it again with `" + unsandboxedMarker + "` as its first line to ask the user to approve it outside the sandbox. " +
	"Do not try to work around the sandbox in any other way."

const onDemandSandboxNote = OnDemandSandboxNote

type sandboxProof struct{ profile string }

func (sandboxProof) MarshalJSON() ([]byte, error) { return []byte("true"), nil }

var (
	strictSandboxWrapRe = regexp.MustCompile(`^(?:'/usr/bin/sandbox-exec'|/usr/bin/sandbox-exec) -f '([^']+)' -- /bin/sh -c '((?:[^']|'\\'')*)'$`)
	hostOnlyToolRe      = regexp.MustCompile(`(?:^|[\s;&|()/` + "`" + `'"])(?:ssh|scp|sftp|rsync|mosh|gh|aws|gcloud|az|kubectl|helm|doctl|flyctl|vercel|netlify|wrangler|heroku|security)(?:$|[\s;&|()'"` + "`" + `])`)
	sandboxDenialRe     = regexp.MustCompile(`Operation not permitted|Hoxi sandbox (?:denied|allows only)`)
)

func OptsOutOfOnDemandSandbox(command string) bool { return optsOutOfOnDemandSandbox(command) }
func AppendOnDemandSandboxNote(output string) string {
	return appendOnDemandSandboxNote(output)
}
func SandboxContainable(item map[string]interface{}, worktree string) bool {
	return sandboxContainable(item, worktree)
}
func SandboxCoversOpacity(item map[string]interface{}, mode, worktree string) bool {
	return sandboxCoversOpacity(item, mode, worktree)
}

func optsOutOfOnDemandSandbox(command string) bool {
	trimmed := strings.TrimLeft(command, "\r\n")
	firstLine, _, _ := strings.Cut(trimmed, "\n")
	return strings.TrimSpace(firstLine) == unsandboxedMarker
}

func appendOnDemandSandboxNote(output string) string {
	if output == "" || strings.Contains(output, unsandboxedMarker) || !sandboxDenialRe.MatchString(output) {
		return output
	}
	return output + onDemandSandboxNote
}

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

func sandboxCoversOpacity(item map[string]interface{}, mode, worktree string) bool {
	if mode != ApprovalModeAuto {
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
