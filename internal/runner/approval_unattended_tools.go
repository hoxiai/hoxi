package runner

import (
	"regexp"
	"strings"
)

// Subcommand judges for the tools unattended mode refuses by subcommand:
// git, gh, the npm family and macOS security. Part of
// unattendedDynamicCategory; mirrored in .opencode/lib/unattended-policy.js.

var (
	// unattendedGitExecConfigRe matches `git -c` keys that make git run a
	// command or resolve an alias.
	unattendedGitExecConfigRe = regexp.MustCompile(`(?i)^(?:alias\.|core\.(?:pager|editor|sshcommand|fsmonitor|hookspath|askpass)|sequence\.editor|diff\.external|credential\.|gpg\.|uploadpack\.|receivepack\.|[^=]*\.(?:command|cmd|textconv|process|program|helper|clean|smudge)=)`)

	// knownGitSubcommands are git's own commands. Anything else may be an
	// alias (`git -c alias.p=push p`, a user alias in ~/.gitconfig), which
	// can stand for push, so it is refused.
	knownGitSubcommands = setOf("add", "am", "annotate", "apply", "archive", "bisect", "blame", "branch", "bundle",
		"cat-file", "check-attr", "check-ignore", "check-mailmap", "check-ref-format", "checkout", "checkout-index",
		"cherry", "cherry-pick", "clean", "clone", "column", "commit", "commit-graph", "commit-tree", "config",
		"count-objects", "describe", "diff", "diff-files", "diff-index", "diff-tree", "difftool", "fetch",
		"fetch-pack", "for-each-ref", "format-patch", "fsck", "gc", "grep", "hash-object", "help", "init",
		"interpret-trailers", "lfs", "log", "ls-files", "ls-remote", "ls-tree", "maintenance", "merge", "merge-base",
		"merge-file", "merge-tree", "mergetool", "mktree", "mv", "name-rev", "notes", "pack-refs", "patch-id",
		"prune", "pull", "push", "range-diff", "read-tree", "rebase", "reflog", "remote", "repack", "replace",
		"request-pull", "rerere", "reset", "restore", "rev-list", "rev-parse", "revert", "rm", "send-email",
		"send-pack", "http-push", "shortlog", "show", "show-branch", "show-ref", "sparse-checkout", "stash", "status",
		"stripspace", "submodule", "subtree", "switch", "symbolic-ref", "tag", "update-index", "update-ref", "var",
		"verify-commit", "verify-tag", "version", "whatchanged", "worktree", "write-tree", "credential",
		"credential-osxkeychain", "credential-store", "credential-cache", "fast-export", "fast-import",
		"filter-branch", "gui", "citool", "hook", "index-pack", "pack-objects", "unpack-objects", "verify-pack",
		"mktag", "multi-pack-index", "prune-packed", "update-server-info", "fmt-merge-msg", "bugreport",
		"show-index", "get-tar-commit-id", "mailinfo", "mailsplit", "merge-index", "replay", "unpack-file")
)

