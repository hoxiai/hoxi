package runner

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

// sessionShellPolicyCheck enforces session ReadOnlyPaths / WritablePaths on
// shell commands. It returns:
//   - ("deny", reason) for explicit mutations/executions violating path scope
//   - ("require_approval", reason) for non-readonly commands when OS Seatbelt
//     confinement is not active to enforce path boundaries at the kernel layer
//   - ("", "") when safe read-only or confined by Seatbelt without explicit violation
func sessionShellPolicyCheck(rec SessionRecord, effectiveRoot, command string) (string, string) {
	if !rec.HasPathScope() {
		return "", ""
	}
	unwrapped := strings.TrimSpace(unwrapSandboxCommand(command))
	if unwrapped == "" {
		return "", ""
	}
	baseRoot := firstNonEmptyStr(strings.TrimSpace(effectiveRoot), rec.WorktreePath, rec.ProjectDir)

	segments := splitCommandChain(unwrapped)
	curDir := baseRoot
	opaqueCD := false
	for _, rawSeg := range segments {
		seg := strings.TrimSpace(rawSeg)
		if seg == "" {
			continue
		}
		if isCDSegment(seg) {
			if target, ok := extractLiteralCDPath(seg); ok {
				if nextDir := resolveShellOperand(curDir, target); nextDir != "" {
					curDir = nextDir
					continue
				}
			}
			opaqueCD = true
			continue
		}
		if reason, denied := checkShellSegmentScopeViolation(rec, baseRoot, curDir, seg); denied {
			return "deny", reason
		}
	}

	if !opaqueCD && decideCommandPermissionIn(unwrapped, baseRoot) == "allow" {
		return "", ""
	}
	if rec.CommandSandbox && sandbox.Available() {
		return "", ""
	}
	return "require_approval", "command may write outside allowed paths or touch read-only protected paths without OS sandbox confinement"
}

type shellToken struct {
	raw     string
	value   string
	dynamic bool
}

func scanScopeShellTokens(seg string) ([]shellToken, []shellToken) {
	var words []shellToken
	var redirs []shellToken
	i := 0
	n := len(seg)
	readWord := func() (shellToken, bool) {
		for i < n && (seg[i] == ' ' || seg[i] == '\t') {
			i++
		}
		if i >= n {
			return shellToken{}, false
		}
		start := i
		var val strings.Builder
		var quote byte
		dynamic := false
		for i < n {
			c := seg[i]
			if quote == '\'' {
				if c == '\'' {
					if i+3 < n && seg[i+1:i+4] == `\''` {
						val.WriteByte('\'')
						i += 4
						continue
					}
					quote = 0
					i++
					continue
				}
				val.WriteByte(c)
				i++
				continue
			}
			if quote == '"' {
				if c == '\\' && i+1 < n {
					val.WriteByte(seg[i+1])
					i += 2
					continue
				}
				if c == '"' {
					quote = 0
					i++
					continue
				}
				if c == '$' || c == '`' {
					dynamic = true
				}
				val.WriteByte(c)
				i++
				continue
			}
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '>' || c == '<' {
				break
			}
			if c == '\\' && i+1 < n {
				val.WriteByte(seg[i+1])
				i += 2
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				i++
				continue
			}
			if c == '$' || c == '`' || c == '*' || c == '?' || c == '[' {
				dynamic = true
			}
			val.WriteByte(c)
			i++
		}
		if quote != 0 {
			dynamic = true
		}
		return shellToken{raw: seg[start:i], value: val.String(), dynamic: dynamic}, i > start
	}

	for i < n {
		for i < n && (seg[i] == ' ' || seg[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		j := i
		for j < n && seg[j] >= '0' && seg[j] <= '9' {
			j++
		}
		if j < n && seg[j] == '&' && j+1 < n && seg[j+1] == '>' {
			j++
		}
		if j < n && (seg[j] == '>' || (seg[j] == '<' && j+1 < n && seg[j+1] == '>')) {
			i = j + 1
			if i < n && (seg[i] == '>' || seg[i] == '|' || seg[i] == '&') {
				if seg[i] == '&' {
					i++
					_, _ = readWord()
					continue
				}
				i++
			}
			if target, ok := readWord(); ok {
				redirs = append(redirs, target)
			}
			continue
		}
		if seg[i] == '<' {
			i++
			if i < n && seg[i] == '<' {
				i++
				if i < n && seg[i] == '<' {
					i++
				}
			}
			_, _ = readWord()
			continue
		}
		tok, ok := readWord()
		if !ok {
			i++
			continue
		}
		words = append(words, tok)
	}
	return words, redirs
}

func isCDSegment(seg string) bool {
	words, _ := scanScopeShellTokens(seg)
	return len(words) > 0 && words[0].value == "cd"
}

func extractLiteralCDPath(seg string) (string, bool) {
	words, _ := scanScopeShellTokens(seg)
	if len(words) != 2 || words[0].value != "cd" || words[1].dynamic {
		return "", false
	}
	v := strings.TrimSpace(words[1].value)
	if v == "" || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "~") {
		return "", false
	}
	return v, true
}

func resolveShellOperand(curDir, operand string) string {
	op := strings.TrimSpace(operand)
	if op == "" || strings.HasPrefix(op, "~") {
		return ""
	}
	if filepath.IsAbs(op) {
		return filepath.Clean(op)
	}
	if curDir == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(curDir, op))
}

