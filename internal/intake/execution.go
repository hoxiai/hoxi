package intake

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ListDesktopTasks returns tasks for directory (or all tasks when directory is empty).
func (s *Service) ListDesktopTasks(directory string) []TaskItem {
	canon := ""
	if strings.TrimSpace(directory) != "" {
		if c, err := canonicalProjectDirectory(directory); err == nil {
			canon = c
		} else {
			canon = filepath.Clean(strings.TrimSpace(directory))
		}
	}
	s.mu.Lock()
	out := make([]TaskItem, 0, len(s.tasks))
	for _, t := range s.tasks {
		if canon != "" && t.Directory != canon {
			continue
		}
		out = append(out, toDesktopTask(t))
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

// ListPortalTasks returns sanitized tasks belonging exclusively to link.
func (s *Service) ListPortalTasks(link Link) []PortalTask {
	s.mu.Lock()
	out := make([]PortalTask, 0, 8)
	for _, t := range s.tasks {
		if t.LinkID != link.ID {
			continue
		}
		out = append(out, toPortalTask(t, link.Token))
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func collectInitialTaskAttachments(t TaskItem) []AttachmentRef {
	seen := make(map[string]bool)
	var out []AttachmentRef
	for _, a := range t.Attachments {
		if !seen[a.ID] {
			seen[a.ID] = true
			out = append(out, a)
		}
	}
	for _, c := range t.Comments {
		if c.AuthorRole != "tester" {
			continue
		}
		for _, a := range c.Attachments {
			if !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, a)
			}
		}
	}
	return out
}

func latestTesterAttachments(t TaskItem) []AttachmentRef {
	for i := len(t.Comments) - 1; i >= 0; i-- {
		if t.Comments[i].AuthorRole == "tester" {
			return cloneAttachments(t.Comments[i].Attachments)
		}
	}
	return nil
}

// ApproveAndRun transitions a staged/reopened/failed task into execution in an isolated session.
// Concurrent approvals on the same taskID are serialized via s.approving, and disk write failures
// roll back in-memory state cleanly so tasks are never wedged in StatusRunning.
func (s *Service) ApproveAndRun(ctx context.Context, in ApproveTaskInput) (TaskItem, error) {
	if s.sessions == nil {
		return TaskItem{}, errors.New("runner sessions unavailable")
	}
	taskID := strings.TrimSpace(in.TaskID)
	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return TaskItem{}, s.loadErr
	}
	item, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return TaskItem{}, ErrTaskNotFound
	}
	if item.Status == StatusRunning || s.approving[taskID] {
		s.mu.Unlock()
		return TaskItem{}, ErrTaskBusy
	}
	s.approving[taskID] = true
	prevStatus := item.Status
	existingSid := strings.TrimSpace(item.SessionID)
	readOnlyPaths := cloneStringSlice(item.ReadOnlyPaths)
	writablePaths := cloneStringSlice(item.WritablePaths)
	if lnk, hasLink := s.links[item.LinkID]; hasLink {
		readOnlyPaths = cloneStringSlice(lnk.ReadOnlyPaths)
		writablePaths = cloneStringSlice(lnk.WritablePaths)
	}
	scopedItem := cloneTask(item)
	scopedItem.ReadOnlyPaths = readOnlyPaths
	scopedItem.WritablePaths = writablePaths
	var system, prompt string
	var atts []AttachmentRef
	if existingSid != "" && prevStatus == StatusReopened {
		system, prompt = BuildFollowupFixTurn(scopedItem)
		atts = latestTesterAttachments(scopedItem)
	} else {
		system, prompt = BuildInitialFixTurn(scopedItem)
		atts = collectInitialTaskAttachments(scopedItem)
	}
	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = "build"
	}
	runReq := RunSessionRequest{
		Directory:     item.Directory,
		SessionID:     existingSid,
		Title:         "QA · " + item.Title,
		System:        system,
		Prompt:        prompt,
		Attachments:   atts,
		Mode:          mode,
		Provider:      strings.TrimSpace(in.Provider),
		Model:         strings.TrimSpace(in.Model),
		ReadOnlyPaths: cloneStringSlice(readOnlyPaths),
		WritablePaths: cloneStringSlice(writablePaths),
	}
	s.mu.Unlock()

	sid, err := s.sessions.EnsureSession(ctx, runReq)
	if err != nil {
		s.mu.Lock()
		delete(s.approving, taskID)
		s.mu.Unlock()
		return TaskItem{}, err
	}
	runReq.SessionID = sid

	s.mu.Lock()
	delete(s.approving, taskID)
	cur, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return TaskItem{}, ErrTaskNotFound
	}
	if cur.Status == StatusRunning {
		s.mu.Unlock()
		return TaskItem{}, ErrTaskBusy
	}
	now := s.now().UnixMilli()
	prevTask := cloneTask(cur)
	runCtx, cancel := context.WithCancel(context.Background())
	s.running[taskID] = cancel
	delete(s.externalRuns, taskID)
	cur.SessionID = sid
	cur.ReadOnlyPaths = cloneStringSlice(readOnlyPaths)
	cur.WritablePaths = cloneStringSlice(writablePaths)
	cur.Status = StatusRunning
	cur.Error = ""
	cur.ApprovedAt = now
	cur.UpdatedAt = now
	s.tasks[taskID] = cur
	s.indexTaskSessionLocked(taskID, prevTask.SessionID, sid)
	linkID := cur.LinkID
	desktopOut := toDesktopTask(cur)
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, snap); err != nil {
		cancel()
		s.mu.Lock()
		if latest, exists := s.tasks[taskID]; exists && latest.Status == StatusRunning && latest.ApprovedAt == now {
			prevTask.SessionID = sid
			s.tasks[taskID] = prevTask
			s.indexTaskSessionLocked(taskID, latest.SessionID, sid)
			delete(s.running, taskID)
		}
		s.mu.Unlock()
		return TaskItem{}, err
	}
	s.ensureSessionWatcher()
	s.broadcastPortal(linkID, SanitizedStreamEvent{Type: "task.updated", TaskID: taskID})
	go s.executeApprovedTurn(runCtx, taskID, linkID, sid, runReq)
	return desktopOut, nil
}

