package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

const storeVersion = 1

type storeDoc struct {
	Version   int                 `json:"version"`
	Schedules map[string]Schedule `json:"schedules"`
}

type runsDoc struct {
	Version int         `json:"version"`
	Runs    []RunRecord `json:"runs"`
}

type diskStore struct {
	path    string
	runsDir string

	writeMu sync.Mutex
	runsMu  sync.Mutex
	lastSeq uint64
}

func newDiskStore(storePath, runsDir string) *diskStore {
	return &diskStore{
		path:    strings.TrimSpace(storePath),
		runsDir: strings.TrimSpace(runsDir),
	}
}

func emptyStoreDoc() storeDoc {
	return storeDoc{
		Version:   storeVersion,
		Schedules: make(map[string]Schedule),
	}
}

// load reads schedules.json. A corrupt or unsupported file is backed up rather
// than silently overwritten, and an error blocks subsequent writes until fixed.
func (d *diskStore) load() (storeDoc, error) {
	doc := emptyStoreDoc()
	if d == nil || d.path == "" {
		return doc, nil
	}
	raw, err := os.ReadFile(d.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doc, nil
		}
		return doc, fmt.Errorf("read schedules store: %w", err)
	}
	var parsed storeDoc
	if err := json.Unmarshal(raw, &parsed); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", d.path, time.Now().UTC().Format("20060102T150405.000000000"))
		_ = os.Rename(d.path, backup)
		return doc, fmt.Errorf("corrupt schedules store backed up to %s: %w", backup, err)
	}
	if parsed.Version != 0 && parsed.Version != storeVersion {
		return doc, fmt.Errorf("unsupported schedules store version %d", parsed.Version)
	}
	if parsed.Schedules == nil {
		parsed.Schedules = make(map[string]Schedule)
	}
	parsed.Version = storeVersion
	return parsed, nil
}

func (d *diskStore) saveSnapshot(seq uint64, doc storeDoc) error {
	if d == nil || d.path == "" {
		return nil
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if seq < d.lastSeq {
		return nil
	}
	doc.Version = storeVersion
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := hoxipaths.WriteFileAtomic(d.path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	d.lastSeq = seq
	return nil
}

func (d *diskStore) runsPath(scheduleID string) string {
	if d == nil || d.runsDir == "" {
		return ""
	}
	safe := strings.TrimSpace(scheduleID)
	if safe == "" || strings.ContainsAny(safe, `/\.`) {
		return ""
	}
	return filepath.Join(d.runsDir, safe+".runs.json")
}

func (d *diskStore) loadRuns(scheduleID string) []RunRecord {
	p := d.runsPath(scheduleID)
	if p == "" {
		return nil
	}
	d.runsMu.Lock()
	defer d.runsMu.Unlock()
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var doc runsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", p, time.Now().UTC().Format("20060102T150405.000000000"))
		_ = os.Rename(p, backup)
		return nil
	}
	return doc.Runs
}

func (d *diskStore) saveRuns(scheduleID string, runs []RunRecord) error {
	p := d.runsPath(scheduleID)
	if p == "" {
		return nil
	}
	d.runsMu.Lock()
	defer d.runsMu.Unlock()
	if len(runs) > MaxStoredRunsPerJob {
		runs = runs[:MaxStoredRunsPerJob]
	}
	doc := runsDoc{Version: storeVersion, Runs: runs}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return hoxipaths.WriteFileAtomic(p, append(raw, '\n'), 0o600)
}

func (d *diskStore) deleteRuns(scheduleID string) {
	p := d.runsPath(scheduleID)
	if p == "" {
		return
	}
	d.runsMu.Lock()
	defer d.runsMu.Unlock()
	_ = os.Remove(p)
}
