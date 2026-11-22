package taskledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	TaskLedgerFile           = "task-ledger.json"
	TaskLedgerVersion        = 2
	MaxPersistedReceipts     = 256
	MaxLedgerPromptBytes     = 64 << 10
	MaxAutoRestartRecoveries = 2
)

// PromptReceipt is returned by POST .../prompt and .../prompt_async so callers
// can correlate node-assigned instruction/message IDs and detect deduplicated retries.
type PromptReceipt struct {
	OK              bool   `json:"ok"`
	InstructionID   int64  `json:"instructionId,omitempty"`
	MessageID       string `json:"messageId,omitempty"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
	Deduplicated    bool   `json:"deduplicated,omitempty"`
}

// InflightTurnEntry records one active conversation turn so a graceful Hoxi
// update or restart can resume the turn automatically, and an unexpected crash
// can settle dangling in-flight tool states on the next boot.
type InflightTurnEntry struct {
	SessionID       string `json:"sessionID"`
	Runtime         string `json:"runtime"`
	ExternalID      string `json:"externalID,omitempty"`
	UserMsgID       string `json:"userMsgID"`
	AsstMsgID       string `json:"asstMsgID,omitempty"`
	Mode            string `json:"mode,omitempty"`
	ProviderID      string `json:"providerID,omitempty"`
	ModelID         string `json:"modelID,omitempty"`
	Effort          string `json:"effort,omitempty"`
	FastMode        bool   `json:"fastMode,omitempty"`
	CallerSystem    string `json:"callerSystem,omitempty"`
	PromptText      string `json:"promptText,omitempty"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
	StartedAt       int64  `json:"startedAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	GracefulRestart bool   `json:"gracefulRestart,omitempty"`
	RecoveryCount   int    `json:"recoveryCount,omitempty"`
}

type persistedPromptReceipt struct {
	SessionID       string        `json:"sessionID"`
	ClientRequestID string        `json:"clientRequestId"`
	Receipt         PromptReceipt `json:"receipt"`
	ExpiresAtMs     int64         `json:"expiresAtMs"`
}

type persistedPermissionReply struct {
	PermissionID    string `json:"permissionID"`
	SessionID       string `json:"sessionID,omitempty"`
	ClientRequestID string `json:"clientRequestId"`
	Reply           string `json:"reply"`
	RawBody         []byte `json:"rawBody,omitempty"`
	ExpiresAtMs     int64  `json:"expiresAtMs"`
}

type Document = taskLedgerDoc

type taskLedgerDoc struct {
	Version           int                        `json:"version"`
	Entries           []InflightTurnEntry        `json:"entries"`
	Requests          []PersistedRequestRecord   `json:"requests,omitempty"`
	PromptReceipts    []persistedPromptReceipt   `json:"promptReceipts,omitempty"`
	PermissionReplies []persistedPermissionReply `json:"permissionReplies,omitempty"`
}

type taskLedgerSnapshot struct {
	seq uint64
	doc taskLedgerDoc
}

type Store struct {
	mu         sync.Mutex
	writeMu    sync.Mutex
	seq        uint64
	writtenSeq uint64
	path       string
	loadErr    error
	entries    map[string]InflightTurnEntry
	requests   map[string]PersistedRequestRecord
}

func NewStore(customStorePath string) *Store {
	path := hoxipaths.TaskLedgerPath()
	if trimmed := strings.TrimSpace(customStorePath); trimmed != "" {
		path = filepath.Join(filepath.Dir(filepath.Clean(trimmed)), TaskLedgerFile)
	}
	s := &Store{
		path:     path,
		entries:  make(map[string]InflightTurnEntry),
		requests: make(map[string]PersistedRequestRecord),
	}
	s.load()
	return s
}

func (s *Store) LoadErr() error {
	if s == nil {
		return nil
	}
	return s.loadErr
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

func (s *Store) SetPathForTest(path string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.path = path
	s.mu.Unlock()
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func cloneStrMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func (s *Store) load() {
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		s.loadErr = fmt.Errorf("read task ledger %s: %w", s.path, err)
		return
	}
	var doc taskLedgerDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		if renameErr := hoxipaths.WriteFileAtomic(backup, raw, 0o600); renameErr != nil {
			s.loadErr = fmt.Errorf("invalid task ledger %s (backup failed: %v): %w", s.path, renameErr, err)
		} else {
			s.loadErr = fmt.Errorf("invalid task ledger %s; preserved as %s: %w", s.path, backup, err)
		}
		return
	}
	if doc.Version != 1 && doc.Version != TaskLedgerVersion {
		backup := fmt.Sprintf("%s.unsupported-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		if renameErr := hoxipaths.WriteFileAtomic(backup, raw, 0o600); renameErr != nil {
			s.loadErr = fmt.Errorf("unsupported task ledger version %d (backup failed: %v)", doc.Version, renameErr)
		} else {
			s.loadErr = fmt.Errorf("unsupported task ledger version %d; preserved as %s", doc.Version, backup)
		}
		return
	}
	nowMs := time.Now().UnixMilli()
	for _, e := range doc.Entries {
		sid := strings.TrimSpace(e.SessionID)
		if sid == "" {
			continue
		}
		e.SessionID = sid
		s.entries[sid] = e
	}
	for _, r := range doc.PromptReceipts {
		sid := strings.TrimSpace(r.SessionID)
		cid := strings.TrimSpace(r.ClientRequestID)
		if sid == "" || cid == "" || r.ExpiresAtMs <= nowMs {
			continue
		}
		rawResp, err := json.Marshal(r.Receipt)
		if err != nil {
			continue
		}
		key := requestLedgerKey(RequestKindPrompt, sid, cid)
		s.requests[key] = PersistedRequestRecord{
			Kind:            RequestKindPrompt,
			Scope:           sid,
			SessionID:       sid,
			ClientRequestID: cid,
			Status:          RequestStatusCompleted,
			HTTPStatus:      http.StatusOK,
			ResponseBody:    rawResp,
			Meta:            cloneStrMap(map[string]string{"userMsgID": r.Receipt.MessageID}),
			CreatedAtMs:     nowMs,
			UpdatedAtMs:     nowMs,
			ExpiresAtMs:     r.ExpiresAtMs,
		}
	}
	for _, p := range doc.PermissionReplies {
		pid := strings.TrimSpace(p.PermissionID)
		cid := strings.TrimSpace(p.ClientRequestID)
		if pid == "" || cid == "" || p.ExpiresAtMs <= nowMs {
			continue
		}
		key := requestLedgerKey(RequestKindPermissionReply, pid, cid)
		s.requests[key] = PersistedRequestRecord{
			Kind:            RequestKindPermissionReply,
			Scope:           pid,
			SessionID:       strings.TrimSpace(p.SessionID),
			ClientRequestID: cid,
			Status:          RequestStatusCompleted,
			HTTPStatus:      http.StatusOK,
			ResponseBody:    append([]byte(nil), p.RawBody...),
			Meta:            cloneStrMap(map[string]string{"reply": p.Reply}),
			CreatedAtMs:     nowMs,
			UpdatedAtMs:     nowMs,
			ExpiresAtMs:     p.ExpiresAtMs,
		}
	}
	for _, req := range doc.Requests {
		kind := strings.TrimSpace(req.Kind)
		scope := strings.TrimSpace(req.Scope)
		cid := strings.TrimSpace(req.ClientRequestID)
		if kind == "" || scope == "" || cid == "" {
			continue
		}
		if req.Status != RequestStatusAdmitted && req.ExpiresAtMs > 0 && req.ExpiresAtMs <= nowMs {
			continue
		}
		req.Kind = kind
		req.Scope = scope
		req.ClientRequestID = cid
		req.ResponseBody = append([]byte(nil), req.ResponseBody...)
		req.Meta = cloneStrMap(req.Meta)
		s.requests[requestLedgerKey(kind, scope, cid)] = req
	}
}

func (s *Store) snapshotLocked(nowMs int64) taskLedgerSnapshot {
	s.seq++
	entries := make([]InflightTurnEntry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].StartedAt == entries[j].StartedAt {
			return entries[i].SessionID < entries[j].SessionID
		}
		return entries[i].StartedAt < entries[j].StartedAt
	})

	admittedReqs := make([]PersistedRequestRecord, 0)
	terminalReqs := make([]PersistedRequestRecord, 0, len(s.requests))
	for k, r := range s.requests {
		if r.Status != RequestStatusAdmitted && r.ExpiresAtMs > 0 && r.ExpiresAtMs <= nowMs {
			delete(s.requests, k)
			continue
		}
		cloned := r
		cloned.ResponseBody = append([]byte(nil), r.ResponseBody...)
		cloned.Meta = cloneStrMap(r.Meta)
		if r.Status == RequestStatusAdmitted {
			admittedReqs = append(admittedReqs, cloned)
		} else {
			terminalReqs = append(terminalReqs, cloned)
		}
	}
	sort.Slice(admittedReqs, func(i, j int) bool {
		if admittedReqs[i].UpdatedAtMs == admittedReqs[j].UpdatedAtMs {
			return admittedReqs[i].ClientRequestID < admittedReqs[j].ClientRequestID
		}
		return admittedReqs[i].UpdatedAtMs > admittedReqs[j].UpdatedAtMs
	})
	sort.Slice(terminalReqs, func(i, j int) bool {
		if terminalReqs[i].UpdatedAtMs == terminalReqs[j].UpdatedAtMs {
			return terminalReqs[i].ClientRequestID < terminalReqs[j].ClientRequestID
		}
		return terminalReqs[i].UpdatedAtMs > terminalReqs[j].UpdatedAtMs
	})
	if len(terminalReqs) > MaxPersistedRequests {
		for _, drop := range terminalReqs[MaxPersistedRequests:] {
			delete(s.requests, requestLedgerKey(drop.Kind, drop.Scope, drop.ClientRequestID))
		}
		terminalReqs = terminalReqs[:MaxPersistedRequests]
	}
	requests := make([]PersistedRequestRecord, 0, len(admittedReqs)+len(terminalReqs))
	requests = append(requests, admittedReqs...)
	requests = append(requests, terminalReqs...)

	return taskLedgerSnapshot{
		seq: s.seq,
		doc: taskLedgerDoc{
			Version:  TaskLedgerVersion,
			Entries:  entries,
			Requests: requests,
		},
	}
}

func (s *Store) persist(snap taskLedgerSnapshot) error {
	if s == nil {
		return errors.New("task ledger storage is unavailable")
	}
	s.mu.Lock()
	path := s.path
	loadErr := s.loadErr
	s.mu.Unlock()
	if path == "" {
		return errors.New("task ledger storage is unavailable")
	}
	if loadErr != nil {
		return loadErr
	}
	raw, err := json.MarshalIndent(snap.doc, "", "  ")
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if snap.seq <= s.writtenSeq {
		return nil
	}
	if err := hoxipaths.WriteFileAtomic(path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	s.writtenSeq = snap.seq
	return nil
}

func (s *Store) ListEntries() []InflightTurnEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InflightTurnEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	return out
}

func (s *Store) PutEntry(e InflightTurnEntry) {
	if s == nil {
		return
	}
	e.SessionID = strings.TrimSpace(e.SessionID)
	if e.SessionID == "" {
		return
	}
	if len(e.PromptText) > MaxLedgerPromptBytes {
		e.PromptText = truncateUTF8(e.PromptText, MaxLedgerPromptBytes)
	}
	nowMs := time.Now().UnixMilli()
	if e.StartedAt <= 0 {
		e.StartedAt = nowMs
	}
	if e.UpdatedAt <= 0 {
		e.UpdatedAt = nowMs
	}
	s.mu.Lock()
	s.entries[e.SessionID] = e
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) UpdateExternalID(sessionID, externalID string) {
	if s == nil {
		return
	}
	sid := strings.TrimSpace(sessionID)
	ext := strings.TrimSpace(externalID)
	if sid == "" || ext == "" {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	e, ok := s.entries[sid]
	if !ok || e.ExternalID == ext {
		s.mu.Unlock()
		return
	}
	e.ExternalID = ext
	e.UpdatedAt = nowMs
	s.entries[sid] = e
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) UpdateSteer(sessionID, userMsgID, mode, providerID, modelID, effort, promptText string) {
	if s == nil {
		return
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	e, ok := s.entries[sid]
	if !ok {
		s.mu.Unlock()
		return
	}
	if strings.TrimSpace(userMsgID) != "" {
		e.UserMsgID = strings.TrimSpace(userMsgID)
	}
	if strings.TrimSpace(mode) != "" {
		e.Mode = strings.TrimSpace(mode)
	}
	if strings.TrimSpace(providerID) != "" {
		e.ProviderID = strings.TrimSpace(providerID)
	}
	if strings.TrimSpace(modelID) != "" {
		e.ModelID = strings.TrimSpace(modelID)
	}
	e.Effort = strings.TrimSpace(effort)
	if strings.TrimSpace(promptText) != "" {
		txt := promptText
		if len(txt) > MaxLedgerPromptBytes {
			txt = truncateUTF8(txt, MaxLedgerPromptBytes)
		}
		e.PromptText = txt
	}
	e.UpdatedAt = nowMs
	s.entries[sid] = e
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) DeleteEntry(sessionID string) {
	s.DeleteEntryForUserMsg(sessionID, "")
}

func (s *Store) DeleteEntryForUserMsg(sessionID, userMsgID string) {
	if s == nil {
		return
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	existing, exists := s.entries[sid]
	if !exists {
		s.mu.Unlock()
		return
	}
	if uid := strings.TrimSpace(userMsgID); uid != "" && existing.UserMsgID != "" && existing.UserMsgID != uid {
		s.mu.Unlock()
		return
	}
	delete(s.entries, sid)
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) MarkGracefulRestart(externalBySession map[string]string) {
	if s == nil || len(externalBySession) == 0 {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	changed := false
	for sid, extID := range externalBySession {
		e, ok := s.entries[sid]
		if !ok {
			continue
		}
		if ext := strings.TrimSpace(extID); ext != "" {
			e.ExternalID = ext
		}
		e.GracefulRestart = true
		e.UpdatedAt = nowMs
		s.entries[sid] = e
		changed = true
	}
	if !changed {
		s.mu.Unlock()
		return
	}
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}

func (s *Store) PutPromptReceipt(sessionID, clientRequestID string, receipt PromptReceipt, _ time.Time) {
	if s == nil {
		return
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return
	}
	s.CompleteRequest(RequestKindPrompt, sessionID, sessionID, clientRequestID, "", http.StatusOK, raw, map[string]string{"userMsgID": receipt.MessageID})
}

func (s *Store) PutPermissionReply(permissionID, sessionID, clientRequestID, reply string, rawBody []byte, _ time.Time) {
	if s == nil {
		return
	}
	s.CompleteRequest(RequestKindPermissionReply, permissionID, sessionID, clientRequestID, "", http.StatusOK, rawBody, map[string]string{"reply": reply})
}

func (s *Store) ForgetSessions(seen map[string]bool) {
	if s == nil || len(seen) == 0 {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	changed := false
	for sid := range seen {
		if _, ok := s.entries[sid]; ok {
			delete(s.entries, sid)
			changed = true
		}
	}
	for k, req := range s.requests {
		if (req.SessionID != "" && seen[req.SessionID]) || seen[req.Scope] {
			delete(s.requests, k)
			changed = true
		}
	}
	if !changed {
		s.mu.Unlock()
		return
	}
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	_ = s.persist(snap)
}
