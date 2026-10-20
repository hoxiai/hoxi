package runner

import (
	"sort"
	"strings"
)

type transcriptCacheEntry struct {
	loaded, active bool
	bytes          int64
	access         uint64
	stamp          int64
}

// The registry lock is released before waiting for an individual conversation.
// Recheck identity after locking to handle concurrent Delete/Put safely.
func (s *Store) lockSession(id string, create bool) *storedSession {
	id = strings.TrimSpace(id)
	for {
		s.mu.Lock()
		ss := s.records[id]
		if ss == nil && create {
			ss = &storedSession{rec: SessionRecord{ID: id}}
			s.records[id] = ss
		}
		s.mu.Unlock()
		if ss == nil {
			return nil
		}
		ss.mu.Lock()
		s.mu.RLock()
		current := s.records[id] == ss
		s.mu.RUnlock()
		if current {
			return ss
		}
		ss.mu.Unlock()
	}
}

func metadataOnly(rec SessionRecord) SessionRecord {
	rec.Turns, rec.RevertedTurns = nil, nil
	return cloneRecord(rec)
}

func (s *Store) publishSessionLocked(ss *storedSession) {
	meta := metadataOnly(ss.rec)
	s.mu.Lock()
	if s.records[ss.rec.ID] == ss {
		if ss.cache.loaded {
			s.cachedSessions--
			s.cachedBytes -= ss.cache.bytes
		}
		if ss.turnsLoaded {
			s.cachedSessions++
			s.cachedBytes += ss.bodyBytes
		}
		s.clock++
		ss.meta = meta
		ss.cache = transcriptCacheEntry{loaded: ss.turnsLoaded, active: ss.active,
			bytes: ss.bodyBytes, access: s.clock, stamp: ss.rev}
	}
	s.mu.Unlock()
}

func (s *Store) finishSession(ss *storedSession) {
	s.publishSessionLocked(ss)
	maintain := !ss.active
	ss.mu.Unlock()
	if maintain {
		s.trimCache()
	}
}

// SetActive protects live runtime/subagent transcripts from cache eviction.
func (s *Store) SetActive(id string, active bool) {
	ss := s.lockSession(id, false)
	if ss == nil {
		return
	}
	defer s.finishSession(ss)
	ss.active = active
}

// Idle transcripts are a bounded LRU cache, not durable state. Persist before
// dropping references; failed writes and in-use records stay resident. TryLock
// means cache maintenance never waits for another conversation's disk I/O.
func (s *Store) trimCache() {
	s.mu.RLock()
	overBudget := s.cachedSessions > s.cacheMaxSessions || s.cachedBytes > s.cacheMaxBytes
	s.mu.RUnlock()
	if !overBudget {
		return
	}
	if !s.cacheMu.TryLock() {
		return
	}
	defer s.cacheMu.Unlock()
	type candidate struct {
		ss     *storedSession
		access uint64
	}
	var candidates []candidate
	var bytes int64
	count := 0
	s.mu.RLock()
	for _, ss := range s.records {
		if !ss.cache.loaded {
			continue
		}
		count++
		bytes += ss.cache.bytes
		if !ss.cache.active {
			candidates = append(candidates, candidate{ss, ss.cache.access})
		}
	}
	s.mu.RUnlock()
	if count <= s.cacheMaxSessions && bytes <= s.cacheMaxBytes {
		return
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].access < candidates[j].access })
	for _, c := range candidates {
		if count <= s.cacheMaxSessions && bytes <= s.cacheMaxBytes {
			break
		}
		ss := c.ss
		if !ss.mu.TryLock() {
			continue
		}
		s.mu.RLock()
		current := s.records[ss.rec.ID] == ss
		// A newly touched or newly pinned candidate must be reconsidered on
		// the next pass, rather than evicting it using an obsolete LRU order.
		unchanged := ss.cache.access == c.access
		s.mu.RUnlock()
		if !current || !unchanged || ss.active || !ss.turnsLoaded || !s.flushSessionLocked(ss) {
			ss.mu.Unlock()
			continue
		}
		bytes -= ss.bodyBytes
		count--
		ss.rec.Turns, ss.rec.RevertedTurns = nil, nil
		ss.turnsLoaded, ss.bodyBytes = false, 0
		ss.index = transcriptIndex{}
		s.publishSessionLocked(ss)
		ss.mu.Unlock()
	}
}

// Estimate retained payloads incrementally; this budget is deliberately below
// total heap usage because objects, indexes and reactive views add overhead.
func estimatePartBytes(part MessagePart) int64 {
	n := int64(256 + len(part.Text) + len(part.URL))
	n += estimateValueBytes(part.Metadata, 0)
	if part.State != nil {
		n += int64(len(part.State.Output))
		n += estimateValueBytes(part.State.Input, 0) + estimateValueBytes(part.State.Metadata, 0)
	}
	return n
}

func estimateValueBytes(value interface{}, depth int) int64 {
	if depth > 16 {
		return 0
	}
	var n int64
	switch v := value.(type) {
	case string:
		return int64(len(v))
	case map[string]interface{}:
		for key, item := range v {
			n += int64(32+len(key)) + estimateValueBytes(item, depth+1)
		}
	case []interface{}:
		for _, item := range v {
			n += 16 + estimateValueBytes(item, depth+1)
		}
	case []string:
		for _, item := range v {
			n += int64(16 + len(item))
		}
	}
	return n
}

func estimateTurnsBytes(turns []MessageTurn) int64 {
	n := int64(len(turns)) * 256
	for _, turn := range turns {
		for _, part := range turn.Parts {
			n += estimatePartBytes(part)
		}
	}
	return n
}
