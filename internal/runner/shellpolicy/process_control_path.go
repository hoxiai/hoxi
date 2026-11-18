package shellpolicy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var processControlPATHRe = regexp.MustCompile(`\bPATH\s*[+]?=`)
var processControlAppendRe = regexp.MustCompile(`^export[ \t]+PATH=("?)(?:\$PATH|\$\{PATH\}):(/[A-Za-z0-9_./@%+,-]+(?::/[A-Za-z0-9_./@%+,-]+)*)("?)$`)

// Only a literal suffix preserves the existing lookup order. All PATH writes
// must be top-level, standalone exports; values in child scripts never qualify.
func processControlPathAppends(command string, script *uScript) (map[*uSeg]bool, bool, bool) {
	changes := processControlPATHRe.FindAllStringIndex(command, -1)
	appends := map[*uSeg]bool{}
	if len(changes) == 0 {
		return appends, false, true
	}
	var ranges [][2]int
	segments := splitConstSegments(command)
	for _, seg := range script.segs {
		if len(seg.words) != 2 || seg.pipedIn || seg.stdin != "" {
			continue
		}
		start, end := seg.words[0].start, seg.words[1].end
		if start < 0 || end > len(command) {
			return nil, true, false
		}
		m := processControlAppendRe.FindStringSubmatch(command[start:end])
		if m == nil || m[1] != m[3] {
			continue
		}
		index := sort.Search(len(segments), func(i int) bool { return segments[i].end > start })
		if index >= len(segments) || strings.TrimSpace(segments[index].text(command)) != command[start:end] {
			continue // redirects before/after export could change executable files
		}
		safe := true
		for _, dir := range strings.Split(m[2], ":") {
			if dir == "/" || strings.Contains(dir, "..") || isSensitiveExternalPath(dir) || isProtectedRelPath(dir) {
				safe = false
			}
		}
		if safe {
			appends[seg] = true
			ranges = append(ranges, [2]int{start, end})
		}
	}
	for _, change := range changes {
		if !indexInside(change[0], ranges) {
			return nil, true, false
		}
	}
	return appends, true, true
}

// Recheck the existing lookup before removing any process-control risk. SDK
// additions cannot supply the tool: the inherited executable must be a system
// pkill/kill. The caller separately verifies every matching process group.
func trustedProcessControlTool(name string) bool {
	return trustedProcessControlToolIn(name, os.Getenv("PATH"))
}

func trustedProcessControlToolIn(name, inheritedPath string) bool {
	if runtime.GOOS == "windows" {
		return trustedWindowsKillToolIn(name, inheritedPath, os.Getenv)
	}
	resolved := name
	if !filepath.IsAbs(name) {
		resolved = ""
		for _, dir := range filepath.SplitList(inheritedPath) {
			if !filepath.IsAbs(dir) {
				return false
			}
			candidate := filepath.Join(dir, name)
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				resolved = candidate
				break
			}
		}
		if resolved == "" {
			return false
		}
	}
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return false
	}
	switch filepath.Base(name) {
	case "pkill":
		return real == "/usr/bin/pkill"
	case "kill":
		return real == "/bin/kill" || real == "/usr/bin/kill"
	}
	return false
}

func trustedWindowsKillToolIn(name, inheritedPath string, getenv func(string) string) bool {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(name), ".exe"))
	if base != "kill" {
		return false // pkill is never verified on Windows
	}
	resolved := name
	if !filepath.IsAbs(name) {
		resolved = ""
		for _, dir := range filepath.SplitList(inheritedPath) {
			if !filepath.IsAbs(dir) {
				return false
			}
			cand := filepath.Join(dir, "kill.exe")
			if info, err := os.Stat(cand); err == nil && !info.IsDir() {
				resolved = cand
				break
			}
		}
		if resolved == "" {
			return false
		}
	}
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return false
	}
	return isTrustedWindowsKillPath(real, getenv)
}

func isTrustedWindowsKillPath(realPath string, getenv func(string) string) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(realPath), "/", `\`))
	if norm == "" {
		return false
	}
	// Only Administrator-protected ProgramFiles Git installations qualify (mirrors
	// root-owned /bin/kill and /usr/bin/kill on Unix; user-writable dirs do not).
	for _, k := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		root := strings.TrimRight(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(getenv(k)), "/", `\`)), `\`)
		if root == "" {
			continue
		}
		if norm == root+`\git\usr\bin\kill.exe` || norm == root+`\git\bin\kill.exe` {
			return true
		}
	}
	return false
}
