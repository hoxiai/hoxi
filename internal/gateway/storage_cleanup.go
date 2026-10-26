package gateway

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
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
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
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

	_, _ = g.pruneEngineLogDirs(autoCleanLogMaxAge)
	_, _ = g.pruneWorkspaceTempPacks( time.Hour)
	if g.runner != nil && g.runner.Store() != nil {
		_, _ = g.runner.Store().CompactIdleTurns()
		_, _ = pruneTempFilesInDir(g.runner.Store().Dir(), time.Hour)
	}
	if dbPath := hoxipaths.DBPath(); dbPath != "" {
		if measureDatabaseBytes(dbPath) >= autoCleanDBThresholdBytes {
			cleanSQLiteEventLog(ctx, dbPath)
		}
	}
}

// InspectStorage calculates the current storage footprint across Hoxi's four
// primary storage categories.
func (g *Gateway) InspectStorage() SystemStorageStats {
	dbBytes := measureDatabaseBytes(hoxipaths.DBPath())
	sessionsBytes := dirSizeBytes(g.sessionsRootDir())
	engineLogsBytes := g.measureEngineLogsBytes()
	cacheIndexBytes := dirSizeBytes(g.codeIndexRootDir()) + g.measureWorkspaceTempPacksBytes()
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
		dbBefore := measureDatabaseBytes(dbPath)
		cleanSQLiteEventLog(ctx, dbPath)
		if dbAfter := measureDatabaseBytes(dbPath); dbBefore > dbAfter {
			explicitFreed += dbBefore - dbAfter
		}
	}

	if g.runner != nil && g.runner.Store() != nil {
		c, freed := g.runner.Store().CompactIdleTurns()
		compacted += c
		explicitFreed += freed
	}
	if sDir := g.sessionsRootDir(); sDir != "" {
		files, freed := pruneTempFilesInDir(sDir, minAge)
		prunedFiles += files
		explicitFreed += freed
	}

	if files, freed := g.pruneEngineLogDirs(minAge); files > 0 || freed > 0 {
		prunedFiles += files
		explicitFreed += freed
	}

	if g.codeIndex != nil {
		explicitFreed += g.codeIndex.ClearCache()
	} else if cDir := g.codeIndexRootDir(); cDir != "" {
		files, freed := pruneFilesInDir(cDir, 0)
		prunedFiles += files
		explicitFreed += freed
	}

	if files, freed := g.pruneWorkspaceTempPacks(minAge); files > 0 || freed > 0 {
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

func (g *Gateway) sessionsRootDir() string {
	if g.runner != nil && g.runner.Store() != nil {
		if dir := strings.TrimSpace(g.runner.Store().Dir()); dir != "" {
			return dir
		}
	}
	return hoxipaths.Join("sessions")
}

func (g *Gateway) codeIndexRootDir() string {
	if g.codeIndex != nil {
		if dir := strings.TrimSpace(g.codeIndex.Root()); dir != "" {
			return dir
		}
	}
	return hoxipaths.Join("code-index")
}

func engineLogDirs() []string {
	home := hoxipaths.Home()
	if home == "" {
		return nil
	}
	ocData := filepath.Join(home, "engine-data", "opencode")
	return []string{
		filepath.Join(ocData, "log"),
		filepath.Join(ocData, "tool-output"),
		filepath.Join(ocData, "snapshot"),
		filepath.Join(home, "logs"),
	}
}

func (g *Gateway) measureEngineLogsBytes() int64 {
	var total int64
	for _, dir := range engineLogDirs() {
		total += dirSizeBytes(dir)
	}
	return total
}

func (g *Gateway) pruneEngineLogDirs(minAge time.Duration) (int, int64) {
	var totalFiles int
	var totalBytes int64
	for _, dir := range engineLogDirs() {
		files, bytes := pruneFilesInDir(dir, minAge)
		totalFiles += files
		totalBytes += bytes
	}
	return totalFiles, totalBytes
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

func (g *Gateway) measureWorkspaceTempPacksBytes() int64 {
	var total int64
	for _, ws := range g.knownWorkspaceDirs() {
		packDir := filepath.Join(ws, ".git", "objects", "pack")
		entries, err := os.ReadDir(packDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "tmp_pack_") {
				continue
			}
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
	}
	return total
}

func (g *Gateway) pruneWorkspaceTempPacks(minAge time.Duration) (int, int64) {
	now := time.Now()
	var count int
	var freed int64
	for _, ws := range g.knownWorkspaceDirs() {
		packDir := filepath.Join(ws, ".git", "objects", "pack")
		entries, err := os.ReadDir(packDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "tmp_pack_") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if minAge > 0 && now.Sub(info.ModTime()) < minAge {
				continue
			}
			fullPath := filepath.Join(packDir, entry.Name())
			if err := os.Chmod(fullPath, 0o600); err != nil && !os.IsNotExist(err) {
				// Git creates tmp_pack_* as 0444; make writable before unlink if needed.
			}
			if os.Remove(fullPath) == nil {
				count++
				freed += info.Size()
			}
		}
	}
	return count, freed
}

func measureDatabaseBytes(dbPath string) int64 {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return 0
	}
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(dbPath + suffix); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}

func cleanSQLiteEventLog(ctx context.Context, dbPath string) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return
	}
	for _, stmt := range []string{
		"DELETE FROM event;",
		"DELETE FROM event_sequence;",
		"PRAGMA wal_checkpoint(TRUNCATE);",
		"VACUUM;",
		"PRAGMA wal_checkpoint(TRUNCATE);",
	} {
		cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		cmd := exec.CommandContext(cmdCtx, "sqlite3", "-cmd", ".timeout 10000", dbPath, stmt)
		_ = cmd.Run()
		cancel()
	}
}

func dirSizeBytes(root string) int64 {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func pruneFilesInDir(root string, minAge time.Duration) (int, int64) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, 0
	}
	now := time.Now()
	var count int
	var freed int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if d.Name() == "opencode.log" {
			if info.Size() > 0 && os.Truncate(path, 0) == nil {
				count++
				freed += info.Size()
			}
			return nil
		}
		if minAge > 0 && now.Sub(info.ModTime()) < minAge {
			return nil
		}
		if os.Remove(path) == nil {
			count++
			freed += info.Size()
		}
		return nil
	})
	return count, freed
}

func pruneTempFilesInDir(root string, minAge time.Duration) (int, int64) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, 0
	}
	now := time.Now()
	var count int
	var freed int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".tmp") {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if minAge > 0 && now.Sub(info.ModTime()) < minAge {
			return nil
		}
		if os.Remove(path) == nil {
			count++
			freed += info.Size()
		}
		return nil
	})
	return count, freed
}
