package runner

import (
	"strings"
	"time"
)

// Index both replay and live updates so a token/tool update does not scan the
// entire conversation, and first load stays linear in transcript size.
type transcriptIndex struct {
	messages map[string]int
	parts    map[partKey]int
}

func pairedStepTextPartID(part MessagePart) string {
	if part.Type != "reasoning" {
		return ""
	}
	idx := strings.LastIndex(part.ID, "_reason_")
	if idx <= 0 {
		return ""
	}
	suffix := part.ID[idx+len("_reason_"):]
	if suffix == "" {
		return ""
	}
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return ""
		}
	}
	return part.ID[:idx] + "_text_" + suffix
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
		// OpenCode does not echo Hoxi's task-boundary metadata on updates.
		info.ContinueTask = info.ContinueTask || (*turns)[i].Info.ContinueTask
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
		preserveToolEditInput(&part, turns[i].Parts[j])
		turns[i].Parts[j] = part
		return true
	}
	// Antigravity flushes step thinking on DONE after ACTIVE has already streamed
	// the step's text_delta; keep same-step reasoning ahead of its paired text part.
	if textID := pairedStepTextPartID(part); textID != "" {
		if textIdx, exists := idx.parts[partKey{part.MessageID, textID}]; exists {
			parts := append(turns[i].Parts, MessagePart{})
			copy(parts[textIdx+1:], parts[textIdx:])
			parts[textIdx] = part
			turns[i].Parts = parts
			for k := textIdx; k < len(parts); k++ {
				idx.parts[partKey{part.MessageID, parts[k].ID}] = k
			}
			return true
		}
	}
	idx.parts[key] = len(turns[i].Parts)
	turns[i].Parts = append(turns[i].Parts, part)
	return true
}

func (s *Store) UpsertTurnInfo(sessionID string, info MessageInfo) {
	s.upsertTurnInfo(sessionID, info, false)
}

func (s *Store) upsertTurnInfo(sessionID string, info MessageInfo, async bool) {
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
	s.mutations.Add(1)
	if async && s.asyncPersistenceEnabled() {
		if ss.dirtyInfos == nil {
			ss.dirtyInfos = make(map[string]struct{})
		}
		ss.dirtyInfos[info.ID] = struct{}{}
		ss.rec.UpdatedAt, ss.metaDirty = time.Now().UnixMilli(), true
		s.schedulePersistLocked(ss)
	} else {
		s.appendLocked(ss, true, turnLogEntry{Info: &copied})
	}
}

func (s *Store) UpsertTurnPart(sessionID string, part MessagePart) {
	s.upsertTurnPart(sessionID, part, false)
}

func (s *Store) upsertTurnPart(sessionID string, part MessagePart, async bool) {
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return
	}
	defer s.finishSession(ss)
	s.ensureTurnsLocked(ss)
	copied := clonePart(part)
	previousBytes := int64(0)
	key := partKey{part.MessageID, part.ID}
	previousEditRelevant := editRelevantPart(ss.index.part(ss.rec.Turns, key))
	delete(ss.deltas, key)
	if previous := ss.index.part(ss.rec.Turns, key); previous != nil {
		previousBytes = estimatePartBytes(*previous)
		preserveToolEditInput(&copied, *previous)
	}
	if !ss.index.upsertPart(ss.rec.Turns, copied) {
		return
	}
	if ss.edits != nil {
		if ss.edits.apply(ss.rec, copied) {
			s.editMutations.Add(1)
		}
	} else if previousEditRelevant || editRelevantPart(&copied) {
		s.editMutations.Add(1)
	}
	ss.bodyBytes += estimatePartBytes(copied) - previousBytes
	ss.rev++
	s.mutations.Add(1)
	if async && s.asyncPersistenceEnabled() {
		if ss.dirty == nil {
			ss.dirty = make(map[partKey]struct{})
		}
		ss.dirty[key] = struct{}{}
		ss.rec.UpdatedAt, ss.metaDirty = time.Now().UnixMilli(), true
		s.schedulePersistLocked(ss)
	} else {
		s.appendLocked(ss, true, turnLogEntry{Part: &copied})
	}
}

