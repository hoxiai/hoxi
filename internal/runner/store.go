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
)

// Store is Hoxi's own conversation store. Metadata for every session stays in
// memory; transcripts are read from disk the first time a session is opened.
type Store struct {
	mu      sync.RWMutex
	dir     string
	records map[string]*storedSession
}

type storedSession struct {
	rec         SessionRecord
	turnsLoaded bool
	logLines    int
	// Parts whose streamed text deltas have not reached turns.jsonl yet.
	dirty map[partKey]struct{}
}

type partKey struct{ messageID, partID string }

func defaultStoreDir() (dir, legacy string) {
	if custom := strings.TrimSpace(os.Getenv("HOXI_SESSION_STORE_DIR")); custom != "" {
		return filepath.Clean(custom), ""
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return filepath.Join(os.TempDir(), "hoxi-sessions"), ""
	}
	return filepath.Join(home, ".hoxi", "sessions"), filepath.Join(home, ".hoxi", legacyStoreFile)
}

// NewStore opens the store rooted at customDir, or ~/.hoxi/sessions when empty.
// The default store also imports the legacy ~/.hoxi/runner-sessions.json once.
func NewStore(customDir string) *Store {
	dir, legacy := strings.TrimSpace(customDir), ""
	if dir == "" {
		dir, legacy = defaultStoreDir()
	}
	s := &Store{dir: filepath.Clean(dir), records: make(map[string]*storedSession)}
	s.loadAllMeta()
	if legacy != "" {
		s.migrateLegacy(legacy)
	}
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

func (s *Store) ensureTurnsLocked(ss *storedSession) {
	if ss.turnsLoaded {
		return
	}
	ss.rec.Turns, ss.logLines = s.readTurns(ss.rec.ID)
	ss.turnsLoaded = true
}

// commitLocked persists next as the new state of ss (nil for a new session).
// The transcript is rewritten only when it differs from prevTurns; otherwise
// pending deltas are appended and just the metadata file is replaced.
func (s *Store) commitLocked(next SessionRecord, ss *storedSession, prevTurns []MessageTurn) error {
	if !validStoreID(next.ID) {
		return errInvalidSessionID
	}
	lines := 0
	turnsChanged := ss == nil || !reflect.DeepEqual(prevTurns, next.Turns)
	if turnsChanged {
		n, err := s.rewriteTurns(next.ID, next.Turns)
		if err != nil {
			return err
		}
		lines = n
	} else {
		entries := takeDirtyLocked(ss)
		if err := s.appendTurnEntries(next.ID, entries); err != nil {
			return err
		}
		lines = ss.logLines + len(entries)
	}
	if err := s.writeMeta(next); err != nil {
		if turnsChanged && ss != nil {
			_, _ = s.rewriteTurns(next.ID, prevTurns)
		}
		return err
	}
	if ss == nil {
		ss = &storedSession{}
		s.records[next.ID] = ss
	}
	ss.rec, ss.turnsLoaded, ss.logLines, ss.dirty = next, true, lines, nil
	return nil
}

func (s *Store) Put(rec SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := cloneRecord(rec)
	ss := s.records[copied.ID]
	var prevTurns []MessageTurn
	if ss != nil {
		s.ensureTurnsLocked(ss)
		prevTurns = ss.rec.Turns
	}
	return s.commitLocked(copied, ss, prevTurns)
}

func (s *Store) Get(sessionID string) (SessionRecord, bool) {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.RLock()
	ss := s.records[sessionID]
	if ss != nil && ss.turnsLoaded {
		rec := cloneRecord(ss.rec)
		s.mu.RUnlock()
		return rec, true
	}
	s.mu.RUnlock()
	if ss == nil {
		return SessionRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss = s.records[sessionID]; ss == nil {
		return SessionRecord{}, false
	}
	s.ensureTurnsLocked(ss)
	return cloneRecord(ss.rec), true
}

// Meta returns a session without its transcript and never touches the disk.
func (s *Store) Meta(sessionID string) (SessionRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ss := s.records[strings.TrimSpace(sessionID)]
	if ss == nil {
		return SessionRecord{}, false
	}
	meta := ss.rec
	meta.Turns = nil
	return cloneRecord(meta), true
}

func (s *Store) Update(sessionID string, fn func(*SessionRecord)) (SessionRecord, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.records[sessionID]
	if ss == nil {
		return SessionRecord{}, false, nil
	}
	s.ensureTurnsLocked(ss)
	updated := cloneRecord(ss.rec)
	fn(&updated)
	updated.UpdatedAt = time.Now().UnixMilli()
	if err := s.commitLocked(updated, ss, ss.rec.Turns); err != nil {
		return cloneRecord(ss.rec), true, err
	}
	return cloneRecord(updated), true, nil
}

func (s *Store) Delete(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, sessionID)
	if !validStoreID(sessionID) {
		return nil
	}
	return os.RemoveAll(s.sessionDir(sessionID))
}

func (s *Store) List(directory string) []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	normFilter := normalizeComparePath(directory)
	var out []SessionInfo
	for _, ss := range s.records {
		rec := &ss.rec
		if normFilter != "" {
			normProj := normalizeComparePath(rec.ProjectDir)
			normWt := normalizeComparePath(rec.WorktreePath)
			if normFilter != normProj && normFilter != normWt &&
				!strings.HasPrefix(normWt, normFilter+"/") &&
				!strings.HasPrefix(normFilter, normProj+"/") {
				continue
			}
		}
		out = append(out, rec.ToSessionInfo())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time.Updated > out[j].Time.Updated
	})
	return out
}

