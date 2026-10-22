package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	// ApprovalSourceHeader is stamped by the gateway on every request so the
	// approval plane can distinguish local desktop actions from GoPanel tunnel
	// requests without importing internal/gateway.
	ApprovalSourceHeader = "X-Hoxi-Request-Source"

	approvalAuditFile        = "approval-audit.json"
	maxApprovalAuditEntries  = 500
	maxOpenCodePermSnapshots = 256
)

// ApprovalGrant describes one in-memory session-scoped "always" grant.
// Grants live in memory only for the lifetime of the Hoxi process and never
// cover destructive commands.
type ApprovalGrant struct {
	ID           string `json:"id"`
	SessionID    string `json:"sessionID"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	Kind         string `json:"kind"` // "bash" | "tool" | "doom_loop"
	Tool         string `json:"tool"` // "bash" or tool name
	// Key is the internal lookup key (the raw command or a tool+args hash).
	// It is never serialized: it can contain credentials the redacted
	// Pattern hides, and only the gate needs it.
	Key       string `json:"-"`       // internal lookup key
	Pattern   string `json:"pattern"` // redacted human-readable command or args summary
	Scope     string `json:"scope"`   // "session_memory"
	Source    string `json:"source"`  // "desktop"
	CreatedAt int64  `json:"createdAt"`
}

// ApprovalAuditEntry records one completed permission decision on disk.
type ApprovalAuditEntry struct {
	ID           string `json:"id"`
	Timestamp    int64  `json:"timestamp"`
	RequestID    string `json:"requestID"`
	SessionID    string `json:"sessionID"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	Permission   string `json:"permission"`
	Tool         string `json:"tool,omitempty"`
	Summary      string `json:"summary"`
	Directory    string `json:"directory,omitempty"`
	Destructive  bool   `json:"destructive,omitempty"`
	Decision     string `json:"decision"` // "once" | "always" | "reject" | "timeout" | "interrupted"
	Source       string `json:"source"`   // "desktop" | "tunnel" | "lan" | "relay" | "system"
	Message      string `json:"message,omitempty"`
}

type openCodePermSnapshot struct {
	SessionID   string
	Permission  string
	Tool        string
	Summary     string
	Directory   string
	Destructive bool
	SeenAt      int64
}

type approvalAuditFileDoc struct {
	Version int                  `json:"version"`
	Entries []ApprovalAuditEntry `json:"entries"`
}

type approvalAuditStore struct {
	mu             sync.Mutex
	persistMu      sync.Mutex
	path           string
	entries        []ApprovalAuditEntry
	seq            uint64
	persistedSeq   uint64
	persistBlocked error
	ocOrder        []string
	ocSnapshots    map[string]openCodePermSnapshot
}

func newApprovalAuditStore(customStorePath string) *approvalAuditStore {
	path := ""
	if trimmed := strings.TrimSpace(customStorePath); trimmed != "" {
		path = filepath.Join(filepath.Dir(filepath.Clean(trimmed)), approvalAuditFile)
	} else {
		path = hoxipaths.Join(approvalAuditFile)
	}
	s := &approvalAuditStore{
		path:        path,
		entries:     []ApprovalAuditEntry{},
		ocSnapshots: make(map[string]openCodePermSnapshot),
	}
	if seq, snapshot, ok := s.loadFromDisk(); ok {
		// Best effort: the in-memory entries are already clean, and the next
		// record retries the write if this one fails.
		_ = s.persistSnapshot(s.path, seq, snapshot)
	}
	return s
}

// loadFromDisk reads the audit file and re-redacts entries written before the
// current redaction rules. It reports a snapshot to write back when any entry
// changed, so plaintext secrets do not survive on disk.
func (s *approvalAuditStore) loadFromDisk() (uint64, []ApprovalAuditEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return 0, nil, false
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.persistBlocked = fmt.Errorf("approval audit file is unreadable: %w", err)
		}
		return 0, nil, false
	}
	var doc approvalAuditFileDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		if renameErr := os.Rename(s.path, backup); renameErr != nil {
			s.persistBlocked = fmt.Errorf("approval audit file is corrupt and could not be backed up: %w", renameErr)
			return 0, nil, false
		}
		s.entries = []ApprovalAuditEntry{}
		return 0, nil, false
	}
	if len(doc.Entries) > maxApprovalAuditEntries {
		doc.Entries = doc.Entries[:maxApprovalAuditEntries]
	}
	changed := false
	for i := range doc.Entries {
		changed = reRedactApprovalAuditEntry(&doc.Entries[i]) || changed
	}
	s.entries = doc.Entries
	if !changed {
		return 0, nil, false
	}
	s.seq++
	snapshot := make([]ApprovalAuditEntry, len(s.entries))
	copy(snapshot, s.entries)
	return s.seq, snapshot, true
}

