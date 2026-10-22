package runner

import (
	"sort"
	"strings"
	"time"
)

// Session class grants back the approval card's "本会话内此类都允许" button.
// Go decides what "this kind" means: the recoverable classes of
// approval_trusted.go that a session may grant (sessionGrantableClasses).
// A destructive request outside them (secret reads, sudo, force pushes,
// process control, git reset/clean, wiping the session root, anything that
// cannot be parsed) gets no grant and keeps asking once per request.
// Grants live in memory for one session, are listed and revocable with the
// other session grants, and every use is audited with source "grant".

const (
	grantSourceSession = "grant"
	grantKindClass     = "class"
	// grantClassesMetaKey carries the classes a docked request could grant,
	// so the card only offers the button when Go would honour it.
	grantClassesMetaKey = "hoxiGrantClasses"
	maxOpenCodeAsks     = 512
)

// openCodeAsk is an OpenCode request shown to the user, kept so a grant reply
// is judged against the exact request instead of anything the client sends.
type openCodeAsk struct {
	item      map[string]interface{}
	sessionID string
	dir       string
	at        time.Time
}

func isShellPermissionItem(item map[string]interface{}) bool {
	perm := permissionName(item)
	if perm == "bash" {
		return true
	}
	return perm == "tool" && bashToolNames[strings.ToLower(firstString(itemMetadata(item)["tool"]))]
}

// grantableRequestClasses returns the sorted classes a destructive shell
// request needs within dir, or nil when any part of it falls outside the
// grantable classes.
func grantableRequestClasses(item map[string]interface{}, dir string) []string {
	if strings.TrimSpace(dir) == "" || !isShellPermissionItem(item) {
		return nil
	}
	meta := itemMetadata(item)
	if destructive, _ := meta["destructive"].(bool); destructive {
		return nil
	}
	if external, _ := meta["externalDirectory"].(bool); external {
		return nil
	}
	candidates := commandCandidates(item)
	if len(candidates) == 0 {
		return nil
	}
	classes := map[string]bool{}
	for _, cmd := range candidates {
		if !recoverableCommandClasses(cmd, dir, classes) {
			return nil
		}
	}
	if len(classes) == 0 {
		return nil
	}
	out := make([]string, 0, len(classes))
	for class := range classes {
		if !sessionGrantableClasses[class] {
			return nil
		}
		out = append(out, class)
	}
	sort.Strings(out)
	return out
}

// annotateGrantClasses tells the approval card which classes a grant reply
// would cover; requests outside them carry no annotation and no button.
func annotateGrantClasses(item map[string]interface{}, dir string) {
	classes := grantableRequestClasses(item, dir)
	if len(classes) == 0 {
		return
	}
	meta, _ := item["metadata"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
		item["metadata"] = meta
	}
	meta[grantClassesMetaKey] = classes
}

// classGrantApproves reports whether earlier session grants cover every class
// this request needs.
func (s *Service) classGrantApproves(item map[string]interface{}, sessionID, dir string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	classes := grantableRequestClasses(item, dir)
	return len(classes) > 0 && s.gate.hasClassGrants(sessionID, classes)
}

func (g *approvalGate) addClassGrants(sessionID string, classes []string, source string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.classGrants[sessionID] == nil {
		g.classGrants[sessionID] = make(map[string]ApprovalGrant)
	}
	now := time.Now().UnixMilli()
	for _, class := range classes {
		if !sessionGrantableClasses[class] {
			continue
		}
		g.classGrants[sessionID][class] = ApprovalGrant{
			ID:        makeGrantID(sessionID, grantKindClass, class),
			SessionID: sessionID,
			Kind:      grantKindClass,
			Tool:      "bash",
			Key:       class,
			Pattern:   class,
			Scope:     "session_memory",
			Source:    firstNonEmptyStr(source, "desktop"),
			CreatedAt: now,
		}
	}
}

func (g *approvalGate) hasClassGrants(sessionID string, classes []string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	granted := g.classGrants[sessionID]
	for _, class := range classes {
		if _, ok := granted[class]; !ok {
			return false
		}
	}
	return len(classes) > 0
}

func (g *approvalGate) rememberOpenCodeAsk(id string, ask openCodeAsk) {
	if id == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.openCodeAsks) >= maxOpenCodeAsks {
		oldestID, oldest := "", time.Time{}
		for k, v := range g.openCodeAsks {
			if oldestID == "" || v.at.Before(oldest) {
				oldestID, oldest = k, v.at
			}
		}
		delete(g.openCodeAsks, oldestID)
	}
	ask.at = time.Now()
	g.openCodeAsks[id] = ask
}

func (g *approvalGate) openCodeAskFor(id string) (openCodeAsk, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ask, ok := g.openCodeAsks[id]
	return ask, ok
}

func (g *approvalGate) forgetOpenCodeAsk(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.openCodeAsks, id)
}

// grantClassesForReply validates a grant reply: only the desktop may grant,
// only together with a one-time approval, and only for a request whose
// classes are all grantable. It returns the classes to grant.
func grantClassesForReply(source, reply string, item map[string]interface{}, dir string) ([]string, string) {
	if isRemoteApprovalSource(source) {
		return nil, "session grants are not allowed over a remote connection"
	}
	if reply != "once" {
		return nil, "a session grant must accompany reply \"once\""
	}
	classes := grantableRequestClasses(item, dir)
	if len(classes) == 0 {
		return nil, "this request cannot be allowed for the whole session"
	}
	return classes, ""
}