func (s *Service) executeApprovedTurn(ctx context.Context, taskID, linkID, sessionID string, req RunSessionRequest) {
	stopRelay := make(chan struct{})
	if s.sessions != nil {
		updates, unsub := s.sessions.SubscribeSession(sessionID)
		if updates != nil {
			go func() {
				defer unsub()
				for {
					select {
					case <-stopRelay:
						return
					case _, ok := <-updates:
						if !ok {
							return
						}
						s.scheduleTimelineBroadcast(linkID, taskID)
					}
				}
			}()
		} else if unsub != nil {
			unsub()
		}
	}

	summary, runErr := s.sessions.RunTurn(ctx, sessionID, req)
	close(stopRelay)
	if runErr == nil && (len(req.ReadOnlyPaths) > 0 || len(req.WritablePaths) > 0) {
		if verifier, ok := s.sessions.(PathScopeVerifier); ok {
			violations, vErr := verifier.VerifyPathScope(sessionID)
			if vErr != nil {
				runErr = errors.New("目录保护边界核验失败，已暂停等待研发检查")
			} else if len(violations) > 0 {
				runErr = errors.New("检测到只读保护或允许修改范围外的文件变更，已拦截等待研发核查")
			}
		}
	}

	s.mu.Lock()
	now := s.now().UnixMilli()
	delete(s.running, taskID)
	delete(s.externalRuns, taskID)
	cur, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return
	}
	// If UpdateTaskStatus("cancel") already reset status away from StatusRunning, preserve that state.
	if cur.Status == StatusRunning {
		if strings.TrimSpace(summary) != "" {
			cur.Summary = SanitizeAssistantText(summary)
		}
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) {
				cur.Status = StatusStaged
			} else {
				cur.Status = StatusFailed
				cur.Error = ScrubLocalPaths(runErr.Error())
			}
		} else {
			cur.Status = StatusSucceeded
			cur.Error = ""
			cur.CompletedAt = now
		}
		cur.UpdatedAt = now
		s.tasks[taskID] = cur
	}
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	_ = s.store.saveSnapshot(seq, snap)
	s.flushTimelineBroadcast(linkID, taskID)
	s.broadcastPortal(linkID, SanitizedStreamEvent{Type: "task.updated", TaskID: taskID})
}

