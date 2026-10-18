package runner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu       sync.RWMutex
	filePath string
	records  map[string]*SessionRecord
}

func defaultStoreFilePath() string {
	if custom := strings.TrimSpace(os.Getenv("HOXI_RUNNER_STORE_PATH")); custom != "" {
		return filepath.Clean(custom)
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return filepath.Join(os.TempDir(), "hoxi-runner-sessions.json")
	}
	return filepath.Join(home, ".hoxi", "runner-sessions.json")
}

func NewStore(customPath string) *Store {
	fp := strings.TrimSpace(customPath)
	if fp == "" {
		fp = defaultStoreFilePath()
	}
	s := &Store{
		filePath: fp,
		records:  make(map[string]*SessionRecord),
	}
	s.loadFromDisk()
	return s
}

func GenerateRunnerSessionID(rt string) string {
	prefix := "ses_agy"
	if rt == string(RuntimeClaudeCode) {
		prefix = "ses_cld"
	}
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}

func IsRunnerSessionID(sessionID string) bool {
	s := strings.TrimSpace(sessionID)
	return strings.HasPrefix(s, "ses_agy_") || strings.HasPrefix(s, "ses_cld_")
}

func (s *Store) loadFromDisk() {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.filePath)
	if err != nil || len(raw) == 0 {
		return
	}
	var doc struct {
		Sessions []*SessionRecord `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return
	}
	for _, rec := range doc.Sessions {
		if rec != nil && strings.TrimSpace(rec.ID) != "" {
			s.records[rec.ID] = rec
		}
	}
}

func (s *Store) saveLocked() error {
	if s.filePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}
	list := make([]*SessionRecord, 0, len(s.records))
	for _, r := range s.records {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt > list[j].UpdatedAt
	})
	raw, err := json.MarshalIndent(map[string]interface{}{"sessions": list}, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.filePath)
}

func (s *Store) Put(rec SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := cloneRecord(rec)
	s.records[copied.ID] = &copied
	return s.saveLocked()
}

func (s *Store) Get(sessionID string) (SessionRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[strings.TrimSpace(sessionID)]
	if !ok || rec == nil {
		return SessionRecord{}, false
	}
	return cloneRecord(*rec), true
}

func (s *Store) Update(sessionID string, fn func(*SessionRecord)) (SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[strings.TrimSpace(sessionID)]
	if !ok || rec == nil {
		return SessionRecord{}, false, nil
	}
	fn(rec)
	rec.UpdatedAt = time.Now().UnixMilli()
	err := s.saveLocked()
	return cloneRecord(*rec), true, err
}

func (s *Store) Delete(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, strings.TrimSpace(sessionID))
	return s.saveLocked()
}

func (s *Store) List(directory string) []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	normFilter := normalizeComparePath(directory)
	var out []SessionInfo
	for _, rec := range s.records {
		if rec == nil {
			continue
		}
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
	rec := s.records[sessionID]
	if rec == nil {
		return
	}
	for i := range rec.Turns {
		if rec.Turns[i].Info.ID == info.ID {
			rec.Turns[i].Info = info
			rec.UpdatedAt = time.Now().UnixMilli()
			_ = s.saveLocked()
			return
		}
	}
	rec.Turns = append(rec.Turns, MessageTurn{Info: info, Parts: []MessagePart{}})
	rec.UpdatedAt = time.Now().UnixMilli()
	_ = s.saveLocked()
}

func (s *Store) UpsertTurnPart(sessionID string, part MessagePart) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.records[sessionID]
	if rec == nil {
		return
	}
	for i := range rec.Turns {
		if rec.Turns[i].Info.ID == part.MessageID {
			for j := range rec.Turns[i].Parts {
				if rec.Turns[i].Parts[j].ID == part.ID {
					rec.Turns[i].Parts[j] = clonePart(part)
					rec.UpdatedAt = time.Now().UnixMilli()
					_ = s.saveLocked()
					return
				}
			}
			rec.Turns[i].Parts = append(rec.Turns[i].Parts, clonePart(part))
			rec.UpdatedAt = time.Now().UnixMilli()
			_ = s.saveLocked()
			return
		}
	}
}

func (s *Store) AppendPartDelta(sessionID, messageID, partID, delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.records[sessionID]
	if rec == nil {
		return
	}
	for i := range rec.Turns {
		if rec.Turns[i].Info.ID == messageID {
			for j := range rec.Turns[i].Parts {
				if rec.Turns[i].Parts[j].ID == partID {
					rec.Turns[i].Parts[j].Text += delta
					return
				}
			}
		}
	}
}

func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.saveLocked()
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
	cleaned := filepath.ToSlash(filepath.Clean(p))
	cleaned = strings.TrimRight(cleaned, "/")
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}