func stripShellCommandWrappers(words []shellToken) []shellToken {
	i := 0
	for i < len(words) {
		w := words[i].value
		if eq := strings.IndexByte(w, '='); eq > 0 && isSimpleEnvIdent(w[:eq]) {
			i++
			continue
		}
		base := strings.ToLower(filepath.Base(w))
		switch base {
		case "nohup", "time", "exec", "command", "builtin", "sudo", "doas":
			i++
			for i < len(words) && strings.HasPrefix(words[i].value, "-") {
				i++
			}
		case "env":
			i++
			for i < len(words) {
				v := words[i].value
				if strings.HasPrefix(v, "-") {
					i++
					continue
				}
				if eq := strings.IndexByte(v, '='); eq > 0 && isSimpleEnvIdent(v[:eq]) {
					i++
					continue
				}
				break
			}
		case "timeout", "gtimeout":
			i++
			for i < len(words) && strings.HasPrefix(words[i].value, "-") {
				flag := words[i].value
				i++
				if (flag == "-k" || flag == "-s" || flag == "--kill-after" || flag == "--signal") && i < len(words) {
					i++
				}
			}
			if i < len(words) {
				i++
			}
		default:
			return words[i:]
		}
	}
	return nil
}

func isSimpleEnvIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func checkShellSegmentScopeViolation(rec SessionRecord, baseRoot, curDir, seg string) (string, bool) {
	return checkShellSegmentScopeViolationDepth(rec, baseRoot, curDir, seg, 0)
}