// reRedactApprovalAuditEntry applies the current secret rules to a stored
// entry. It does not truncate again: stored fields are already bounded and a
// second "…" would change entries that hold no secret.
func reRedactApprovalAuditEntry(e *ApprovalAuditEntry) bool {
	changed := false
	for _, field := range []*string{&e.Summary, &e.Message, &e.SessionTitle} {
		if clean := diagnostics.RedactSecrets(*field); clean != *field {
			*field = clean
			changed = true
		}
	}
	return changed
}

func (s *approvalAuditStore) record(e ApprovalAuditEntry) {
	if e.Timestamp <= 0 {
		e.Timestamp = time.Now().UnixMilli()
	}
	if strings.TrimSpace(e.ID) == "" {
		e.ID = "aud_" + randomHex(8)
	}
	e.Summary = redactApprovalText(e.Summary, 400)
	e.Message = redactApprovalText(e.Message, 240)
	e.SessionTitle = redactApprovalText(e.SessionTitle, 120)
	if e.Source == "" {
		e.Source = "desktop"
	}

	s.mu.Lock()
	s.entries = append([]ApprovalAuditEntry{e}, s.entries...)
	if len(s.entries) > maxApprovalAuditEntries {
		s.entries = s.entries[:maxApprovalAuditEntries]
	}
	s.seq++
	seq := s.seq
	snapshot := make([]ApprovalAuditEntry, len(s.entries))
	copy(snapshot, s.entries)
	blocked := s.persistBlocked
	path := s.path
	s.mu.Unlock()

	if path == "" || blocked != nil {
		return
	}
	_ = s.persistSnapshot(path, seq, snapshot)
}

func (s *approvalAuditStore) persistSnapshot(path string, seq uint64, snapshot []ApprovalAuditEntry) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if seq <= s.persistedSeq {
		return nil
	}
	raw, err := json.MarshalIndent(approvalAuditFileDoc{
		Version: 1,
		Entries: snapshot,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeApprovalAuditAtomic(path, append(raw, '\n')); err != nil {
		return err
	}
	s.persistedSeq = seq
	return nil
}

func writeApprovalAuditAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func (s *approvalAuditStore) list(sessionID string, limit int) []ApprovalAuditEntry {
	if limit <= 0 || limit > maxApprovalAuditEntries {
		limit = 200
	}
	sid := strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ApprovalAuditEntry, 0, min(len(s.entries), limit))
	for _, e := range s.entries {
		if sid != "" && e.SessionID != sid {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *approvalAuditStore) clear() error {
	s.mu.Lock()
	s.entries = []ApprovalAuditEntry{}
	s.seq++
	seq := s.seq
	blocked := s.persistBlocked
	path := s.path
	s.mu.Unlock()
	if blocked != nil {
		return blocked
	}
	if path == "" {
		return nil
	}
	return s.persistSnapshot(path, seq, []ApprovalAuditEntry{})
}

func (s *approvalAuditStore) rememberOpenCodePerm(item map[string]interface{}, directory string) {
	id, _ := item["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, runnerPermissionID) {
		return
	}
	sid, _ := item["sessionID"].(string)
	perm, _ := item["permission"].(string)
	if perm == "" {
		perm, _ = item["type"].(string)
	}
	summary, tool := summarizePermissionItem(item)
	destructive := isDestructivePermissionRequest(item)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.ocSnapshots[id]; !exists {
		s.ocOrder = append(s.ocOrder, id)
		if len(s.ocOrder) > maxOpenCodePermSnapshots {
			oldest := s.ocOrder[0]
			s.ocOrder = s.ocOrder[1:]
			delete(s.ocSnapshots, oldest)
		}
	}
	s.ocSnapshots[id] = openCodePermSnapshot{
		SessionID:   strings.TrimSpace(sid),
		Permission:  firstNonEmptyStr(strings.TrimSpace(perm), "tool"),
		Tool:        tool,
		Summary:     summary,
		Directory:   strings.TrimSpace(directory),
		Destructive: destructive,
		SeenAt:      time.Now().UnixMilli(),
	}
}

func (s *approvalAuditStore) lookupOpenCodePerm(id string) (openCodePermSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.ocSnapshots[strings.TrimSpace(id)]
	return snap, ok
}

func summarizePermissionItem(item map[string]interface{}) (string, string) {
	meta, _ := item["metadata"].(map[string]interface{})
	tool := firstString(meta["tool"], item["permission"], item["type"])
	if cmd := firstString(meta["command"], meta["cmd"], item["pattern"]); cmd != "" {
		return cmd, firstNonEmptyStr(tool, "bash")
	}
	if args := firstString(meta["args"]); args != "" {
		return firstNonEmptyStr(tool, "tool") + " " + args, tool
	}
	if patterns, ok := item["patterns"].([]string); ok && len(patterns) > 0 {
		return strings.Join(patterns, " "), tool
	}
	if rawPatterns, ok := item["patterns"].([]interface{}); ok && len(rawPatterns) > 0 {
		parts := make([]string, 0, len(rawPatterns))
		for _, p := range rawPatterns {
			if s, ok := p.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " "), tool
		}
	}
	return firstNonEmptyStr(firstString(item["title"], meta["description"]), tool, "permission"), tool
}

func redactApprovalText(v string, limit int) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) > 4096 {
		v = v[:4096]
	}
	v = diagnostics.RedactSecrets(v)
	v = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, v)
	v = strings.Join(strings.Fields(v), " ")
	if limit > 0 && len(v) > limit {
		v = v[:limit]
		for !utf8.ValidString(v) {
			v = v[:len(v)-1]
		}
		v += "…"
	}
	return v
}

