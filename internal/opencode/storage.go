package opencode

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// EngineLogDirs returns the transient OpenCode log and tool-output directories
// managed under ~/.hoxi. Note that engine-data/opencode/snapshot is intentionally
// excluded because it stores bare Git snapshot repositories required for session
// revert and diff tracking.
func EngineLogDirs() []string {
	home := hoxipaths.Home()
	if home == "" {
		return nil
	}
	ocData := hoxipaths.EngineOpenCodeDir()
	if ocData == "" {
		ocData = filepath.Join(home, "engine-data", "opencode")
	}
	return []string{
		filepath.Join(ocData, "log"),
		filepath.Join(ocData, "tool-output"),
		filepath.Join(home, "logs"),
	}
}

// MeasureEngineLogsBytes returns the total disk footprint of transient OpenCode
// logs and tool outputs.
func MeasureEngineLogsBytes() int64 {
	var total int64
	for _, dir := range EngineLogDirs() {
		total += dirSizeBytes(dir)
	}
	return total
}

// PruneEngineLogDirs truncates active opencode.log files and removes log/tool-output
// files older than minAge across EngineLogDirs.
func PruneEngineLogDirs(minAge time.Duration) (prunedFiles int, freedBytes int64) {
	for _, dir := range EngineLogDirs() {
		files, bytes := pruneLogFilesInDir(dir, minAge)
		prunedFiles += files
		freedBytes += bytes
	}
	return prunedFiles, freedBytes
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

func pruneLogFilesInDir(root string, minAge time.Duration) (int, int64) {
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
