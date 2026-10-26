package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
	"github.com/hoxiai/hoxi/internal/runner"
	"github.com/hoxiai/hoxi/internal/worktree"
)

const (
	// autoCleanDBThresholdBytes triggers background event-table purge + VACUUM
	// when ~/.hoxi/hoxi.db grows past 512 MB.
	autoCleanDBThresholdBytes = 512 << 20
	// autoCleanLogMaxAge is the retention window for background log/tool-output pruning.
	autoCleanLogMaxAge = 7 * 24 * time.Hour
	// busyCleanProtectionWindow protects recently written logs/packs while a session is active.
	busyCleanProtectionWindow = 5 * time.Minute
)

// SystemStorageStats summarizes disk space used by Hoxi's local data plane.
type SystemStorageStats struct {
	TotalBytes      int64  `json:"totalBytes"`
	DatabaseBytes   int64  `json:"databaseBytes"`
	SessionsBytes   int64  `json:"sessionsBytes"`
	EngineLogsBytes int64  `json:"engineLogsBytes"`
	CacheIndexBytes int64  `json:"cacheIndexBytes"`
	DataDir         string `json:"dataDir"`
}

// SystemStorageCleanResult reports space reclaimed by a cleanup pass and the updated breakdown.
type SystemStorageCleanResult struct {
	FreedBytes        int64              `json:"freedBytes"`
	CompactedSessions int                `json:"compactedSessions"`
	PrunedFiles       int                `json:"prunedFiles"`
	Storage           SystemStorageStats `json:"storage"`
}

func (g *Gateway) handleSystemStorage(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "system storage inspection is only available on the desktop")
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	writeJSON(w, http.StatusOK, g.InspectStorage())
}

func (g *Gateway) handleSystemStorageClean(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "system storage cleanup is only available on the desktop")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, g.CleanStorage(ctx))
}

// StartStorageMaintenance runs periodic background cleanup of transient engine
// logs, stale git pack tempfiles, redundant session JSONL lines, and oversized
// SQLite event logs until ctx is cancelled.
func (g *Gateway) StartStorageMaintenance(ctx context.Context) {
	go func() {
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			g.runBackgroundStorageMaintenance(ctx)
		}

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.runBackgroundStorageMaintenance(ctx)
			}
		}
	}()
}

func (g *Gateway) runBackgroundStorageMaintenance(ctx context.Context) {
	if g.isAnySessionBusy() {
		return
	}
	g.storageCleanMu.Lock()
	defer g.storageCleanMu.Unlock()

	_, _ = opencode.PruneEngineLogDirs(autoCleanLogMaxAge)
	_, _ = worktree.PruneTempPacks(g.knownWorkspaceDirs(), time.Hour)
	if g.runner != nil && g.runner.Store() != nil {
		_, _, _ = g.runner.Store().CleanStorage(time.Hour)
	}
	if dbPath := hoxipaths.DBPath(); dbPath != "" {
		if ocstate.MeasureDatabaseBytes(dbPath) >= autoCleanDBThresholdBytes {
			if err := ocstate.CleanDatabaseEventLog(ctx, dbPath); err != nil {
				diagnostics.Record(diagnostics.Entry{
					Kind:    "system",
					Level:   "warn",
					Source:  "storage",
					Message: err.Error(),
				})
			}
		}
	}
}

// InspectStorage calculates the current storage footprint across Hoxi's four
// primary storage categories.
func (g *Gateway) InspectStorage() SystemStorageStats {
	dbBytes := ocstate.MeasureDatabaseBytes(hoxipaths.DBPath())
	var sessionsBytes int64
	if g.runner != nil && g.runner.Store() != nil {
		sessionsBytes = g.runner.Store().StorageBytes()
	} else {
		sessionsBytes = runner.MeasureDirBytes(hoxipaths.Join("sessions"))
	}
	engineLogsBytes := opencode.MeasureEngineLogsBytes()
	var codeIndexBytes int64
	if g.codeIndex != nil {
		codeIndexBytes = g.codeIndex.StorageBytes()
	} else {
		codeIndexBytes = runner.MeasureDirBytes(hoxipaths.Join("code-index"))
	}
	cacheIndexBytes := codeIndexBytes + worktree.MeasureTempPacksBytes(g.knownWorkspaceDirs())
	total := dbBytes + sessionsBytes + engineLogsBytes + cacheIndexBytes
	return SystemStorageStats{
		TotalBytes:      total,
		DatabaseBytes:   dbBytes,
		SessionsBytes:   sessionsBytes,
		EngineLogsBytes: engineLogsBytes,
		CacheIndexBytes: cacheIndexBytes,
		DataDir:         hoxipaths.Home(),
	}
}

// CleanStorage performs an on-demand cleanup across all four storage categories
// while preserving conversations, messages, workspaces, providers, and credentials.
func (g *Gateway) CleanStorage(ctx context.Context) SystemStorageCleanResult {
	g.storageCleanMu.Lock()
	defer g.storageCleanMu.Unlock()

	before := g.InspectStorage()
	minAge := time.Duration(0)
	if g.isAnySessionBusy() {
		minAge = busyCleanProtectionWindow
	}

	var explicitFreed int64
	var compacted int
	var prunedFiles int

	if dbPath := hoxipaths.DBPath(); dbPath != "" {
		dbBefore := ocstate.MeasureDatabaseBytes(dbPath)
		if err := ocstate.CleanDatabaseEventLog(ctx, dbPath); err != nil {
			diagnostics.Record(diagnostics.Entry{
				Kind:    "system",
				Level:   "warn",
				Source:  "storage",
				Message: err.Error(),
			})
		}
		if dbAfter := ocstate.MeasureDatabaseBytes(dbPath); dbBefore > dbAfter {
			explicitFreed += dbBefore - dbAfter
		}
	}

	if g.runner != nil && g.runner.Store() != nil {
		c, files, freed := g.runner.Store().CleanStorage(minAge)
		compacted += c
		prunedFiles += files
		explicitFreed += freed
	} else if sDir := hoxipaths.Join("sessions"); sDir != "" {
		files, freed := runner.PruneTempFiles(sDir, minAge)
		prunedFiles += files
		explicitFreed += freed
	}

	if files, freed := opencode.PruneEngineLogDirs(minAge); files > 0 || freed > 0 {
		prunedFiles += files
		explicitFreed += freed
	}

	if g.codeIndex != nil {
		explicitFreed += g.codeIndex.ClearCache()
	}

	if files, freed := worktree.PruneTempPacks(g.knownWorkspaceDirs(), minAge); files > 0 || freed > 0 {
		prunedFiles += files
		explicitFreed += freed
	}

	after := g.InspectStorage()
	freed := before.TotalBytes - after.TotalBytes
	if explicitFreed > freed {
		freed = explicitFreed
	}
	if freed < 0 {
		freed = 0
	}

	return SystemStorageCleanResult{
		FreedBytes:        freed,
		CompactedSessions: compacted,
		PrunedFiles:       prunedFiles,
		Storage:           after,
	}
}

func (g *Gateway) knownWorkspaceDirs() []string {
	seen := make(map[string]bool)
	var dirs []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		if !seen[clean] {
			seen[clean] = true
			dirs = append(dirs, clean)
		}
	}
	add(g.rootDir)
	if g.workspaces != nil {
		for _, root := range g.workspaces.AllProjectRoots() {
			add(root)
		}
	}
	return dirs
}
