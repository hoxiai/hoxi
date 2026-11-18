package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const (
	ocMigrationJournalFile    = "_opencode_migration_journal.json"
	ocMigrationReportFile     = "_opencode_migration_report.json"
	ocMigrationJournalVersion = 1
)

// OpenCodeMigrationStatus classifies the outcome of migrating one legacy session.
type OpenCodeMigrationStatus string

const (
	OCMigrateCreated  OpenCodeMigrationStatus = "migrated"
	OCMigrateUpgraded OpenCodeMigrationStatus = "upgraded"
	OCMigrateSkipped  OpenCodeMigrationStatus = "skipped"
	OCMigrateFailed   OpenCodeMigrationStatus = "failed"
)

// OpenCodeMigrationItem reports the per-session migration outcome and failure reason.
type OpenCodeMigrationItem struct {
	SessionID      string                  `json:"sessionID"`
	SourceID       string                  `json:"sourceID,omitempty"`
	ParentID       string                  `json:"parentID,omitempty"`
	Title          string                  `json:"title,omitempty"`
	Directory      string                  `json:"directory,omitempty"`
	SourceDB       string                  `json:"sourceDB,omitempty"`
	Status         OpenCodeMigrationStatus `json:"status"`
	Reason         string                  `json:"reason,omitempty"`
	RecoveredTurns int                     `json:"recoveredTurns,omitempty"`
	Attachments    int                     `json:"attachments,omitempty"`
	SandboxMode    string                  `json:"sandboxMode,omitempty"`
	Archived       bool                    `json:"archived,omitempty"`
	HasRevert      bool                    `json:"hasRevert,omitempty"`
}

// OpenCodeMigrationReport summarizes an idempotent OpenCode-to-Native migration pass.
type OpenCodeMigrationReport struct {
	Scanned     int                     `json:"scanned"`
	Migrated    int                     `json:"migrated"`
	Upgraded    int                     `json:"upgraded"`
	Skipped     int                     `json:"skipped"`
	Failed      int                     `json:"failed"`
	StartedAt   int64                   `json:"startedAt"`
	CompletedAt int64                   `json:"completedAt"`
	DBPaths     []string                `json:"dbPaths,omitempty"`
	Items       []OpenCodeMigrationItem `json:"items,omitempty"`
}

// OpenCodeMigrationJournalEntry preserves recoverable pre-migration state so a
// migrated or upgraded session can be rolled back without losing original data.
type OpenCodeMigrationJournalEntry struct {
	SessionID      string         `json:"sessionID"`
	SourceID       string         `json:"sourceID,omitempty"`
	SourceDB       string         `json:"sourceDB,omitempty"`
	Action         string         `json:"action"` // "created" | "upgraded"
	MigratedAt     int64          `json:"migratedAt"`
	PriorRecord    *SessionRecord `json:"priorRecord,omitempty"`
	CreatedChildID []string       `json:"createdChildIDs,omitempty"`
}

type openCodeMigrationJournalDoc struct {
	Version int                                      `json:"version"`
	Entries map[string]OpenCodeMigrationJournalEntry `json:"entries"`
}

func (s *Service) ocMigrationJournalPath() string {
	if s == nil || s.store == nil || strings.TrimSpace(s.store.Dir()) == "" {
		return ""
	}
	return filepath.Join(s.store.Dir(), ocMigrationJournalFile)
}

func (s *Service) ocMigrationReportPath() string {
	if s == nil || s.store == nil || strings.TrimSpace(s.store.Dir()) == "" {
		return ""
	}
	return filepath.Join(s.store.Dir(), ocMigrationReportFile)
}

func backupCorruptStateFile(path string, raw []byte) {
	if path == "" || len(raw) == 0 {
		return
	}
	backup := fmt.Sprintf("%s.corrupt-%s", path, time.Now().UTC().Format("20060102T150405.000000000"))
	_ = hoxipaths.WriteFileAtomic(backup, raw, 0o600)
}

// ocMigrationJournalBatch buffers journal writes for one migration pass. Each
// journal entry can embed a full prior SessionRecord, so the file grows to
// hundreds of MB; re-reading and atomically rewriting it once per migrated row
// made a pass quadratic and froze startup for many minutes.
type ocMigrationJournalBatch struct {
	depth int
	doc   *openCodeMigrationJournalDoc
	dirty bool
}

