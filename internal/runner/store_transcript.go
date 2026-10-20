package runner

import "time"

// Index both replay and live updates so a token/tool update does not scan the
// entire conversation, and first load stays linear in transcript size.
type transcriptIndex struct {
	messages map[string]int
	parts    map[partKey]int
}

func (idx *transcriptIndex) rebuild(turns []MessageTurn) {
	idx.messages = make(map[string]int, len(turns))
	idx.parts = make(map[partKey]int)
	for i, turn := range turns {
		idx.messages[turn.Info.ID] = i
		for j, part := range turn.Parts {
			idx.parts[partKey{turn.Info.ID, part.ID}] = j
		}
	}
}

func (idx *transcriptIndex) upsertInfo(turns *[]MessageTurn, info MessageInfo) bool {
	if idx.messages == nil {
		idx.rebuild(*turns)
	}
	if i, ok := idx.messages[info.ID]; ok {
		(*turns)[i].Info = info
		return false
	}
	idx.messages[info.ID] = len(*turns)
	*turns = append(*turns, MessageTurn{Info: info, Parts: []MessagePart{}})
	return true
}

func (idx *transcriptIndex) part(turns []MessageTurn, key partKey) *MessagePart {
	i, ok := idx.messages[key.messageID]
	if !ok {
		return nil
	}
	j, ok := idx.parts[key]
	if !ok {
		return nil
	}
	return &turns[i].Parts[j]
}

func (idx *transcriptIndex) upsertPart(turns []MessageTurn, part MessagePart) bool {
	i, ok := idx.messages[part.MessageID]
	if !ok {
		return false
	}
	key := partKey{part.MessageID, part.ID}
	if j, ok := idx.parts[key]; ok {
		turns[i].Parts[j] = part
		return true
	}
	idx.parts[key] = len(turns[i].Parts)
	turns[i].Parts = append(turns[i].Parts, part)
	return true
}

func (s *Store) UpsertTurnInfo(sessionID string, info MessageInfo) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	copied := cloneMessageInfo(info)
	if ss.index.upsertInfo(&ss.rec.Turns, copied) {
		ss.bodyBytes += 256
	}
	ss.rev++
	s.appendLocked(ss, true, turnLogEntry{Info: &copied})
}

func (s *Store) UpsertTurnPart(sessionID string, part MessagePart) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	copied := clonePart(part)
	previousBytes := int64(0)
	if previous := ss.index.part(ss.rec.Turns, partKey{part.MessageID, part.ID}); previous != nil {
		previousBytes = estimatePartBytes(*previous)
	}
	if !ss.index.upsertPart(ss.rec.Turns, copied) {
		return
	}
	ss.bodyBytes += estimatePartBytes(copied) - previousBytes
	ss.rev++
	s.appendLocked(ss, true, turnLogEntry{Part: &copied})
}

// Deltas stay in memory until a snapshot or FlushSession persists them.
func (s *Store) AppendPartDelta(sessionID, messageID, partID, delta string) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	key := partKey{messageID, partID}
	if part := ss.index.part(ss.rec.Turns, key); part != nil {
		part.Text += delta
		ss.bodyBytes += int64(len(delta))
		ss.rev++
		if ss.dirty == nil {
			ss.dirty = make(map[partKey]struct{})
		}
		ss.dirty[key] = struct{}{}
	}
}

func (s *Store) FlushSession(id string) {
	ss := s.lockSession(id, false)
	if ss == nil {
		return
	}
	defer s.finishSession(ss)
	s.flushSessionLocked(ss)
}

func (s *Store) Flush() {
	s.mu.RLock()
	ids := make([]string, 0, len(s.records))
	for id := range s.records {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	for _, id := range ids {
		s.FlushSession(id)
	}
}

func (s *Store) flushSessionLocked(ss *storedSession) bool {
	if len(ss.dirty) > 0 || len(ss.pending) > 0 {
		s.appendLocked(ss, false)
	}
	if ss.metaDirty && s.writeMeta(ss.rec) == nil {
		ss.metaDirty = false
	}
	return len(ss.dirty) == 0 && len(ss.pending) == 0 && !ss.metaDirty
}

func (s *Store) appendLocked(ss *storedSession, touch bool, extra ...turnLogEntry) {
	entries := append(append(append([]turnLogEntry{}, ss.pending...), dirtyEntriesLocked(ss)...), extra...)
	if touch {
		ss.rec.UpdatedAt, ss.metaDirty = time.Now().UnixMilli(), true
	}
	if err := s.appendTurnEntries(ss.rec.ID, entries); err != nil {
		ss.pending = append(ss.pending, extra...)
		return
	}
	ss.logLines += len(entries)
	ss.dirty, ss.pending = nil, nil
	if live := len(ss.index.messages) + len(ss.index.parts); ss.logLines > 2*live+64 {
		if n, err := s.rewriteTurns(ss.rec.ID, ss.rec.Turns); err == nil {
			ss.logLines = n
		}
		if ss.metaDirty && s.writeMeta(ss.rec) == nil {
			ss.metaDirty = false
		}
	}
}

func dirtyEntriesLocked(ss *storedSession) []turnLogEntry {
	entries := make([]turnLogEntry, 0, len(ss.dirty))
	for key := range ss.dirty {
		if part := ss.index.part(ss.rec.Turns, key); part != nil {
			copied := clonePart(*part)
			entries = append(entries, turnLogEntry{Part: &copied})
		}
	}
	return entries
}
