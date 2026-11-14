package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

type openCodePollDirsPlan struct {
	dirs        []string
	dirStamps   map[string]int64
	projectDirs map[string]bool
	normFilter  string
}

func (s *Service) openCodeEngineSessionMap() (map[string]string, map[string]string, map[string]bool) {
	engineToHoxi := make(map[string]string)
	sessionDirByID := make(map[string]string)
	archivedHoxi := make(map[string]bool)
	knownHoxi := make(map[string]bool)
	s.store.eachMeta(func(rec SessionRecord) {
		knownHoxi[rec.ID] = true
		if rec.ArchivedAt > 0 {
			archivedHoxi[rec.ID] = true
		}
		recDir := firstNonEmptyStr(strings.TrimSpace(rec.WorktreePath), strings.TrimSpace(rec.ProjectDir))
		if recDir != "" {
			sessionDirByID[rec.ID] = recDir
		}
		if rec.ExternalID != "" {
			engineToHoxi[rec.ExternalID] = rec.ID
			if recDir != "" {
				sessionDirByID[rec.ExternalID] = recDir
			}
		}
		for _, ocID := range rec.OpenCodeIDs {
			if ocID != "" {
				engineToHoxi[ocID] = rec.ID
				if ocDir := strings.TrimSpace(rec.OpenCodeDirectories[ocID]); ocDir != "" {
					sessionDirByID[ocID] = ocDir
				} else if recDir != "" {
					sessionDirByID[ocID] = recDir
				}
			}
		}
		for _, childID := range rec.OpenCodeChildIDs {
			if childID != "" {
				engineToHoxi[childID] = rec.ID
				if recDir != "" {
					sessionDirByID[childID] = recDir
				}
			}
		}
	})
	s.openCodeChildParent.Range(func(childID, parentID string) bool {
		if childID == "" || parentID == "" || !knownHoxi[parentID] {
			return true
		}
		engineToHoxi[childID] = parentID
		if hoxiDir := sessionDirByID[parentID]; hoxiDir != "" {
			sessionDirByID[childID] = hoxiDir
		}
		return true
	})
	s.mu.Lock()
	for hoxiID, ap := range s.procs {
		if ap == nil {
			continue
		}
		ap.openCodeChildren.Range(func(childID string, _ bool) bool {
			if childID != "" {
				engineToHoxi[childID] = hoxiID
				if hoxiDir := sessionDirByID[hoxiID]; hoxiDir != "" {
					sessionDirByID[childID] = hoxiDir
				}
			}
			return true
		})
	}
	s.mu.Unlock()
	return engineToHoxi, sessionDirByID, archivedHoxi
}

func (s *Service) openCodeCandidateDirs(directory string) []string {
	return s.openCodePollCandidateDirs(directory, nil).dirs
}

func openCodeRecordStamp(rec SessionRecord) int64 {
	if rec.UpdatedAt > 0 {
		return rec.UpdatedAt
	}
	return 1
}

