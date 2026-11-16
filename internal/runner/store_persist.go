package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
)

const streamPersistDelay = 25 * time.Millisecond
const maxPendingStreamEntries = 256

// StartPersistence binds asynchronous I/O to the host's lifetime. Stores used
// without a running host retain the synchronous API and spawn no background jobs.
func (s *Store) StartPersistence(ctx context.Context) {
	if ctx == nil {
		return
	}
	s.mu.Lock()
	if s.persistCtx != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.persistCtx, s.persistCancel, s.persistDone = ctx, cancel, done
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.Flush()
		close(done)
	}()
}

func (s *Store) ClosePersistence() {
	s.mu.RLock()
	cancel, done := s.persistCancel, s.persistDone
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
		<-done
	}
	// Aborting runtimes can produce final deltas after the context was cancelled.
	s.Flush()
}

func (s *Store) asyncPersistenceEnabled() bool {
	s.mu.RLock()
	ctx := s.persistCtx
	s.mu.RUnlock()
	return ctx != nil && ctx.Err() == nil
}

type persistJob struct {
	timer    *time.Timer
	maintain bool
}

// diskMu protects this result. A synchronous flush can consume it while holding
// mu, without waiting for the worker to reacquire mu (which would deadlock).
type persistResult struct {
	entries   []turnLogEntry
	err       error
	rewritten bool
	lines     int
}

func recordPersistenceError(err error) {
	if err != nil {
		diagnostics.Record(diagnostics.Entry{Kind: "system", Level: "error", Source: "runner", Message: "session persistence: " + err.Error()})
	}
}

func (s *Store) cancelPersistLocked(ss *storedSession) {
	if job := ss.persistJob; job != nil {
		job.timer.Stop()
		ss.persistJob = nil
	}
	ss.persistRequested = false
}

func (s *Store) applyPersistResultLocked(ss *storedSession) {
	result := ss.persistResult
	if result == nil {
		return
	}
	ss.persistResult = nil
	if result.err == nil {
		if result.rewritten {
			ss.logLines = result.lines
		} else {
			ss.logLines += len(result.entries)
		}
		return
	}
	// Requeue keys, never stale payloads: newer deltas/snapshots may already be
	// in memory while disk I/O was running. A retry serializes their latest state.
	for _, entry := range result.entries {
		if entry.Info != nil {
			if _, ok := ss.index.messages[entry.Info.ID]; ok {
				if ss.dirtyInfos == nil {
					ss.dirtyInfos = make(map[string]struct{})
				}
				ss.dirtyInfos[entry.Info.ID] = struct{}{}
			}
		} else if entry.Part != nil {
			key := partKey{entry.Part.MessageID, entry.Part.ID}
			if ss.index.part(ss.rec.Turns, key) != nil {
				if ss.dirty == nil {
					ss.dirty = make(map[partKey]struct{})
				}
				ss.dirty[key] = struct{}{}
			}
		}
	}
	recordPersistenceError(result.err)
}

func pendingEntriesLocked(ss *storedSession) []turnLogEntry {
	entries := append(append([]turnLogEntry{}, ss.pending...), dirtyEntriesLocked(ss)...)
	for id := range ss.dirtyInfos {
		if i, ok := ss.index.messages[id]; ok {
			info := cloneMessageInfo(ss.rec.Turns[i].Info)
			entries = append(entries, turnLogEntry{Info: &info})
		}
	}
	// Preserve first-turn/part order even though coalesced dirty sets are maps.
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.Info != nil) != (b.Info != nil) {
			return a.Info != nil
		}
		if a.Info != nil {
			return ss.index.messages[a.Info.ID] < ss.index.messages[b.Info.ID]
		}
		if a.Part == nil || b.Part == nil {
			return false
		}
		am, bm := ss.index.messages[a.Part.MessageID], ss.index.messages[b.Part.MessageID]
		if am != bm {
			return am < bm
		}
		return ss.index.parts[partKey{a.Part.MessageID, a.Part.ID}] < ss.index.parts[partKey{b.Part.MessageID, b.Part.ID}]
	})
	return entries
}