// UpdateTaskStatus updates developer review state (mark awaiting_retest, reject, resolve, or cancel).
func (s *Service) UpdateTaskStatus(in UpdateTaskStatusInput) (TaskItem, error) {
	taskID := strings.TrimSpace(in.TaskID)
	action := strings.ToLower(strings.TrimSpace(in.Status))
	note := strings.TrimSpace(in.DeveloperNote)
	if len(note) > MaxBodyBytes {
		return TaskItem{}, errors.New("developer note is too long")
	}

	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return TaskItem{}, s.loadErr
	}
	cur, ok := s.tasks[taskID]
	if !ok {
		s.mu.Unlock()
		return TaskItem{}, ErrTaskNotFound
	}
	prevTask := cloneTask(cur)
	now := s.now().UnixMilli()
	var cancelFn context.CancelFunc
	var abortSid string
	switch action {
	case StatusAwaitingRetest:
		cur.Status = StatusAwaitingRetest
		if note != "" {
			cur.DeveloperNote = note
		}
	case StatusRejected:
		cur.Status = StatusRejected
		if note != "" {
			cur.DeveloperNote = note
		}
		cur.CompletedAt = now
	case StatusResolved:
		cur.Status = StatusResolved
		if note != "" {
			cur.DeveloperNote = note
		}
		cur.CompletedAt = now
	case "cancel":
		cancelFn = s.running[taskID]
		delete(s.running, taskID)
		delete(s.externalRuns, taskID)
		abortSid = cur.SessionID
		cur.Status = StatusStaged
	default:
		s.mu.Unlock()
		return TaskItem{}, errors.New("unsupported status transition")
	}
	if note != "" {
		cur.Comments = append(cur.Comments, TaskComment{
			ID:         randomID("cmt_", 4),
			AuthorRole: "developer",
			Action:     action,
			Text:       note,
			CreatedAt:  now,
		})
	}
	cur.UpdatedAt = now
	s.tasks[taskID] = cur
	linkID := cur.LinkID
	out := toDesktopTask(cur)
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	if abortSid != "" && s.sessions != nil {
		s.sessions.Abort(abortSid)
	}
	if err := s.store.saveSnapshot(seq, snap); err != nil {
		if action != "cancel" {
			s.mu.Lock()
			s.tasks[taskID] = prevTask
			s.mu.Unlock()
		}
		return TaskItem{}, err
	}
	s.broadcastPortal(linkID, SanitizedStreamEvent{Type: "task.updated", TaskID: taskID})
	return out, nil
}

// PortalTimeline returns the code-free execution steps of taskID if owned by link.
func (s *Service) PortalTimeline(link Link, taskID string) (SanitizedTimeline, error) {
	taskID = strings.TrimSpace(taskID)
	s.mu.Lock()
	cur, ok := s.tasks[taskID]
	s.mu.Unlock()
	if !ok || cur.LinkID != link.ID {
		return SanitizedTimeline{}, ErrTaskNotFound
	}
	sid := strings.TrimSpace(cur.SessionID)
	if sid == "" || s.sessions == nil {
		return SanitizedTimeline{
			TaskID: taskID,
			Busy:   cur.Status == StatusRunning,
			Turns:  []SanitizedTurn{},
		}, nil
	}
	tl, found := s.sessions.SanitizedTimeline(sid)
	if !found {
		return SanitizedTimeline{
			TaskID: taskID,
			Busy:   cur.Status == StatusRunning,
			Turns:  []SanitizedTurn{},
		}, nil
	}
	tl.TaskID = taskID
	if cur.Status == StatusRunning {
		tl.Busy = true
	}
	return tl, nil
}

// SubscribePortal subscribes to real-time sanitized task & timeline updates for linkID.
func (s *Service) SubscribePortal(linkID string) (<-chan SanitizedStreamEvent, func()) {
	ch := make(chan SanitizedStreamEvent, 32)
	s.mu.Lock()
	subs := s.portalSubs[linkID]
	if subs == nil {
		subs = make(map[chan SanitizedStreamEvent]struct{})
		s.portalSubs[linkID] = subs
	}
	subs[ch] = struct{}{}
	s.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			if set := s.portalSubs[linkID]; set != nil {
				delete(set, ch)
				if len(set) == 0 {
					delete(s.portalSubs, linkID)
				}
			}
			s.mu.Unlock()
		})
	}
}

func (s *Service) broadcastPortal(linkID string, evt SanitizedStreamEvent) {
	if linkID == "" {
		return
	}
	s.mu.Lock()
	set := s.portalSubs[linkID]
	targets := make([]chan SanitizedStreamEvent, 0, len(set))
	for ch := range set {
		targets = append(targets, ch)
	}
	s.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- evt:
		default:
		}
	}
}

const portalTimelineThrottle = 120 * time.Millisecond

func (s *Service) indexTaskSessionLocked(taskID, prevSID, nextSID string) {
	prevSID = strings.TrimSpace(prevSID)
	nextSID = strings.TrimSpace(nextSID)
	if prevSID == nextSID {
		return
	}
	if prevSID != "" {
		if set := s.sessionTasks[prevSID]; set != nil {
			delete(set, taskID)
			if len(set) == 0 {
				delete(s.sessionTasks, prevSID)
			}
		}
	}
	if nextSID != "" {
		set := s.sessionTasks[nextSID]
		if set == nil {
			set = make(map[string]struct{})
			s.sessionTasks[nextSID] = set
		}
		set[taskID] = struct{}{}
	}
}

