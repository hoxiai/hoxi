package sandbox

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

const (
	MaxScopePaths      = 64
	MaxScopePathLength = 256
)

// PathScope defines optional workspace-relative write boundaries for a session.
// ReadOnlyPaths remain readable (subject to global secret rules) but are denied
// all writes and direct execution. When WritablePaths is non-empty, writes inside
// the workspace are narrowed exclusively to those subtrees while the rest of the
// workspace becomes read-only. ReadOnlyPaths always take precedence over WritablePaths.
type PathScope struct {
	ReadOnlyPaths []string `json:"readOnlyPaths,omitempty"`
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// IsEmpty reports whether no custom read-only or writable path constraints apply.
func (s PathScope) IsEmpty() bool {
	return len(s.ReadOnlyPaths) == 0 && len(s.WritablePaths) == 0
}

// CanonicalKey returns a stable deterministic string identifying the scope for
// caching and on-demand profile keying.
func (s PathScope) CanonicalKey() string {
	if s.IsEmpty() {
		return ""
	}
	return "ro:" + strings.Join(s.ReadOnlyPaths, "\x1f") + "|rw:" + strings.Join(s.WritablePaths, "\x1f")
}

// NormalizeRelativePaths validates and canonicalizes workspace-relative paths.
// Invalid non-empty entries return an explicit error instead of being dropped.
func NormalizeRelativePaths(workspace string, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	canonWorkspace := ""
	if strings.TrimSpace(workspace) != "" {
		var err error
		canonWorkspace, err = canonicalDirectory(workspace)
		if err != nil {
			return nil, fmt.Errorf("invalid workspace for path scope: %w", err)
		}
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		norm, err := normalizeSingleScopePath(canonWorkspace, trimmed)
		if err != nil {
			return nil, err
		}
		if seen[norm] {
			continue
		}
		if len(out) >= MaxScopePaths {
			return nil, fmt.Errorf("at most %d scoped paths are allowed", MaxScopePaths)
		}
		seen[norm] = true
		out = append(out, norm)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// NormalizePathScope validates and normalizes both ReadOnlyPaths and WritablePaths.
func NormalizePathScope(workspace string, scope PathScope) (PathScope, error) {
	ro, err := NormalizeRelativePaths(workspace, scope.ReadOnlyPaths)
	if err != nil {
		return PathScope{}, fmt.Errorf("readOnlyPaths: %w", err)
	}
	rw, err := NormalizeRelativePaths(workspace, scope.WritablePaths)
	if err != nil {
		return PathScope{}, fmt.Errorf("writablePaths: %w", err)
	}
	return PathScope{
		ReadOnlyPaths: ro,
		WritablePaths: rw,
	}, nil
}

func normalizeSingleScopePath(canonWorkspace, raw string) (string, error) {
	if len(raw) > MaxScopePathLength {
		return "", fmt.Errorf("path %q exceeds maximum length (%d)", raw, MaxScopePathLength)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("path %q contains a control character", raw)
		}
	}
	if strings.ContainsAny(raw, "*?[\"'`") {
		return "", fmt.Errorf("path %q must be a literal relative path without wildcards or quotes", raw)
	}
	slashed := strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(slashed, "/") || strings.HasPrefix(slashed, "~") {
		return "", fmt.Errorf("path %q must be relative to the project root", raw)
	}
	if len(slashed) >= 2 && ((slashed[0] >= 'a' && slashed[0] <= 'z') || (slashed[0] >= 'A' && slashed[0] <= 'Z')) && slashed[1] == ':' {
		return "", fmt.Errorf("path %q must be relative to the project root", raw)
	}
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path %q cannot traverse outside the project root", raw)
		}
	}
	cleaned := strings.TrimPrefix(path.Clean(slashed), "./")
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path %q must name a subdirectory or file inside the project", raw)
	}
	if canonWorkspace != "" {
		joined := filepath.Join(canonWorkspace, filepath.FromSlash(cleaned))
		resolved := resolvePathSymlinks(joined)
		if !isPathWithinDir(canonWorkspace, resolved) {
			return "", fmt.Errorf("path %q resolves outside the project root", raw)
		}
	}
	return cleaned, nil
}

func isPathWithinDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// ApplyPathScope returns a copy of profile constrained by scope inside workspace
// (and optional extraBaseDirs such as the main project directory for a worktree).
func ApplyPathScope(profile Profile, workspace string, scope PathScope, extraBaseDirs ...string) (Profile, error) {
	if scope.IsEmpty() {
		return profile, nil
	}
	canonWorkspace, err := canonicalDirectory(workspace)
	if err != nil {
		return Profile{}, err
	}
	norm, err := NormalizePathScope(canonWorkspace, scope)
	if err != nil {
		return Profile{}, err
	}
	if norm.IsEmpty() {
		return profile, nil
	}

	out := profile
	out.Filesystem.AllowWrite = append([]PathRule(nil), profile.Filesystem.AllowWrite...)
	out.Filesystem.DenyWrite = append([]PathRule(nil), profile.Filesystem.DenyWrite...)
	out.Filesystem.DenyRead = append([]PathRule(nil), profile.Filesystem.DenyRead...)
	out.Filesystem.DenyExec = append([]PathRule(nil), profile.Filesystem.DenyExec...)
	out.Filesystem.WriteRestrictions = append([]WriteScopeRestriction(nil), profile.Filesystem.WriteRestrictions...)
	out.Filesystem.DenyNames = append([]string(nil), profile.Filesystem.DenyNames...)
	out.Filesystem.NameDenyExemptions = append([]PathRule(nil), profile.Filesystem.NameDenyExemptions...)

	baseRoots := []string{canonWorkspace}
	seenBases := map[string]bool{canonWorkspace: true}
	for _, rawBase := range extraBaseDirs {
		if strings.TrimSpace(rawBase) == "" {
			continue
		}
		if cb, cErr := canonicalDirectory(rawBase); cErr == nil && !seenBases[cb] {
			seenBases[cb] = true
			baseRoots = append(baseRoots, cb)
		}
	}

	if len(norm.WritablePaths) > 0 {
		writableRules := make([]PathRule, 0, len(norm.WritablePaths)*2)
		for _, rel := range norm.WritablePaths {
			lexical := filepath.Join(canonWorkspace, filepath.FromSlash(rel))
			writableRules = appendUniqueRule(writableRules, PathRule{Path: lexical, Kind: PathSubtree})
			resolved := resolvePathSymlinks(lexical)
			if resolved != lexical && isPathWithinDir(canonWorkspace, resolved) {
				writableRules = appendUniqueRule(writableRules, PathRule{Path: resolved, Kind: PathSubtree})
			}
		}
		narrowed := make([]PathRule, 0, len(out.Filesystem.AllowWrite)+len(writableRules))
		added := false
		appendWritableTargets := func() {
			if added {
				return
			}
			added = true
			for _, rule := range writableRules {
				narrowed = appendUniqueRule(narrowed, rule)
			}
		}
		for _, rule := range out.Filesystem.AllowWrite {
			if rule.Kind == PathSubtree && filepath.Clean(rule.Path) == canonWorkspace {
				appendWritableTargets()
				continue
			}
			narrowed = appendUniqueRule(narrowed, rule)
		}
		if !added {
			appendWritableTargets()
		}
		if len(narrowed) == 0 {
			return Profile{}, errors.New("writable path scope left no writable directories")
		}
		out.Filesystem.AllowWrite = narrowed

		// Explicitly restrict writes inside canonWorkspace (and extra base roots)
		// to writableRules even when an ancestor such as /private/tmp or
		// /private/var/folders appears in AllowWrite.
		for _, base := range baseRoots {
			allowedInBase := append([]PathRule(nil), writableRules...)
			for _, exempt := range out.Filesystem.NameDenyExemptions {
				cleanExempt := filepath.Clean(exempt.Path)
				if cleanExempt != base && isPathWithinDir(base, cleanExempt) {
					allowedInBase = appendUniqueRule(allowedInBase, exempt)
				}
			}
			out.Filesystem.WriteRestrictions = append(out.Filesystem.WriteRestrictions, WriteScopeRestriction{
				Root:       PathRule{Path: base, Kind: PathSubtree},
				AllowWrite: allowedInBase,
			})
		}
	}

	if len(norm.ReadOnlyPaths) > 0 {
		for _, base := range baseRoots {
			for _, rel := range norm.ReadOnlyPaths {
				lexical := filepath.Join(base, filepath.FromSlash(rel))
				out.Filesystem.DenyWrite = appendUniqueRule(out.Filesystem.DenyWrite, PathRule{Path: lexical, Kind: PathSubtree})
				out.Filesystem.DenyExec = appendUniqueRule(out.Filesystem.DenyExec, PathRule{Path: lexical, Kind: PathSubtree})
				resolved := resolvePathSymlinks(lexical)
				if resolved != lexical && isPathWithinDir(base, resolved) {
					out.Filesystem.DenyWrite = appendUniqueRule(out.Filesystem.DenyWrite, PathRule{Path: resolved, Kind: PathSubtree})
					out.Filesystem.DenyExec = appendUniqueRule(out.Filesystem.DenyExec, PathRule{Path: resolved, Kind: PathSubtree})
				}
			}
		}
	}

	return out, nil
}