func compactPartDeltaBuildersLocked(ss *storedSession) {
	for key, b := range ss.deltas {
		if b != nil && b.Cap()-b.Len() > 4096 {
			if part := ss.index.part(ss.rec.Turns, key); part != nil && b.String() == part.Text {
				part.Text = strings.Clone(part.Text)
			}
		}
	}
	ss.deltas = nil
}

// Deltas stay in memory until a snapshot or FlushSession persists them.
func (s *Store) AppendPartDelta(sessionID, messageID, partID, delta string) {
	if delta == "" {
		return
	}
	ss := s.lockSession(sessionID, false)
	if ss == nil {
		return
	}
	defer s.finishDeltaSession(ss)
	s.ensureTurnsLocked(ss)
	key := partKey{messageID, partID}
	if part := ss.index.part(ss.rec.Turns, key); part != nil {
		if ss.deltas == nil {
			ss.deltas = make(map[partKey]*strings.Builder)
		}
		b := ss.deltas[key]
		if b == nil || b.String() != part.Text {
			b = &strings.Builder{}
			b.Grow(len(part.Text) + max(len(delta)*4, 64))
			b.WriteString(part.Text)
			ss.deltas[key] = b
		}
		b.WriteString(delta)
		part.Text = b.String()
		ss.bodyBytes += int64(len(delta))
		ss.rev++
		s.mutations.Add(1)
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
	s.cancelPersistLocked(ss)
	ss.diskMu.Lock()
	defer ss.diskMu.Unlock()
	s.applyPersistResultLocked(ss)
	if len(ss.dirty) > 0 || len(ss.dirtyInfos) > 0 || len(ss.pending) > 0 {
		s.appendDiskLocked(ss, false, false)
	}
	if !ss.active {
		compactPartDeltaBuildersLocked(ss)
	}
	if ss.metaDirty && s.writeMeta(ss.rec) == nil {
		ss.metaDirty = false
	}
	if err := s.syncTurnLogLocked(ss); err != nil {
		recordPersistenceError(err)
		return false
	}
	return len(ss.dirty) == 0 && len(ss.dirtyInfos) == 0 && len(ss.pending) == 0 && !ss.metaDirty
}

func (s *Store) appendLocked(ss *storedSession, touch bool, extra ...turnLogEntry) {
	ss.diskMu.Lock()
	defer ss.diskMu.Unlock()
	s.applyPersistResultLocked(ss)
	s.appendDiskLocked(ss, touch, true, extra...)
}

func (s *Store) appendDiskLocked(ss *storedSession, touch, compact bool, extra ...turnLogEntry) {
	entries := append(pendingEntriesLocked(ss), extra...)
	if touch {
		ss.rec.UpdatedAt, ss.metaDirty = time.Now().UnixMilli(), true
	}
	if err := s.appendTurnEntries(ss, entries); err != nil {
		ss.pending = retainLatestEntries(ss.pending, extra)
		return
	}
	ss.logLines += len(entries)
	ss.dirty, ss.pending = nil, nil
	ss.dirtyInfos = nil
	if live := len(ss.index.messages) + len(ss.index.parts); compact && ss.logLines > 2*live+64 {
		if n, err := s.rewriteTurns(ss.rec.ID, ss.rec.Turns); err == nil {
			ss.logLines = n
		}
		if ss.metaDirty && s.writeMeta(ss.rec) == nil {
			ss.metaDirty = false
		}
	}
}

// A persistent write failure must retain the current state, not every streamed
// version of a growing part (which would itself grow quadratically in memory).
func retainLatestEntries(pending, extra []turnLogEntry) []turnLogEntry {
	for _, next := range extra {
		replaced := false
		for i, previous := range pending {
			sameInfo := next.Info != nil && previous.Info != nil && next.Info.ID == previous.Info.ID
			samePart := next.Part != nil && previous.Part != nil && next.Part.MessageID == previous.Part.MessageID && next.Part.ID == previous.Part.ID
			if sameInfo || samePart {
				pending[i], replaced = next, true
				break
			}
		}
		if !replaced {
			pending = append(pending, next)
		}
	}
	return pending
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
