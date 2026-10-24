package runner

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ApprovalModeUnattended lets a confined session run without asking. The OS
// sandbox is the boundary, not a static command allow-list, so opaque shell
// and recoverable destructive commands inside the worktree go through. Four
// classes are refused immediately and never become an approval prompt, in
// every mode that reaches this check: secret reads, push and publish, privilege
// escalation, and sending credentials off the machine. Shell code built at
// run time (eval, a pipe into a shell, `$CMD args`, inline interpreter code
// that can run processes) is refused too, because the four static classes
// cannot see through it (approval_unattended_dynamic.go). These static rules
// are best effort: a script file the session wrote runs as written. What the
// sandbox guarantees instead is in sandbox_env.go and AGENTS.md rule 6. A
// session that is not both an isolated worktree and command-sandboxed does
// not get this path; it stays on the full-trust rules.
const ApprovalModeUnattended = "unattended"

const (
	unattendedCategorySecret  = "secret"
	unattendedCategoryPublish = "publish"
	unattendedCategoryPriv    = "privilege"
	unattendedCategoryEgress  = "egress"
	unattendedCategoryEdit    = "edit"
	unattendedCategoryTool    = "tool"
)

// unattendedDenyReason is what the model reads. It names the class and never
// echoes the command, so a secret inside the command cannot leak back.
func unattendedDenyReason(category string) string {
	label := unattendedCategoryLabel(category)
	if category == unattendedCategoryDynamic {
		return "Unattended mode blocked this (" + label + "): eval, piping text into a shell or interpreter, a command name taken from a variable or substitution, a git alias, and inline interpreter code that runs processes, opens the network or reads the environment are refused. Write the command out literally; if it is a push, publish, privilege or credential step, list it for a person in the final summary instead."
	}
	return "Unattended mode blocked this (" + label + "). Do not rewrite the command to bypass the block; finish the rest of the work and list the steps a person must run in the final summary."
}

func unattendedCategoryLabel(category string) string {
	switch category {
	case unattendedCategorySecret:
		return "secret read"
	case unattendedCategoryPublish:
		return "push/publish"
	case unattendedCategoryPriv:
		return "privilege escalation"
	case unattendedCategoryEgress:
		return "credential egress"
	case unattendedCategoryEdit:
		return "write outside the worktree"
	case unattendedCategoryDynamic:
		return "dynamic code execution"
	default:
		return "unsupported tool"
	}
}

// unattendedIsolated reports whether this session may use the unattended
// allow path: a Hoxi worktree whose runtime commands are OS-sandboxed.
func unattendedIsolated(rec SessionRecord) bool {
	return rec.CommandSandbox && isIsolatedWorktree(rec.WorktreePath) && runtimeSandboxConfirmed(rec.Runtime)
}

// runtimeSandboxConfirmed reports whether the whole runtime process, or every
// shell command it runs, is wrapped by the OS sandbox. OpenCode only confines
// its bash tool (the sandbox plugin); its file tools run in the daemon, so it
// does not qualify for the blanket allow.
func runtimeSandboxConfirmed(runtime string) bool {
	switch runtime {
	case string(RuntimeClaudeCode), string(RuntimeAntigravity), string(RuntimeQoder), string(RuntimeCodeBuddy):
		return true
	default:
		return false
	}
}

// unattendedDecision is the unattended answer for one request: allow, or deny
// with a category. deny is true only for the hard-refuse classes.
func unattendedDecision(item map[string]interface{}, worktree string) (allow bool, category string) {
	if cat := unattendedHardDeny(item, worktree); cat != "" {
		return false, cat
	}
	perm := permissionName(item)
	kind := permissionCategory(item)
	switch {
	case kind == permissionCategoryBash || bashToolNames[perm]:
		return true, ""
	case kind == permissionCategoryRead || readPermissions[perm] || readToolNames[perm]:
		return true, ""
	case kind == permissionCategoryEdit || editPermissions[perm] || editToolNames[perm]:
		if unattendedEditInside(item, worktree) {
			return true, ""
		}
		return false, unattendedCategoryEdit
	case kind == permissionCategoryWebfetch || webfetchToolNames[perm]:
		return true, ""
	}
	return false, unattendedCategoryTool
}

