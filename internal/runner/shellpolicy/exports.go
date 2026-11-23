package shellpolicy

type ProcessControlTarget = processControlTarget
type ScriptTrust = scriptTrust

const (
	PermissionCategoryRead     = permissionCategoryRead
	PermissionCategoryEdit     = permissionCategoryEdit
	PermissionCategoryWebfetch = permissionCategoryWebfetch
	PermissionCategoryBash     = permissionCategoryBash
	PermissionCategoryOther    = permissionCategoryOther

	FileGrantPrefix       = fileGrantPrefix
	GrantKindFile         = grantKindFile
	SSHHostGrantPrefix    = sshHostGrantPrefix
	SSHProfileGrantPrefix = sshProfileGrantPrefix
	GrantKindSSH          = grantKindSSH

	RecoverClassTempDelete   = recoverClassTempDelete
	RecoverClassDirDelete    = recoverClassDirDelete
	RecoverClassGitDiscard   = recoverClassGitDiscard
	RecoverClassInlineScript = recoverClassInlineScript

	ScriptTrustNone     = scriptTrustNone
	ScriptTrustReadOnly = scriptTrustReadOnly
	ScriptTrustFull     = scriptTrustFull

	UnattendedCategoryPublish = unattendedCategoryPublish
	UnattendedCategoryTool    = unattendedCategoryTool
	UnattendedCategorySecret  = unattendedCategorySecret
	UnattendedCategoryDynamic = unattendedCategoryDynamic

	SeqLoopPlaceholder  = seqLoopPlaceholder
	MaxLiteralLoopBytes = maxLiteralLoopBytes
)

var (
	BashToolNames           = bashToolNames
	ReadToolNames           = readToolNames
	EditToolNames           = editToolNames
	EditPermissions         = editPermissions
	EditTargetKeys          = editTargetKeys
	HookReadTools           = hookReadTools
	HookEditTools           = hookEditTools
	ProtectedWorktreeDirs   = protectedWorktreeDirs
	SessionGrantableClasses = sessionGrantableClasses
)

func ApprovalAutoApproves(item map[string]interface{}, policy ApprovalPolicy, rules map[string]interface{}, worktree string) bool {
	return approvalAutoApproves(item, policy, rules, worktree)
}

func ApprovalAutoApprovesIsolated(item map[string]interface{}, policy ApprovalPolicy, rules map[string]interface{}, worktree string, isolated bool) bool {
	return approvalAutoApprovesIsolated(item, policy, rules, worktree, isolated)
}

