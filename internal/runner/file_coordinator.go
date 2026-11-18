package runner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxRecentCoordinatedFiles   = 4096
	defaultFileLeaseMaxWait     = 5 * time.Minute
	defaultHookFileLeaseMaxWait = 45 * time.Second
)

// NativeFileEditCoordination transfers cross-session lease and peer-edit state
// across the narrow runner -> gateway -> agent boundary.
type NativeFileEditCoordination struct {
	Release        func()
	Waited         bool
	ModifiedByPeer map[string]bool
}

type diskFileStamp struct {
	exists bool
	size   int64
	sha256 [32]byte
}

func probeDiskFileStamp(path string) diskFileStamp {
	raw, err := os.ReadFile(path)
	if err != nil {
		return diskFileStamp{}
	}
	return diskFileStamp{
		exists: true,
		size:   int64(len(raw)),
		sha256: sha256.Sum256(raw),
	}
}

type ownerLeaseGroup struct {
	ownerID     string
	done        chan struct{}
	paths       map[string]struct{}
	initialDisk map[string]diskFileStamp
}

type recentCoordEdit struct {
	ownerID string
	seq     uint64
}

type coordPathMutex struct {
	mu   sync.Mutex
	refs int
}

// fileEditCoordinator serializes concurrent file edits across multiple active
// sessions sharing the same working directory (such as two local-mode tasks on
// the main branch) while keeping unrelated files 100% parallel.
type fileEditCoordinator struct {
	mu            sync.Mutex
	maxWait       time.Duration
	hookMaxWait   time.Duration
	isOwnerActive func(ownerID string) bool

	seq          uint64
	turnStartSeq map[string]uint64
	ownerTurns   map[string]*ownerLeaseGroup
	leases       map[string]*ownerLeaseGroup
	waitingOn    map[string]string
	recentEdits  map[string]recentCoordEdit
	recentOrder  []string

	pathMu    sync.Mutex
	pathLocks map[string]*coordPathMutex
}

func newFileEditCoordinator(isOwnerActive func(string) bool) *fileEditCoordinator {
	return &fileEditCoordinator{
		maxWait:       defaultFileLeaseMaxWait,
		hookMaxWait:   defaultHookFileLeaseMaxWait,
		isOwnerActive: isOwnerActive,
		turnStartSeq:  make(map[string]uint64),
		ownerTurns:    make(map[string]*ownerLeaseGroup),
		leases:        make(map[string]*ownerLeaseGroup),
		waitingOn:     make(map[string]string),
		recentEdits:   make(map[string]recentCoordEdit),
		pathLocks:     make(map[string]*coordPathMutex),
	}
}

func canonicalCoordFileKey(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(trimmed); err == nil && real != "" {
		return filepath.Clean(real)
	}
	abs := trimmed
	if a, err := filepath.Abs(trimmed); err == nil && a != "" {
		abs = a
	}
	cleanAbs := filepath.Clean(abs)
	cur := cleanAbs
	var tail []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
		if realCur, err := filepath.EvalSymlinks(cur); err == nil && realCur != "" {
			for i := len(tail) - 1; i >= 0; i-- {
				realCur = filepath.Join(realCur, tail[i])
			}
			return filepath.Clean(realCur)
		}
	}
	return cleanAbs
}

func (c *fileEditCoordinator) BeginTurn(ownerID string) {
	ownerID = strings.TrimSpace(ownerID)
	if c == nil || ownerID == "" {
		return
	}
	c.mu.Lock()
	if _, exists := c.turnStartSeq[ownerID]; !exists {
		c.turnStartSeq[ownerID] = c.seq
	}
	c.mu.Unlock()
}

func (c *fileEditCoordinator) ReleaseTurn(ownerID string) {
	ownerID = strings.TrimSpace(ownerID)
	if c == nil || ownerID == "" {
		return
	}
	c.mu.Lock()
	c.releaseTurnLocked(ownerID)
	c.mu.Unlock()
}

func (c *fileEditCoordinator) ForgetSession(ownerID string) {
	c.ReleaseTurn(ownerID)
}

func (c *fileEditCoordinator) releaseTurnLocked(ownerID string) {
	delete(c.turnStartSeq, ownerID)
	delete(c.waitingOn, ownerID)
	grp := c.ownerTurns[ownerID]
	if grp == nil {
		return
	}
	for k := range grp.paths {
		if c.leases[k] == grp {
			delete(c.leases, k)
		}
	}
	delete(c.ownerTurns, ownerID)
	close(grp.done)
}

