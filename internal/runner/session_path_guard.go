package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/sandbox"
	"github.com/hoxiai/hoxi/internal/subproc"
)

const approvalSourcePathScope = "path_scope"

func sessionPathScope(rec SessionRecord) sandbox.PathScope {
	return sandbox.PathScope{
		ReadOnlyPaths: rec.ReadOnlyPaths,
		WritablePaths: rec.WritablePaths,
	}
}

func normalizeCreatePathScope(projDir string, readOnly, denied, writable []string) ([]string, []string, error) {
	mergedRO := make([]string, 0, len(readOnly)+len(denied))
	mergedRO = append(mergedRO, readOnly...)
	mergedRO = append(mergedRO, denied...)
	if len(mergedRO) == 0 && len(writable) == 0 {
		return nil, nil, nil
	}
	norm, err := sandbox.NormalizePathScope(projDir, sandbox.PathScope{
		ReadOnlyPaths: mergedRO,
		WritablePaths: writable,
	})
	if err != nil {
		return nil, nil, err
	}
	return norm.ReadOnlyPaths, norm.WritablePaths, nil
}

// effectiveSessionRecord returns the session metadata enriched with any path
// scope inherited from its parent session chain so subagents cannot escape
// parent read-only or writable directory boundaries.
func (s *Service) effectiveSessionRecord(sessionID string) (SessionRecord, bool) {
	if s == nil || s.store == nil {
		return SessionRecord{}, false
	}
	rec, ok := s.store.Meta(strings.TrimSpace(sessionID))
	if !ok {
		return SessionRecord{}, false
	}
	if rec.HasPathScope() || strings.TrimSpace(rec.ParentID) == "" {
		return rec, true
	}
	curID := strings.TrimSpace(rec.ParentID)
	for depth := 0; curID != "" && depth < 8; depth++ {
		parent, pOK := s.store.Meta(curID)
		if !pOK {
			break
		}
		if parent.HasPathScope() {
			rec.ReadOnlyPaths = append([]string(nil), parent.ReadOnlyPaths...)
			rec.WritablePaths = append([]string(nil), parent.WritablePaths...)
			if parent.CommandSandbox {
				rec.CommandSandbox = true
			}
			break
		}
		if strings.TrimSpace(parent.ParentID) == curID {
			break
		}
		curID = strings.TrimSpace(parent.ParentID)
	}
	return rec, true
}

// SetSessionPathScope updates and persists session-level read-only / writable
// directory boundaries (used by QA intake and collaboration extensions).
func (s *Service) SetSessionPathScope(sessionID string, readOnlyPaths, writablePaths []string) (SessionInfo, error) {
	if s == nil || s.store == nil {
		return SessionInfo{}, errors.New("runner store is unavailable")
	}
	sid := strings.TrimSpace(sessionID)
	rec, ok := s.store.Meta(sid)
	if !ok {
		return SessionInfo{}, errors.New("session not found")
	}
	baseDir := firstNonEmptyStr(rec.WorktreePath, rec.ProjectDir)
	normRO, normRW, err := normalizeCreatePathScope(baseDir, readOnlyPaths, nil, writablePaths)
	if err != nil {
		return SessionInfo{}, err
	}
	enableSandbox := (len(normRO) > 0 || len(normRW) > 0) && !rec.CommandSandbox && sandbox.CanApply()
	var updated SessionRecord
	if enableSandbox {
		updated, ok, err = s.store.Update(sid, func(r *SessionRecord) {
			r.ReadOnlyPaths = append([]string(nil), normRO...)
			r.WritablePaths = append([]string(nil), normRW...)
			r.CommandSandbox = true
		})
	} else {
		updated, ok, err = s.store.UpdateMetadata(sid, func(m *SessionMetadata) {
			m.ReadOnlyPaths = append([]string(nil), normRO...)
			m.WritablePaths = append([]string(nil), normRW...)
		})
	}
	if err != nil {
		return SessionInfo{}, err
	}
	if !ok {
		return SessionInfo{}, errors.New("session not found")
	}
	if updated.ExternalID != "" && updated.Runtime == "opencode" {
		_ = syncOpenCodeCommandSandbox(updated)
	}
	info := updated.ToSessionInfo()
	s.broadcast(sid, SSEEvent{
		Type:       "session.updated",
		Properties: map[string]interface{}{"info": info},
	})
	return info, nil
}