func CommandCandidates(item map[string]interface{}) []string { return commandCandidates(item) }
func DecideCommandPermissionIn(command, worktree string) string {
	return decideCommandPermissionIn(command, worktree)
}
func FileGrantRefused(path string) bool { return fileGrantRefused(path) }
func GrantableEditFiles(item map[string]interface{}, worktree string) []string {
	return grantableEditFiles(item, worktree)
}
func GrantableInlineScriptRequest(item map[string]interface{}, worktree string) bool {
	return grantableInlineScriptRequest(item, worktree)
}
func GrantableSSHRequestClasses(item map[string]interface{}, worktree string) []string {
	return grantableSSHRequestClasses(item, worktree)
}
func IsSSHGrantClass(class string) bool { return isSSHGrantClass(class) }
func SSHGrantTarget(class string) (kind, target string, ok bool) {
	return sshGrantTarget(class)
}
func IsAbsOrRootedPath(p string) bool { return isAbsOrRootedPath(p) }
func IsAttributionPreferenceReadRequest(item map[string]interface{}, worktree string) bool {
	return isAttributionPreferenceReadRequest(item, worktree)
}
func IsAutoControllableCommand(command, worktree string) bool {
	return isAutoControllableCommand(command, worktree)
}
func IsDestructiveCommand(command string) bool { return isDestructiveCommand(command) }
func IsDestructivePermissionRequest(item map[string]interface{}) bool {
	return isDestructivePermissionRequest(item)
}
func IsFileGrantClass(class string) bool  { return isFileGrantClass(class) }
func IsIsolatedWorktree(path string) bool { return isIsolatedWorktree(path) }
func IsOpaquePermissionRequestWith(item map[string]interface{}, worktree string, scripts ScriptTrust) bool {
	return isOpaquePermissionRequestWith(item, worktree, scripts)
}
func IsOpaqueShellCommandWith(command, worktree string, scripts ScriptTrust) bool {
	return isOpaqueShellCommandWith(command, worktree, scripts)
}
func IsProtectedRelPath(rel string) bool               { return isProtectedRelPath(rel) }
func IsProtectedWorktreePath(base, target string) bool { return isProtectedWorktreePath(base, target) }
func IsReadOnlyTaskControlCall(name string, args map[string]interface{}) bool {
	return isReadOnlyTaskControlCall(name, args)
}
func IsRoutineAttachmentSubpath(slash string) bool { return isRoutineAttachmentSubpath(slash) }
func IsSafeScriptOutput(rawTarget, base, worktree string) bool {
	return isSafeScriptOutput(rawTarget, base, worktree)
}
func IsSecretFilePath(p string) bool            { return isSecretFilePath(p) }
func IsSensitiveExternalPath(p string) bool     { return isSensitiveExternalPath(p) }
func IsSensitiveExternalReadPath(p string) bool { return isSensitiveExternalReadPath(p) }
func ItemMetadata(item map[string]interface{}) map[string]interface{} {
	return itemMetadata(item)
}
func MaskVerifiedProcessControlWithResolver(command string, owns func(ProcessControlTarget) bool, trustedTool func(string) bool) (string, bool) {
	return maskVerifiedProcessControlWithResolver(command, owns, trustedTool)
}
func PathUnderOwnSession(path string, sessionIDs []string) bool {
	return pathUnderOwnSession(path, sessionIDs)
}
func PathWithin(base, target string) bool                   { return pathWithin(base, target) }
func PermissionCategory(item map[string]interface{}) string { return permissionCategory(item) }
func PermissionName(item map[string]interface{}) string     { return permissionName(item) }
func PreferencePathEqual(path, allowed string) bool         { return preferencePathEqual(path, allowed) }
func RawHookToolPath(args map[string]interface{}) string    { return rawHookToolPath(args) }
func RecoverableCandidates(item map[string]interface{}, worktree string) ([]string, bool) {
	return recoverableCandidates(item, worktree)
}
func RecoverableCommandClasses(command, worktree string, classes map[string]bool) bool {
	return recoverableCommandClasses(command, worktree, classes)
}
func ResolvePathWithSymlinks(p string, depth int) (string, bool) {
	return resolvePathWithSymlinks(p, depth)
}
func RuntimeSandboxConfirmed(rt string) bool { return runtimeSandboxConfirmed(rt) }
func SanitizeOpenCodePermissionItem(item map[string]interface{}) bool {
	return sanitizeOpenCodePermissionItem(item)
}
func ScriptTrustFor(mode string) ScriptTrust    { return scriptTrustFor(mode) }
func SetOf(items ...string) map[string]bool     { return setOf(items...) }
func SplitCommandChain(command string) []string { return splitCommandChain(command) }
func StringList(v interface{}) []string         { return stringList(v) }
func StripWorktreePrefix(slash string) string   { return stripWorktreePrefix(slash) }
func TrustedProcessControlToolIn(name, inheritedPath string) bool {
	return trustedProcessControlToolIn(name, inheritedPath)
}
func UnattendedDecision(item map[string]interface{}, worktree string) (bool, string) {
	return unattendedDecision(item, worktree)
}
func UnattendedDenyReason(category string) string { return unattendedDenyReason(category) }
func UnattendedIsolated(rec SessionRecord) bool   { return unattendedIsolated(rec) }
func UnwrapSandboxCommand(raw string) string      { return unwrapSandboxCommand(raw) }
func ValidSessionID(id string) bool               { return validSessionID(id) }

func UnattendedHardDeny(item map[string]interface{}, worktree string) string {
	return unattendedHardDeny(item, worktree)
}
func UnattendedDynamicCategory(command string) string { return unattendedDynamicCategory(command) }
func IsClaudeSkillReadPath(raw string, directoryScope bool) bool {
	return isClaudeSkillReadPath(raw, directoryScope)
}
func IsAttributionPreferenceReadPath(raw string) bool { return isAttributionPreferenceReadPath(raw) }
func NumericLoopValues(expr string) ([]string, bool)  { return numericLoopValues(expr) }
func NormalizeNumericForHeaders(s string) string      { return normalizeNumericForHeaders(s) }
func ConcretizeLiterals(command string) (string, int) { return concretizeLiterals(command) }
func MaskVerifiedProcessControl(cmd string, owned func(ProcessControlTarget) bool) (string, bool) {
	return maskVerifiedProcessControl(cmd, owned)
}
func TrustedProcessControlTool(name string) bool { return trustedProcessControlTool(name) }
func IsTrustedWindowsKillPath(realPath string, getenv func(string) string) bool {
	return isTrustedWindowsKillPath(realPath, getenv)
}