func (s *Service) cleanupTaskRuntimeLocked(taskID string) {
	if timer, ok := s.timelineTimers[taskID]; ok {
		timer.Stop()
		delete(s.timelineTimers, taskID)
	}
	delete(s.lastTimelineEmit, taskID)
	delete(s.externalRuns, taskID)
}

// HasLinkedSession reports whether sessionID is attached to at least one intake task.
func (s *Service) HasLinkedSession(sessionID string) bool {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessionTasks[sid]) > 0
}

// LinkedSessionIDs returns all distinct runner session IDs currently linked to intake tasks.
func (s *Service) LinkedSessionIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessionTasks) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.sessionTasks))
	for sid := range s.sessionTasks {
		out = append(out, sid)
	}
	return out
}

func (s *Service) scheduleTimelineBroadcast(linkID, taskID string) {
	if linkID == "" || taskID == "" {
		return
	}
	now := time.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	elapsed := now.Sub(s.lastTimelineEmit[taskID])
	if elapsed >= portalTimelineThrottle {
		if prev, ok := s.timelineTimers[taskID]; ok {
			prev.Stop()
			delete(s.timelineTimers, taskID)
		}
		s.lastTimelineEmit[taskID] = now
		s.mu.Unlock()
		s.broadcastPortal(linkID, SanitizedStreamEvent{Type: "timeline.updated", TaskID: taskID})
		return
	}
	if _, armed := s.timelineTimers[taskID]; armed {
		s.mu.Unlock()
		return
	}
	wait := portalTimelineThrottle - elapsed
	var myTimer *time.Timer
	myTimer = time.AfterFunc(wait, func() {
		s.mu.Lock()
		if s.closed || s.timelineTimers[taskID] != myTimer {
			s.mu.Unlock()
			return
		}
		delete(s.timelineTimers, taskID)
		s.lastTimelineEmit[taskID] = time.Now()
		s.mu.Unlock()
		s.broadcastPortal(linkID, SanitizedStreamEvent{Type: "timeline.updated", TaskID: taskID})
	})
	s.timelineTimers[taskID] = myTimer
	s.mu.Unlock()
}

