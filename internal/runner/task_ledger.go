package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	taskLedgerFile           = "task-ledger.json"
	taskLedgerVersion        = 1
	maxPersistedReceipts     = 256
	maxLedgerPromptBytes     = 64 << 10
	maxAutoRestartRecoveries = 2
)

var (
	// updateStepDrainTimeout bounds how long PrepareForRestart waits for an
	// actively running tool step to finish cleanly before stopping child processes.
	updateStepDrainTimeout = 12 * time.Second
	// stepDrainPollInterval is the polling cadence while draining active tool steps.
	stepDrainPollInterval = 50 * time.Millisecond
	// restartResumeMaxAge bounds how old a graceful-restart checkpoint may be
	// and still auto-resume on the next Hoxi launch.
	restartResumeMaxAge = 30 * time.Minute
)

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

type taskLedgerDoc struct {
	Version           int                        `json:"version"`
	Entries           []InflightTurnEntry        `json:"entries"`
	PromptReceipts    []persistedPromptReceipt   `json:"promptReceipts,omitempty"`
	PermissionReplies []persistedPermissionReply `json:"permissionReplies,omitempty"`
}

type taskLedgerSnapshot struct {
	seq uint64
	doc taskLedgerDoc
}

type taskLedgerStore struct {
	mu             sync.Mutex
	writeMu        sync.Mutex
	seq            uint64
	writtenSeq     uint64
	path           string
	entries        map[string]InflightTurnEntry
	promptReceipts map[string]persistedPromptReceipt
	permReplies    map[string]persistedPermissionReply
}

func newTaskLedgerStore(customStorePath string) *taskLedgerStore {
	path := hoxipaths.TaskLedgerPath()
	if trimmed := strings.TrimSpace(customStorePath); trimmed != "" {
		path = filepath.Join(filepath.Dir(filepath.Clean(trimmed)), taskLedgerFile)
	}
	s := &taskLedgerStore{
		path:           path,
		entries:        make(map[string]InflightTurnEntry),
		promptReceipts: make(map[string]persistedPromptReceipt),
		permReplies:    make(map[string]persistedPermissionReply),
	}
	s.load()
	return s
}

func (s *taskLedgerStore) load() {
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return
	}
	var doc taskLedgerDoc
	if json.Unmarshal(raw, &doc) != nil || doc.Version != taskLedgerVersion {
		backup := fmt.Sprintf("%s.corrupt-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
		_ = os.Rename(s.path, backup)
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
		s.promptReceipts[sid+"\x00"+cid] = r
	}
	for _, p := range doc.PermissionReplies {
		pid := strings.TrimSpace(p.PermissionID)
		cid := strings.TrimSpace(p.ClientRequestID)
		if pid == "" || cid == "" || p.ExpiresAtMs <= nowMs {
			continue
		}
		p.RawBody = append([]byte(nil), p.RawBody...)
		s.permReplies[pid+"\x00"+cid] = p
	}
}

func (s *taskLedgerStore) snapshotLocked(nowMs int64) taskLedgerSnapshot {
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

	prompts := make([]persistedPromptReceipt, 0, len(s.promptReceipts))
	for k, r := range s.promptReceipts {
		if r.ExpiresAtMs <= nowMs {
			delete(s.promptReceipts, k)
			continue
		}
		prompts = append(prompts, r)
	}
	sort.Slice(prompts, func(i, j int) bool { return prompts[i].ExpiresAtMs > prompts[j].ExpiresAtMs })
	if len(prompts) > maxPersistedReceipts {
		for _, drop := range prompts[maxPersistedReceipts:] {
			delete(s.promptReceipts, drop.SessionID+"\x00"+drop.ClientRequestID)
		}
		prompts = prompts[:maxPersistedReceipts]
	}

	perms := make([]persistedPermissionReply, 0, len(s.permReplies))
	for k, p := range s.permReplies {
		if p.ExpiresAtMs <= nowMs {
			delete(s.permReplies, k)
			continue
		}
		perms = append(perms, p)
	}
	sort.Slice(perms, func(i, j int) bool { return perms[i].ExpiresAtMs > perms[j].ExpiresAtMs })
	if len(perms) > maxPersistedReceipts {
		for _, drop := range perms[maxPersistedReceipts:] {
			delete(s.permReplies, drop.PermissionID+"\x00"+drop.ClientRequestID)
		}
		perms = perms[:maxPersistedReceipts]
	}

	return taskLedgerSnapshot{
		seq: s.seq,
		doc: taskLedgerDoc{
			Version:           taskLedgerVersion,
			Entries:           entries,
			PromptReceipts:    prompts,
			PermissionReplies: perms,
		},
	}
}

