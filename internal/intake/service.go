package intake

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnauthorizedToken       = errors.New("invalid or expired intake link")
	ErrLinkNotFound            = errors.New("intake link not found")
	ErrTaskNotFound            = errors.New("intake task not found")
	ErrTaskBusy                = errors.New("intake task is currently running")
	ErrTooManyPending          = errors.New("too many pending tasks on this intake link")
	ErrAttachmentQuotaExceeded = errors.New("too many unsubmitted or total attachments on this intake link")
	ErrUploadRateLimited       = errors.New("attachment upload rate limit exceeded; please retry shortly")
)

// CreateLinkInput creates a scoped tester intake link for one project directory.
type CreateLinkInput struct {
	Directory   string `json:"directory"`
	ProjectName string `json:"projectName,omitempty"`
	Label       string `json:"label"`
	ExpiresInHr int    `json:"expiresInHours,omitempty"`
}

// SubmitTaskInput is submitted by a part-time tester (never auto-executes).
type SubmitTaskInput struct {
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Steps         string   `json:"steps,omitempty"`
	AttachmentIDs []string `json:"attachmentIds,omitempty"`
}

// PortalFeedbackInput records tester retest results or supplementary notes.
type PortalFeedbackInput struct {
	TaskID        string   `json:"taskId"`
	Action        string   `json:"action"` // "pass" | "reopen" | "comment"
	Text          string   `json:"text,omitempty"`
	AttachmentIDs []string `json:"attachmentIds,omitempty"`
}

