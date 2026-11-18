package procgroup

import (
	"os"
	"strings"
	"sync"
	"time"
)

const MaxOwnedGroupsPerSession = 256

type ownedProcessGroup struct {
	pgid       int
	leaderArgs string
}

type ownedProcessInstance struct {
	pid        int
	createTime uint64
	args       string
}

type OwnedLedger struct {
	mu       sync.Mutex
	groups   map[string][]ownedProcessGroup
	pids     map[string][]ownedProcessInstance
	watching map[string]*OwnedWatch
}

type OwnedWatch struct {
	done chan struct{}
}

func (w *OwnedWatch) Done() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.done
}

func (w *OwnedWatch) Wait(delay time.Duration) bool {
	select {
	case <-w.done:
		return false
	default:
	}
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-w.done:
		return false
	case <-timer.C:
		return true
	}
}

func NewOwnedLedger() *OwnedLedger {
	return &OwnedLedger{
		groups:   map[string][]ownedProcessGroup{},
		pids:     map[string][]ownedProcessInstance{},
		watching: map[string]*OwnedWatch{},
	}
}

func (l *OwnedLedger) StartWatch(sessionID, callID string) *OwnedWatch {
	if l == nil || sessionID == "" || callID == "" {
		return nil
	}
	key := sessionID + "\x00" + callID
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watching[key] != nil {
		return nil
	}
	watch := &OwnedWatch{done: make(chan struct{})}
	l.watching[key] = watch
	return watch
}

func (l *OwnedLedger) EndWatch(sessionID, callID string, watch *OwnedWatch) {
	if l == nil {
		return
	}
	l.mu.Lock()
	key := sessionID + "\x00" + callID
	if l.watching[key] == watch {
		delete(l.watching, key)
	}
	l.mu.Unlock()
}

func (l *OwnedLedger) Forget(sessionID string) {
	if l == nil || sessionID == "" {
		return
	}
	prefix := sessionID + "\x00"
	l.mu.Lock()
	delete(l.groups, sessionID)
	delete(l.pids, sessionID)
	for k := range l.watching {
		if strings.HasPrefix(k, prefix) {
			close(l.watching[k].done)
			delete(l.watching, k)
		}
	}
	l.mu.Unlock()
}

func (l *OwnedLedger) Record(sessionID string, pgid int, leaderArgs string) {
	if l == nil || sessionID == "" || pgid <= 1 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recordLocked(sessionID, pgid, leaderArgs)
}

func (l *OwnedLedger) RecordWatched(sessionID, callID string, watch *OwnedWatch, pgid int, leaderArgs string) {
	if l == nil || watch == nil || pgid <= 1 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watching[sessionID+"\x00"+callID] == watch {
		l.recordLocked(sessionID, pgid, leaderArgs)
	}
}

func (l *OwnedLedger) recordLocked(sessionID string, pgid int, leaderArgs string) {
	groups := l.groups[sessionID]
	for i, g := range groups {
		if g.pgid == pgid {
			groups[i].leaderArgs = leaderArgs
			return
		}
	}
	groups = append(groups, ownedProcessGroup{pgid: pgid, leaderArgs: leaderArgs})
	if len(groups) > MaxOwnedGroupsPerSession {
		groups = groups[len(groups)-MaxOwnedGroupsPerSession:]
	}
	l.groups[sessionID] = groups
}

func (l *OwnedLedger) RecordWatchedPID(sessionID, callID string, watch *OwnedWatch, pid int, createTime uint64, args string) {
	if l == nil || watch == nil || pid <= 4 || createTime == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watching[sessionID+"\x00"+callID] == watch {
		l.recordPIDLocked(sessionID, pid, createTime, args)
	}
}

func (l *OwnedLedger) RecordWindowsSubtree(sessionID string, rootPID int) {
	if l == nil || sessionID == "" || rootPID <= 4 {
		return
	}
	byPID, children := ReadProcessSnapshots()
	rootSnap, ok := byPID[rootPID]
	if !ok || rootSnap.CreateTime == 0 {
		return
	}
	selfPID := os.Getpid()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, pid := range CollectSubtreePIDs(rootPID, children) {
		if pid <= 4 || pid == selfPID {
			continue
		}
		cSnap, exists := byPID[pid]
		if !exists || cSnap.CreateTime == 0 || cSnap.CreateTime < rootSnap.CreateTime || IsAgentRuntimeProcessArgs(cSnap.Args) {
			continue
		}
		l.recordPIDLocked(sessionID, pid, cSnap.CreateTime, cSnap.Args)
	}
}

