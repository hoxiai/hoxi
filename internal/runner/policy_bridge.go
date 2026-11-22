package runner

import "github.com/hoxiai/hoxi/internal/runner/shellpolicy"

type (
	ApprovalPolicy       = shellpolicy.ApprovalPolicy
	processControlTarget = shellpolicy.ProcessControlTarget
	scriptTrust          = shellpolicy.ScriptTrust
)

const (
	ApprovalModeAuto       = shellpolicy.ApprovalModeAuto
	ApprovalModeSmart      = shellpolicy.ApprovalModeSmart
	ApprovalModeManual     = shellpolicy.ApprovalModeManual
	ApprovalModeTrusted    = shellpolicy.ApprovalModeTrusted
	ApprovalModeUnattended = shellpolicy.ApprovalModeUnattended

	defaultApprovalTimeoutSeconds = shellpolicy.DefaultApprovalTimeoutSeconds
	minApprovalTimeoutSeconds     = shellpolicy.MinApprovalTimeoutSeconds
	maxApprovalTimeoutSeconds     = shellpolicy.MaxApprovalTimeoutSeconds

	permissionCategoryRead     = shellpolicy.PermissionCategoryRead
	permissionCategoryEdit     = shellpolicy.PermissionCategoryEdit
	permissionCategoryWebfetch = shellpolicy.PermissionCategoryWebfetch
	permissionCategoryBash     = shellpolicy.PermissionCategoryBash
	permissionCategoryOther    = shellpolicy.PermissionCategoryOther

	fileGrantPrefix = shellpolicy.FileGrantPrefix
	grantKindFile   = shellpolicy.GrantKindFile

	sessionGrantRefusedDestructive = shellpolicy.SessionGrantRefusedDestructive
	sessionGrantRefusedOpaque      = shellpolicy.SessionGrantRefusedOpaque

	recoverClassTempDelete   = shellpolicy.RecoverClassTempDelete
	recoverClassDirDelete    = shellpolicy.RecoverClassDirDelete
	recoverClassGitDiscard   = shellpolicy.RecoverClassGitDiscard
	recoverClassInlineScript = shellpolicy.RecoverClassInlineScript

	scriptTrustNone     = shellpolicy.ScriptTrustNone
	scriptTrustReadOnly = shellpolicy.ScriptTrustReadOnly
	scriptTrustFull     = shellpolicy.ScriptTrustFull

	unattendedCategoryPublish = shellpolicy.UnattendedCategoryPublish
	unattendedCategoryTool    = shellpolicy.UnattendedCategoryTool

	sandboxProofKey     = shellpolicy.SandboxProofKey
	sandboxExecPath     = shellpolicy.SandboxExecPath
	unsandboxedMarker   = shellpolicy.UnsandboxedMarker
	onDemandSandboxNote = shellpolicy.OnDemandSandboxNote
)

var (
	bashToolNames           = shellpolicy.BashToolNames
	readToolNames           = shellpolicy.ReadToolNames
	editToolNames           = shellpolicy.EditToolNames
	editPermissions         = shellpolicy.EditPermissions
	editTargetKeys          = shellpolicy.EditTargetKeys
	hookReadTools           = shellpolicy.HookReadTools
	hookEditTools           = shellpolicy.HookEditTools
	protectedWorktreeDirs   = shellpolicy.ProtectedWorktreeDirs
	sessionGrantableClasses = shellpolicy.SessionGrantableClasses
)

func RegisterSafeHoxiReadSubcommands(group string, subs ...string) {
	shellpolicy.RegisterSafeHoxiReadSubcommands(group, subs...)
}

func defaultApprovalPolicy() ApprovalPolicy         { return shellpolicy.DefaultApprovalPolicy() }
func approvalTimeoutReason(p ApprovalPolicy) string { return shellpolicy.ApprovalTimeoutReason(p) }
func unattendedQuestionTimeoutReason() string       { return shellpolicy.UnattendedQuestionTimeoutReason() }
func isQuestionRequest(req map[string]interface{}) bool {
	return shellpolicy.IsQuestionRequest(req)
}