func makeGrantID(sessionID, kind, key string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + kind + "\x00" + key))
	return "grt_" + hex.EncodeToString(sum[:8])
}

func requestApprovalSource(r *http.Request) string {
	if r == nil {
		return "desktop"
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get(ApprovalSourceHeader))) {
	case "tunnel":
		return "tunnel"
	case "lan":
		return "lan"
	case "relay":
		return "relay"
	default:
		return "desktop"
	}
}

// isRemoteApprovalSource reports whether an approval originated from a paired
// remote controller (GoPanel tunnel, LAN pairing or a relay ingress) rather
// than the desktop. Remote sources may not change the approval policy, audit or
// grants.
func isRemoteApprovalSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "tunnel", "lan", "relay":
		return true
	default:
		return false
	}
}

func (s *Service) enrichSessionMeta(sessionID string) (title, runtime, dir string) {
	if rec, ok := s.store.Meta(sessionID); ok {
		return rec.Title, rec.Runtime, firstNonEmptyStr(rec.WorktreePath, rec.ProjectDir)
	}
	return "", "", ""
}

func (s *Service) recordRunnerApprovalAudit(cred hookCredential, req map[string]interface{}, decision string, answer approvalReply) {
	if s.audit == nil {
		return
	}
	reqID, _ := req["id"].(string)
	perm, _ := req["permission"].(string)
	summary, tool := summarizePermissionItem(req)
	meta, _ := req["metadata"].(map[string]interface{})
	cwd := firstString(meta["cwd"])
	title, rt, sessionDir := s.enrichSessionMeta(cred.sessionID)
	if rt == "" {
		rt = cred.runtime
	}
	if cwd == "" {
		cwd = sessionDir
	}
	source := answer.Source
	if source == "" {
		if decision == "timeout" || decision == "interrupted" {
			source = "system"
		} else {
			source = "desktop"
		}
	}
	destructive := isDestructivePermissionRequest(req)
	s.audit.record(ApprovalAuditEntry{
		Timestamp:    time.Now().UnixMilli(),
		RequestID:    reqID,
		SessionID:    cred.sessionID,
		SessionTitle: title,
		Runtime:      rt,
		Permission:   firstNonEmptyStr(perm, "tool"),
		Tool:         firstNonEmptyStr(tool, perm),
		Summary:      summary,
		Directory:    cwd,
		Destructive:  destructive,
		Decision:     decision,
		Source:       source,
		Message:      answer.Message,
	})
}

func (s *Service) recordOpenCodeApprovalAudit(id, directory, reply, message, source string) {
	s.recordOpenCodeAudit(id, "", directory, reply, message, source)
}

// recordOpenCodeAudit writes one OpenCode decision. fallbackSessionID covers
// decisions Hoxi makes itself (a stopped session's pending requests) and any
// reply that arrives before the permission snapshot was captured.
func (s *Service) recordOpenCodeAudit(id, fallbackSessionID, directory, reply, message, source string) {
	if s.audit == nil {
		return
	}
	snap, ok := s.audit.lookupOpenCodePerm(id)
	if !ok {
		snap = openCodePermSnapshot{
			SessionID:  strings.TrimSpace(fallbackSessionID),
			Permission: "permission",
			Tool:       "opencode",
			Summary:    id,
			Directory:  strings.TrimSpace(directory),
		}
	}
	title, rt, sessionDir := s.enrichSessionMeta(snap.SessionID)
	if rt == "" {
		rt = "opencode"
	}
	dir := firstNonEmptyStr(directory, snap.Directory, sessionDir)
	s.audit.record(ApprovalAuditEntry{
		Timestamp:    time.Now().UnixMilli(),
		RequestID:    id,
		SessionID:    snap.SessionID,
		SessionTitle: title,
		Runtime:      rt,
		Permission:   firstNonEmptyStr(snap.Permission, "permission"),
		Tool:         firstNonEmptyStr(snap.Tool, snap.Permission, "opencode"),
		Summary:      firstNonEmptyStr(snap.Summary, id),
		Directory:    dir,
		Destructive:  snap.Destructive,
		Decision:     reply,
		Source:       firstNonEmptyStr(source, "desktop"),
		Message:      message,
	})
}