func buildTurnSystemPrompt(rec SessionRecord, callerSystem string) string {
	base := effectiveCommandExecutionGuidance()
	if scopeRule := formatSessionPathScopeRule(rec); scopeRule != "" && !strings.Contains(callerSystem, "<hoxi_path_scope>") {
		if strings.TrimSpace(callerSystem) == "" {
			callerSystem = scopeRule
		} else {
			callerSystem = scopeRule + "\n\n" + callerSystem
		}
	}
	return strings.TrimSpace(base + "\n\n" + callerSystem)
}

func formatSessionPathScopeRule(rec SessionRecord) string {
	if !rec.HasPathScope() {
		return ""
	}
	var b strings.Builder
	b.WriteString("<hoxi_path_scope>\n")
	b.WriteString("Session path boundaries (hard-enforced by Hoxi tools and sandbox):\n")
	if len(rec.WritablePaths) > 0 {
		b.WriteString("- Writable paths ONLY (all other workspace files are strictly read-only): ")
		b.WriteString(strings.Join(rec.WritablePaths, ", "))
		b.WriteString("\n")
	}
	if len(rec.ReadOnlyPaths) > 0 {
		b.WriteString("- Read-only protected paths (reading is permitted; any edit, write, create, delete, rename, chmod, or script execution inside them is forbidden): ")
		b.WriteString(strings.Join(rec.ReadOnlyPaths, ", "))
		b.WriteString("\n")
	}
	b.WriteString("Do not run repository-wide formatters/codemods or broad git discard commands that touch read-only paths.\n")
	b.WriteString("</hoxi_path_scope>")
	return b.String()
}