func (s *Store) schedulePersistLocked(ss *storedSession) {
	if !s.asyncPersistenceEnabled() {
		s.flushSessionLocked(ss)
		return
	}
	ss.persistRequested = true
	if len(ss.dirty)+len(ss.dirtyInfos)+len(ss.pending) >= maxPendingStreamEntries {
		// Bounded backpressure, instead of an unbounded event/payload queue.
		s.flushSessionLocked(ss)
		return
	}
	if ss.persistJob == nil {
		job := &persistJob{}
		ss.persistJob = job
		job.timer = time.AfterFunc(streamPersistDelay, func() { s.persistSession(ss, job) })
	}
}

func (s *Store) scheduleIdleMaintenance(id string) {
	if !s.asyncPersistenceEnabled() {
		return
	}
	ss := s.lockSession(id, false)
	if ss == nil {
		return
	}
	defer ss.mu.Unlock()
	if ss.active {
		return
	}
	if ss.persistJob == nil {
		job := &persistJob{maintain: true}
		ss.persistJob = job
		job.timer = time.AfterFunc(streamPersistDelay, func() { s.persistSession(ss, job) })
	} else {
		ss.persistJob.maintain = true
	}
}

func (s *Store) persistSession(ss *storedSession, job *persistJob) {
	s.mu.RLock()
	ctx := s.persistCtx
	s.mu.RUnlock()
	if ctx == nil {
		return
	}
	select {
	case s.persistSlots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-s.persistSlots }()
	ss.mu.Lock()
	s.mu.RLock()
	current := s.records[ss.rec.ID] == ss
	s.mu.RUnlock()
	if !current || ss.persistJob != job {
		ss.mu.Unlock()
		return
	}
	ss.diskMu.Lock()
	s.applyPersistResultLocked(ss)
	entries := pendingEntriesLocked(ss)
	ss.dirty, ss.dirtyInfos, ss.pending = nil, nil, nil
	ss.persistRequested = false
	maintain := job.maintain && !ss.active
	var compact []MessageTurn
	live := ss.liveLines
	if ss.turnsLoaded {
		live = len(ss.index.messages) + len(ss.index.parts)
	}
	compactNeeded := maintain && ss.logLines+len(entries) > 2*live+64
	if compactNeeded && ss.turnsLoaded {
		compact = cloneTurns(ss.rec.Turns)
	}
	id := ss.rec.ID
	ss.mu.Unlock()

	result := &persistResult{entries: entries, err: s.appendTurnEntries(ss, entries)}
	if result.err == nil && compactNeeded && compact == nil {
		turns, lines := s.readTurns(id)
		if lines > 2*countCanonicalTurnLines(turns)+64 {
			compact = turns
		}
	}
	if result.err == nil && compact != nil {
		lines, err := s.rewriteTurns(id, compact)
		if err == nil {
			result.rewritten, result.lines = true, lines
		} else {
			recordPersistenceError(err)
		}
	} else if result.err == nil && maintain {
		if f, err := os.Open(filepath.Join(s.sessionDir(id), sessionTurnsFile)); err == nil {
			_, err = s.historyIndex(id, f)
			_ = f.Close()
			recordPersistenceError(err)
		}
	}
	ss.persistResult = result
	ss.diskMu.Unlock()

	ss.mu.Lock()
	ss.diskMu.Lock()
	s.applyPersistResultLocked(ss)
	ss.diskMu.Unlock()
	if ss.persistJob == job {
		ss.persistJob = nil
		if ss.persistRequested {
			s.schedulePersistLocked(ss)
		}
	}
	ss.mu.Unlock()
}

func (s *Store) syncTurnLogLocked(ss *storedSession) error {
	if !ss.needsSync {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(s.sessionDir(ss.rec.ID), sessionTurnsFile), os.O_WRONLY, 0o600)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("sync transcript: %w", err)
	}
	ss.needsSync = false
	return nil
}