func (s *taskLedgerStore) persist(snap taskLedgerSnapshot) {
	if s == nil || s.path == "" {
		return
	}
	raw, err := json.MarshalIndent(snap.doc, "", "  ")
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if snap.seq <= s.writtenSeq {
		return
	}
	if err := hoxipaths.WriteFileAtomic(s.path, append(raw, '\n'), 0o600); err == nil {
		s.writtenSeq = snap.seq
	}
}

func (s *taskLedgerStore) listEntries() []InflightTurnEntry {
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

func (s *taskLedgerStore) putEntry(e InflightTurnEntry) {
	if s == nil {
		return
	}
	e.SessionID = strings.TrimSpace(e.SessionID)
	if e.SessionID == "" {
		return
	}
	if len(e.PromptText) > maxLedgerPromptBytes {
		e.PromptText = truncateUTF8Bytes(e.PromptText, maxLedgerPromptBytes)
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
	s.persist(snap)
}

func (s *taskLedgerStore) updateExternalID(sessionID, externalID string) {
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
	s.persist(snap)
}

func (s *taskLedgerStore) updateSteer(sessionID, userMsgID, mode, providerID, modelID, effort, promptText string) {
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
		if len(txt) > maxLedgerPromptBytes {
			txt = truncateUTF8Bytes(txt, maxLedgerPromptBytes)
		}
		e.PromptText = txt
	}
	e.UpdatedAt = nowMs
	s.entries[sid] = e
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	s.persist(snap)
}

func (s *taskLedgerStore) deleteEntry(sessionID string) {
	s.deleteEntryForUserMsg(sessionID, "")
}

func (s *taskLedgerStore) deleteEntryForUserMsg(sessionID, userMsgID string) {
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
	// When a specific userMsgID is supplied (and the turn was not steered onto a
	// newer message within the same process), never delete a newer turn's entry.
	if uid := strings.TrimSpace(userMsgID); uid != "" && existing.UserMsgID != "" && existing.UserMsgID != uid {
		s.mu.Unlock()
		return
	}
	delete(s.entries, sid)
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	s.persist(snap)
}

func (s *taskLedgerStore) markGracefulRestart(externalBySession map[string]string) {
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
	s.persist(snap)
}

func (s *taskLedgerStore) putPromptReceipt(sessionID, clientRequestID string, receipt PromptReceipt, expiresAt time.Time) {
	if s == nil {
		return
	}
	sid := strings.TrimSpace(sessionID)
	cid := strings.TrimSpace(clientRequestID)
	if sid == "" || cid == "" {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	s.promptReceipts[sid+"\x00"+cid] = persistedPromptReceipt{
		SessionID:       sid,
		ClientRequestID: cid,
		Receipt:         receipt,
		ExpiresAtMs:     expiresAt.UnixMilli(),
	}
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	s.persist(snap)
}

func (s *taskLedgerStore) putPermissionReply(permissionID, sessionID, clientRequestID, reply string, rawBody []byte, expiresAt time.Time) {
	if s == nil {
		return
	}
	pid := strings.TrimSpace(permissionID)
	cid := strings.TrimSpace(clientRequestID)
	if pid == "" || cid == "" {
		return
	}
	nowMs := time.Now().UnixMilli()
	s.mu.Lock()
	s.permReplies[pid+"\x00"+cid] = persistedPermissionReply{
		PermissionID:    pid,
		SessionID:       strings.TrimSpace(sessionID),
		ClientRequestID: cid,
		Reply:           reply,
		RawBody:         append([]byte(nil), rawBody...),
		ExpiresAtMs:     expiresAt.UnixMilli(),
	}
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	s.persist(snap)
}

func (s *taskLedgerStore) forgetSessions(seen map[string]bool) {
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
	for k, r := range s.promptReceipts {
		if seen[r.SessionID] {
			delete(s.promptReceipts, k)
			changed = true
		}
	}
	for k, p := range s.permReplies {
		if p.SessionID != "" && seen[p.SessionID] {
			delete(s.permReplies, k)
			changed = true
		}
	}
	if !changed {
		s.mu.Unlock()
		return
	}
	snap := s.snapshotLocked(nowMs)
	s.mu.Unlock()
	s.persist(snap)
}

func (s *Service) hydrateReceiptsFromLedger() {
	if s == nil || s.ledger == nil {
		return
	}
	now := time.Now()
	s.ledger.mu.Lock()
	prompts := make([]persistedPromptReceipt, 0, len(s.ledger.promptReceipts))
	for _, r := range s.ledger.promptReceipts {
		prompts = append(prompts, r)
	}
	perms := make([]persistedPermissionReply, 0, len(s.ledger.permReplies))
	for _, p := range s.ledger.permReplies {
		perms = append(perms, p)
	}
	s.ledger.mu.Unlock()

	for _, r := range prompts {
		exp := time.UnixMilli(r.ExpiresAtMs)
		if now.Before(exp) {
			s.promptReceipts.Store(r.SessionID+"\x00"+r.ClientRequestID, cachedPromptReceipt{
				receipt:   r.Receipt,
				expiresAt: exp,
			})
		}
	}
	for _, p := range perms {
		exp := time.UnixMilli(p.ExpiresAtMs)
		if now.Before(exp) {
			s.permissionReplies.Store(p.PermissionID+"\x00"+p.ClientRequestID, cachedPermissionReply{
				reply:     p.Reply,
				rawBody:   append([]byte(nil), p.RawBody...),
				expiresAt: exp,
			})
		}
	}
}

func (s *Service) hasInFlightRunningToolPart(sessionID string) bool {
	if s == nil || sessionID == "" {
		return false
	}
	running := false
	s.store.VisitTurns(sessionID, func(rec SessionRecord) {
		for i := len(rec.Turns) - 1; i >= 0; i-- {
			t := rec.Turns[i]
			if t.Info.Role == "user" && !isInternalRecoveryMessageID(t.Info.ID) {
				break
			}
			for _, p := range t.Parts {
				if p.Type == "tool" && p.State != nil && p.State.Status == "running" {
					running = true
					return
				}
			}
		}
	})
	return running
}

// WaitForToolStepDrain waits up to maxWait for any actively executing tool call
// (excluding sessions paused on user approval/questions) to finish its current
// step before an update or restart stops child processes.
func (s *Service) WaitForToolStepDrain(ctx context.Context, maxWait time.Duration) bool {
	if s == nil || maxWait <= 0 {
		return !s.anyActiveToolStepRunning()
	}
	if !s.anyActiveToolStepRunning() {
		return true
	}
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(stepDrainPollInterval)
	defer ticker.Stop()
	for {
		if !s.anyActiveToolStepRunning() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return !s.anyActiveToolStepRunning()
		case <-ticker.C:
		}
	}
}

func (s *Service) anyActiveToolStepRunning() bool {
	s.mu.Lock()
	active := make([]*activeProcess, 0, len(s.procs))
	for _, ap := range s.procs {
		if ap != nil && !ap.wasAborted() && !ap.hasPendingError() {
			active = append(active, ap)
		}
	}
	s.mu.Unlock()
	for _, ap := range active {
		if s.awaitingUser(ap) {
			continue
		}
		if s.hasInFlightRunningToolPart(ap.sessionID) || s.hasRunningToolSubprocess(ap) {
			return true
		}
	}
	return false
}

// PrepareForRestart drains any fast in-flight tool step, records a graceful
// restart checkpoint for every still-running or boot-resuming turn on disk,
// and cleanly stops child processes while preserving their ledger checkpoints.
func (s *Service) PrepareForRestart(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.WaitForToolStepDrain(ctx, updateStepDrainTimeout)

	extBySession := make(map[string]string)

	// Any session still waiting on boot-time engine startup keeps its checkpoint
	// for the next launch instead of failing closed when cancelled.
	s.pendingResumes.Range(func(sid string, e InflightTurnEntry) bool {
		extBySession[sid] = e.ExternalID
		return true
	})
	s.resumingFlights.Range(func(sid string, flight *resumeFlight) bool {
		if flight != nil {
			flight.markRestarting()
			flight.cancel()
		}
		if rec, ok := s.store.Meta(sid); ok {
			extBySession[sid] = rec.ExternalID
		} else {
			extBySession[sid] = ""
		}
		return true
	})

	s.mu.Lock()
	for _, ap := range s.procs {
		if ap != nil && !ap.wasAborted() && !ap.hasPendingError() {
			ap.markRestarting()
			extID := ""
			if rec, ok := s.store.Meta(ap.sessionID); ok {
				extID = rec.ExternalID
			}
			extBySession[ap.sessionID] = extID
		}
	}
	s.mu.Unlock()

	if len(extBySession) > 0 && s.ledger != nil {
		s.ledger.markGracefulRestart(extBySession)
	}

	s.CloseAll()
}