func (s *Service) flushTimelineBroadcast(linkID, taskID string) {
	if linkID == "" || taskID == "" {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if prev, ok := s.timelineTimers[taskID]; ok {
		prev.Stop()
		delete(s.timelineTimers, taskID)
	}
	s.lastTimelineEmit[taskID] = time.Now()
	s.mu.Unlock()
	s.broadcastPortal(linkID, SanitizedStreamEvent{Type: "timeline.updated", TaskID: taskID})
}

// HandleSessionEvent synchronizes tester portal streams and task state when an owner
// continues a linked QA session directly from Hoxi desktop outside ApproveAndRun.
func (s *Service) HandleSessionEvent(evt SessionEvent) {
	sid := strings.TrimSpace(evt.SessionID)
	if sid == "" {
		return
	}
	type portalTarget struct {
		linkID     string
		taskID     string
		taskUpdate bool
		immediate  bool
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	linked := s.sessionTasks[sid]
	if len(linked) == 0 {
		s.mu.Unlock()
		return
	}
	now := s.now().UnixMilli()
	var targets []portalTarget
	prevTasks := make(map[string]TaskItem)
	prevExternal := make(map[string]string)
	hadExternal := make(map[string]bool)
	needSave := false

	for taskID := range linked {
		if s.running[taskID] != nil || s.approving[taskID] {
			continue
		}
		cur, ok := s.tasks[taskID]
		if !ok {
			continue
		}
		switch evt.Kind {
		case SessionEventProgress:
			targets = append(targets, portalTarget{
				linkID:    cur.LinkID,
				taskID:    taskID,
				immediate: false,
			})
		case SessionEventBusy:
			statusChanged := false
			if s.loadErr == nil && cur.Status != StatusResolved && cur.Status != StatusRejected && cur.Status != StatusRunning {
				prevTasks[taskID] = cloneTask(cur)
				prevExternal[taskID], hadExternal[taskID] = s.externalRuns[taskID]
				s.externalRuns[taskID] = cur.Status
				cur.Status = StatusRunning
				cur.Error = ""
				cur.UpdatedAt = now
				s.tasks[taskID] = cur
				needSave = true
				statusChanged = true
			}
			targets = append(targets, portalTarget{
				linkID:     cur.LinkID,
				taskID:     taskID,
				taskUpdate: statusChanged,
				immediate:  true,
			})
		case SessionEventError:
			statusChanged := false
			prevStatus, wasExt := s.externalRuns[taskID]
			delete(s.externalRuns, taskID)
			if s.loadErr == nil && cur.Status == StatusRunning {
				prevTasks[taskID] = cloneTask(cur)
				prevExternal[taskID], hadExternal[taskID] = prevStatus, wasExt
				if strings.TrimSpace(evt.Summary) != "" {
					cur.Summary = SanitizeAssistantText(evt.Summary)
				}
				errMsg := strings.TrimSpace(evt.Error)
				if errMsg == "" {
					errMsg = "execution failed"
				}
				cur.Status = StatusFailed
				cur.Error = ScrubLocalPaths(errMsg)
				cur.UpdatedAt = now
				s.tasks[taskID] = cur
				needSave = true
				statusChanged = true
			}
			targets = append(targets, portalTarget{
				linkID:     cur.LinkID,
				taskID:     taskID,
				taskUpdate: statusChanged,
				immediate:  true,
			})
		case SessionEventIdle:
			statusChanged := false
			prevStatus, wasExt := s.externalRuns[taskID]
			delete(s.externalRuns, taskID)
			if s.loadErr == nil && cur.Status == StatusRunning {
				prevTasks[taskID] = cloneTask(cur)
				prevExternal[taskID], hadExternal[taskID] = prevStatus, wasExt
				if strings.TrimSpace(evt.Error) != "" {
					if strings.TrimSpace(evt.Summary) != "" {
						cur.Summary = SanitizeAssistantText(evt.Summary)
					}
					cur.Status = StatusFailed
					cur.Error = ScrubLocalPaths(evt.Error)
				} else if !evt.Aborted && (strings.TrimSpace(evt.Summary) != "" || evt.Completed) {
					if strings.TrimSpace(evt.Summary) != "" {
						cur.Summary = SanitizeAssistantText(evt.Summary)
					}
					cur.Status = StatusSucceeded
					cur.Error = ""
					cur.CompletedAt = now
				} else if wasExt && prevStatus != "" {
					cur.Status = prevStatus
				} else {
					cur.Status = StatusSucceeded
					cur.Error = ""
					cur.CompletedAt = now
				}
				cur.UpdatedAt = now
				s.tasks[taskID] = cur
				needSave = true
				statusChanged = true
			}
			targets = append(targets, portalTarget{
				linkID:     cur.LinkID,
				taskID:     taskID,
				taskUpdate: statusChanged,
				immediate:  true,
			})
		}
	}

	var seq uint64
	var snap storeDoc
	if needSave {
		seq, snap = s.snapshotLocked()
	}
	s.mu.Unlock()

	if needSave {
		if err := s.store.saveSnapshot(seq, snap); err != nil {
			s.mu.Lock()
			for taskID, prev := range prevTasks {
				if latest, exists := s.tasks[taskID]; exists && latest.UpdatedAt == now {
					s.tasks[taskID] = prev
					if hadExternal[taskID] {
						s.externalRuns[taskID] = prevExternal[taskID]
					} else {
						delete(s.externalRuns, taskID)
					}
				}
			}
			s.mu.Unlock()
			for i := range targets {
				targets[i].taskUpdate = false
			}
		}
	}

	for _, t := range targets {
		if t.immediate {
			s.flushTimelineBroadcast(t.linkID, t.taskID)
		} else {
			s.scheduleTimelineBroadcast(t.linkID, t.taskID)
		}
		if t.taskUpdate {
			s.broadcastPortal(t.linkID, SanitizedStreamEvent{Type: "task.updated", TaskID: t.taskID})
		}
	}
}

// ForgetSession detaches deleted Hoxi sessions from intake tasks so stale IDs are never retained.
func (s *Service) ForgetSession(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	s.mu.Lock()
	changed := false
	for id, t := range s.tasks {
		if t.SessionID == sid {
			if cancel, ok := s.running[id]; ok {
				cancel()
				delete(s.running, id)
			}
			s.indexTaskSessionLocked(id, sid, "")
			s.cleanupTaskRuntimeLocked(id)
			t.SessionID = ""
			if t.Status == StatusRunning {
				t.Status = StatusStaged
			}
			s.tasks[id] = t
			changed = true
		}
	}
	if !changed || s.loadErr != nil {
		s.mu.Unlock()
		return
	}
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()
	_ = s.store.saveSnapshot(seq, snap)
}