// unattendedHardDeny reports the hard-refuse category, or "" when the request
// is not one of them. It runs in every mode: a class grant or an "always"
// pattern cannot relax it.
func unattendedHardDeny(item map[string]interface{}, worktree string) string {
	perm := permissionName(item)
	meta := itemMetadata(item)
	if perm == "read" || readPermissions[perm] || readToolNames[strings.ToLower(firstString(meta["tool"], firstPattern(item)))] {
		for _, p := range readTargets(item) {
			if isSecretFilePath(p) || unattendedSecretDir(p) {
				// The session's own record is not a credential. Other
				// ~/.hoxi files, and every secret directory, still refuse.
				if isOwnSessionRecordRead(item, p) && !unattendedSecretDir(p) {
					continue
				}
				return unattendedCategorySecret
			}
		}
	}
	if category := permissionCategory(item); category == permissionCategoryBash || bashToolNames[perm] ||
		(perm == "tool" && bashToolNames[strings.ToLower(firstString(meta["tool"]))]) {
		for _, cmd := range commandCandidates(item) {
			if cat := unattendedCommandCategory(cmd); cat != "" {
				return cat
			}
		}
		// Dynamic execution is judged on the whole command: OpenCode's split
		// patterns lose the quoting and heredocs that tie pieces together.
		for _, cmd := range opacityCandidates(item) {
			if cat := unattendedDynamicCategory(cmd); cat != "" {
				return cat
			}
		}
	}
	if perm == "external_directory" || perm == "doom_loop" {
		return unattendedCategoryTool
	}
	return ""
}

func unattendedEditInside(item map[string]interface{}, worktree string) bool {
	if strings.TrimSpace(worktree) == "" {
		return false
	}
	targets := categoryTargets(item, permissionCategoryEdit)
	if len(targets) == 0 {
		return false
	}
	for _, target := range targets {
		if !pathInsideWorktree(worktree, target) {
			return false
		}
	}
	return true
}

func pathInsideWorktree(worktree, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	return pathWithin(worktree, target)
}

var (
	unattendedPublishRe = regexp.MustCompile(`(?i)(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:git(?:\s+-C\s+\S+|\s+-c\s+\S+|\s+--git-dir=\S+|\s+--work-tree=\S+)*\s+push|gh\s+(?:release\s+create|pr\s+merge)|(?:npm|pnpm|yarn|bun)\s+(?:-r\s+|--recursive\s+)?publish|cargo\s+publish|twine\s+upload|gem\s+push|docker\s+push|terraform\s+(?:apply|destroy)|kubectl\s+(?:apply|delete)|helm\s+(?:install|upgrade)|(?:\./)?scripts/ai-release\.sh)\b`)
	unattendedSudoRe    = regexp.MustCompile(`(?i)(?:^|[;&|(\n]|&&|\|\|)\s*(?:nohup\s+|time\s+|exec\s+|command\s+|env(?:\s+[A-Za-z_][A-Za-z0-9_]*=\S*)*\s+)*(?:sudo|su|doas)\b`)
	unattendedEgressRe  = regexp.MustCompile(`(?i)\b(?:curl|wget|http)\b[^\n]*?(?:authorization|bearer|x-api-key|\$\{?[A-Za-z_][A-Za-z0-9_]*(?:_KEY|_TOKEN|_SECRET)\b)`)
	unattendedSecretRe  = regexp.MustCompile(`(?i)(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})/(?:\.ssh|\.aws|\.gnupg|\.kube|\.azure)(?:/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})/\.docker/config\.json|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})/\.config/(?:gh|gcloud)(?:/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})/\.(?:netrc|npmrc|pypirc|git-credentials)\b|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})/\.cargo/credentials|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})/Library/Keychains(?:/|$)|(?:^|[\s'"=(<>:,])(?:~|\$HOME|\$\{HOME\})/\.hoxi/(?:relay-keys|tunnel(?:-dev)?|auth|cloudflared|pairing|approval-policy|models)\.(?:json|db)\b|(?:^|[\s'"=(<>:,])\.env(?:\.(?:local|prod(?:uction)?|dev(?:elopment)?))?(?:$|[\s;&|'"*?])`)
	unattendedWrappedRe = regexp.MustCompile(`(?i)(?:bash|sh|zsh|eval|xargs)\b[^|&;\n]*\b(?:git\s+push|sudo|publish)\b`)
)