// ApproveTaskInput authorizes a staged/reopened task to execute on the host.
type ApproveTaskInput struct {
	TaskID    string `json:"taskId"`
	Directory string `json:"directory,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Provider  string `json:"providerID,omitempty"`
	Model     string `json:"model,omitempty"`
}

// UpdateTaskStatusInput updates developer review state (mark retest, reject, resolve, cancel).
type UpdateTaskStatusInput struct {
	TaskID        string `json:"taskId"`
	Status        string `json:"status"` // "awaiting_retest" | "rejected" | "resolved" | "cancel"
	DeveloperNote string `json:"developerNote,omitempty"`
}

// Service coordinates project-scoped tester intake links, staged tasks, and sanitized streams.
type Service struct {
	mu                   sync.Mutex
	store                *diskStore
	loadErr              error
	seq                  uint64
	links                map[string]Link
	tasks                map[string]TaskItem
	attachmentLink       map[string]string
	attachmentUploadedAt map[string]int64
	uploadHistory        map[string][]int64
	approving            map[string]bool
	running              map[string]context.CancelFunc
	portalSubs           map[string]map[chan SanitizedStreamEvent]struct{}
	nowFn                func() time.Time

	attachments    Attachments
	sessions       Sessions
	endpoints      func() EndpointInfo
	onLinksChanged func()
}

// NewService opens the intake registry at storePath.
func NewService(storePath string, attachments Attachments, sessions Sessions, endpoints func() EndpointInfo) *Service {
	ds := newDiskStore(storePath)
	doc, err := ds.load()
	now := time.Now().UnixMilli()
	recovered := false
	// Any task left in "running" across a process restart settles conservatively to "failed"
	// so the owner explicitly re-approves rather than silently re-running.
	for id, t := range doc.Tasks {
		if t.Status == StatusRunning {
			t.Status = StatusFailed
			t.Error = "interrupted by host restart"
			t.UpdatedAt = now
			doc.Tasks[id] = t
			recovered = true
		}
	}
	var initSeq uint64
	if err == nil && recovered {
		initSeq = 1
		if saveErr := ds.saveSnapshot(initSeq, doc); saveErr != nil {
			err = saveErr
		}
	}
	return &Service{
		store:                ds,
		loadErr:              err,
		seq:                  initSeq,
		links:                doc.Links,
		tasks:                doc.Tasks,
		attachmentLink:       doc.AttachmentLink,
		attachmentUploadedAt: doc.AttachmentUploadedAt,
		uploadHistory:        make(map[string][]int64),
		approving:            make(map[string]bool),
		running:              make(map[string]context.CancelFunc),
		portalSubs:           make(map[string]map[chan SanitizedStreamEvent]struct{}),
		nowFn:                time.Now,
		attachments:          attachments,
		sessions:             sessions,
		endpoints:            endpoints,
	}
}

func (s *Service) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// LoadError returns any startup ledger corruption or recovery persistence error.
func (s *Service) LoadError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadErr
}

func (s *Service) snapshotLocked() (uint64, storeDoc) {
	s.seq++
	links := make(map[string]Link, len(s.links))
	for k, v := range s.links {
		links[k] = v
	}
	tasks := make(map[string]TaskItem, len(s.tasks))
	for k, v := range s.tasks {
		tasks[k] = cloneTask(v)
	}
	attMap := make(map[string]string, len(s.attachmentLink))
	for k, v := range s.attachmentLink {
		attMap[k] = v
	}
	upMap := make(map[string]int64, len(s.attachmentUploadedAt))
	for k, v := range s.attachmentUploadedAt {
		upMap[k] = v
	}
	return s.seq, storeDoc{
		Version:              1,
		Links:                links,
		Tasks:                tasks,
		AttachmentLink:       attMap,
		AttachmentUploadedAt: upMap,
	}
}

func (s *Service) referencedAttachmentIDsLocked() (referenced map[string]bool, sessionBacked map[string]bool) {
	referenced = make(map[string]bool)
	sessionBacked = make(map[string]bool)
	for _, t := range s.tasks {
		hasSession := strings.TrimSpace(t.SessionID) != ""
		for _, a := range t.Attachments {
			referenced[a.ID] = true
			if hasSession {
				sessionBacked[a.ID] = true
			}
		}
		for _, c := range t.Comments {
			for _, a := range c.Attachments {
				referenced[a.ID] = true
				if hasSession {
					sessionBacked[a.ID] = true
				}
			}
		}
	}
	return referenced, sessionBacked
}

func (s *Service) sweepExpiredOrphansLocked(nowMs int64) []string {
	referenced, _ := s.referencedAttachmentIDsLocked()
	ttlMs := int64(OrphanAttachmentTTL / time.Millisecond)
	var evicted []string
	for attID := range s.attachmentLink {
		if referenced[attID] {
			continue
		}
		uploadedAt := s.attachmentUploadedAt[attID]
		if uploadedAt > 0 && nowMs-uploadedAt >= ttlMs {
			delete(s.attachmentLink, attID)
			delete(s.attachmentUploadedAt, attID)
			evicted = append(evicted, attID)
		}
	}
	return evicted
}

func (s *Service) checkLinkUploadQuotaLocked(linkID string, nowMs int64) ([]int64, error) {
	cutoff := nowMs - int64(UploadRateWindow/time.Millisecond)
	var activeWindow []int64
	for _, ts := range s.uploadHistory[linkID] {
		if ts > cutoff {
			activeWindow = append(activeWindow, ts)
		}
	}
	s.uploadHistory[linkID] = activeWindow
	if len(activeWindow) >= MaxUploadsPerLinkWindow {
		return activeWindow, ErrUploadRateLimited
	}
	referenced, _ := s.referencedAttachmentIDsLocked()
	totalForLink := 0
	orphansForLink := 0
	for attID, owner := range s.attachmentLink {
		if owner != linkID {
			continue
		}
		totalForLink++
		if !referenced[attID] {
			orphansForLink++
		}
	}
	if totalForLink >= MaxTotalAttachmentsPerLink || orphansForLink >= MaxOrphanAttachmentsPerLink {
		return activeWindow, ErrAttachmentQuotaExceeded
	}
	return activeWindow, nil
}

func (s *Service) deleteAttachmentBlobs(ids []string) {
	if s.attachments == nil || len(ids) == 0 {
		return
	}
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			_ = s.attachments.Delete(id)
		}
	}
}

// SavePortalAttachment saves an uploaded screenshot/log scoped strictly to link with rate-limit and orphan quota enforcement.
func (s *Service) SavePortalAttachment(link Link, filename, mime string, data []byte) (AttachmentRef, error) {
	if s.attachments == nil {
		return AttachmentRef{}, errors.New("attachment storage unavailable")
	}
	if len(data) == 0 {
		return AttachmentRef{}, errors.New("empty file")
	}
	maxBytes := s.attachments.MaxBytes()
	if maxBytes <= 0 {
		maxBytes = DefaultMaxAttachmentBytes
	}
	if len(data) > maxBytes {
		return AttachmentRef{}, fmt.Errorf("attachment exceeds maximum size (%d MB)", maxBytes>>20)
	}

	s.mu.Lock()
	if s.loadErr != nil {
		err := s.loadErr
		s.mu.Unlock()
		return AttachmentRef{}, err
	}
	curLink, ok := s.links[link.ID]
	if !ok || !curLink.Enabled {
		s.mu.Unlock()
		return AttachmentRef{}, ErrUnauthorizedToken
	}
	nowMs := s.now().UnixMilli()
	if curLink.ExpiresAt > 0 && nowMs > curLink.ExpiresAt {
		s.mu.Unlock()
		return AttachmentRef{}, ErrUnauthorizedToken
	}
	evicted := s.sweepExpiredOrphansLocked(nowMs)
	_, quotaErr := s.checkLinkUploadQuotaLocked(link.ID, nowMs)
	var preSweepSeq uint64
	var preSweepSnap storeDoc
	if len(evicted) > 0 && quotaErr != nil {
		preSweepSeq, preSweepSnap = s.snapshotLocked()
	}
	s.mu.Unlock()

	if len(evicted) > 0 {
		s.deleteAttachmentBlobs(evicted)
		if quotaErr != nil && preSweepSeq > 0 {
			_ = s.store.saveSnapshot(preSweepSeq, preSweepSnap)
		}
	}
	if quotaErr != nil {
		return AttachmentRef{}, quotaErr
	}

	meta, err := s.attachments.Save(link.Directory, filename, mime, data)
	if err != nil {
		return AttachmentRef{}, err
	}

	s.mu.Lock()
	if s.loadErr != nil {
		loadErr := s.loadErr
		s.mu.Unlock()
		_ = s.attachments.Delete(meta.ID)
		return AttachmentRef{}, loadErr
	}
	curLink, ok = s.links[link.ID]
	if !ok || !curLink.Enabled {
		s.mu.Unlock()
		_ = s.attachments.Delete(meta.ID)
		return AttachmentRef{}, ErrUnauthorizedToken
	}
	nowMs = s.now().UnixMilli()
	activeWindow, postQuotaErr := s.checkLinkUploadQuotaLocked(link.ID, nowMs)
	if postQuotaErr != nil {
		s.mu.Unlock()
		_ = s.attachments.Delete(meta.ID)
		return AttachmentRef{}, postQuotaErr
	}
	s.attachmentLink[meta.ID] = link.ID
	s.attachmentUploadedAt[meta.ID] = nowMs
	s.uploadHistory[link.ID] = append(activeWindow, nowMs)
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, snap); err != nil {
		s.mu.Lock()
		delete(s.attachmentLink, meta.ID)
		delete(s.attachmentUploadedAt, meta.ID)
		s.mu.Unlock()
		_ = s.attachments.Delete(meta.ID)
		return AttachmentRef{}, err
	}
	meta.URL = "/api/hoxi/intake/portal/attachments/" + meta.ID + "?token=" + link.Token
	return meta, nil
}

// ReadPortalAttachment verifies attID belongs to link before returning file bytes.
func (s *Service) ReadPortalAttachment(link Link, attID string) (AttachmentRef, []byte, error) {
	if s.attachments == nil {
		return AttachmentRef{}, nil, ErrUnauthorizedToken
	}
	attID = strings.TrimSpace(attID)
	s.mu.Lock()
	ownerLink := s.attachmentLink[attID]
	s.mu.Unlock()
	if ownerLink == "" || ownerLink != link.ID {
		return AttachmentRef{}, nil, ErrUnauthorizedToken
	}
	return s.attachments.ReadBytes(attID)
}

func (s *Service) resolveLinkAttachmentsLocked(linkID string, ids []string) ([]AttachmentRef, error) {
	if len(ids) > MaxAttachmentsPerSubmit {
		return nil, fmt.Errorf("at most %d attachments per submission", MaxAttachmentsPerSubmit)
	}
	if len(ids) == 0 || s.attachments == nil {
		return nil, nil
	}
	seen := make(map[string]bool, len(ids))
	var refs []AttachmentRef
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if s.attachmentLink[id] != linkID {
			return nil, errors.New("attachment does not belong to this intake link")
		}
		meta, err := s.attachments.Get(id)
		if err != nil {
			return nil, errors.New("attachment not found: " + id)
		}
		refs = append(refs, AttachmentRef{
			ID:       meta.ID,
			Filename: meta.Filename,
			Mime:     meta.Mime,
			Size:     meta.Size,
			Kind:     meta.Kind,
		})
	}
	return refs, nil
}

// SubmitPortalTask persists a new task in "staged" status without executing anything.
func (s *Service) SubmitPortalTask(link Link, in SubmitTaskInput) (PortalTask, error) {
	title := strings.TrimSpace(in.Title)
	desc := strings.TrimSpace(in.Description)
	steps := strings.TrimSpace(in.Steps)
	if title == "" {
		return PortalTask{}, errors.New("task title is required")
	}
	if len(title) > MaxTitleBytes {
		return PortalTask{}, errors.New("task title is too long")
	}
	if desc == "" && steps == "" {
		return PortalTask{}, errors.New("task description or steps is required")
	}
	if len(desc)+len(steps) > MaxBodyBytes {
		return PortalTask{}, errors.New("task description exceeds size limit")
	}

	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return PortalTask{}, s.loadErr
	}
	pendingCount := 0
	for _, t := range s.tasks {
		if t.LinkID == link.ID && (t.Status == StatusStaged || t.Status == StatusReopened) {
			pendingCount++
		}
	}
	if pendingCount >= MaxPendingTasksPerLink {
		s.mu.Unlock()
		return PortalTask{}, ErrTooManyPending
	}
	refs, err := s.resolveLinkAttachmentsLocked(link.ID, in.AttachmentIDs)
	if err != nil {
		s.mu.Unlock()
		return PortalTask{}, err
	}
	now := s.now().UnixMilli()
	item := TaskItem{
		ID:             randomID("tsk_", 6),
		LinkID:         link.ID,
		SubmitterLabel: link.Label,
		Directory:      link.Directory,
		ProjectName:    link.ProjectName,
		Title:          title,
		Description:    desc,
		Steps:          steps,
		Attachments:    refs,
		Comments: []TaskComment{{
			ID:          randomID("cmt_", 4),
			AuthorRole:  "tester",
			AuthorLabel: link.Label,
			Action:      "submit",
			Text:        strings.TrimSpace(strings.Join([]string{desc, steps}, "\n\n")),
			Attachments: cloneAttachments(refs),
			CreatedAt:   now,
		}},
		Status:    StatusStaged, // Strictly staged: never executes until owner approves.
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.tasks[item.ID] = item
	prunedBlobs := s.pruneCompletedTasksLocked(link.Directory)
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, snap); err != nil {
		s.mu.Lock()
		delete(s.tasks, item.ID)
		s.mu.Unlock()
		return PortalTask{}, err
	}
	s.deleteAttachmentBlobs(prunedBlobs)
	s.broadcastPortal(link.ID, SanitizedStreamEvent{Type: "task.updated", TaskID: item.ID})
	return toPortalTask(item, link.Token), nil
}

func (s *Service) pruneCompletedTasksLocked(directory string) []string {
	var dirTasks []TaskItem
	for _, t := range s.tasks {
		if t.Directory == directory {
			dirTasks = append(dirTasks, t)
		}
	}
	if len(dirTasks) <= MaxTasksPerProject {
		return nil
	}
	sort.Slice(dirTasks, func(i, j int) bool { return dirTasks[i].UpdatedAt < dirTasks[j].UpdatedAt })
	excess := len(dirTasks) - MaxTasksPerProject
	prunedCandidates := make(map[string]bool)
	prunedSessionless := make(map[string]bool)
	for _, t := range dirTasks {
		if excess <= 0 {
			break
		}
		if t.Status == StatusResolved || t.Status == StatusRejected {
			hasSession := strings.TrimSpace(t.SessionID) != ""
			for _, a := range t.Attachments {
				prunedCandidates[a.ID] = true
				if !hasSession {
					prunedSessionless[a.ID] = true
				}
			}
			for _, c := range t.Comments {
				for _, a := range c.Attachments {
					prunedCandidates[a.ID] = true
					if !hasSession {
						prunedSessionless[a.ID] = true
					}
				}
			}
			delete(s.tasks, t.ID)
			excess--
		}
	}
	if len(prunedCandidates) == 0 {
		return nil
	}
	referenced, sessionBacked := s.referencedAttachmentIDsLocked()
	var blobsToDelete []string
	for attID := range prunedCandidates {
		if !referenced[attID] {
			delete(s.attachmentLink, attID)
			delete(s.attachmentUploadedAt, attID)
			if prunedSessionless[attID] && !sessionBacked[attID] {
				blobsToDelete = append(blobsToDelete, attID)
			}
		}
	}
	return blobsToDelete
}

// SubmitPortalFeedback records tester pass / reopen / comment without auto-starting execution.
func (s *Service) SubmitPortalFeedback(link Link, in PortalFeedbackInput) (PortalTask, error) {
	taskID := strings.TrimSpace(in.TaskID)
	action := strings.ToLower(strings.TrimSpace(in.Action))
	note := strings.TrimSpace(in.Text)
	if action != "pass" && action != "reopen" && action != "comment" {
		return PortalTask{}, errors.New("action must be pass, reopen, or comment")
	}
	if (action == "reopen" || action == "comment") && note == "" && len(in.AttachmentIDs) == 0 {
		return PortalTask{}, errors.New("feedback text or attachment is required")
	}
	if len(note) > MaxBodyBytes {
		return PortalTask{}, errors.New("feedback text is too long")
	}

	s.mu.Lock()
	if s.loadErr != nil {
		s.mu.Unlock()
		return PortalTask{}, s.loadErr
	}
	item, ok := s.tasks[taskID]
	if !ok || item.LinkID != link.ID {
		s.mu.Unlock()
		return PortalTask{}, ErrTaskNotFound
	}
	prevItem := cloneTask(item)
	refs, err := s.resolveLinkAttachmentsLocked(link.ID, in.AttachmentIDs)
	if err != nil {
		s.mu.Unlock()
		return PortalTask{}, err
	}
	now := s.now().UnixMilli()
	if note != "" || len(refs) > 0 || action == "pass" {
		commentText := note
		if commentText == "" && action == "pass" {
			commentText = "复测通过"
		}
		item.Comments = append(item.Comments, TaskComment{
			ID:          randomID("cmt_", 4),
			AuthorRole:  "tester",
			AuthorLabel: link.Label,
			Action:      action,
			Text:        commentText,
			Attachments: refs,
			CreatedAt:   now,
		})
	}
	switch action {
	case "pass":
		item.Status = StatusResolved
		item.CompletedAt = now
	case "reopen":
		if item.Status != StatusRunning {
			item.Status = StatusReopened // Staged for owner re-approval; never auto-executes.
		}
	}
	item.UpdatedAt = now
	s.tasks[item.ID] = item
	seq, snap := s.snapshotLocked()
	s.mu.Unlock()

	if err := s.store.saveSnapshot(seq, snap); err != nil {
		s.mu.Lock()
		s.tasks[item.ID] = prevItem
		s.mu.Unlock()
		return PortalTask{}, err
	}
	s.broadcastPortal(link.ID, SanitizedStreamEvent{Type: "task.updated", TaskID: item.ID})
	return toPortalTask(item, link.Token), nil
}