func (s *Service) openCodePollCandidateDirs(directory string, cleanCache *syncMap[string, int64]) openCodePollDirsPlan {
	normFilter := normalizeComparePath(directory)
	busyDirs := make(map[string]bool)
	dirStamps := make(map[string]int64)
	projectDirs := make(map[string]bool)

	recordMeta := func(rec SessionRecord, busy bool) bool {
		if rec.Runtime != "opencode" || rec.ArchivedAt > 0 {
			return false
		}
		normProj := normalizeComparePath(rec.ProjectDir)
		normWt := normalizeComparePath(rec.WorktreePath)
		if normProj != "" {
			projectDirs[normProj] = true
		}
		if normFilter != "" && normProj != normFilter && normWt != normFilter {
			return false
		}
		stamp := openCodeRecordStamp(rec)
		for _, key := range [2]string{normWt, normProj} {
			if key == "" {
				continue
			}
			if stamp > dirStamps[key] {
				dirStamps[key] = stamp
			}
			if busy {
				busyDirs[key] = true
			}
		}
		return true
	}

	busyIDs := s.busySessionIDs()
	busyRecords := make([]SessionRecord, 0, len(busyIDs))
	for _, sid := range busyIDs {
		if rec, ok := s.store.Meta(sid); ok && recordMeta(rec, true) {
			busyRecords = append(busyRecords, rec)
		}
	}
	var allRecords []SessionRecord
	s.store.eachMeta(func(rec SessionRecord) {
		if recordMeta(rec, false) {
			allRecords = append(allRecords, rec)
		}
	})

	seen := make(map[string]bool)
	out := make([]string, 0, 4)
	add := func(d string, allowSkip bool) {
		trimmed := strings.TrimSpace(d)
		if trimmed == "" {
			return
		}
		key := normalizeComparePath(trimmed)
		if seen[key] {
			return
		}
		seen[key] = true
		if allowSkip && cleanCache != nil && key != "" && key != normFilter && !projectDirs[key] {
			if busyDirs[key] {
				cleanCache.Delete(key)
			} else if stamp := dirStamps[key]; stamp > 0 {
				if cached, ok := cleanCache.Load(key); ok && cached == stamp {
					return
				}
			}
		}
		out = append(out, trimmed)
	}
	addRec := func(rec SessionRecord) {
		if rec.WorktreePath != "" {
			add(rec.WorktreePath, true)
		}
		if rec.ProjectDir != "" {
			add(rec.ProjectDir, true)
		}
	}
	for _, rec := range busyRecords {
		addRec(rec)
	}
	for _, rec := range allRecords {
		addRec(rec)
	}
	add(directory, false)
	if len(out) == 0 {
		out = append(out, "")
	}
	return openCodePollDirsPlan{
		dirs:        out,
		dirStamps:   dirStamps,
		projectDirs: projectDirs,
		normFilter:  normFilter,
	}
}

func (s *Service) isOpenCodeDirBusy(normDir string) bool {
	if normDir == "" {
		return false
	}
	for _, sid := range s.busySessionIDs() {
		rec, ok := s.store.Meta(sid)
		if !ok || rec.Runtime != "opencode" || rec.ArchivedAt > 0 {
			continue
		}
		if normalizeComparePath(rec.WorktreePath) == normDir || normalizeComparePath(rec.ProjectDir) == normDir {
			return true
		}
	}
	return false
}

func (s *Service) recordOpenCodeDirPollOutcome(cleanCache *syncMap[string, int64], plan openCodePollDirsPlan, dir string, empty bool) {
	if cleanCache == nil {
		return
	}
	normDir := normalizeComparePath(dir)
	if normDir == "" || normDir == plan.normFilter || plan.projectDirs[normDir] {
		return
	}
	if !empty || s.isOpenCodeDirBusy(normDir) {
		cleanCache.Delete(normDir)
		return
	}
	if stamp := plan.dirStamps[normDir]; stamp > 0 {
		cleanCache.Store(normDir, stamp)
	}
}

func (s *Service) forgetCleanIdleOpenCodeDirs(rec SessionRecord) {
	drop := func(d string) {
		if key := normalizeComparePath(d); key != "" {
			s.cleanIdlePermDirs.Delete(key)
			s.cleanIdleQuestionDirs.Delete(key)
		}
	}
	drop(rec.WorktreePath)
	drop(rec.ProjectDir)
	for _, d := range rec.OpenCodeDirectories {
		drop(d)
	}
}

func (s *Service) fetchOpenCodeListByDirs(ctx context.Context, path string, dirs []string) ([][]map[string]interface{}, []bool) {
	results := make([][]map[string]interface{}, len(dirs))
	ok := make([]bool, len(dirs))
	fetchOne := func(i int) {
		status, raw, err := s.openCodeRaw(ctx, http.MethodGet, path, directoryQuery(dirs[i], nil), nil)
		if err != nil || status != http.StatusOK {
			return
		}
		var batch []map[string]interface{}
		if json.Unmarshal(raw, &batch) == nil {
			results[i] = batch
			ok[i] = true
		}
	}
	if len(dirs) <= 1 {
		if len(dirs) == 1 {
			fetchOne(0)
		}
		return results, ok
	}
	workers := len(dirs)
	if workers > 4 {
		workers = 4
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				idx := int(next.Add(1) - 1)
				if idx >= len(dirs) {
					return
				}
				fetchOne(idx)
			}
		}()
	}
	wg.Wait()
	return results, ok
}
