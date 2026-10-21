package runner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// Store is Hoxi's own conversation store. Metadata for every session stays in
// memory; transcripts are read from disk the first time a session is opened.
type Store struct {
	// mu protects the registry and immutable metadata/cache summaries only.
	// Never acquire a session mutex or perform disk I/O while holding mu.
	mu               sync.RWMutex
	cacheMu          sync.Mutex
	dir              string
	records          map[string]*storedSession
	clock            uint64
	cacheMaxSessions int
	cacheMaxBytes    int64
	cachedSessions   int
	cachedBytes      int64
}

type storedSession struct {
	mu               sync.Mutex
	rec              SessionRecord
	meta             SessionRecord        // protected by Store.mu; no transcript references
	cache            transcriptCacheEntry // protected by Store.mu
	turnsLoaded      bool
	hasRevertedTurns bool
	active           bool
	logLines         int
	rev              int64
	metaDirty        bool
	dirty            map[partKey]struct{}
	pending          []turnLogEntry
	index            transcriptIndex
	bodyBytes        int64
}

type partKey struct{ messageID, partID string }

func defaultStoreDir() (dir, legacy string) {
	if custom := strings.TrimSpace(os.Getenv("HOXI_SESSION_STORE_DIR")); custom != "" {
		return filepath.Clean(custom), ""
	}
	if hoxi := hoxipaths.Home(); hoxi != "" {
		return filepath.Join(hoxi, "sessions"), filepath.Join(hoxi, legacyStoreFile)
	}
	return filepath.Join(os.TempDir(), "hoxi-sessions"), ""
}

// NewStore opens the store rooted at customDir, or ~/.hoxi/sessions when empty.
// The default store also imports the legacy ~/.hoxi/runner-sessions.json once.
func NewStore(customDir string) *Store {
	dir, legacy := strings.TrimSpace(customDir), ""
	if dir == "" {
		dir, legacy = defaultStoreDir()
	}
	s := &Store{dir: filepath.Clean(dir), records: make(map[string]*storedSession), cacheMaxSessions: 12, cacheMaxBytes: 32 << 20}
	s.loadAllMeta()
	if legacy != "" {
		s.migrateLegacy(legacy)
	}
	s.trimCache()
	return s
}

func GenerateRunnerSessionID(rt string) string {
	prefix := "ses_agy"
	switch rt {
	case string(RuntimeClaudeCode):
		prefix = "ses_cld"
	case string(RuntimeCodeBuddy):
		prefix = "ses_cbd"
	case string(RuntimeQoder):
		prefix = "ses_qdr"
	case "opencode":
		prefix = "ses_hoxi"
	}
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}

func IsRunnerSessionID(sessionID string) bool {
	s := strings.TrimSpace(sessionID)
	return strings.HasPrefix(s, "ses_agy_") ||
		strings.HasPrefix(s, "ses_cld_") ||
		strings.HasPrefix(s, "ses_cbd_") ||
		strings.HasPrefix(s, "ses_qdr_") ||
		strings.HasPrefix(s, "ses_hoxi_")
}

// Each caller holds only this session's lock, including during disk I/O.
func (s *Store) ensureTurnsLocked(ss *storedSession) {
	if ss.turnsLoaded {
		return
	}
	ss.rec.Turns, ss.logLines = s.readTurns(ss.rec.ID)
	if ss.hasRevertedTurns {
		raw, err := os.ReadFile(filepath.Join(s.sessionDir(ss.rec.ID), sessionMetaFile))
		if err == nil {
			var meta SessionRecord
			if json.Unmarshal(raw, &meta) == nil {
				ss.rec.RevertedTurns = meta.RevertedTurns
			}
		}
	}
	ss.turnsLoaded = true
	ss.index.rebuild(ss.rec.Turns)
	ss.bodyBytes = estimateTurnsBytes(ss.rec.Turns) + estimateTurnsBytes(ss.rec.RevertedTurns)
	ss.rev++
}