// unattendedCommandCategory classifies one shell command. Data arguments are
// masked first so a commit message that mentions "git push" is not a push.
// Process substitutions are unpacked and scanned too. An opaque command
// (bash -c, eval, xargs) is scanned again as raw text: a false deny is
// preferred over letting a wrapped push through.
func unattendedCommandCategory(command string) string {
	command = unwrapSandboxCommand(command)
	for _, form := range destructiveScanForms(command) {
		if cat := scanUnattendedText(form); cat != "" {
			return cat
		}
	}
	if cat := unattendedSegmentCategory(command); cat != "" {
		return cat
	}
	if isOpaqueShellCommand(command, "") {
		if cat := scanUnattendedText(command); cat != "" {
			return cat
		}
	}
	return ""
}

func scanUnattendedText(text string) string {
	switch {
	case unattendedPublishRe.MatchString(text), unattendedWrappedRe.MatchString(text):
		return unattendedCategoryPublish
	case unattendedSudoRe.MatchString(text):
		return unattendedCategoryPriv
	case unattendedEgressRe.MatchString(text):
		return unattendedCategoryEgress
	case unattendedSecretRe.MatchString(text):
		return unattendedCategorySecret
	}
	return ""
}

// unattendedSegmentCategory judges each plain segment after prefix unwrapping,
// which catches `git -C sub push` and `command git push` the regex also sees,
// and `echo $(git push)` once the substitution is split out.
func unattendedSegmentCategory(command string) string {
	segments, _ := splitCommandChainWithPipes(joinLineContinuations(command))
	for _, seg := range segments {
		words := shellWords(seg)
		words, _, ok := unwrapCommandPrefixes(words)
		if !ok || len(words) == 0 {
			if cat := scanUnattendedText(seg); cat != "" {
				return cat
			}
			continue
		}
		if cat := unattendedArgvCategory(words); cat != "" {
			return cat
		}
	}
	return ""
}

func unattendedArgvCategory(words []string) string {
	base := strings.ToLower(filepath.Base(words[0]))
	switch base {
	case "sudo", "su", "doas":
		return unattendedCategoryPriv
	case "git":
		if gitHasSubcommand(words[1:], "push") {
			return unattendedCategoryPublish
		}
	case "gh":
		if len(words) >= 3 && words[1] == "release" && words[2] == "create" {
			return unattendedCategoryPublish
		}
		if len(words) >= 3 && words[1] == "pr" && words[2] == "merge" {
			return unattendedCategoryPublish
		}
	case "npm", "pnpm", "yarn", "bun":
		if argvHas(words[1:], "publish") {
			return unattendedCategoryPublish
		}
	case "cargo":
		if argvHas(words[1:], "publish") {
			return unattendedCategoryPublish
		}
	case "twine":
		if argvHas(words[1:], "upload") {
			return unattendedCategoryPublish
		}
	case "gem":
		if argvHas(words[1:], "push") {
			return unattendedCategoryPublish
		}
	case "docker":
		if argvHas(words[1:], "push") {
			return unattendedCategoryPublish
		}
	case "terraform":
		if argvHas(words[1:], "apply") || argvHas(words[1:], "destroy") {
			return unattendedCategoryPublish
		}
	case "kubectl":
		if argvHas(words[1:], "apply") || argvHas(words[1:], "delete") {
			return unattendedCategoryPublish
		}
	case "helm":
		if argvHas(words[1:], "install") || argvHas(words[1:], "upgrade") {
			return unattendedCategoryPublish
		}
	case "curl", "wget", "http":
		joined := strings.Join(words, " ")
		if unattendedEgressRe.MatchString(joined) {
			return unattendedCategoryEgress
		}
	}
	if base == "ai-release.sh" || strings.HasSuffix(strings.ToLower(words[0]), "scripts/ai-release.sh") {
		return unattendedCategoryPublish
	}
	return ""
}

func gitHasSubcommand(args []string, name string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return false
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace" || a == "--exec-path":
			i++
		case strings.HasPrefix(a, "-"):
			continue
		default:
			return a == name
		}
	}
	return false
}

func argvHas(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") && a == name {
			return true
		}
	}
	return false
}

// unattendedSecretDir matches the credential directories the sandbox also
// refuses to read, so a tool read of ~/.aws is denied even when the path is
// not a single secret file.
func unattendedSecretDir(p string) bool {
	slash := strings.ToLower(filepath.ToSlash(strings.TrimSpace(p)))
	for _, fragment := range []string{"/.ssh/", "/.aws/", "/.gnupg/", "/.kube/", "/.azure/", "/library/keychains/", "/.config/gh/", "/.config/gcloud/"} {
		if strings.Contains(slash, fragment) || strings.HasSuffix(slash, strings.TrimSuffix(fragment, "/")) {
			return true
		}
	}
	return false
}