func normalizeScopeRel(rel string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(rel, `\`, "/"))
	cleaned = strings.TrimPrefix(path.Clean(cleaned), "./")
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func scopeRelEqualOrUnder(rel, prefix string) bool {
	rel = normalizeScopeRel(rel)
	prefix = normalizeScopeRel(prefix)
	if prefix == "" || rel == "" {
		return false
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		rl := strings.ToLower(rel)
		pl := strings.ToLower(prefix)
		return rl == pl || strings.HasPrefix(rl, pl+"/")
	}
	return rel == prefix || strings.HasPrefix(rel, prefix+"/")
}

func scopeRelIsAncestorOf(rel, target string) bool {
	rel = normalizeScopeRel(rel)
	target = normalizeScopeRel(target)
	if target == "" {
		return false
	}
	if rel == "" {
		return true
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		rl := strings.ToLower(rel)
		tl := strings.ToLower(target)
		return rl == tl || strings.HasPrefix(tl, rl+"/")
	}
	return rel == target || strings.HasPrefix(target, rel+"/")
}

func matchScopePattern(rel string, patterns []string) (string, bool) {
	for _, p := range patterns {
		if scopeRelEqualOrUnder(rel, p) {
			return p, true
		}
	}
	return "", false
}

func matchScopeAncestor(rel string, patterns []string) (string, bool) {
	for _, p := range patterns {
		if scopeRelIsAncestorOf(rel, p) {
			return p, true
		}
	}
	return "", false
}

func relWithinBase(base, target string) (string, bool) {
	base = filepath.Clean(strings.TrimSpace(base))
	target = filepath.Clean(strings.TrimSpace(target))
	if base == "" || target == "" {
		return "", false
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return normalizeScopeRel(rel), true
}

func sessionScopeRoots(rec SessionRecord, rootDir string) []string {
	var roots []string
	seen := make(map[string]bool)
	add := func(dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "" || dir == "." {
			return
		}
		key := normalizeComparePath(dir)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		roots = append(roots, dir)
	}
	add(rec.WorktreePath)
	add(rootDir)
	add(rec.ProjectDir)
	return roots
}

// lexicalRelUnderRealBase resolves symlinks along ancestor prefixes of absTarget
// until it matches realBase, then returns the remaining un-evaluated tail inside
// the workspace. This handles symlinked workspace parent prefixes (e.g. macOS
// /var -> /private/var) without following symlinks inside the workspace itself.
func lexicalRelUnderRealBase(realBase, absTarget string) (string, bool) {
	realBase = filepath.Clean(strings.TrimSpace(realBase))
	absTarget = filepath.Clean(strings.TrimSpace(absTarget))
	if realBase == "" || absTarget == "" {
		return "", false
	}
	if rel, ok := relWithinBase(realBase, absTarget); ok {
		return rel, true
	}
	cur := absTarget
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if normalizeComparePath(filepath.Clean(resolved)) == normalizeComparePath(realBase) {
				if len(tail) == 0 {
					return "", true
				}
				for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
					tail[i], tail[j] = tail[j], tail[i]
				}
				return normalizeScopeRel(strings.Join(tail, "/")), true
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func sanitizedGitStatusEnv() []string {
	base := os.Environ()
	filtered := make([]string, 0, len(base)+2)
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(name) {
		case "GIT_TERMINAL_PROMPT",
			"GCM_INTERACTIVE",
			"GIT_ASKPASS",
			"SSH_ASKPASS",
			"SUDO_ASKPASS",
			"GIT_EDITOR",
			"GIT_SEQUENCE_EDITOR",
			"EDITOR",
			"VISUAL",
			"PAGER",
			"GIT_PAGER":
			continue
		}
		filtered = append(filtered, kv)
	}
	return append(filtered, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
}

// sessionWriteTargetViolation reports whether writing/mutating absTarget violates
// rec's ReadOnlyPaths or WritablePaths. Both lexical and symlink-resolved paths
// are checked so symlinks cannot escape writable boundaries or hit read-only dirs.
func sessionWriteTargetViolation(rec SessionRecord, rootDir, absTarget string) (string, bool) {
	if !rec.HasPathScope() {
		return "", false
	}
	cleaned := filepath.Clean(strings.TrimSpace(absTarget))
	if cleaned == "" || cleaned == "." {
		return "target path could not be resolved against session path boundaries", true
	}
	realTarget, okReal := resolvePathWithSymlinks(cleaned, 0)
	if !okReal {
		return fmt.Sprintf("target path %q could not be verified against session path boundaries", cleaned), true
	}

	roots := sessionScopeRoots(rec, rootDir)
	matchedAnyRoot := false
	lexWritable := false
	realWritable := false
	lexRelForMsg := ""
	realRelForMsg := ""

	for _, base := range roots {
		realBase, okBase := resolvePathWithSymlinks(base, 0)
		lexRel, inLex := relWithinBase(base, cleaned)
		if !inLex && okBase {
			lexRel, inLex = lexicalRelUnderRealBase(realBase, cleaned)
		}
		if inLex {
			matchedAnyRoot = true
			if lexRelForMsg == "" {
				lexRelForMsg = lexRel
			}
			if prot, hit := matchScopePattern(lexRel, rec.ReadOnlyPaths); hit {
				return fmt.Sprintf("path %q is read-only in this session (protected by %q)", lexRel, prot), true
			}
			if len(rec.WritablePaths) > 0 {
				if _, ok := matchScopePattern(lexRel, rec.WritablePaths); ok {
					lexWritable = true
				}
			}
		}
		if okBase {
			if realRel, inReal := relWithinBase(realBase, realTarget); inReal {
				matchedAnyRoot = true
				if realRelForMsg == "" {
					realRelForMsg = realRel
				}
				if prot, hit := matchScopePattern(realRel, rec.ReadOnlyPaths); hit {
					return fmt.Sprintf("path %q is read-only in this session (protected by %q)", realRel, prot), true
				}
				if len(rec.WritablePaths) > 0 {
					if _, ok := matchScopePattern(realRel, rec.WritablePaths); ok {
						realWritable = true
					}
				}
			}
		}
	}

	if len(rec.WritablePaths) > 0 {
		if !matchedAnyRoot {
			if isAllowedTempScratchPath(realTarget) {
				return "", false
			}
			return fmt.Sprintf("path %q is outside writable paths for this session (%s)", cleaned, strings.Join(rec.WritablePaths, ", ")), true
		}
		if !lexWritable || !realWritable {
			display := firstNonEmptyStr(realRelForMsg, lexRelForMsg, cleaned)
			return fmt.Sprintf("path %q is read-only in this session (writable paths: %s)", display, strings.Join(rec.WritablePaths, ", ")), true
		}
	}
	return "", false
}

// sessionAncestorWriteViolation checks whether a broad/recursive operation on
// absTarget (such as rm -rf, mv, chmod -R, gofmt -w) would impact a protected
// read-only child or paths outside WritablePaths.
func sessionAncestorWriteViolation(rec SessionRecord, rootDir, absTarget string) (string, bool) {
	if reason, bad := sessionWriteTargetViolation(rec, rootDir, absTarget); bad {
		return reason, true
	}
	cleaned := filepath.Clean(strings.TrimSpace(absTarget))
	realTarget, okReal := resolvePathWithSymlinks(cleaned, 0)
	if !okReal {
		return fmt.Sprintf("target path %q could not be verified against session path boundaries", cleaned), true
	}
	for _, base := range sessionScopeRoots(rec, rootDir) {
		if lexRel, ok := relWithinBase(base, cleaned); ok {
			if prot, hit := matchScopeAncestor(lexRel, rec.ReadOnlyPaths); hit {
				display := firstNonEmptyStr(lexRel, ".")
				return fmt.Sprintf("operation on %q affects read-only path %q in this session", display, prot), true
			}
		}
		if realBase, okBase := resolvePathWithSymlinks(base, 0); okBase {
			if realRel, ok := relWithinBase(realBase, realTarget); ok {
				if prot, hit := matchScopeAncestor(realRel, rec.ReadOnlyPaths); hit {
					display := firstNonEmptyStr(realRel, ".")
					return fmt.Sprintf("operation on %q affects read-only path %q in this session", display, prot), true
				}
			}
		}
	}
	return "", false
}

func isAllowedTempScratchPath(realPath string) bool {
	slashed := filepath.ToSlash(filepath.Clean(realPath))
	if slashed == "/dev/null" {
		return true
	}
	for _, tmp := range []string{"/tmp", "/private/tmp", os.TempDir()} {
		if strings.TrimSpace(tmp) == "" {
			continue
		}
		if rTmp, ok := resolvePathWithSymlinks(tmp, 0); ok && pathWithin(rTmp, realPath) {
			return true
		}
	}
	return false
}

// sessionToolWriteViolation checks structured non-shell tools across native,
// Claude Code, Gemini CLI, and Antigravity runtimes. Pure read tools return false.
func sessionToolWriteViolation(rec SessionRecord, rootDir, tool string, args map[string]interface{}) (string, bool) {
	if !rec.HasPathScope() {
		return "", false
	}
	lower := strings.ToLower(strings.TrimSpace(tool))
	baseDir := firstNonEmptyStr(strings.TrimSpace(rootDir), rec.WorktreePath, rec.ProjectDir)

	switch lower {
	case "apply_patch":
		targets := extractNativePatchTargets(baseDir, args)
		if len(targets) == 0 {
			return "apply_patch targets could not be verified against session path boundaries", true
		}
		for _, t := range targets {
			if reason, bad := sessionWriteTargetViolation(rec, baseDir, t); bad {
				return reason, true
			}
		}
		return "", false

	case "hoxi_image", "hoxi_video", "hoxi_speech":
		outPath := strings.TrimSpace(firstString(args["outputPath"], args["out"]))
		if outPath == "" {
			return "", false
		}
		resolved := resolveHookToolPath(baseDir, map[string]interface{}{"path": outPath})
		if resolved == "" {
			return fmt.Sprintf("output path %q could not be verified against session path boundaries", outPath), true
		}
		return sessionWriteTargetViolation(rec, baseDir, resolved)
	}

	isEdit := editToolNames[lower] || hookEditTools[lower] ||
		lower == "write" || lower == "edit" || lower == "multiedit" ||
		lower == "notebookedit" || lower == "write_file" || lower == "replace"
	if !isEdit {
		return "", false
	}

	var candidates []string
	if p := resolveHookToolPath(baseDir, args); p != "" {
		candidates = append(candidates, p)
	}
	for _, key := range []string{"movePath", "destination", "dest", "newPath", "new_path", "outputPath"} {
		if raw := strings.TrimSpace(firstString(args[key])); raw != "" {
			if p := resolveHookToolPath(baseDir, map[string]interface{}{"path": raw}); p != "" {
				candidates = append(candidates, p)
			} else {
				return fmt.Sprintf("target path %q could not be verified against session path boundaries", raw), true
			}
		}
	}
	for _, raw := range stringList(args["paths"]) {
		if p := resolveHookToolPath(baseDir, map[string]interface{}{"path": raw}); p != "" {
			candidates = append(candidates, p)
		} else {
			return fmt.Sprintf("target path %q could not be verified against session path boundaries", raw), true
		}
	}
	if len(candidates) == 0 {
		return fmt.Sprintf("%s target path could not be verified against session path boundaries", tool), true
	}
	for _, target := range candidates {
		if reason, bad := sessionWriteTargetViolation(rec, baseDir, target); bad {
			return reason, true
		}
	}
	return "", false
}

// sessionOpenCodeItemViolation evaluates OpenCode permission requests against
// the session's path scope. Returns (reason, true) when the request must be
// hard-rejected.
func sessionOpenCodeItemViolation(rec SessionRecord, dir string, item map[string]interface{}) (string, bool) {
	if !rec.HasPathScope() {
		return "", false
	}
	perm := permissionName(item)
	baseDir := firstNonEmptyStr(strings.TrimSpace(dir), rec.WorktreePath, rec.ProjectDir)
	if perm == "bash" {
		for _, cmd := range commandCandidates(item) {
			if action, reason := sessionShellPolicyCheck(rec, baseDir, cmd); action == "deny" {
				return reason, true
			}
		}
		return "", false
	}
	meta := itemMetadata(item)
	toolObj, _ := item["tool"].(map[string]interface{})
	toolName := strings.ToLower(strings.TrimSpace(firstString(meta["tool"], toolObj["name"])))
	isEdit := perm == "edit" || perm == "write" || perm == "apply_patch" ||
		editToolNames[toolName] || hookEditTools[toolName]
	if !isEdit {
		return "", false
	}
	var rawPaths []string
	rawPaths = append(rawPaths, stringList(item["patterns"])...)
	for _, k := range []string{"filepath", "filePath", "path", "target", "movePath"} {
		rawPaths = append(rawPaths, stringList(meta[k])...)
	}
	if files, ok := meta["files"].([]interface{}); ok {
		for _, f := range files {
			if fm, ok := f.(map[string]interface{}); ok {
				for _, k := range []string{"filePath", "path", "movePath"} {
					rawPaths = append(rawPaths, stringList(fm[k])...)
				}
			}
		}
	}
	if len(rawPaths) == 0 {
		return "edit target could not be verified against session path boundaries", true
	}
	for _, raw := range rawPaths {
		resolved := resolveHookToolPath(baseDir, map[string]interface{}{"path": raw})
		if resolved == "" {
			return fmt.Sprintf("edit target %q could not be verified against session path boundaries", raw), true
		}
		if reason, bad := sessionWriteTargetViolation(rec, baseDir, resolved); bad {
			return reason, true
		}
	}
	return "", false
}

// VerifySessionPathScope inspects git status in the session worktree and returns
// any modified, added, deleted, renamed, or untracked workspace paths that violate
// the session's ReadOnlyPaths or WritablePaths.
func (s *Service) VerifySessionPathScope(sessionID string) ([]string, error) {
	rec, ok := s.effectiveSessionRecord(sessionID)
	if !ok || !rec.HasPathScope() {
		return nil, nil
	}
	worktreeDir := strings.TrimSpace(rec.WorktreePath)
	if worktreeDir == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := subproc.CommandContext(ctx, "git", "-C", worktreeDir, "status", "--porcelain=v1", "-z", "-uall")
	cmd.Env = sanitizedGitStatusEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("verify session path scope: %w", err)
	}
	entries := bytes.Split(out, []byte{0})
	seen := make(map[string]bool)
	var violations []string
	checkRel := func(rel string) {
		norm := normalizeScopeRel(rel)
		if norm == "" || seen[norm] {
			return
		}
		abs := filepath.Join(worktreeDir, filepath.FromSlash(norm))
		if _, bad := sessionWriteTargetViolation(rec, worktreeDir, abs); bad {
			seen[norm] = true
			violations = append(violations, norm)
		}
	}
	for i := 0; i < len(entries); i++ {
		raw := entries[i]
		if len(raw) < 4 {
			continue
		}
		xy := string(raw[:2])
		p1 := string(raw[3:])
		checkRel(p1)
		if (xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C') && i+1 < len(entries) {
			i++
			checkRel(string(entries[i]))
		}
	}
	sort.Strings(violations)
	return violations, nil
}
