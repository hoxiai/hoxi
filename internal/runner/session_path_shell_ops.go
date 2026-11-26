package runner

import (
	"fmt"
	"path/filepath"
	"strings"
)

func checkReadOnlyExecTarget(rec SessionRecord, baseRoot, absTarget string) (string, bool) {
	if len(rec.ReadOnlyPaths) == 0 {
		return "", false
	}
	cleaned := filepath.Clean(absTarget)
	realTarget, okReal := resolvePathWithSymlinks(cleaned, 0)
	for _, base := range sessionScopeRoots(rec, baseRoot) {
		if lexRel, ok := relWithinBase(base, cleaned); ok {
			if prot, hit := matchScopePattern(lexRel, rec.ReadOnlyPaths); hit {
				return fmt.Sprintf("executing %q inside read-only protected path %q is forbidden in this session", lexRel, prot), true
			}
		}
		if okReal {
			if realBase, okBase := resolvePathWithSymlinks(base, 0); okBase {
				if realRel, ok := relWithinBase(realBase, realTarget); ok {
					if prot, hit := matchScopePattern(realRel, rec.ReadOnlyPaths); hit {
						return fmt.Sprintf("executing %q inside read-only protected path %q is forbidden in this session", realRel, prot), true
					}
				}
			}
		}
	}
	return "", false
}

func checkInterpreterScriptExecDepth(rec SessionRecord, baseRoot, curDir, cmdBase string, args []shellToken, depth int) (string, bool) {
	i := 0
	for i < len(args) {
		v := args[i].value
		if v == "--" {
			i++
			break
		}
		if v == "-c" && (cmdBase == "sh" || cmdBase == "bash" || cmdBase == "zsh" || cmdBase == "dash" || cmdBase == "ksh") {
			if depth < 2 && i+1 < len(args) && !args[i+1].dynamic {
				subDir := curDir
				for _, rawSub := range splitCommandChain(args[i+1].value) {
					sub := strings.TrimSpace(rawSub)
					if sub == "" {
						continue
					}
					if isCDSegment(sub) {
						if t, ok := extractLiteralCDPath(sub); ok {
							if next := resolveShellOperand(subDir, t); next != "" {
								subDir = next
							}
						}
						continue
					}
					if reason, bad := checkShellSegmentScopeViolationDepth(rec, baseRoot, subDir, sub, depth+1); bad {
						return reason, true
					}
				}
			}
			return "", false
		}
		if v == "-c" || v == "-e" || v == "--eval" || v == "-p" || v == "--print" {
			return "", false
		}
		if strings.HasPrefix(v, "-") {
			if v == "-o" || v == "-m" || v == "-r" || v == "--require" || v == "--import" {
				i += 2
				continue
			}
			i++
			continue
		}
		if len(rec.ReadOnlyPaths) > 0 && !args[i].dynamic {
			if target := resolveShellOperand(curDir, v); target != "" {
				if reason, bad := checkReadOnlyExecTarget(rec, baseRoot, target); bad {
					return reason, true
				}
			}
		}
		break
	}
	if len(rec.ReadOnlyPaths) > 0 && i < len(args) && !args[i].dynamic {
		if target := resolveShellOperand(curDir, args[i].value); target != "" {
			if reason, bad := checkReadOnlyExecTarget(rec, baseRoot, target); bad {
				return reason, true
			}
		}
	}
	return "", false
}

func inPlaceScriptFileOperands(cmdBase string, args []shellToken) []shellToken {
	hasExprFlag := false
	var pos []shellToken
	for i := 0; i < len(args); i++ {
		v := args[i].value
		if v == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if cmdBase == "sed" {
			if v == "-e" || v == "-f" {
				hasExprFlag = true
				i++
				continue
			}
			if strings.HasPrefix(v, "-e") || strings.HasPrefix(v, "-f") || strings.HasPrefix(v, "--expression=") || strings.HasPrefix(v, "--file=") {
				hasExprFlag = true
				continue
			}
			if v == "-i" && i+1 < len(args) && args[i+1].value == "" {
				i++
				continue
			}
		} else if cmdBase == "perl" {
			if v == "-e" || v == "-E" {
				hasExprFlag = true
				i++
				continue
			}
		}
		if strings.HasPrefix(v, "-") && v != "-" {
			continue
		}
		pos = append(pos, args[i])
	}
	if !hasExprFlag && len(pos) > 0 {
		pos = pos[1:]
	}
	return pos
}