func (c *fileEditCoordinator) WaitingHolder(ownerID string) (string, bool) {
	ownerID = strings.TrimSpace(ownerID)
	if c == nil || ownerID == "" {
		return "", false
	}
	c.mu.Lock()
	holder, ok := c.waitingOn[ownerID]
	c.mu.Unlock()
	return holder, ok && holder != ""
}

func (c *fileEditCoordinator) claimKeyLocked(ownerID, key, rawPath string) {
	if ownerID == "" || key == "" {
		return
	}
	if c.isOwnerActive != nil && !c.isOwnerActive(ownerID) {
		return
	}
	grp := c.ownerTurns[ownerID]
	if grp == nil {
		grp = &ownerLeaseGroup{
			ownerID:     ownerID,
			done:        make(chan struct{}),
			paths:       make(map[string]struct{}),
			initialDisk: make(map[string]diskFileStamp),
		}
		c.ownerTurns[ownerID] = grp
	}
	if _, already := grp.paths[key]; !already {
		grp.paths[key] = struct{}{}
		if rawPath != "" {
			grp.initialDisk[key] = probeDiskFileStamp(rawPath)
		}
	}
	c.leases[key] = grp
}

func (c *fileEditCoordinator) storeRecentLocked(key string, rec recentCoordEdit) {
	if _, exists := c.recentEdits[key]; !exists {
		for len(c.recentOrder) >= maxRecentCoordinatedFiles {
			oldest := c.recentOrder[0]
			c.recentOrder = c.recentOrder[1:]
			delete(c.recentEdits, oldest)
		}
		c.recentOrder = append(c.recentOrder, key)
	}
	c.recentEdits[key] = rec
}

func (c *fileEditCoordinator) RecordModified(ownerID string, rawPaths []string) {
	ownerID = strings.TrimSpace(ownerID)
	if c == nil || len(rawPaths) == 0 {
		return
	}
	c.mu.Lock()
	c.seq++
	seq := c.seq
	for _, raw := range rawPaths {
		k := canonicalCoordFileKey(raw)
		if k == "" {
			continue
		}
		if ownerID != "" && (c.leases[k] == nil || c.leases[k].ownerID == ownerID) {
			c.claimKeyLocked(ownerID, k, raw)
		}
		if ownerID != "" {
			c.storeRecentLocked(k, recentCoordEdit{ownerID: ownerID, seq: seq})
		}
	}
	c.mu.Unlock()
}

func (c *fileEditCoordinator) lockSortedKeys(keys []string) func() {
	if c == nil || len(keys) == 0 {
		return func() {}
	}
	entries := make([]*coordPathMutex, len(keys))
	c.pathMu.Lock()
	for i, k := range keys {
		e := c.pathLocks[k]
		if e == nil {
			e = &coordPathMutex{}
			c.pathLocks[k] = e
		}
		e.refs++
		entries[i] = e
	}
	c.pathMu.Unlock()

	for _, e := range entries {
		e.mu.Lock()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(entries) - 1; i >= 0; i-- {
				entries[i].mu.Unlock()
			}
			c.pathMu.Lock()
			for i, k := range keys {
				entries[i].refs--
				if entries[i].refs <= 0 && c.pathLocks[k] == entries[i] {
					delete(c.pathLocks, k)
				}
			}
			c.pathMu.Unlock()
		})
	}
}