func (l *OwnedLedger) recordPIDLocked(sessionID string, pid int, createTime uint64, args string) {
	if sessionID == "" || pid <= 4 || createTime == 0 {
		return
	}
	list := l.pids[sessionID]
	for i, inst := range list {
		if inst.pid == pid {
			list[i] = ownedProcessInstance{pid: pid, createTime: createTime, args: args}
			return
		}
	}
	list = append(list, ownedProcessInstance{pid: pid, createTime: createTime, args: args})
	if len(list) > MaxOwnedGroupsPerSession {
		list = list[len(list)-MaxOwnedGroupsPerSession:]
	}
	l.pids[sessionID] = list
}

func (l *OwnedLedger) LivePIDs(sessionID string, byPID map[int]Snapshot, children map[int][]int) map[int]bool {
	if l == nil || len(byPID) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pids) == 0 {
		return nil
	}
	out := map[int]bool{}
	for sid, list := range l.pids {
		kept := list[:0]
		seen := make(map[int]bool, len(list))
		for _, inst := range list {
			snap, alive := byPID[inst.pid]
			if !alive || snap.CreateTime == 0 || snap.CreateTime != inst.createTime {
				continue
			}
			if inst.args != "" && snap.Args != "" && snap.Args != inst.args {
				continue
			}
			if !seen[inst.pid] {
				seen[inst.pid] = true
				kept = append(kept, inst)
			}
		}
		for idx := 0; idx < len(kept); idx++ {
			parent := kept[idx]
			for _, childPID := range children[parent.pid] {
				if childPID <= 4 || seen[childPID] {
					continue
				}
				cSnap, ok := byPID[childPID]
				if !ok || cSnap.CreateTime == 0 || cSnap.CreateTime < parent.createTime || IsAgentRuntimeProcessArgs(cSnap.Args) {
					continue
				}
				seen[childPID] = true
				kept = append(kept, ownedProcessInstance{
					pid:        childPID,
					createTime: cSnap.CreateTime,
					args:       cSnap.Args,
				})
			}
		}
		if len(kept) > MaxOwnedGroupsPerSession {
			kept = kept[len(kept)-MaxOwnedGroupsPerSession:]
		}
		if len(kept) == 0 {
			delete(l.pids, sid)
			continue
		}
		l.pids[sid] = kept
		if sid == sessionID {
			for _, inst := range kept {
				out[inst.pid] = true
			}
		}
	}
	return out
}

func (l *OwnedLedger) LiveGroups(sessionID string, byPID map[int]Snapshot) map[int]bool {
	if l == nil || len(byPID) == 0 {
		return nil
	}
	held := make(map[int]bool, len(byPID))
	for _, snap := range byPID {
		if snap.PGID > 1 {
			held[snap.PGID] = true
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[int]bool{}
	for sid, groups := range l.groups {
		kept := groups[:0]
		for _, g := range groups {
			if !held[g.pgid] {
				continue
			}
			if leader, alive := byPID[g.pgid]; alive && leader.Args != g.leaderArgs {
				continue
			}
			kept = append(kept, g)
			if sid == sessionID {
				out[g.pgid] = true
			}
		}
		if len(kept) == 0 {
			delete(l.groups, sid)
		} else {
			l.groups[sid] = kept
		}
	}
	return out
}

func FindOpenCodeShellGroup(byPID map[int]Snapshot, children map[int][]int, port, marker string) int {
	daemon := SelectOpenCodeDaemonPID(byPID, children, port)
	if daemon <= 1 || marker == "" {
		return 0
	}
	for _, child := range children[daemon] {
		snap := byPID[child]
		if child > 1 && snap.PGID == child && ArgsCarryStopMarker(snap.Args, marker) {
			return child
		}
	}
	return 0
}

func RuntimeShellGroups(byPID map[int]Snapshot, children map[int][]int, root, protected int) []int {
	var out []int
	for _, pid := range CollectSubtreePIDs(root, children) {
		if snap, ok := byPID[pid]; ok && pid > 1 && snap.PGID == pid && pid != protected {
			out = append(out, pid)
		}
	}
	return out
}