func checkGitMutationScope(rec SessionRecord, baseRoot, curDir string, args []shellToken) (string, bool) {
	gitDir := curDir
	i := 0
	for i < len(args) {
		v := args[i].value
		if v == "-C" && i+1 < len(args) {
			if !args[i+1].dynamic {
				if next := resolveShellOperand(gitDir, args[i+1].value); next != "" {
					gitDir = next
				}
			}
			i += 2
			continue
		}
		if strings.HasPrefix(v, "-C") && len(v) > 2 {
			if next := resolveShellOperand(gitDir, v[2:]); next != "" {
				gitDir = next
			}
			i++
			continue
		}
		if strings.HasPrefix(v, "-") {
			if v == "-c" || v == "--git-dir" || v == "--work-tree" {
				i += 2
				continue
			}
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return "", false
	}
	sub := strings.ToLower(args[i].value)
	rest := args[i+1:]

	switch sub {
	case "diff", "log", "show":
		for j := 0; j < len(rest); j++ {
			v := rest[j].value
			var outVal string
			if strings.HasPrefix(v, "--output=") {
				outVal = strings.TrimPrefix(v, "--output=")
			} else if v == "--output" && j+1 < len(rest) {
				outVal = rest[j+1].value
				j++
			}
			if outVal != "" {
				if target := resolveShellOperand(gitDir, outVal); target != "" {
					if reason, bad := sessionWriteTargetViolation(rec, baseRoot, target); bad {
						return reason, true
					}
				}
			}
		}
	case "reset":
		if hasLongFlag(rest, "--hard", "--merge") {
			return "git reset --hard is forbidden in sessions with read-only or restricted writable paths", true
		}
	case "clean":
		if !hasAnyFlagChar(rest, 'n') && !hasLongFlag(rest, "--dry-run") {
			ops := nonFlagOperands(rest)
			if len(ops) == 0 {
				return "git clean without explicit writable paths is forbidden in scoped sessions", true
			}
			for _, op := range ops {
				if target := resolveShellOperand(gitDir, op.value); target != "" {
					if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
						return reason, true
					}
				}
			}
		}
	case "checkout", "restore":
		if sub == "restore" && (hasAnyFlagChar(rest, 'S') || hasLongFlag(rest, "--staged")) &&
			!hasAnyFlagChar(rest, 'W') && !hasLongFlag(rest, "--worktree") {
			return "", false
		}
		pathspecs := gitPathspecOperands(rest)
		if sub == "checkout" && len(pathspecs) == 0 {
			if hasAnyFlagChar(rest, 'f') || hasLongFlag(rest, "--force") {
				return "git checkout --force is forbidden in sessions with read-only or restricted writable paths", true
			}
			for _, op := range nonFlagOperands(rest) {
				if op.value == "." {
					return "git checkout . is forbidden in sessions with read-only or restricted writable paths", true
				}
			}
			return "", false
		}
		if len(pathspecs) == 0 {
			pathspecs = nonFlagOperands(rest)
		}
		for _, op := range pathspecs {
			if target := resolveShellOperand(gitDir, op.value); target != "" {
				if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
					return reason, true
				}
			}
		}
	case "rm", "mv":
		for _, op := range nonFlagOperands(rest) {
			if target := resolveShellOperand(gitDir, op.value); target != "" {
				if reason, bad := sessionAncestorWriteViolation(rec, baseRoot, target); bad {
					return reason, true
				}
			}
		}
	case "apply":
		return "git apply is blocked in sessions with read-only or restricted writable paths; use structured edit tools", true
	}
	return "", false
}

func gitPathspecOperands(args []shellToken) []shellToken {
	for i, a := range args {
		if a.value == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func nonFlagOperands(args []shellToken) []shellToken {
	var out []shellToken
	afterDash := false
	for _, a := range args {
		if !afterDash && a.value == "--" {
			afterDash = true
			continue
		}
		if !afterDash && strings.HasPrefix(a.value, "-") && a.value != "-" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func copyDestOperands(args []shellToken) []shellToken {
	var dests []shellToken
	for i := 0; i < len(args); i++ {
		v := args[i].value
		if (v == "-t" || v == "--target-directory") && i+1 < len(args) {
			dests = append(dests, args[i+1])
			i++
		} else if strings.HasPrefix(v, "--target-directory=") {
			dests = append(dests, shellToken{value: strings.TrimPrefix(v, "--target-directory="), dynamic: args[i].dynamic})
		}
	}
	if len(dests) > 0 {
		return dests
	}
	ops := nonFlagOperands(args)
	if len(ops) >= 2 {
		return []shellToken{ops[len(ops)-1]}
	}
	return ops
}

func hasAnyFlagChar(args []shellToken, chars ...byte) bool {
	for _, a := range args {
		if a.value == "--" {
			break
		}
		if !strings.HasPrefix(a.value, "-") || strings.HasPrefix(a.value, "--") || len(a.value) < 2 {
			continue
		}
		for _, c := range chars {
			if strings.IndexByte(a.value[1:], c) >= 0 {
				return true
			}
		}
	}
	return false
}

func hasLongFlag(args []shellToken, flags ...string) bool {
	for _, a := range args {
		if a.value == "--" {
			break
		}
		for _, f := range flags {
			if a.value == f || strings.HasPrefix(a.value, f+"=") {
				return true
			}
		}
	}
	return false
}

func hasInPlaceFlag(args []shellToken) bool {
	for _, a := range args {
		if a.value == "--" {
			break
		}
		if a.value == "-i" || strings.HasPrefix(a.value, "-i") || a.value == "--in-place" || strings.HasPrefix(a.value, "--in-place=") {
			return true
		}
		if strings.HasPrefix(a.value, "-") && !strings.HasPrefix(a.value, "--") && strings.IndexByte(a.value[1:], 'i') >= 0 {
			return true
		}
	}
	return false
}

func findHasMutatingAction(args []shellToken) bool {
	for _, a := range args {
		switch a.value {
		case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls":
			return true
		}
	}
	return false
}

func findSearchRoots(args []shellToken) []shellToken {
	var roots []shellToken
	for _, a := range args {
		if strings.HasPrefix(a.value, "-") || a.value == "!" || a.value == "(" {
			break
		}
		roots = append(roots, a)
	}
	return roots
}

func globTouchesProtected(rec SessionRecord, baseRoot, curDir, pattern string) bool {
	if strings.ContainsAny(pattern, "$`") {
		return true
	}
	prefix := pattern
	if idx := strings.IndexAny(prefix, "*?["); idx >= 0 {
		prefix = prefix[:idx]
	}
	if prefix == "" {
		prefix = "."
	} else {
		prefix = strings.TrimRight(prefix, `/\`)
		if prefix == "" {
			return true
		}
	}
	target := resolveShellOperand(curDir, prefix)
	if target == "" {
		return true
	}
	_, bad := sessionAncestorWriteViolation(rec, baseRoot, target)
	return bad
}
