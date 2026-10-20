package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// On-disk layout, one directory per conversation so that a long transcript
// never forces a rewrite of every other session:
//
//	<root>/<session-id>/session.json  metadata only, rewritten atomically
//	<root>/<session-id>/turns.jsonl   append-only upserts of message infos and parts
//
// turns.jsonl is replayed with upsert semantics and compacted once stale
// entries dominate it.
const (
	sessionMetaFile  = "session.json"
	sessionTurnsFile = "turns.jsonl"
	legacyStoreFile  = "runner-sessions.json"
)

type turnLogEntry struct {
	Info *MessageInfo `json:"info,omitempty"`
	Part *MessagePart `json:"part,omitempty"`
}

var errInvalidSessionID = errors.New("invalid session id")

func validStoreID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 200 {
		return false
	}
	return !strings.ContainsAny(id, "/\\\x00:")
}

func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.dir, id)
}

func writeFileAtomic(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) writeMeta(rec SessionRecord) error {
	if !validStoreID(rec.ID) {
		return errInvalidSessionID
	}
	rec.Turns = nil
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.sessionDir(rec.ID), sessionMetaFile), raw)
}

func encodeTurns(turns []MessageTurn) ([]byte, int, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	lines := 0
	for i := range turns {
		info := turns[i].Info
		if err := enc.Encode(turnLogEntry{Info: &info}); err != nil {
			return nil, 0, err
		}
		lines++
		for j := range turns[i].Parts {
			part := turns[i].Parts[j]
			if err := enc.Encode(turnLogEntry{Part: &part}); err != nil {
				return nil, 0, err
			}
			lines++
		}
	}
	return buf.Bytes(), lines, nil
}

// rewriteTurns replaces the log with one entry per message info and part.
func (s *Store) rewriteTurns(id string, turns []MessageTurn) (int, error) {
	raw, lines, err := encodeTurns(turns)
	if err != nil {
		return 0, err
	}
	return lines, writeFileAtomic(filepath.Join(s.sessionDir(id), sessionTurnsFile), raw)
}

func (s *Store) appendTurnEntries(id string, entries []turnLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(s.sessionDir(id), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.sessionDir(id), sessionTurnsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// readTurns replays the log. A torn trailing line from a crash is skipped.
func (s *Store) readTurns(id string) ([]MessageTurn, int) {
	f, err := os.Open(filepath.Join(s.sessionDir(id), sessionTurnsFile))
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	var turns []MessageTurn
	var index transcriptIndex
	lines := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e turnLogEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		lines++
		switch {
		case e.Info != nil:
			index.upsertInfo(&turns, *e.Info)
		case e.Part != nil:
			index.upsertPart(turns, *e.Part)
		}
	}
	for i := range turns {
		turns[i].Parts = dedupeClaudeStreamParts(turns[i].Parts)
	}
	return turns, lines
}

func claudePartStem(id string) string {
	idx := strings.LastIndexByte(id, '_')
	if idx <= 0 || idx == len(id)-1 {
		return ""
	}
	for _, ch := range id[idx+1:] {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	stem := id[:idx+1]
	if strings.HasSuffix(stem, "_text_") || strings.HasSuffix(stem, "_reason_") {
		return stem
	}
	return ""
}

func dedupeClaudeStreamParts(parts []MessagePart) []MessagePart {
	if len(parts) < 2 {
		return parts
	}
	out := make([]MessagePart, 0, len(parts))
	for _, p := range parts {
		stem := claudePartStem(p.ID)
		if stem != "" && p.Text != "" {
			merged := false
			for i := range out {
				if out[i].Type == p.Type && out[i].Text == p.Text && claudePartStem(out[i].ID) == stem {
					if p.Time != nil && p.Time.End > 0 {
						start := p.Time.Start
						if out[i].Time != nil && out[i].Time.Start > 0 {
							start = out[i].Time.Start
						}
						out[i].Time = &PartTime{Start: start, End: p.Time.End}
					}
					merged = true
					break
				}
			}
			if merged {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

func (s *Store) loadAllMeta() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validStoreID(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, entry.Name(), sessionMetaFile))
		if err != nil {
			continue
		}
		var rec SessionRecord
		if json.Unmarshal(raw, &rec) != nil || rec.ID != entry.Name() {
			continue
		}
		hasReverted := len(rec.RevertedTurns) > 0
		rec.Turns, rec.RevertedTurns = nil, nil
		s.records[rec.ID] = &storedSession{rec: rec, meta: metadataOnly(rec), hasRevertedTurns: hasReverted}
	}
}

// migrateLegacy imports the single-file store used before per-session
// directories. The old file is renamed only after every record was written,
// so an interrupted import is retried on the next start.
func (s *Store) migrateLegacy(legacyPath string) {
	raw, err := os.ReadFile(legacyPath)
	if err != nil || len(raw) == 0 {
		return
	}
	var doc struct {
		Sessions []*SessionRecord `json:"sessions"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return
	}
	for _, rec := range doc.Sessions {
		if rec == nil || !validStoreID(strings.TrimSpace(rec.ID)) {
			continue
		}
		if _, exists := s.records[rec.ID]; exists {
			continue
		}
		copied := cloneRecord(*rec)
		lines, err := s.rewriteTurns(copied.ID, copied.Turns)
		if err == nil {
			err = s.writeMeta(copied)
		}
		if err != nil {
			return
		}
		ss := &storedSession{rec: copied, turnsLoaded: true, logLines: lines, hasRevertedTurns: len(copied.RevertedTurns) > 0}
		ss.index.rebuild(copied.Turns)
		ss.bodyBytes = estimateTurnsBytes(copied.Turns) + estimateTurnsBytes(copied.RevertedTurns)
		s.records[copied.ID] = ss
		s.publishSessionLocked(ss)
	}
	_ = os.Rename(legacyPath, legacyPath+".migrated")
}