func (s *Store) UpsertTurnInfo(sessionID string, info MessageInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.records[sessionID]
	if ss == nil {
		return
	}
	s.ensureTurnsLocked(ss)
	ss.rec.Turns = upsertTurnInfo(ss.rec.Turns, info)
	s.appendLocked(ss, true, turnLogEntry{Info: &info})
}

func (s *Store) UpsertTurnPart(sessionID string, part MessagePart) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.records[sessionID]
	if ss == nil {
		return
	}
	s.ensureTurnsLocked(ss)
	copied := clonePart(part)
	turns, ok := upsertTurnPart(ss.rec.Turns, copied)
	if !ok {
		return
	}
	ss.rec.Turns = turns
	delete(ss.dirty, partKey{copied.MessageID, copied.ID})
	s.appendLocked(ss, true, turnLogEntry{Part: &copied})
}

// AppendPartDelta only updates memory; Flush or the next write persists it.
func (s *Store) AppendPartDelta(sessionID, messageID, partID, delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.records[sessionID]
	if ss == nil {
		return
	}
	s.ensureTurnsLocked(ss)
	if part := findPart(ss.rec.Turns, messageID, partID); part != nil {
		part.Text += delta
		if ss.dirty == nil {
			ss.dirty = make(map[partKey]struct{})
		}
		ss.dirty[partKey{messageID, partID}] = struct{}{}
	}
}

func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.records {
		if len(ss.dirty) > 0 {
			s.appendLocked(ss, false)
		}
	}
}

// appendLocked writes pending deltas plus extra entries; touch also bumps
// UpdatedAt. Write errors are dropped: the in-memory transcript stays
// authoritative and the next successful rewrite or compaction persists it.
func (s *Store) appendLocked(ss *storedSession, touch bool, extra ...turnLogEntry) {
	entries := append(takeDirtyLocked(ss), extra...)
	if err := s.appendTurnEntries(ss.rec.ID, entries); err == nil {
		ss.logLines += len(entries)
	}
	if touch {
		ss.rec.UpdatedAt = time.Now().UnixMilli()
		_ = s.writeMeta(ss.rec)
	}
	if live := liveEntryCount(ss.rec.Turns); ss.logLines > 2*live+64 {
		if n, err := s.rewriteTurns(ss.rec.ID, ss.rec.Turns); err == nil {
			ss.logLines = n
		}
	}
}

func takeDirtyLocked(ss *storedSession) []turnLogEntry {
	if len(ss.dirty) == 0 {
		return nil
	}
	entries := make([]turnLogEntry, 0, len(ss.dirty))
	for key := range ss.dirty {
		if part := findPart(ss.rec.Turns, key.messageID, key.partID); part != nil {
			copied := *part
			entries = append(entries, turnLogEntry{Part: &copied})
		}
	}
	ss.dirty = nil
	return entries
}

func liveEntryCount(turns []MessageTurn) int {
	n := len(turns)
	for i := range turns {
		n += len(turns[i].Parts)
	}
	return n
}

func findPart(turns []MessageTurn, messageID, partID string) *MessagePart {
	for i := range turns {
		if turns[i].Info.ID != messageID {
			continue
		}
		for j := range turns[i].Parts {
			if turns[i].Parts[j].ID == partID {
				return &turns[i].Parts[j]
			}
		}
	}
	return nil
}

func upsertTurnInfo(turns []MessageTurn, info MessageInfo) []MessageTurn {
	for i := range turns {
		if turns[i].Info.ID == info.ID {
			turns[i].Info = info
			return turns
		}
	}
	return append(turns, MessageTurn{Info: info, Parts: []MessagePart{}})
}

// upsertTurnPart reports false when the part's message is unknown.
func upsertTurnPart(turns []MessageTurn, part MessagePart) ([]MessageTurn, bool) {
	for i := range turns {
		if turns[i].Info.ID != part.MessageID {
			continue
		}
		for j := range turns[i].Parts {
			if turns[i].Parts[j].ID == part.ID {
				turns[i].Parts[j] = part
				return turns, true
			}
		}
		turns[i].Parts = append(turns[i].Parts, part)
		return turns, true
	}
	return turns, false
}

func clonePart(p MessagePart) MessagePart {
	raw, _ := json.Marshal(p)
	var out MessagePart
	_ = json.Unmarshal(raw, &out)
	return out
}

func cloneRecord(r SessionRecord) SessionRecord {
	raw, _ := json.Marshal(r)
	var out SessionRecord
	_ = json.Unmarshal(raw, &out)
	return out
}

func normalizeComparePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	cleaned = strings.TrimRight(cleaned, "/")
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}