func (s *Store) commitLocked(next SessionRecord, ss *storedSession) error {
	if !validStoreID(next.ID) {
		return errInvalidSessionID
	}
	turnsChanged := !ss.turnsLoaded || !reflect.DeepEqual(ss.rec.Turns, next.Turns)
	lines := ss.logLines
	if turnsChanged {
		n, err := s.rewriteTurns(next.ID, next.Turns)
		if err != nil {
			return err
		}
		lines = n
	} else {
		entries := append(append([]turnLogEntry{}, ss.pending...), dirtyEntriesLocked(ss)...)
		if err := s.appendTurnEntries(next.ID, entries); err != nil {
			return err
		}
		lines += len(entries)
	}
	if err := s.writeMeta(next); err != nil {
		if turnsChanged && ss.turnsLoaded {
			_, _ = s.rewriteTurns(next.ID, ss.rec.Turns)
		}
		return err
	}
	ss.rec, ss.turnsLoaded, ss.logLines = next, true, lines
	ss.metaDirty, ss.dirty, ss.pending = false, nil, nil
	ss.hasRevertedTurns = len(next.RevertedTurns) > 0
	ss.index.rebuild(next.Turns)
	ss.bodyBytes = estimateTurnsBytes(next.Turns) + estimateTurnsBytes(next.RevertedTurns)
	ss.rev++
	return nil
}

func (s *Store) Put(rec SessionRecord) error {
	if !validStoreID(rec.ID) {
		return errInvalidSessionID
	}
	ss := s.lockSession(rec.ID, true)
	defer s.finishSession(ss)
	if ss.meta.ID != "" {
		s.ensureTurnsLocked(ss)
	}
	if err := s.commitLocked(cloneRecord(rec), ss); err != nil {
		if ss.meta.ID == "" {
			s.mu.Lock()
			delete(s.records, rec.ID)
			s.mu.Unlock()
		}
		return err
	}
	return nil
}

func (s *Store) Get(sessionID string) (SessionRecord, bool) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return SessionRecord{}, false
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	return cloneRecord(ss.rec), true
}

// MarshalTurns holds a session lock, so a large history response never blocks
// streaming writes or metadata reads for other conversations.
func (s *Store) MarshalTurns(sessionID string) ([]byte, bool) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return nil, false
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	turns := ss.rec.Turns
	if turns == nil {
		turns = []MessageTurn{}
	}
	raw, err := json.Marshal(turns)
	if err != nil {
		return []byte("[]"), true
	}
	return raw, true
}

// VisitTurns supplies a read-only record under its session lock. The callback
// must not mutate the record or call Store methods for this same session.
func (s *Store) VisitTurns(sessionID string, fn func(rec SessionRecord)) bool {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return false
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	fn(ss.rec)
	return true
}

func (s *Store) Meta(sessionID string) (SessionRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ss := s.records[strings.TrimSpace(sessionID)]
	if ss == nil || ss.meta.ID == "" {
		return SessionRecord{}, false
	}
	return cloneRecord(ss.meta), true
}

func (s *Store) Update(sessionID string, fn func(*SessionRecord)) (SessionRecord, bool, error) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return SessionRecord{}, false, nil
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	updated := cloneRecord(ss.rec)
	fn(&updated)
	updated.UpdatedAt = time.Now().UnixMilli()
	if err := s.commitLocked(updated, ss); err != nil {
		return cloneRecord(ss.rec), true, err
	}
	return cloneRecord(updated), true, nil
}

func (s *Store) Delete(sessionID string) error {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return nil
	}
	defer ss.mu.Unlock()
	// Keep the record registered until removal completes so a concurrent Put
	// of the same ID cannot have its files removed by this deletion.
	if err := os.RemoveAll(s.sessionDir(ss.rec.ID)); err != nil {
		return err
	}
	s.mu.Lock()
	if ss.cache.loaded {
		s.cachedSessions--
		s.cachedBytes -= ss.cache.bytes
	}
	delete(s.records, ss.rec.ID)
	s.mu.Unlock()
	return nil
}

func (s *Store) List(directory string) []SessionInfo {
	normFilter := normalizeComparePath(directory)
	s.mu.RLock()
	metas := make([]SessionRecord, 0, len(s.records))
	for _, ss := range s.records {
		if ss.meta.ID != "" {
			metas = append(metas, ss.meta)
		}
	}
	s.mu.RUnlock()
	var out []SessionInfo
	for _, rec := range metas {
		if normFilter != "" && !sessionMatchesDirectory(rec, normFilter) {
			continue
		}
		out = append(out, rec.ToSessionInfo())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Updated > out[j].Time.Updated })
	return out
}