func approvalAutoApproves(item map[string]interface{}, policy ApprovalPolicy, rules map[string]interface{}, worktree string) bool {
	return shellpolicy.ApprovalAutoApproves(item, policy, rules, worktree)
}
func approvalAutoApprovesIsolated(item map[string]interface{}, policy ApprovalPolicy, rules map[string]interface{}, worktree string, isolated bool) bool {
	return shellpolicy.ApprovalAutoApprovesIsolated(item, policy, rules, worktree, isolated)
}
func commandCandidates(item map[string]interface{}) []string {
	return shellpolicy.CommandCandidates(item)
}
func decideCommandPermissionIn(command, worktree string) string {
	return shellpolicy.DecideCommandPermissionIn(command, worktree)
}
func fileGrantRefused(path string) bool { return shellpolicy.FileGrantRefused(path) }
func grantableEditFiles(item map[string]interface{}, worktree string) []string {
	return shellpolicy.GrantableEditFiles(item, worktree)
}
func grantableInlineScriptRequest(item map[string]interface{}, worktree string) bool {
	return shellpolicy.GrantableInlineScriptRequest(item, worktree)
}
func isAbsOrRootedPath(p string) bool { return shellpolicy.IsAbsOrRootedPath(p) }
func hasRiskySearchSelector(tool string, args map[string]interface{}) bool {
	return shellpolicy.HasRiskySearchSelector(tool, args)
}
func isAttributionPreferenceReadRequest(item map[string]interface{}, worktree string) bool {
	return shellpolicy.IsAttributionPreferenceReadRequest(item, worktree)
}
func isAutoControllableCommand(command, worktree string) bool {
	return shellpolicy.IsAutoControllableCommand(command, worktree)
}
func isCredentialEnvName(name string) bool     { return shellpolicy.IsCredentialEnvName(name) }
func isDestructiveCommand(command string) bool { return shellpolicy.IsDestructiveCommand(command) }
func isDestructivePermissionRequest(item map[string]interface{}) bool {
	return shellpolicy.IsDestructivePermissionRequest(item)
}
func isFileGrantClass(class string) bool  { return shellpolicy.IsFileGrantClass(class) }
func isIsolatedWorktree(path string) bool { return shellpolicy.IsIsolatedWorktree(path) }
func isOpaquePermissionRequestWith(item map[string]interface{}, worktree string, scripts scriptTrust) bool {
	return shellpolicy.IsOpaquePermissionRequestWith(item, worktree, scripts)
}
func isOpaqueShellCommandWith(command, worktree string, scripts scriptTrust) bool {
	return shellpolicy.IsOpaqueShellCommandWith(command, worktree, scripts)
}
func isProtectedRelPath(rel string) bool { return shellpolicy.IsProtectedRelPath(rel) }
func isProtectedWorktreePath(base, target string) bool {
	return shellpolicy.IsProtectedWorktreePath(base, target)
}
func isReadOnlyTaskControlCall(name string, args map[string]interface{}) bool {
	return shellpolicy.IsReadOnlyTaskControlCall(name, args)
}
func isRoutineAttachmentSubpath(slash string) bool {
	return shellpolicy.IsRoutineAttachmentSubpath(slash)
}
func isSafeScriptOutput(rawTarget, base, worktree string) bool {
	return shellpolicy.IsSafeScriptOutput(rawTarget, base, worktree)
}
func isSecretFilePath(p string) bool            { return shellpolicy.IsSecretFilePath(p) }
func isSensitiveExternalPath(p string) bool     { return shellpolicy.IsSensitiveExternalPath(p) }
func isSensitiveExternalReadPath(p string) bool { return shellpolicy.IsSensitiveExternalReadPath(p) }
func itemMetadata(item map[string]interface{}) map[string]interface{} {
	return shellpolicy.ItemMetadata(item)
}
func maskVerifiedProcessControlWithResolver(command string, owns func(processControlTarget) bool, trustedTool func(string) bool) (string, bool) {
	return shellpolicy.MaskVerifiedProcessControlWithResolver(command, owns, trustedTool)
}
func optsOutOfOnDemandSandbox(command string) bool {
	return shellpolicy.OptsOutOfOnDemandSandbox(command)
}
func appendOnDemandSandboxNote(output string) string {
	return shellpolicy.AppendOnDemandSandboxNote(output)
}
func sandboxContainable(item map[string]interface{}, worktree string) bool {
	return shellpolicy.SandboxContainable(item, worktree)
}
func sandboxCoversOpacity(item map[string]interface{}, mode, worktree string) bool {
	return shellpolicy.SandboxCoversOpacity(item, mode, worktree)
}
func pathUnderOwnSession(path string, sessionIDs []string) bool {
	return shellpolicy.PathUnderOwnSession(path, sessionIDs)
}
func pathWithin(base, target string) bool { return shellpolicy.PathWithin(base, target) }
func permissionCategory(item map[string]interface{}) string {
	return shellpolicy.PermissionCategory(item)
}
func permissionName(item map[string]interface{}) string { return shellpolicy.PermissionName(item) }
func preferencePathEqual(path, allowed string) bool {
	return shellpolicy.PreferencePathEqual(path, allowed)
}
func rawHookToolPath(args map[string]interface{}) string { return shellpolicy.RawHookToolPath(args) }
func recoverableCandidates(item map[string]interface{}, worktree string) ([]string, bool) {
	return shellpolicy.RecoverableCandidates(item, worktree)
}
func recoverableCommandClasses(command, worktree string, classes map[string]bool) bool {
	return shellpolicy.RecoverableCommandClasses(command, worktree, classes)
}
func resolvePathWithSymlinks(p string, depth int) (string, bool) {
	return shellpolicy.ResolvePathWithSymlinks(p, depth)
}
func runtimeSandboxConfirmed(rt string) bool { return shellpolicy.RuntimeSandboxConfirmed(rt) }
func sanitizeOpenCodePermissionItem(item map[string]interface{}) bool {
	return shellpolicy.SanitizeOpenCodePermissionItem(item)
}
func scriptTrustFor(mode string) scriptTrust    { return shellpolicy.ScriptTrustFor(mode) }
func sessionGrantRefusal(command string) string { return shellpolicy.SessionGrantRefusal(command) }
func setOf(items ...string) map[string]bool     { return shellpolicy.SetOf(items...) }
func splitCommandChain(command string) []string { return shellpolicy.SplitCommandChain(command) }
func stringList(v interface{}) []string         { return shellpolicy.StringList(v) }
func stripWorktreePrefix(slash string) string   { return shellpolicy.StripWorktreePrefix(slash) }
func trustedProcessControlToolIn(name, inheritedPath string) bool {
	return shellpolicy.TrustedProcessControlToolIn(name, inheritedPath)
}
func unattendedDecision(item map[string]interface{}, worktree string) (bool, string) {
	return shellpolicy.UnattendedDecision(item, worktree)
}
func unattendedDenyReason(category string) string { return shellpolicy.UnattendedDenyReason(category) }
func unattendedIsolated(rec SessionRecord) bool   { return shellpolicy.UnattendedIsolated(rec) }
func unwrapSandboxCommand(raw string) string      { return shellpolicy.UnwrapSandboxCommand(raw) }
func validSessionID(id string) bool               { return shellpolicy.ValidSessionID(id) }