func (c *fileEditCoordinator) AcquireAndLock(
	ctx context.Context,
	ownerID string,
	rawPaths []string,
	waitLimit time.Duration,
	onWait func(waitedPath string),
) (func(), bool, map[string]bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	if c == nil || len(rawPaths) == 0 {
		return func() {}, false, nil, nil
	}
	rawByKey := make(map[string]string, len(rawPaths))
	keys := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		k := canonicalCoordFileKey(raw)
		if k == "" {
			continue
		}
		if _, dup := rawByKey[k]; !dup {
			rawByKey[k] = raw
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return func() {}, false, nil, nil
	}
	sort.Strings(keys)
	if ownerID == "" {
		return c.lockSortedKeys(keys), false, nil, nil
	}
	if waitLimit <= 0 {
		c.mu.Lock()
		waitLimit = c.maxWait
		c.mu.Unlock()
	}

	var (
		waited        bool
		notifiedWait  = make(map[string]bool)
		baselineDisk  = make(map[string]diskFileStamp)
		deadlineTimer *time.Timer
		deadlineCh    <-chan time.Time
	)
	defer func() {
		if deadlineTimer != nil {
			deadlineTimer.Stop()
		}
	}()

	var modifiedByPeer map[string]bool
	for {
		if err := ctx.Err(); err != nil {
			return nil, waited, nil, err
		}

		c.mu.Lock()
		ownerActive := c.isOwnerActive == nil || c.isOwnerActive(ownerID)
		startSeq, hasStartSeq := c.turnStartSeq[ownerID]
		if !hasStartSeq {
			startSeq = c.seq
			if ownerActive {
				c.turnStartSeq[ownerID] = startSeq
			}
		}

		var (
			conflictKey string
			conflictGrp *ownerLeaseGroup
		)
		for _, k := range keys {
			grp := c.leases[k]
			if grp == nil || grp.ownerID == ownerID {
				continue
			}
			if c.isOwnerActive != nil && !c.isOwnerActive(grp.ownerID) {
				c.releaseTurnLocked(grp.ownerID)
				continue
			}
			if _, recorded := baselineDisk[k]; !recorded {
				if initStamp, ok := grp.initialDisk[k]; ok {
					baselineDisk[k] = initStamp
				} else {
					baselineDisk[k] = probeDiskFileStamp(rawByKey[k])
				}
			}
			conflictKey = k
			conflictGrp = grp
			break
		}

		if conflictGrp == nil {
			for _, k := range keys {
				c.claimKeyLocked(ownerID, k, rawByKey[k])
			}
			for _, k := range keys {
				peerChanged := false
				if rec, ok := c.recentEdits[k]; ok && rec.ownerID != ownerID && rec.seq > startSeq {
					peerChanged = true
				} else if waited {
					if base, ok := baselineDisk[k]; ok && probeDiskFileStamp(rawByKey[k]) != base {
						peerChanged = true
					}
				}
				if peerChanged {
					if modifiedByPeer == nil {
						modifiedByPeer = make(map[string]bool)
					}
					modifiedByPeer[k] = true
					modifiedByPeer[rawByKey[k]] = true
				}
			}
			c.mu.Unlock()
			break
		}

		for cur := conflictGrp.ownerID; cur != ""; cur = c.waitingOn[cur] {
			if cur == ownerID {
				c.mu.Unlock()
				return nil, waited, nil, fmt.Errorf(
					"concurrent file edit conflict on %q: another active session in this workspace is waiting on a file modified by this session; read or retry after the other task finishes",
					filepath.Base(rawByKey[conflictKey]),
				)
			}
		}

		c.waitingOn[ownerID] = conflictGrp.ownerID
		waitCh := conflictGrp.done
		holderID := conflictGrp.ownerID
		c.mu.Unlock()

		noticeKey := conflictKey + "\x00" + holderID
		if onWait != nil && !notifiedWait[noticeKey] {
			notifiedWait[noticeKey] = true
			onWait(rawByKey[conflictKey])
		}
		if deadlineTimer == nil && waitLimit > 0 {
			deadlineTimer = time.NewTimer(waitLimit)
			deadlineCh = deadlineTimer.C
		}

		var waitErr error
		select {
		case <-waitCh:
			waited = true
		case <-ctx.Done():
			waitErr = ctx.Err()
		case <-deadlineCh:
			waitErr = fmt.Errorf(
				"timed out waiting for another active session in this workspace to finish modifying %q",
				filepath.Base(rawByKey[conflictKey]),
			)
		}

		c.mu.Lock()
		delete(c.waitingOn, ownerID)
		c.mu.Unlock()

		if waitErr != nil {
			return nil, waited, nil, waitErr
		}
	}

	release := c.lockSortedKeys(keys)
	return release, waited, modifiedByPeer, nil
}

func (s *Service) rootLeaseOwner(sessionID string) string {
	cur := strings.TrimSpace(sessionID)
	if s == nil || cur == "" {
		return cur
	}
	for hop := 0; hop < 8; hop++ {
		if parentID, ok := s.openCodeChildParent.Load(cur); ok && strings.TrimSpace(parentID) != "" && parentID != cur {
			cur = strings.TrimSpace(parentID)
			continue
		}
		rec, ok := s.store.Meta(cur)
		if !ok || !rec.Subagent || strings.TrimSpace(rec.ParentID) == "" || rec.ParentID == cur {
			break
		}
		cur = strings.TrimSpace(rec.ParentID)
	}
	return cur
}

func (s *Service) isLeaseOwnerActive(ownerID string) bool {
	if s == nil || strings.TrimSpace(ownerID) == "" {
		return false
	}
	s.mu.Lock()
	ap := s.procs[ownerID]
	s.mu.Unlock()
	if ap == nil {
		return false
	}
	if ap.wasAborted() && !ap.wasSteered() && !ap.wasRestarting() {
		return false
	}
	return true
}

