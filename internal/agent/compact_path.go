package agent

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

func extractWorkspaceRootFromSystem(system string) string {
	if !strings.Contains(system, "<environment>") {
		return ""
	}
	cleaned := stripTaggedSection(system, "<project_instructions ", "</project_instructions>")
	cleaned = stripTaggedSection(cleaned, "<subdirectory_instructions ", "</subdirectory_instructions>")
	var found string
	for rem := cleaned; ; {
		start := strings.Index(rem, "<environment>")
		if start < 0 {
			break
		}
		endRel := strings.Index(rem[start:], "</environment>")
		if endRel < 0 {
			break
		}
		block := rem[start : start+endRel]
		rem = rem[start+endRel+len("</environment>"):]
		if !strings.Contains(block, "\nPlatform: ") {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if after, ok := strings.CutPrefix(strings.TrimSpace(line), "Working directory:"); ok {
				if wd := strings.TrimSpace(after); wd != "" {
					found = wd
				}
			}
		}
	}
	return found
}

type pruneRootPrefix struct {
	drive     string
	dispDrive string
	slashPath string
}

type prunePathNormalizer struct {
	roots           []pruneRootPrefix
	caseInsensitive bool
	rootExists      bool
	realCache       map[string]string
}

func parseCleanPrunePath(raw string) (drive, dispDrive, cleaned string, isAbs, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", "", false, false
	}
	s = strings.ReplaceAll(s, "\\", "/")
	if len(s) >= 2 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':' {
		if len(s) < 3 || s[2] != '/' {
			return "", "", "", false, false
		}
		return strings.ToLower(s[:2]), s[:2], path.Clean(s[2:]), true, true
	}
	if strings.HasPrefix(s, "/") {
		return "", "", path.Clean(s), true, true
	}
	return "", "", path.Clean(s), false, true
}

func newPrunePathNormalizer(worktreeDir string) *prunePathNormalizer {
	wd := strings.TrimSpace(worktreeDir)
	if wd == "" {
		return nil
	}
	drive, dispDrive, cleaned, isAbs, ok := parseCleanPrunePath(wd)
	if !ok {
		return nil
	}
	if !isAbs {
		absWD, err := filepath.Abs(filepath.FromSlash(wd))
		if err != nil {
			return nil
		}
		drive, dispDrive, cleaned, isAbs, ok = parseCleanPrunePath(absWD)
		if !ok || !isAbs {
			return nil
		}
	}
	n := &prunePathNormalizer{
		roots:           []pruneRootPrefix{{drive: drive, dispDrive: dispDrive, slashPath: cleaned}},
		caseInsensitive: drive != "",
	}
	if (runtime.GOOS == "windows") == (drive != "") {
		nativeRoot := filepath.FromSlash(dispDrive + cleaned)
		if st, err := os.Stat(nativeRoot); err == nil && st.IsDir() {
			n.rootExists = true
			if realWD, okReal := evalAgentRealPath(nativeRoot, 0); okReal {
				if rDrive, rDisp, rClean, rAbs, rOK := parseCleanPrunePath(realWD); rOK && rAbs && (rDrive != drive || rClean != cleaned) {
					n.roots = append(n.roots, pruneRootPrefix{drive: rDrive, dispDrive: rDisp, slashPath: rClean})
				}
			}
		}
	}
	return n
}

func normalizePrunePathKey(raw string) (key, display string) {
	return (*prunePathNormalizer)(nil).normalize(raw)
}

func (n *prunePathNormalizer) normalize(raw string) (key, display string) {
	drive, dispDrive, cleaned, isAbs, ok := parseCleanPrunePath(raw)
	if !ok {
		return "", ""
	}
	if !isAbs {
		if cleaned == "." {
			return "", ""
		}
		if n == nil || len(n.roots) == 0 {
			if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
				return "", ""
			}
			return "rel:" + cleaned, cleaned
		}
		r0 := n.roots[0]
		return n.normalizeAbs(r0.drive, r0.dispDrive, path.Clean(r0.slashPath+"/"+cleaned))
	}
	return n.normalizeAbs(drive, dispDrive, cleaned)
}

func (n *prunePathNormalizer) matchRootRel(drive, cleaned string) (rel string, isRoot bool) {
	if n == nil {
		return "", false
	}
	for _, r := range n.roots {
		if r.drive != drive {
			continue
		}
		if r.slashPath == "/" {
			if cleaned == "/" {
				return "", true
			}
			return strings.TrimPrefix(cleaned, "/"), false
		}
		if r.drive != "" {
			if len(cleaned) == len(r.slashPath) && strings.EqualFold(cleaned, r.slashPath) {
				return "", true
			}
			if len(cleaned) > len(r.slashPath) && cleaned[len(r.slashPath)] == '/' &&
				strings.EqualFold(cleaned[:len(r.slashPath)], r.slashPath) {
				return cleaned[len(r.slashPath)+1:], false
			}
			continue
		}
		if cleaned == r.slashPath {
			return "", true
		}
		if len(cleaned) > len(r.slashPath) && cleaned[len(r.slashPath)] == '/' && strings.HasPrefix(cleaned, r.slashPath) {
			return cleaned[len(r.slashPath)+1:], false
		}
	}
	return "", false
}

func (n *prunePathNormalizer) formatRelKey(rel string) (string, string) {
	if n != nil && n.caseInsensitive {
		return "rel:" + strings.ToLower(rel), rel
	}
	return "rel:" + rel, rel
}

func (n *prunePathNormalizer) normalizeAbs(drive, dispDrive, cleaned string) (string, string) {
	if cleaned == "/" || cleaned == "." {
		return "", ""
	}
	origDisp := dispDrive + cleaned
	if n != nil && n.rootExists && (runtime.GOOS == "windows") == (drive != "") {
		realStr, seen := n.realCache[origDisp]
		if !seen {
			if resolved, ok := evalAgentRealPath(filepath.FromSlash(origDisp), 0); ok {
				realStr = resolved
			}
			if n.realCache == nil {
				n.realCache = make(map[string]string)
			}
			n.realCache[origDisp] = realStr
		}
		if rDrive, rDisp, rClean, rAbs, rOK := parseCleanPrunePath(realStr); rOK && rAbs {
			if rel, isRoot := n.matchRootRel(rDrive, rClean); isRoot {
				return "", ""
			} else if rel != "" {
				return n.formatRelKey(rel)
			}
			_ = rDisp
			if rDrive != "" {
				return "abs:" + rDrive + strings.ToLower(rClean), origDisp
			}
			return "abs:" + rClean, origDisp
		}
	}
	if rel, isRoot := n.matchRootRel(drive, cleaned); isRoot {
		return "", ""
	} else if rel != "" {
		return n.formatRelKey(rel)
	}
	if drive != "" {
		return "abs:" + drive + strings.ToLower(cleaned), origDisp
	}
	return "abs:" + cleaned, origDisp
}