// beginOCMigrationJournalBatch defers journal persistence until the returned
// end function runs. Batches nest; only the outermost end writes the file, and
// only when an entry actually changed. The journal is read lazily on first use.
func (s *Service) beginOCMigrationJournalBatch() (end func()) {
	if s == nil {
		return func() {}
	}
	s.ocMigrateMu.Lock()
	if s.ocJournalBatch == nil {
		s.ocJournalBatch = &ocMigrationJournalBatch{}
	}
	s.ocJournalBatch.depth++
	s.ocMigrateMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.ocMigrateMu.Lock()
			defer s.ocMigrateMu.Unlock()
			b := s.ocJournalBatch
			if b == nil {
				return
			}
			b.depth--
			if b.depth > 0 {
				return
			}
			s.ocJournalBatch = nil
			if b.dirty && b.doc != nil {
				_ = s.writeOCMigrationJournalFileLocked(*b.doc)
			}
		})
	}
}

// loadOCMigrationJournalLocked returns the current journal. Inside a batch it
// serves a shallow copy of the buffered document so callers may mutate the
// returned map freely, even after releasing ocMigrateMu.
func (s *Service) loadOCMigrationJournalLocked() openCodeMigrationJournalDoc {
	b := s.ocJournalBatch
	if b == nil {
		return s.readOCMigrationJournalFileLocked()
	}
	if b.doc == nil {
		doc := s.readOCMigrationJournalFileLocked()
		b.doc = &doc
	}
	entries := make(map[string]OpenCodeMigrationJournalEntry, len(b.doc.Entries))
	for k, v := range b.doc.Entries {
		entries[k] = v
	}
	return openCodeMigrationJournalDoc{Version: b.doc.Version, Entries: entries}
}

// saveOCMigrationJournalLocked persists doc, or buffers it while a batch is open.
func (s *Service) saveOCMigrationJournalLocked(doc openCodeMigrationJournalDoc) error {
	b := s.ocJournalBatch
	if b == nil {
		return s.writeOCMigrationJournalFileLocked(doc)
	}
	doc.Version = ocMigrationJournalVersion
	if doc.Entries == nil {
		doc.Entries = make(map[string]OpenCodeMigrationJournalEntry)
	}
	b.doc = &doc
	b.dirty = true
	return nil
}

func (s *Service) readOCMigrationJournalFileLocked() openCodeMigrationJournalDoc {
	doc := openCodeMigrationJournalDoc{
		Version: ocMigrationJournalVersion,
		Entries: make(map[string]OpenCodeMigrationJournalEntry),
	}
	p := s.ocMigrationJournalPath()
	if p == "" {
		return doc
	}
	raw, err := os.ReadFile(p)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return doc
	}
	var parsed openCodeMigrationJournalDoc
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Version != ocMigrationJournalVersion {
		backupCorruptStateFile(p, raw)
		return doc
	}
	if parsed.Entries != nil {
		doc.Entries = parsed.Entries
	}
	return doc
}