func checkShellSegmentScopeViolationDepth(rec SessionRecord, baseRoot, curDir, seg string, depth int) (string, bool) {
	words, redirs := scanScopeShellTokens(seg)
	for _, r := range redirs {
		if r.dynamic || r.value == "/dev/null" || r.value == "/dev/stdout" || r.value == "/dev/stderr" {
			continue
		}
		if target := resolveShellOperand(curDir, r.value); target != "" {
			if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
				return reason, true
			}
		}
	}

	words = stripShellCommandWrappers(words)
	if len(words) == 0 {
		return "", false
	}
	cmdRaw := words[0].value
	cmdBase := strings.ToLower(filepath.Base(cmdRaw))
	args := words[1:]

	if strings.Contains(cmdRaw, "/") || strings.Contains(cmdRaw, `\`) {
		if target := resolveShellOperand(curDir, cmdRaw); target != "" {
			if reason, bad := checkReadOnlyExecTarget(rec, baseRoot, target); bad {
				return reason, true
			}
		}
	}

	switch cmdBase {
	case "sh", "bash", "zsh", "dash", "ksh", "source", ".", "python", "python3", "node", "deno", "bun", "ruby", "perl", "php":
		if reason, bad := checkInterpreterScriptExecDepth(rec, baseRoot, curDir, cmdBase, args, depth); bad {
			return reason, true
		}
	case "go":
		if len(args) >= 2 && args[0].value == "run" {
			for _, a := range args[1:] {
				if strings.HasPrefix(a.value, "-") {
					continue
				}
				if target := resolveShellOperand(curDir, strings.TrimSuffix(a.value, "/...")); target != "" {
					if reason, bad := checkReadOnlyExecTarget(rec, baseRoot, target); bad {
						return reason, true
					}
				}
				break
			}
		}
	}

	return checkMutatingCommandScope(rec, baseRoot, curDir, cmdBase, args)
}

func checkMutatingCommandScope(rec SessionRecord, baseRoot, curDir, cmdBase string, args []shellToken) (string, bool) {
	switch cmdBase {
	case "rm", "rmdir", "unlink", "shred", "truncate", "srm":
		recursive := cmdBase == "rm" && hasAnyFlagChar(args, 'r', 'R')
		for _, op := range nonFlagOperands(args) {
			if op.dynamic {
				if globTouchesProtected(rec, baseRoot, curDir, op.value) {
					return fmt.Sprintf("%s with pattern %q may affect read-only or restricted paths", cmdBase, op.value), true
				}
				continue
			}
			target := resolveShellOperand(curDir, op.value)
			if target == "" {
				continue
			}
			if recursive {
				if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
					return reason, true
				}
			} else if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
				return reason, true
			}
		}

	case "mv":
		for _, op := range nonFlagOperands(args) {
			if op.dynamic {
				if globTouchesProtected(rec, baseRoot, curDir, op.value) {
					return fmt.Sprintf("mv with pattern %q may affect read-only or restricted paths", op.value), true
				}
				continue
			}
			if target := resolveShellOperand(curDir, op.value); target != "" {
				if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
					return reason, true
				}
			}
		}

	case "cp", "install", "ln", "rsync":
		for _, op := range copyDestOperands(args) {
			if op.dynamic {
				continue
			}
			target := resolveShellOperand(curDir, op.value)
			if target == "" {
				continue
			}
			if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
				return reason, true
			}
			if hasAnyFlagChar(args, 'r', 'R', 'a') {
				if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
					return reason, true
				}
			}
		}

	case "chmod", "chown", "chgrp", "chflags", "xattr":
		recursive := hasAnyFlagChar(args, 'R')
		ops := nonFlagOperands(args)
		if !hasLongFlag(args, "--reference") && len(ops) > 1 {
			ops = ops[1:]
		}
		for _, op := range ops {
			if op.dynamic {
				if globTouchesProtected(rec, baseRoot, curDir, op.value) {
					return fmt.Sprintf("%s with pattern %q may affect read-only or restricted paths", cmdBase, op.value), true
				}
				continue
			}
			target := resolveShellOperand(curDir, op.value)
			if target == "" {
				continue
			}
			if recursive {
				if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
					return reason, true
				}
			} else if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
				return reason, true
			}
		}

	case "touch", "mkdir", "tee":
		for _, op := range nonFlagOperands(args) {
			if op.dynamic || op.value == "-" {
				continue
			}
			if target := resolveShellOperand(curDir, op.value); target != "" {
				if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
					return reason, true
				}
			}
		}

	case "sed", "perl":
		if hasInPlaceFlag(args) {
			for _, op := range inPlaceScriptFileOperands(cmdBase, args) {
				if op.dynamic {
					continue
				}
				if target := resolveShellOperand(curDir, op.value); target != "" {
					if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
						return reason, true
					}
				}
			}
		}

	case "gofmt", "goimports":
		if hasAnyFlagChar(args, 'w') {
			ops := nonFlagOperands(args)
			if len(ops) == 0 {
				ops = []shellToken{{value: "."}}
			}
			for _, op := range ops {
				if op.dynamic {
					continue
				}
				if target := resolveShellOperand(curDir, op.value); target != "" {
					if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
						return reason, true
					}
				}
			}
		}

	case "prettier", "eslint", "biome":
		if hasLongFlag(args, "--write", "-w", "--fix") {
			for _, op := range nonFlagOperands(args) {
				if op.dynamic {
					continue
				}
				if target := resolveShellOperand(curDir, op.value); target != "" {
					if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
						return reason, true
					}
				}
			}
		}

	case "dd":
		for _, a := range args {
			if strings.HasPrefix(a.value, "of=") && !a.dynamic {
				if target := resolveShellOperand(curDir, strings.TrimPrefix(a.value, "of=")); target != "" {
					if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
						return reason, true
					}
				}
			}
		}

	case "find":
		if findHasMutatingAction(args) {
			roots := findSearchRoots(args)
			if len(roots) == 0 {
				roots = []shellToken{{value: "."}}
			}
			for _, op := range roots {
				if op.dynamic {
					continue
				}
				if target := resolveShellOperand(curDir, op.value); target != "" {
					if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
						return reason, true
					}
				}
			}
		}

	case "git":
		return checkGitMutationScope(rec, baseRoot, curDir, args)
	}
	return "", false
}