// sessionMatchesDirectory reports whether rec belongs to the workspace rooted
// at the already-normalized directory. A session belongs to exactly one
// project root, which also owns the isolated worktrees it provisions under
// .hoxi/worktrees. Nested projects stay separate workspaces even though one
// path is a prefix of the other.
func sessionMatchesDirectory(rec SessionRecord, normFilter string) bool {
	normProj := normalizeComparePath(rec.ProjectDir)
	normWt := normalizeComparePath(rec.WorktreePath)
	if normFilter == normProj || normFilter == normWt {
		return true
	}
	prefix := normFilter + "/.hoxi/worktrees/"
	if !strings.HasPrefix(normWt, prefix) {
		return false
	}
	id := strings.TrimPrefix(normWt, prefix)
	return id != "" && !strings.Contains(id, "/")
}

func (s *Store) ChildSessionIDs(parentID string) ([]string, int64) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []string
	var stamp int64
	if root := s.records[parentID]; root != nil {
		stamp = root.cache.stamp
	}
	for id, ss := range s.records {
		if ss.meta.ParentID == parentID {
			ids = append(ids, id)
			stamp += ss.cache.stamp
		}
	}
	return ids, stamp
}

func equalMessageInfo(a, b MessageInfo) bool { return reflect.DeepEqual(a, b) }
func equalMessagePart(a, b MessagePart) bool { return reflect.DeepEqual(a, b) }

func cloneAnyValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		return cloneGenericMap(val)
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = cloneAnyValue(item)
		}
		return out
	case []string:
		out := make([]string, len(val))
		copy(out, val)
		return out
	default:
		return val
	}
}

func cloneGenericMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = cloneAnyValue(v)
	}
	return out
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneMessageInfo(info MessageInfo) MessageInfo {
	out := info
	if info.Tokens != nil {
		tok := *info.Tokens
		out.Tokens = &tok
	}
	if info.Error != nil {
		errCopy := *info.Error
		if info.Error.Data != nil {
			dataCopy := *info.Error.Data
			errCopy.Data = &dataCopy
		}
		out.Error = &errCopy
	}
	return out
}

func clonePart(p MessagePart) MessagePart {
	out := p
	if p.Time != nil {
		t := *p.Time
		out.Time = &t
	}
	if p.Metadata != nil {
		out.Metadata = cloneGenericMap(p.Metadata)
	}
	if p.State != nil {
		st := *p.State
		st.Input = cloneGenericMap(p.State.Input)
		st.Metadata = cloneGenericMap(p.State.Metadata)
		if p.State.Time != nil {
			t := *p.State.Time
			st.Time = &t
		}
		out.State = &st
	}
	return out
}

func cloneTurns(turns []MessageTurn) []MessageTurn {
	if turns == nil {
		return nil
	}
	out := make([]MessageTurn, len(turns))
	for i, t := range turns {
		var parts []MessagePart
		if t.Parts != nil {
			parts = make([]MessagePart, len(t.Parts))
			for j, p := range t.Parts {
				parts[j] = clonePart(p)
			}
		}
		out[i] = MessageTurn{
			Info:  cloneMessageInfo(t.Info),
			Parts: parts,
		}
	}
	return out
}

func cloneRecord(r SessionRecord) SessionRecord {
	out := r
	out.Turns = cloneTurns(r.Turns)
	out.RevertedTurns = cloneTurns(r.RevertedTurns)
	if r.Revert != nil {
		rev := *r.Revert
		out.Revert = &rev
	}
	out.Snapshots = cloneStringMap(r.Snapshots)
	if r.OpenCodeIDs != nil {
		out.OpenCodeIDs = make([]string, len(r.OpenCodeIDs))
		copy(out.OpenCodeIDs, r.OpenCodeIDs)
	}
	out.OpenCodeDirectories = cloneStringMap(r.OpenCodeDirectories)
	return out
}

var normalizedComparePathCache sync.Map

func normalizeComparePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if cached, ok := normalizedComparePathCache.Load(p); ok {
		return cached.(string)
	}
	raw := p
	resolvedOK := false
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
		resolvedOK = true
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	cleaned = strings.TrimRight(cleaned, "/")
	if runtime.GOOS == "windows" {
		cleaned = strings.ToLower(cleaned)
	}
	if resolvedOK {
		normalizedComparePathCache.Store(raw, cleaned)
	}
	return cleaned
}