func (s *Service) writeOCMigrationJournalFileLocked(doc openCodeMigrationJournalDoc) error {
	p := s.ocMigrationJournalPath()
	if p == "" {
		return nil
	}
	doc.Version = ocMigrationJournalVersion
	if doc.Entries == nil {
		doc.Entries = make(map[string]OpenCodeMigrationJournalEntry)
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return hoxipaths.WriteFileAtomic(p, append(raw, '\n'), 0o600)
}

func (s *Service) recordMigrationJournalCreated(sessionID, sourceID, sourceDB string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.ocMigrateMu.Lock()
	defer s.ocMigrateMu.Unlock()
	doc := s.loadOCMigrationJournalLocked()
	if _, exists := doc.Entries[sessionID]; exists {
		return
	}
	doc.Entries[sessionID] = OpenCodeMigrationJournalEntry{
		SessionID:  sessionID,
		SourceID:   strings.TrimSpace(sourceID),
		SourceDB:   strings.TrimSpace(sourceDB),
		Action:     "created",
		MigratedAt: time.Now().UnixMilli(),
	}
	_ = s.saveOCMigrationJournalLocked(doc)
}

func (s *Service) recordMigrationJournalUpgraded(prior SessionRecord, sourceID, sourceDB string) {
	sessionID := strings.TrimSpace(prior.ID)
	if sessionID == "" {
		return
	}
	cloned := cloneRecord(prior)
	s.ocMigrateMu.Lock()
	defer s.ocMigrateMu.Unlock()
	doc := s.loadOCMigrationJournalLocked()
	if _, exists := doc.Entries[sessionID]; exists {
		return
	}
	doc.Entries[sessionID] = OpenCodeMigrationJournalEntry{
		SessionID:   sessionID,
		SourceID:    strings.TrimSpace(sourceID),
		SourceDB:    strings.TrimSpace(sourceDB),
		Action:      "upgraded",
		MigratedAt:  time.Now().UnixMilli(),
		PriorRecord: &cloned,
	}
	_ = s.saveOCMigrationJournalLocked(doc)
}

func (s *Service) recordMigrationJournalChild(parentID, childID string) {
	parentID = strings.TrimSpace(parentID)
	childID = strings.TrimSpace(childID)
	if parentID == "" || childID == "" {
		return
	}
	s.ocMigrateMu.Lock()
	defer s.ocMigrateMu.Unlock()
	doc := s.loadOCMigrationJournalLocked()
	entry, exists := doc.Entries[parentID]
	if !exists {
		return
	}
	for _, existing := range entry.CreatedChildID {
		if existing == childID {
			return
		}
	}
	entry.CreatedChildID = append(entry.CreatedChildID, childID)
	doc.Entries[parentID] = entry
	_ = s.saveOCMigrationJournalLocked(doc)
}

func (s *Service) saveOCMigrationReport(report OpenCodeMigrationReport) {
	if s == nil {
		return
	}
	s.ocMigrateMu.Lock()
	s.lastOCMigration = &report
	p := s.ocMigrationReportPath()
	s.ocMigrateMu.Unlock()
	if p == "" {
		return
	}
	if raw, err := json.MarshalIndent(report, "", "  "); err == nil {
		_ = hoxipaths.WriteFileAtomic(p, append(raw, '\n'), 0o600)
	}
}

// LastOpenCodeMigrationReport returns the most recent migration report from memory or disk.
func (s *Service) LastOpenCodeMigrationReport() OpenCodeMigrationReport {
	if s == nil {
		return OpenCodeMigrationReport{}
	}
	s.ocMigrateMu.Lock()
	if s.lastOCMigration != nil {
		cp := *s.lastOCMigration
		cp.Items = append([]OpenCodeMigrationItem(nil), s.lastOCMigration.Items...)
		cp.DBPaths = append([]string(nil), s.lastOCMigration.DBPaths...)
		s.ocMigrateMu.Unlock()
		return cp
	}
	p := s.ocMigrationReportPath()
	s.ocMigrateMu.Unlock()
	if p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			var parsed OpenCodeMigrationReport
			if json.Unmarshal(raw, &parsed) == nil {
				return parsed
			}
		}
	}
	return OpenCodeMigrationReport{}
}

// ListOpenCodeMigrationJournal returns all recoverable migration journal entries.
func (s *Service) ListOpenCodeMigrationJournal() []OpenCodeMigrationJournalEntry {
	if s == nil {
		return nil
	}
	s.ocMigrateMu.Lock()
	doc := s.loadOCMigrationJournalLocked()
	s.ocMigrateMu.Unlock()
	out := make([]OpenCodeMigrationJournalEntry, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		eCopy := e
		eCopy.PriorRecord = nil // Do not clone full turns in summary listing
		out = append(out, eCopy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MigratedAt > out[j].MigratedAt })
	return out
}

