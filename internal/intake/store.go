package intake

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type storeDoc struct {
	Version              int                 `json:"version"`
	Links                map[string]Link     `json:"links"`
	Tasks                map[string]TaskItem `json:"tasks"`
	AttachmentLink       map[string]string   `json:"attachmentLink,omitempty"`
	AttachmentUploadedAt map[string]int64    `json:"attachmentUploadedAt,omitempty"`
}

type diskStore struct {
	path        string
	writeMu     sync.Mutex
	persistedSq uint64
	writeHook   func() error // test hook to simulate atomic write failures
}

func newDiskStore(storePath string) *diskStore {
	return &diskStore{path: strings.TrimSpace(storePath)}
}

func writeAtomic0600(targetPath string, raw []byte) error {
	if strings.TrimSpace(targetPath) == "" {
		return errors.New("storage path is empty")
	}
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-intake-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (ds *diskStore) load() (storeDoc, error) {
	empty := storeDoc{
		Version:              1,
		Links:                make(map[string]Link),
		Tasks:                make(map[string]TaskItem),
		AttachmentLink:       make(map[string]string),
		AttachmentUploadedAt: make(map[string]int64),
	}
	if ds == nil || ds.path == "" {
		return empty, nil
	}
	raw, err := os.ReadFile(ds.path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		return empty, err
	}
	var doc storeDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", ds.path, time.Now().UnixNano())
		if bErr := writeAtomic0600(backup, raw); bErr != nil {
			return empty, fmt.Errorf("corrupt intake store (%w); backup failed: %v", err, bErr)
		}
		return empty, fmt.Errorf("corrupt intake store backed up to %s: %w", filepath.Base(backup), err)
	}
	if doc.Links == nil {
		doc.Links = make(map[string]Link)
	}
	if doc.Tasks == nil {
		doc.Tasks = make(map[string]TaskItem)
	}
	if doc.AttachmentLink == nil {
		doc.AttachmentLink = make(map[string]string)
	}
	if doc.AttachmentUploadedAt == nil {
		doc.AttachmentUploadedAt = make(map[string]int64)
	}
	return doc, nil
}

func (ds *diskStore) saveSnapshot(seq uint64, doc storeDoc) error {
	if ds == nil {
		return nil
	}
	if ds.writeHook != nil {
		if err := ds.writeHook(); err != nil {
			return err
		}
	}
	if ds.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	ds.writeMu.Lock()
	defer ds.writeMu.Unlock()
	if seq <= ds.persistedSq {
		return nil
	}
	if err := writeAtomic0600(ds.path, append(raw, '\n')); err != nil {
		return err
	}
	ds.persistedSq = seq
	return nil
}

func cloneAttachments(in []AttachmentRef) []AttachmentRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]AttachmentRef, len(in))
	copy(out, in)
	return out
}

func cloneTask(t TaskItem) TaskItem {
	t.Attachments = cloneAttachments(t.Attachments)
	if len(t.Comments) > 0 {
		comments := make([]TaskComment, len(t.Comments))
		for i, c := range t.Comments {
			c.Attachments = cloneAttachments(c.Attachments)
			comments[i] = c
		}
		t.Comments = comments
	}
	return t
}

func portalAttachments(in []AttachmentRef, token string) []AttachmentRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]AttachmentRef, len(in))
	for i, a := range in {
		a.URL = "/api/hoxi/intake/portal/attachments/" + a.ID
		if token != "" {
			a.URL += "?token=" + token
		}
		out[i] = a
	}
	return out
}

func desktopAttachments(in []AttachmentRef) []AttachmentRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]AttachmentRef, len(in))
	for i, a := range in {
		a.URL = "/api/hoxi/attachments/" + a.ID + "/content"
		out[i] = a
	}
	return out
}

func toPortalTask(t TaskItem, token string) PortalTask {
	var comments []TaskComment
	if len(t.Comments) > 0 {
		comments = make([]TaskComment, len(t.Comments))
		for i, c := range t.Comments {
			c.Attachments = portalAttachments(c.Attachments, token)
			comments[i] = c
		}
	}
	return PortalTask{
		ID:             t.ID,
		SubmitterLabel: t.SubmitterLabel,
		ProjectName:    t.ProjectName,
		Title:          t.Title,
		Description:    t.Description,
		Steps:          t.Steps,
		Attachments:    portalAttachments(t.Attachments, token),
		Comments:       comments,
		Status:         t.Status,
		HasTimeline:    strings.TrimSpace(t.SessionID) != "",
		DeveloperNote:  t.DeveloperNote,
		Summary:        SanitizeAssistantText(t.Summary),
		Error:          ScrubLocalPaths(t.Error),
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
		ApprovedAt:     t.ApprovedAt,
		CompletedAt:    t.CompletedAt,
	}
}

func toDesktopTask(t TaskItem) TaskItem {
	out := cloneTask(t)
	out.Attachments = desktopAttachments(out.Attachments)
	for i := range out.Comments {
		out.Comments[i].Attachments = desktopAttachments(out.Comments[i].Attachments)
	}
	return out
}