// unattendedToolCategory judges tools whose subcommand decides what they
// change: a subcommand built at run time is refused.
func unattendedToolCategory(base string, args []uWord, depth int, ctx *unattendedCtx) string {
	if base == "git" {
		return unattendedGitCategory(args, depth, ctx)
	}
	idx := -1
	for i, a := range args {
		if a.dynamic && !strings.HasPrefix(a.text, "-") {
			return unattendedCategoryDynamic
		}
		if !strings.HasPrefix(a.text, "-") {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ""
	}
	sub := args[idx].text
	rest := args[idx+1:]
	restText := make([]string, len(rest))
	for i, a := range rest {
		restText[i] = a.text
	}
	switch base {
	case "npm", "pnpm", "yarn", "bun":
		if len(sub) >= 3 && strings.HasPrefix("publish", sub) {
			return unattendedCategoryPublish // npm resolves `npm publ` to publish
		}
	case "gh":
		if len(rest) > 0 && rest[0].dynamic {
			return unattendedCategoryDynamic
		}
		next := ""
		if len(restText) > 0 {
			next = restText[0]
		}
		switch {
		case sub == "auth" && next == "token",
			sub == "auth" && next == "status" && (containsWord(restText, "-t") || containsWord(restText, "--show-token")):
			return unattendedCategorySecret
		case sub == "api" && ghAPIWrites(restText):
			return unattendedCategoryPublish
		case sub == "release" && (next == "upload" || next == "edit" || next == "delete"),
			sub == "gist" && next == "create",
			sub == "repo" && (next == "create" || next == "delete" || next == "edit"):
			return unattendedCategoryPublish
		}
	case "security":
		switch sub {
		case "find-generic-password", "find-internet-password", "dump-keychain", "export", "find-key", "export-keychain":
			return unattendedCategorySecret
		}
	}
	return ""
}

func ghAPIWrites(args []string) bool {
	for i, a := range args {
		switch {
		case a == "-X" || a == "--method":
			if i+1 < len(args) && !strings.EqualFold(args[i+1], "GET") {
				return true
			}
		case strings.HasPrefix(a, "--method=") && !strings.EqualFold(strings.TrimPrefix(a, "--method="), "GET"),
			strings.HasPrefix(a, "-X") && len(a) > 2 && !strings.EqualFold(a[2:], "GET"):
			return true
		case a == "-f" || a == "-F" || a == "--field" || a == "--raw-field" || a == "--input",
			strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field=") || strings.HasPrefix(a, "--input="):
			return true
		}
	}
	return false
}

func containsWord(args []string, w string) bool {
	for _, a := range args {
		if a == w {
			return true
		}
	}
	return false
}

// unattendedGitCategory judges git beyond the literal push check: config
// that runs commands, an unknown subcommand (an alias), credential helpers,
// and subcommands that run a command (`rebase -x`, `submodule foreach`,
// `bisect run`).
func unattendedGitCategory(args []uWord, depth int, ctx *unattendedCtx) string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i].text, "-") {
		t := args[i].text
		if args[i].dynamic {
			return unattendedCategoryDynamic
		}
		if t == "-c" || t == "--config-env" {
			if i+1 < len(args) {
				v := args[i+1]
				if v.dynamic || unattendedGitExecConfigRe.MatchString(v.text) {
					return unattendedCategoryDynamic
				}
			}
			i += 2
			continue
		}
		if gitGlobalValueFlags[t] {
			i++
		}
		i++
	}
	if i >= len(args) {
		return ""
	}
	sub := args[i]
	if sub.dynamic {
		return unattendedCategoryDynamic
	}
	rest := args[i+1:]
	restText := make([]string, len(rest))
	for j, a := range rest {
		restText[j] = a.text
	}
	switch {
	case sub.text == "send-email":
		return unattendedCategoryPublish
	case sub.text == "credential" || strings.HasPrefix(sub.text, "credential-"):
		return unattendedCategorySecret
	case sub.text == "lfs" || sub.text == "subtree":
		if argvHas(restText, "push") {
			return unattendedCategoryPublish
		}
	case sub.text == "submodule":
		for j, a := range restText {
			if a == "foreach" {
				return unattendedJoinedCategory(rest[j+1:], depth, ctx, true)
			}
		}
	case sub.text == "rebase":
		for j, a := range rest {
			switch {
			case (a.text == "-x" || a.text == "--exec") && j+1 < len(rest):
				if cat := unattendedJoinedCategory(rest[j+1:j+2], depth, ctx, false); cat != "" {
					return cat
				}
			case strings.HasPrefix(a.text, "--exec="):
				if a.dynamic {
					return unattendedCategoryDynamic
				}
				if cat := unattendedScriptCategory(strings.TrimPrefix(a.text, "--exec="), depth+1, ctx); cat != "" {
					return cat
				}
			}
		}
	case sub.text == "bisect":
		if len(restText) > 0 && restText[0] == "run" && len(rest) > 1 {
			return unattendedRunnerCategory(rest[1:], "", false, depth, ctx)
		}
	case !knownGitSubcommands[sub.text]:
		return unattendedCategoryDynamic
	}
	return ""
}

// unattendedJoinedCategory judges words git hands to a shell as one script.
func unattendedJoinedCategory(words []uWord, depth int, ctx *unattendedCtx, skipOptions bool) string {
	parts := make([]string, 0, len(words))
	for _, w := range words {
		if skipOptions && len(parts) == 0 && strings.HasPrefix(w.text, "--") {
			continue
		}
		if w.dynamic || w.procSub {
			return unattendedCategoryDynamic
		}
		parts = append(parts, w.text)
	}
	if len(parts) == 0 {
		return ""
	}
	return unattendedScriptCategory(strings.Join(parts, " "), depth+1, ctx)
}
