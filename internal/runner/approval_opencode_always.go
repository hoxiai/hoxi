package runner

import (
	"regexp"
	"strings"
	"time"
)

// OpenCode keeps an "always" reply as allow rules for the request's `always`
// patterns across its whole instance: every session, evaluated after its
// config rules, so they win over "ask" and "deny" alike. Its edit, read and
// fetch tools offer `*`, and a shell command offers its prefix (`git reset *`),
// so one click on an ordinary edit would let later edits of .git/hooks or
// .opencode/plugins, or a later `git reset --hard`, skip Hoxi entirely.
//
// Hoxi therefore never forwards "always". OpenCode runs the request once and
// Hoxi keeps the same patterns as a grant for that session only, which is
// what the card's "本会话始终允许" promises. A grant approves a later request
// of the same permission when its patterns cover every pattern of the request
// (OpenCode's own wildcard rules) and the request is one the card would itself
// offer "always" for: never a destructive request, a protected or secret
// path, or a shell command sessionGrantRefusal refuses.

const (
	grantKindOpenCode   = "opencode"
	openCodeGrantPrefix = "oc:"
)

func openCodeGrantKey(perm, pattern string) string {
	return openCodeGrantPrefix + perm + "\x00" + pattern
}

// openCodeAlwaysRefused reports whether no "always" grant, new or earlier,
// may cover item.
func openCodeAlwaysRefused(item map[string]interface{}) bool {
	return isDestructivePermissionRequest(item) || openCodeSessionGrantRefused(item)
}

// openCodeAlwaysGrant returns the permission and patterns an "always" reply to
// item keeps for the session, or ok false when it keeps none.
func openCodeAlwaysGrant(item map[string]interface{}) (perm string, patterns []string, ok bool) {
	perm = permissionName(item)
	if perm == "" || openCodeAlwaysRefused(item) {
		return "", nil, false
	}
	for _, p := range stringList(item["always"]) {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}
	return perm, patterns, len(patterns) > 0
}

// openCodeWildcardMatch is OpenCode's Wildcard.match: `*` is any run of
// characters (slashes and newlines too), `?` is one character, and a trailing
// " *" also matches nothing, so "git status *" covers "git status".
func openCodeWildcardMatch(s, pattern string) bool {
	s = strings.ReplaceAll(s, "\\", "/")
	var b strings.Builder
	for _, r := range strings.ReplaceAll(pattern, "\\", "/") {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	expr := b.String()
	if strings.HasSuffix(expr, " .*") {
		expr = strings.TrimSuffix(expr, " .*") + "( .*)?"
	}
	re, err := regexp.Compile("(?s)^" + expr + "$")
	return err == nil && re.MatchString(s)
}

// openCodeAlwaysApproves reports whether an earlier "always" reply in this
// session covers item.
func (s *Service) openCodeAlwaysApproves(item map[string]interface{}, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" || openCodeAlwaysRefused(item) {
		return false
	}
	var patterns []string
	for _, p := range append(stringList(item["patterns"]), stringList(item["pattern"])...) {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}
	return s.gate.openCodeAlwaysCovers(sessionID, permissionName(item), patterns)
}

func (g *approvalGate) addOpenCodeAlwaysGrants(sessionID, perm string, patterns []string, source string) {
	if strings.TrimSpace(sessionID) == "" || perm == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.classGrants[sessionID] == nil {
		g.classGrants[sessionID] = make(map[string]ApprovalGrant)
	}
	now := time.Now().UnixMilli()
	for _, pattern := range patterns {
		key := openCodeGrantKey(perm, pattern)
		g.classGrants[sessionID][key] = ApprovalGrant{
			ID:        makeGrantID(sessionID, grantKindOpenCode, key),
			SessionID: sessionID,
			Kind:      grantKindOpenCode,
			Tool:      perm,
			Key:       key,
			Pattern:   redactApprovalText(pattern, 240),
			Scope:     "session_memory",
			Source:    firstNonEmptyStr(source, "desktop"),
			CreatedAt: now,
		}
	}
}

// openCodeAlwaysCovers reports whether the session's "always" grants for perm
// cover every one of patterns.
func (g *approvalGate) openCodeAlwaysCovers(sessionID, perm string, patterns []string) bool {
	if perm == "" || len(patterns) == 0 {
		return false
	}
	g.mu.Lock()
	var granted []string
	for key, gr := range g.classGrants[sessionID] {
		if gr.Kind != grantKindOpenCode || gr.Tool != perm {
			continue
		}
		if _, pattern, ok := strings.Cut(strings.TrimPrefix(key, openCodeGrantPrefix), "\x00"); ok {
			granted = append(granted, pattern)
		}
	}
	g.mu.Unlock()
	for _, p := range patterns {
		covered := false
		for _, rule := range granted {
			if openCodeWildcardMatch(p, rule) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}