func (s *Service) listApprovalGrants(sessionID, directory string) []ApprovalGrant {
	sidFilter := strings.TrimSpace(sessionID)
	dirFilter := normalizeComparePath(directory)

	raw := s.gate.listGrants()
	raw = append(raw, s.doomLoop.listExemptGrants()...)

	out := make([]ApprovalGrant, 0, len(raw))
	for _, g := range raw {
		if sidFilter != "" && g.SessionID != sidFilter {
			continue
		}
		title, rt, _ := s.enrichSessionMeta(g.SessionID)
		if dirFilter != "" {
			rec, ok := s.store.Meta(g.SessionID)
			if !ok || (normalizeComparePath(rec.ProjectDir) != dirFilter && normalizeComparePath(rec.WorktreePath) != dirFilter) {
				continue
			}
		}
		g.SessionTitle = title
		g.Runtime = rt
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Service) revokeApprovalGrants(id, sessionID string, all bool) int {
	id = strings.TrimSpace(id)
	sessionID = strings.TrimSpace(sessionID)
	revoked := s.gate.revokeGrants(id, sessionID, all)
	revoked += s.doomLoop.revokeExemptGrants(id, sessionID, all)
	return revoked
}

func (s *Service) handleApprovalAudit(w http.ResponseWriter, r *http.Request) {
	if isRemoteApprovalSource(requestApprovalSource(r)) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit := 200
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("sessionID"))
		entries := []ApprovalAuditEntry{}
		if s.audit != nil {
			entries = s.audit.list(sessionID, limit)
			for i := range entries {
				if entries[i].SessionTitle == "" && entries[i].SessionID != "" {
					title, rt, _ := s.enrichSessionMeta(entries[i].SessionID)
					entries[i].SessionTitle = title
					if entries[i].Runtime == "" {
						entries[i].Runtime = rt
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"entries": entries})
	case http.MethodDelete:
		if s.audit != nil {
			if err := s.audit.clear(); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"entries": []ApprovalAuditEntry{}})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or DELETE required")
	}
}

func (s *Service) handleApprovalGrants(w http.ResponseWriter, r *http.Request) {
	if isRemoteApprovalSource(requestApprovalSource(r)) {
		writeJSONError(w, http.StatusForbidden, "route is not available for remote controllers")
		return
	}
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		grants := s.listApprovalGrants(q.Get("sessionID"), q.Get("directory"))
		writeJSON(w, http.StatusOK, map[string]interface{}{"grants": grants})
	case http.MethodDelete:
		id := strings.TrimSpace(q.Get("id"))
		sessionID := strings.TrimSpace(q.Get("sessionID"))
		all := q.Get("all") == "1" || q.Get("all") == "true"
		if id == "" && sessionID == "" && !all {
			writeJSONError(w, http.StatusBadRequest, "id, sessionID, or all=1 is required to revoke grants")
			return
		}
		revoked := s.revokeApprovalGrants(id, sessionID, all)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"revoked": revoked,
			"grants":  s.listApprovalGrants("", ""),
		})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or DELETE required")
	}
}

func (g *approvalGate) listGrants() []ApprovalGrant {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ApprovalGrant
	for _, cmds := range g.always {
		for _, gr := range cmds {
			out = append(out, gr)
		}
	}
	for _, tools := range g.toolAlways {
		for _, gr := range tools {
			out = append(out, gr)
		}
	}
	return out
}

func (g *approvalGate) revokeGrants(id, sessionID string, all bool) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	revoked := 0
	for sid, cmds := range g.always {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for k, gr := range cmds {
			if all || (sessionID != "" && id == "") || (id != "" && gr.ID == id) {
				delete(cmds, k)
				revoked++
			}
		}
		if len(cmds) == 0 {
			delete(g.always, sid)
		}
	}
	for sid, tools := range g.toolAlways {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for k, gr := range tools {
			if all || (sessionID != "" && id == "") || (id != "" && gr.ID == id) {
				delete(tools, k)
				revoked++
			}
		}
		if len(tools) == 0 {
			delete(g.toolAlways, sid)
		}
	}
	return revoked
}
