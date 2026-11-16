package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MeasureTempPacksBytes returns the total size of orphaned git tmp_pack_* files
// across the given workspace root directories.
func MeasureTempPacksBytes(roots []string) int64 {
	var total int64
	for _, ws := range roots {
		ws = strings.TrimSpace(ws)
		if ws == "" {
			continue
		}
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

// PruneTempPacks removes orphaned git tmp_pack_* files older than minAge across
// the given workspace root directories.
func PruneTempPacks(roots []string, minAge time.Duration) (prunedFiles int, freedBytes int64) {
	now := time.Now()
	for _, ws := range roots {
		ws = strings.TrimSpace(ws)
		if ws == "" {
			continue
		}
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
			// Git creates tmp_pack_* as 0444; make writable before unlink if needed.
			_ = os.Chmod(fullPath, 0o600)
			if os.Remove(fullPath) == nil {
				prunedFiles++
				freedBytes += info.Size()
			}
		}
	}
	return prunedFiles, freedBytes
}