// RestoreMigratedOpenCodeSession rolls back a single migrated or upgraded session
// to its exact pre-migration state while leaving original SQLite databases untouched.
func (s *Service) RestoreMigratedOpenCodeSession(sessionID string) error {
	if s == nil || s.store == nil {
		return errors.New("runner store is not initialized")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("sessionID is required")
	}
	unlock := s.turnMu.lock(sessionID)
	defer unlock()

	s.mu.Lock()
	busy := s.procs[sessionID] != nil || s.childBusy[sessionID]
	s.mu.Unlock()
	if busy {
		return errors.New("cannot rollback migration while session is running")
	}

	s.ocMigrateMu.Lock()
	doc := s.loadOCMigrationJournalLocked()
	entry, ok := doc.Entries[sessionID]
	s.ocMigrateMu.Unlock()
	if !ok {
		return fmt.Errorf("no migration rollback record found for session %s", sessionID)
	}

	for _, cid := range entry.CreatedChildID {
		if s.attachments != nil {
			s.attachments.DeleteBySession(cid)
		}
		_ = s.store.Delete(cid)
		s.managedOpenCode.Delete(cid)
		s.openCodeChildParent.Delete(cid)
		delete(doc.Entries, cid)
	}

	switch entry.Action {
	case "upgraded":
		if entry.PriorRecord == nil {
			return fmt.Errorf("missing prior snapshot for upgraded session %s", sessionID)
		}
		if err := s.store.Put(cloneRecord(*entry.PriorRecord)); err != nil {
			return err
		}
		s.restoreOpenCodeSessionMappings(*entry.PriorRecord)
	case "created":
		if s.attachments != nil {
			s.attachments.DeleteBySession(sessionID)
		}
		if err := s.store.Delete(sessionID); err != nil {
			return err
		}
		s.managedOpenCode.Delete(sessionID)
		if entry.SourceID != "" {
			s.managedOpenCode.Delete(entry.SourceID)
		}
	default:
		return fmt.Errorf("unsupported rollback action %q", entry.Action)
	}

	s.ocMigrateMu.Lock()
	freshDoc := s.loadOCMigrationJournalLocked()
	delete(freshDoc.Entries, sessionID)
	for _, cid := range entry.CreatedChildID {
		delete(freshDoc.Entries, cid)
	}
	err := s.saveOCMigrationJournalLocked(freshDoc)
	s.ocMigrateMu.Unlock()
	return err
}

// RollbackOpenCodeMigration restores all journaled sessions to their pre-migration state.
func (s *Service) RollbackOpenCodeMigration() (int, error) {
	if s == nil {
		return 0, nil
	}
	s.ocMigrateMu.Lock()
	doc := s.loadOCMigrationJournalLocked()
	ids := make([]string, 0, len(doc.Entries))
	// Roll back root sessions first (which also clean up CreatedChildID), then any remaining children.
	for id, e := range doc.Entries {
		if e.PriorRecord != nil || !strings.Contains(id, "_child_") {
			ids = append(ids, id)
		}
	}
	for id := range doc.Entries {
		found := false
		for _, existing := range ids {
			if existing == id {
				found = true
				break
			}
		}
		if !found {
			ids = append(ids, id)
		}
	}
	s.ocMigrateMu.Unlock()

	restored := 0
	var firstErr error
	for _, id := range ids {
		s.ocMigrateMu.Lock()
		_, stillPresent := s.loadOCMigrationJournalLocked().Entries[id]
		s.ocMigrateMu.Unlock()
		if !stillPresent {
			continue
		}
		if err := s.RestoreMigratedOpenCodeSession(id); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		restored++
	}
	return restored, firstErr
}

func (s *Service) handleOpenCodeMigration(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"report":  s.LastOpenCodeMigrationReport(),
			"journal": s.ListOpenCodeMigrationJournal(),
		})
	case http.MethodPost:
		var req struct {
			Action    string `json:"action"` // "run" | "rollback"
			DBPath    string `json:"dbPath,omitempty"`
			SessionID string `json:"sessionID,omitempty"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req)
		}
		switch strings.ToLower(strings.TrimSpace(firstNonEmptyStr(req.Action, "run"))) {
		case "run":
			var rep OpenCodeMigrationReport
			if strings.TrimSpace(req.DBPath) != "" {
				rep = s.MigrateOpenCodeDBWithReport(strings.TrimSpace(req.DBPath))
			} else {
				rep = s.MigrateAllOpenCodeDBsWithReport()
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"ok":     true,
				"report": rep,
			})
		case "rollback":
			if sid := strings.TrimSpace(req.SessionID); sid != "" {
				if err := s.RestoreMigratedOpenCodeSession(sid); err != nil {
					writeJSONError(w, http.StatusConflict, err.Error())
					return
				}
				writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "restored": 1, "sessionID": sid})
				return
			}
			n, err := s.RollbackOpenCodeMigration()
			if err != nil {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "restored": n})
		default:
			writeJSONError(w, http.StatusBadRequest, "unsupported migration action")
		}
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}
