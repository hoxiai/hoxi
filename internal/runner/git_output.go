package runner

import (
	"path"
	"path/filepath"
	"strings"
)

// hasProtectedGitOutput follows parsed argv, not text inside --format or path
// arguments. --output (including Git's abbreviations) writes even for log/show.
// Keep this independent of approval mode so grants cannot permit protected writes.
func hasProtectedGitOutput(command string) bool {
	cwd := "."
	for _, segment := range splitCommandChain(command) {
		words := stripShellKeywords(shellWords(segment))
		for len(words) > 0 && envVarAssignmentWordRe.MatchString(words[0]) {
			words = words[1:]
		}
		words, lookup, ok := unwrapCommandPrefixes(words)
		if !ok || lookup || len(words) == 0 {
			continue
		}
		base := filepath.Base(words[0])
		if base == "cd" && len(words) == 2 {
			cwd = joinGitOutputPath(cwd, words[1])
			continue
		}
		if base != "git" {
			continue
		}
		dir, i := cwd, 1
		for i < len(words) && strings.HasPrefix(words[i], "-") {
			flag := words[i]
			if flag == "-C" && i+1 < len(words) {
				dir = joinGitOutputPath(dir, words[i+1])
				i += 2
				continue
			}
			if strings.HasPrefix(flag, "-C") && len(flag) > 2 {
				dir = joinGitOutputPath(dir, flag[2:])
			} else if gitGlobalValueFlags[flag] {
				i++
			}
			i++
		}
		if i >= len(words) || (words[i] != "log" && words[i] != "show" && words[i] != "diff") {
			continue
		}
		for i++; i < len(words) && words[i] != "--"; i++ {
			flag, target, attached := strings.Cut(words[i], "=")
			if len(flag) < 3 || !strings.HasPrefix("--output", flag) {
				continue
			}
			if !attached && i+1 < len(words) {
				i++
				target = words[i]
			}
			if target != "" {
				resolved := joinGitOutputPath(dir, target)
				if isProtectedRelPath(resolved) || isDisallowedFileCallTarget(resolved) {
					return true
				}
			}
		}
	}
	return false
}

func joinGitOutputPath(dir, target string) string {
	target = filepath.ToSlash(target)
	if strings.HasPrefix(target, "/") {
		return path.Clean(target)
	}
	return path.Join(dir, target)
}
