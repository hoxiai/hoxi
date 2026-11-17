package runner

import (
	"net/http"
	"strconv"
	"strings"
)

const sseReplayCapacity = 2048

type sseReplayEntry struct {
	seq       uint64
	sessionID string
	evt       SSEEvent
}

// sseReplayRing maintains a bounded ring buffer of sequenced runner SSE events
// guarded by Service.mu so reconnecting desktop/mobile/tunnel clients can catch
// up seamlessly via `?after_seq=<N>` without reloading full snapshots, while
// automatically falling back to `hoxi.stream.resync` on overflow or restart.
type sseReplayRing struct {
	nextSeq uint64
	entries []sseReplayEntry
	head    int
	count   int
}

func (r *sseReplayRing) currentSeq() uint64 {
	return r.nextSeq
}

func (r *sseReplayRing) append(sessionID string, evt SSEEvent) SSEEvent {
	r.nextSeq++
	evt.Seq = r.nextSeq
	entry := sseReplayEntry{
		seq:       evt.Seq,
		sessionID: strings.TrimSpace(sessionID),
		evt:       evt,
	}
	if len(r.entries) < sseReplayCapacity {
		r.entries = append(r.entries, entry)
		r.count = len(r.entries)
		return evt
	}
	r.entries[r.head] = entry
	r.head = (r.head + 1) % sseReplayCapacity
	return evt
}

func (r *sseReplayRing) replaySince(afterSeq uint64, filterSessionID string) ([]SSEEvent, bool) {
	if afterSeq == 0 {
		return nil, false
	}
	if afterSeq > r.nextSeq {
		// Client cursor is ahead of server state (e.g., server restarted).
		return nil, true
	}
	if afterSeq == r.nextSeq {
		return nil, false
	}
	if r.count == 0 {
		return nil, true
	}
	oldestIdx := 0
	if len(r.entries) == sseReplayCapacity {
		oldestIdx = r.head
	}
	oldestSeq := r.entries[oldestIdx].seq
	if afterSeq+1 < oldestSeq {
		// Missed events were evicted from the bounded ring buffer.
		return nil, true
	}

	filterSessionID = strings.TrimSpace(filterSessionID)
	out := make([]SSEEvent, 0, int(r.nextSeq-afterSeq))
	ringLen := len(r.entries)
	for i := 0; i < r.count; i++ {
		entry := r.entries[(oldestIdx+i)%ringLen]
		if entry.seq <= afterSeq {
			continue
		}
		if filterSessionID == "" || entry.sessionID == filterSessionID {
			out = append(out, entry.evt)
		}
	}
	return out, false
}

func parseAfterSeqRequest(r *http.Request) uint64 {
	if r == nil {
		return 0
	}
	raw := strings.TrimSpace(r.URL.Query().Get("after_seq"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// CurrentEventSeq returns the latest monotonic runner SSE sequence number.
func (s *Service) CurrentEventSeq() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	seq := s.sseReplay.currentSeq()
	s.mu.Unlock()
	return seq
}

// SubscribeSince registers an SSE subscriber and atomically replays missed
// events strictly newer than afterSeq from the bounded ring buffer, or enqueues
// `hoxi.stream.resync` if afterSeq fell behind the ring buffer or crossed a restart.
func (s *Service) SubscribeSince(sessionID string, afterSeq uint64) (chan SSEEvent, func()) {
	ch, _, unsub := s.subscribeSinceWithSeq(sessionID, afterSeq)
	return ch, unsub
}

func (s *Service) subscribeSinceWithSeq(sessionID string, afterSeq uint64) (chan SSEEvent, uint64, func()) {
	sid := strings.TrimSpace(sessionID)
	s.mu.Lock()
	curSeq := s.sseReplay.currentSeq()
	replay, gap := s.sseReplay.replaySince(afterSeq, sid)
	bufCap := max(512, len(replay)+128)
	ch := make(chan SSEEvent, bufCap)
	if gap {
		ch <- SSEEvent{
			Seq:        curSeq,
			Type:       resyncEventType,
			Properties: map[string]interface{}{"seq": curSeq},
		}
	} else {
		for _, evt := range replay {
			ch <- evt
		}
	}
	s.subs[ch] = sid
	s.mu.Unlock()
	unsub := func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
	return ch, curSeq, unsub
}