func (s *Service) beginSessionFileTurn(sessionID string) {
	if s == nil || s.fileCoord == nil {
		return
	}
	s.fileCoord.BeginTurn(s.rootLeaseOwner(sessionID))
}

func (s *Service) isWaitingOnPeerFileLease(sessionID string) bool {
	if s == nil || s.fileCoord == nil {
		return false
	}
	holderID, ok := s.fileCoord.WaitingHolder(s.rootLeaseOwner(sessionID))
	if !ok {
		return false
	}
	s.mu.Lock()
	holderAP := s.procs[holderID]
	s.mu.Unlock()
	return holderAP != nil && holderAP.stall.stalledSince.Load() == 0
}

func (s *Service) nativeCoordinateFileEdit(rec SessionRecord, ap *activeProcess) func(context.Context, []string, func(string)) (NativeFileEditCoordination, error) {
	ownerID := s.rootLeaseOwner(rec.ID)
	return func(ctx context.Context, paths []string, onWait func(string)) (NativeFileEditCoordination, error) {
		if s.fileCoord == nil {
			for _, p := range paths {
				if _, err := os.Lstat(p); os.IsNotExist(err) && s.gate != nil {
					s.gate.recordSessionCreatedFile(rec.ID, p)
				}
			}
			return NativeFileEditCoordination{Release: func() {}}, nil
		}
		release, waited, modifiedByPeer, err := s.fileCoord.AcquireAndLock(ctx, ownerID, paths, 0, func(waitedPath string) {
			if ap != nil {
				s.noteActivity(ap)
			}
			if onWait != nil {
				onWait(waitedPath)
			}
		})
		if ap != nil && waited {
			s.noteActivity(ap)
		}
		if err != nil {
			return NativeFileEditCoordination{}, err
		}
		if s.gate != nil {
			for _, p := range paths {
				if _, statErr := os.Lstat(p); os.IsNotExist(statErr) {
					s.gate.recordSessionCreatedFile(rec.ID, p)
				}
			}
		}
		return NativeFileEditCoordination{
			Release:        release,
			Waited:         waited,
			ModifiedByPeer: modifiedByPeer,
		}, nil
	}
}

func (s *Service) nativeRecordFileEdit(rec SessionRecord) func([]string) {
	ownerID := s.rootLeaseOwner(rec.ID)
	return func(paths []string) {
		if s.fileCoord != nil {
			s.fileCoord.RecordModified(ownerID, paths)
		}
	}
}

func (s *Service) acquireHookFileLease(ctx context.Context, sessionID, path string) (bool, string) {
	if s == nil || s.fileCoord == nil || strings.TrimSpace(path) == "" {
		return true, ""
	}
	ownerID := s.rootLeaseOwner(sessionID)
	s.fileCoord.mu.Lock()
	limit := s.fileCoord.hookMaxWait
	s.fileCoord.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl) - 2*time.Second; rem > 0 && rem < limit {
			limit = rem
		}
	}
	release, waited, _, err := s.fileCoord.AcquireAndLock(ctx, ownerID, []string{path}, limit, nil)
	if release != nil {
		release()
	}
	if waited {
		s.mu.Lock()
		ap := s.procs[ownerID]
		s.mu.Unlock()
		if ap != nil {
			s.noteActivity(ap)
		}
	}
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (s *Service) noteCompletedFileEdits(sessionID string, part MessagePart) {
	if s == nil || s.fileCoord == nil || part.Type != "tool" || part.State == nil || part.State.Status != "completed" {
		return
	}
	if !isRunnerEditingTool(part.Tool) {
		return
	}
	rec, ok := s.store.Meta(sessionID)
	if ok && rec.Runtime == string(RuntimeNative) {
		return
	}
	rawPaths := extractEditedPathsFromPart(part)
	if len(rawPaths) == 0 {
		return
	}
	baseDir := firstNonEmptyStr(rec.WorktreePath, rec.ProjectDir, s.rootDir)
	absPaths := make([]string, 0, len(rawPaths))
	for _, p := range rawPaths {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if !filepath.IsAbs(trimmed) && baseDir != "" {
			trimmed = filepath.Join(baseDir, trimmed)
		}
		if _, err := os.Stat(filepath.Dir(trimmed)); err == nil {
			absPaths = append(absPaths, trimmed)
		}
	}
	if len(absPaths) > 0 {
		s.fileCoord.RecordModified(s.rootLeaseOwner(sessionID), absPaths)
	}
}
