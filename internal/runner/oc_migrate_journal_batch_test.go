package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOCMigrationJournalBatchDefersAndCoalescesWrites(t *testing.T) {
	storeDir := t.TempDir()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(storeDir, "sessions.json"))
	path := svc.ocMigrationJournalPath()
	if path == "" {
		t.Fatal("journal path must resolve for a custom store")
	}

	endOuter := svc.beginOCMigrationJournalBatch()
	endInner := svc.beginOCMigrationJournalBatch()
	for i := 0; i < 50; i++ {
		svc.recordMigrationJournalCreated(fmt.Sprintf("ses_batch_%02d", i), "", "")
	}
	svc.recordMigrationJournalChild("ses_batch_00", "ses_batch_child")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("journal must not be written while a batch is open, stat err=%v", err)
	}
	if got := len(svc.ListOpenCodeMigrationJournal()); got != 50 {
		t.Fatalf("buffered journal must be readable inside the batch, got %d entries", got)
	}

	endInner()
	endInner() // idempotent: must not unwind the outer batch
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("nested batch end must not flush, stat err=%v", err)
	}
	endOuter()

	svc.ocMigrateMu.Lock()
	doc := svc.readOCMigrationJournalFileLocked()
	svc.ocMigrateMu.Unlock()
	if len(doc.Entries) != 50 {
		t.Fatalf("flushed journal entries = %d, want 50", len(doc.Entries))
	}
	if kids := doc.Entries["ses_batch_00"].CreatedChildID; len(kids) != 1 || kids[0] != "ses_batch_child" {
		t.Fatalf("child link lost in batch flush: %v", kids)
	}
}

func TestOCMigrationJournalBatchWithoutChangesDoesNotTouchFile(t *testing.T) {
	storeDir := t.TempDir()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(storeDir, "sessions.json"))
	end := svc.beginOCMigrationJournalBatch()
	_ = svc.ListOpenCodeMigrationJournal()
	end()
	if _, err := os.Stat(svc.ocMigrationJournalPath()); !os.IsNotExist(err) {
		t.Fatalf("read-only batch must not create the journal, stat err=%v", err)
	}
}

func TestBackgroundOpenCodeMigrationStopsOnCloseAll(t *testing.T) {
	t.Setenv("HOXI_HOME", t.TempDir())
	t.Setenv("HOXI_DB_PATH", filepath.Join(t.TempDir(), "absent.db"))
	t.Setenv("OPENCODE_DB", "")
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(t.TempDir(), "sessions.json"))
	svc.StartBackgroundOpenCodeMigration()
	svc.CloseAll() // must wait for the pass instead of leaking it past shutdown
	if !svc.ocMigrateStop.Load() {
		t.Fatal("CloseAll must signal the background migration to stop")
	}
	rep := svc.migrateOpenCodeDBInternal(filepath.Join(t.TempDir(), "missing.db"), false, true)
	if rep.Scanned != 0 {
		t.Fatalf("stopped service must not scan rows, got %d", rep.Scanned)
	}
}

func TestOCMigrationJournalBatchLoadReturnsIsolatedCopy(t *testing.T) {
	storeDir := t.TempDir()
	svc := NewService(t.TempDir(), nil, nil, filepath.Join(storeDir, "sessions.json"))
	end := svc.beginOCMigrationJournalBatch()
	defer end()
	svc.recordMigrationJournalCreated("ses_keep", "", "")

	svc.ocMigrateMu.Lock()
	doc := svc.loadOCMigrationJournalLocked()
	svc.ocMigrateMu.Unlock()
	delete(doc.Entries, "ses_keep")

	svc.ocMigrateMu.Lock()
	again := svc.loadOCMigrationJournalLocked()
	svc.ocMigrateMu.Unlock()
	if _, ok := again.Entries["ses_keep"]; !ok {
		t.Fatal("mutating a loaded copy must not alter the buffered journal")
	}
}
