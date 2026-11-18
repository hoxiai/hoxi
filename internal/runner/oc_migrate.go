package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
	"github.com/hoxiai/hoxi/internal/sandbox"
)

func legacyOpenCodeDBPaths() []string {
	var paths []string
	seen := make(map[string]bool)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		if seen[clean] {
			return
		}
		if fi, err := os.Stat(clean); err == nil && !fi.IsDir() && fi.Size() > 0 {
			seen[clean] = true
			paths = append(paths, clean)
		}
	}
	add(hoxipaths.DBPath())
	add(hoxipaths.EngineLegacyDBPath())
	for _, ext := range ocstate.ExternalDBPaths() {
		add(ext)
	}
	return paths
}

// openCodeDaemonless reports whether no OpenCode engine endpoint is configured.
func (s *Service) openCodeDaemonless() bool {
	return s.openCode == nil || !s.openCode.Available()
}

// StartBackgroundOpenCodeMigration runs the default legacy import off the
// caller's goroutine so the desktop main thread (window creation) never waits
// on it. Migrated sessions are announced over SSE as they land. Like the former
// synchronous startup pass, which ran before ConfigureOpenCode, it treats the
// OpenCode engine as absent; it never reads s.openCode concurrently.
func (s *Service) StartBackgroundOpenCodeMigration() {
	if s == nil || s.store == nil {
		return
	}
	s.ocMigrateWG.Add(1)
	go func() {
		defer s.ocMigrateWG.Done()
		s.migrateAllOpenCodeDBs(true)
	}()
}

// stopBackgroundOpenCodeMigration asks a running pass to stop after its
// current row, then waits for it to flush the journal and report.
func (s *Service) stopBackgroundOpenCodeMigration() {
	if s == nil {
		return
	}
	s.ocMigrateStop.Store(true)
	s.ocMigrateWG.Wait()
}

func (s *Service) announceMigratedSession(item OpenCodeMigrationItem) {
	if item.Status != OCMigrateCreated && item.Status != OCMigrateUpgraded {
		return
	}
	rec, ok := s.store.Meta(item.SessionID)
	if !ok {
		return
	}
	evtType := "session.updated"
	if item.Status == OCMigrateCreated {
		evtType = "session.created"
	}
	s.broadcast(rec.ID, SSEEvent{Type: evtType, Properties: map[string]interface{}{"info": rec.ToSessionInfo()}})
}

// MigrateAllOpenCodeDBsWithReport runs an idempotent migration across all known
// legacy OpenCode databases plus any Hoxi store sessions still stamped with
// Runtime="opencode" when no OpenCode daemon is active.
func (s *Service) MigrateAllOpenCodeDBsWithReport() OpenCodeMigrationReport {
	if s == nil {
		return s.migrateAllOpenCodeDBs(true)
	}
	return s.migrateAllOpenCodeDBs(s.openCodeDaemonless())
}

func (s *Service) migrateAllOpenCodeDBs(daemonless bool) OpenCodeMigrationReport {
	started := time.Now().UnixMilli()
	agg := OpenCodeMigrationReport{StartedAt: started}
	if s == nil || s.store == nil {
		agg.CompletedAt = time.Now().UnixMilli()
		return agg
	}
	s.ocMigrateRunMu.Lock()
	defer s.ocMigrateRunMu.Unlock()
	endJournal := s.beginOCMigrationJournalBatch()
	defer endJournal()
	dbPaths := legacyOpenCodeDBPaths()
	agg.DBPaths = dbPaths
	for _, dbPath := range dbPaths {
		sub := s.migrateOpenCodeDBInternal(dbPath, false, daemonless)
		agg.Scanned += sub.Scanned
		agg.Migrated += sub.Migrated
		agg.Upgraded += sub.Upgraded
		agg.Skipped += sub.Skipped
		agg.Failed += sub.Failed
		agg.Items = append(agg.Items, sub.Items...)
	}
	if daemonless {
		for _, item := range s.upgradeStandaloneStoreOpenCodeSessions() {
			s.announceMigratedSession(item)
			agg.Scanned++
			if item.Status == OCMigrateUpgraded {
				agg.Upgraded++
			} else if item.Status == OCMigrateSkipped {
				agg.Skipped++
			}
			agg.Items = append(agg.Items, item)
		}
	}
	agg.CompletedAt = time.Now().UnixMilli()
	s.saveOCMigrationReport(agg)
	return agg
}

// MigrateOpenCodeDB imports and reconciles legacy OpenCode SQLite sessions into
// runner.Store using pure-Go SQLite without requiring `opencode` or `sqlite3`.
func (s *Service) MigrateOpenCodeDB(dbPath string) int {
	rep := s.MigrateOpenCodeDBWithReport(dbPath)
	return rep.Migrated + rep.Upgraded
}

// MigrateOpenCodeDBWithReport performs a full repeatable pure-Go OpenCode SQLite
// migration on dbPath and returns per-session status and failure reasons.
func (s *Service) MigrateOpenCodeDBWithReport(dbPath string) OpenCodeMigrationReport {
	if s != nil {
		s.ocMigrateRunMu.Lock()
		defer s.ocMigrateRunMu.Unlock()
		return s.migrateOpenCodeDBInternal(dbPath, true, s.openCodeDaemonless())
	}
	return s.migrateOpenCodeDBInternal(dbPath, true, true)
}

func (s *Service) migrateOpenCodeDBInternal(dbPath string, persistReport, daemonless bool) OpenCodeMigrationReport {
	started := time.Now().UnixMilli()
	rep := OpenCodeMigrationReport{StartedAt: started}
	if s == nil || s.store == nil || strings.TrimSpace(dbPath) == "" {
		rep.CompletedAt = time.Now().UnixMilli()
		return rep
	}
	cleanDB := filepath.Clean(strings.TrimSpace(dbPath))
	rep.DBPaths = []string{cleanDB}
	if fi, err := os.Stat(cleanDB); err != nil || fi.IsDir() || fi.Size() == 0 {
		rep.CompletedAt = time.Now().UnixMilli()
		return rep
	}

	rows, readErr := readAllOpenCodeSessionRows(cleanDB)
	if readErr != nil {
		rep.Failed = 1
		rep.Items = append(rep.Items, OpenCodeMigrationItem{
			SourceDB: cleanDB,
			Status:   OCMigrateFailed,
			Reason:   "read session table: " + readErr.Error(),
		})
		rep.CompletedAt = time.Now().UnixMilli()
		if persistReport {
			s.saveOCMigrationReport(rep)
		}
		return rep
	}

	// Process root sessions before child subagent sessions so parent IDs map cleanly.
	sort.SliceStable(rows, func(i, j int) bool {
		pi := strings.TrimSpace(rows[i].ParentID) != ""
		pj := strings.TrimSpace(rows[j].ParentID) != ""
		if pi != pj {
			return !pi
		}
		if rows[i].TimeCreated != rows[j].TimeCreated {
			return rows[i].TimeCreated < rows[j].TimeCreated
		}
		return rows[i].ID < rows[j].ID
	})

	tombstones := s.loadDeletedOpenCodeIDs()
	ocToHoxi := s.buildExistingOpenCodeToHoxiMap()
	endJournal := s.beginOCMigrationJournalBatch()
	defer endJournal()

	for _, row := range rows {
		if s.ocMigrateStop.Load() {
			break
		}
		rep.Scanned++
		item := s.migrateSingleOpenCodeRow(cleanDB, row, tombstones, ocToHoxi, daemonless)
		s.announceMigratedSession(item)
		switch item.Status {
		case OCMigrateCreated:
			rep.Migrated++
		case OCMigrateUpgraded:
			rep.Upgraded++
		case OCMigrateSkipped:
			rep.Skipped++
		case OCMigrateFailed:
			rep.Failed++
		}
		rep.Items = append(rep.Items, item)
	}

	rep.CompletedAt = time.Now().UnixMilli()
	if persistReport {
		s.saveOCMigrationReport(rep)
	}
	return rep
}

func (s *Service) buildExistingOpenCodeToHoxiMap() map[string]string {
	out := make(map[string]string)
	s.store.EachMeta(func(rec SessionRecord) {
		if rec.ID == "" {
			return
		}
		out[rec.ID] = rec.ID
		if ext := strings.TrimSpace(rec.ExternalID); ext != "" {
			out[ext] = rec.ID
		}
		for _, ocID := range rec.OpenCodeIDs {
			if clean := strings.TrimSpace(ocID); clean != "" {
				out[clean] = rec.ID
			}
		}
	})
	return out
}

func (s *Service) migrateSingleOpenCodeRow(
	dbPath string,
	row ocRawSessionRow,
	tombstones map[string]bool,
	ocToHoxi map[string]string,
	daemonless bool,
) OpenCodeMigrationItem {
	srcID := strings.TrimSpace(row.ID)
	item := OpenCodeMigrationItem{
		SessionID: srcID,
		SourceID:  srcID,
		ParentID:  strings.TrimSpace(row.ParentID),
		Title:     strings.TrimSpace(row.Title),
		Directory: strings.TrimSpace(row.Directory),
		SourceDB:  dbPath,
	}
	if !validStoreID(srcID) {
		item.Status = OCMigrateFailed
		item.Reason = "invalid session id"
		return item
	}

	hoxiConvID := extractHoxiConversationMetaID(row.Metadata)
	if hoxiConvID == "" {
		hoxiConvID = ocToHoxi[srcID]
	}
	targetID := srcID
	if hoxiConvID != "" && validStoreID(hoxiConvID) {
		targetID = hoxiConvID
	}
	item.SessionID = targetID

	if tombstones[srcID] || tombstones[targetID] {
		item.Status = OCMigrateSkipped
		item.Reason = "deleted_tombstone"
		return item
	}

	rawParent := strings.TrimSpace(row.ParentID)
	resolvedParent := ""
	isSubagent := false
	if rawParent != "" {
		resolvedParent = firstNonEmptyStr(ocToHoxi[rawParent], rawParent)
		isSubagent = true
		item.ParentID = resolvedParent
	}

	projDir, wtDir, sandboxMode, branch, baseCommit := s.resolveMigratedWorktree(targetID, srcID, row.Directory, isSubagent)
	item.SandboxMode = sandboxMode

	parsed, err := s.readOpenCodeTranscriptFromDB(dbPath, srcID, targetID, wtDir)
	if err != nil {
		item.Status = OCMigrateFailed
		item.Reason = err.Error()
		return item
	}
	item.RecoveredTurns = len(parsed.turns)
	item.Attachments = parsed.attachmentsCnt
	item.Archived = row.TimeArchived > 0

	ocRev := parseOCRevertRaw(row.RevertRaw)
	item.HasRevert = ocRev != nil
	ocToHoxi[srcID] = targetID

	if existing, exists := s.store.Get(targetID); exists {
		return s.upgradeExistingMigratedSession(dbPath, srcID, targetID, resolvedParent, isSubagent, projDir, wtDir, sandboxMode, branch, baseCommit, row, parsed, ocRev, daemonless, existing, item)
	}

	provID, modID := parseMigratedModelField(row.Model)
	if modID == "" {
		provID, modID = parsed.inferredProv, parsed.inferredMod
	}
	if provID == "" {
		provID = "auto"
	}

	projectID, _ := s.projectIDForDirectory(projDir)
	if projectID == "" {
		projectID, _ = s.ensureProjectID(projDir)
	}
	createdAt := row.TimeCreated
	if createdAt <= 0 {
		createdAt = time.Now().UnixMilli()
	}
	updatedAt := row.TimeUpdated
	if updatedAt <= 0 {
		updatedAt = createdAt
	}
	title := strings.TrimSpace(row.Title)
	if title == "" {
		if isSubagent {
			title = "Subagent Task"
		} else {
			title = "Migrated Session"
		}
	}
	_, cmdSandbox, _ := sandbox.NewManager("").Get(targetID)

	activeTurns := parsed.turns
	var revertedTurns []MessageTurn
	var revState *SessionRevert
	var revSnap, revScope string
	if ocRev != nil && messageTurnIndex(parsed.turns, ocRev.MessageID) >= 0 {
		revertedTurns = cloneTurns(parsed.turns)
		activeTurns = truncateTurnsForRevert(parsed.turns, ocRev.MessageID, ocRev.PartID)
		revState = &SessionRevert{
			MessageID:   ocRev.MessageID,
			PartID:      ocRev.PartID,
			CodeChanged: ocRev.Snapshot != "",
		}
		revSnap = ocRev.Snapshot
		if sandboxMode == "worktree" {
			revScope = revertScopeWorkspace
		} else {
			revScope = revertScopePaths
		}
	}

	var snaps map[string]string
	if len(parsed.snapshots) > 0 {
		snaps = parsed.snapshots
	}

	rec := SessionRecord{
		ID:                  targetID,
		ProjectID:           projectID,
		ParentID:            resolvedParent,
		Subagent:            isSubagent,
		Surface:             NormalizeSessionSurface("", projDir),
		Runtime:             string(RuntimeNative),
		ProjectDir:          projDir,
		WorktreePath:        wtDir,
		Branch:              branch,
		BaseCommit:          baseCommit,
		SandboxMode:         sandboxMode,
		CommandSandbox:      cmdSandbox,
		ProviderID:          provID,
		ModelID:             modID,
		Mode:                "build",
		Title:               title,
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
		ArchivedAt:          row.TimeArchived,
		Turns:               activeTurns,
		Revert:              revState,
		RevertedTurns:       revertedTurns,
		RevertSnapshot:      revSnap,
		RevertScope:         revScope,
		Snapshots:           snaps,
		OpenCodeIDs:         []string{srcID},
		OpenCodeDirectories: map[string]string{srcID: wtDir},
		ContextPending:      len(activeTurns) > 0,
		ContextTurnCount:    len(activeTurns),
	}
	if err := s.store.Put(rec); err != nil {
		item.Status = OCMigrateFailed
		item.Reason = "persist migrated session: " + err.Error()
		return item
	}
	s.recordMigrationJournalCreated(targetID, srcID, dbPath)
	s.managedOpenCode.Store(srcID, true)
	if isSubagent && resolvedParent != "" {
		s.linkMigratedChildToParent(resolvedParent, targetID, srcID)
		s.recordMigrationJournalChild(resolvedParent, targetID)
	}
	item.Status = OCMigrateCreated
	return item
}

func (s *Service) upgradeExistingMigratedSession(
	dbPath, srcID, targetID, resolvedParent string,
	isSubagent bool,
	projDir, wtDir, sandboxMode, branch, baseCommit string,
	row ocRawSessionRow,
	parsed *parsedOCTranscript,
	ocRev *ocRevertPayload,
	daemonless bool,
	existing SessionRecord,
	item OpenCodeMigrationItem,
) OpenCodeMigrationItem {
	alreadyNative := existing.Runtime == string(RuntimeNative)
	dbNewerThanStore := row.TimeUpdated > existing.UpdatedAt
	needRuntimeUpgrade := daemonless && existing.Runtime == "opencode"
	needTurnsBackfill := (!alreadyNative || dbNewerThanStore) && len(parsed.turns) > len(existing.Turns) && existing.Revert == nil
	needArchiveSync := (!alreadyNative || dbNewerThanStore) && row.TimeArchived > 0 && existing.ArchivedAt == 0
	needRevertSync := (!alreadyNative || dbNewerThanStore) && ocRev != nil && existing.Revert == nil && messageTurnIndex(parsed.turns, ocRev.MessageID) >= 0
	needWorktreeSync := sandboxMode == "worktree" && existing.SandboxMode != "worktree"
	needSnapSync := false
	for k := range parsed.snapshots {
		if existing.Snapshots == nil || existing.Snapshots[k] == "" {
			needSnapSync = true
			break
		}
	}

	if !needRuntimeUpgrade && !needTurnsBackfill && !needArchiveSync && !needRevertSync && !needWorktreeSync && !needSnapSync {
		s.managedOpenCode.Store(srcID, true)
		if isSubagent && resolvedParent != "" {
			s.linkMigratedChildToParent(resolvedParent, targetID, srcID)
		}
		item.Status = OCMigrateSkipped
		item.Reason = "already_migrated"
		return item
	}

	s.recordMigrationJournalUpgraded(existing, srcID, dbPath)
	updated := cloneRecord(existing)
	if needRuntimeUpgrade {
		updated.Runtime = string(RuntimeNative)
		updated.ExternalID = ""
	}
	if needTurnsBackfill {
		updated.Turns = parsed.turns
	}
	if needSnapSync {
		if updated.Snapshots == nil {
			updated.Snapshots = make(map[string]string, len(parsed.snapshots))
		}
		for k, v := range parsed.snapshots {
			if updated.Snapshots[k] == "" {
				updated.Snapshots[k] = v
			}
		}
	}
	if needArchiveSync {
		updated.ArchivedAt = row.TimeArchived
	}
	if needWorktreeSync {
		updated.ProjectDir = projDir
		updated.WorktreePath = wtDir
		updated.SandboxMode = "worktree"
		updated.Branch = firstNonEmptyStr(branch, updated.Branch)
		updated.BaseCommit = firstNonEmptyStr(baseCommit, updated.BaseCommit)
	}
	if needRevertSync {
		baseTurns := updated.Turns
		if len(parsed.turns) > len(baseTurns) {
			baseTurns = parsed.turns
		}
		updated.RevertedTurns = cloneTurns(baseTurns)
		updated.Turns = truncateTurnsForRevert(baseTurns, ocRev.MessageID, ocRev.PartID)
		updated.Revert = &SessionRevert{
			MessageID:   ocRev.MessageID,
			PartID:      ocRev.PartID,
			CodeChanged: ocRev.Snapshot != "",
		}
		updated.RevertSnapshot = ocRev.Snapshot
		updated.RevertScope = revertScopeFor(updated)
	}
	if updated.ProviderID == "" || updated.ModelID == "" {
		provID, modID := parseMigratedModelField(row.Model)
		if modID == "" {
			provID, modID = parsed.inferredProv, parsed.inferredMod
		}
		if updated.ProviderID == "" {
			updated.ProviderID = firstNonEmptyStr(provID, "auto")
		}
		if updated.ModelID == "" {
			updated.ModelID = modID
		}
	}
	updated.OpenCodeIDs = mergeStringLists(updated.OpenCodeIDs, []string{srcID})
	if updated.OpenCodeDirectories == nil {
		updated.OpenCodeDirectories = make(map[string]string)
	}
	updated.OpenCodeDirectories[srcID] = wtDir
	updated.ContextPending = len(updated.Turns) > 0
	updated.ContextTurnCount = len(updated.Turns)

	if err := s.store.Put(updated); err != nil {
		item.Status = OCMigrateFailed
		item.Reason = "persist upgraded session: " + err.Error()
		return item
	}
	s.managedOpenCode.Store(srcID, true)
	if isSubagent && resolvedParent != "" {
		s.linkMigratedChildToParent(resolvedParent, targetID, srcID)
	}
	item.Status = OCMigrateUpgraded
	return item
}

func (s *Service) linkMigratedChildToParent(parentID, childID, sourceChildID string) {
	s.openCodeChildParent.Store(childID, parentID)
	if sourceChildID != "" {
		s.openCodeChildParent.Store(sourceChildID, parentID)
	}
	parentRec, ok := s.store.Get(parentID)
	if !ok {
		return
	}
	changed := false
	nextChildren := mergeStringLists(parentRec.OpenCodeChildIDs, []string{childID})
	if len(nextChildren) != len(parentRec.OpenCodeChildIDs) {
		parentRec.OpenCodeChildIDs = nextChildren
		changed = true
	}
	for ti := range parentRec.Turns {
		for pi := range parentRec.Turns[ti].Parts {
			p := &parentRec.Turns[ti].Parts[pi]
			if p.Type != "tool" || p.State == nil || !isRunnerTaskTool(p.Tool) {
				continue
			}
			sid := stringMetaField(p.State.Metadata, "sessionId")
			if sid == sourceChildID && childID != sourceChildID {
				if p.State.Metadata == nil {
					p.State.Metadata = make(map[string]interface{})
				}
				p.State.Metadata["sessionId"] = childID
				changed = true
			}
		}
	}
	if changed {
		_, _, _ = s.store.UpdateWithoutActivity(parentID, func(r *SessionRecord) {
			r.OpenCodeChildIDs = parentRec.OpenCodeChildIDs
			r.Turns = parentRec.Turns
		})
	}
}

func (s *Service) upgradeStandaloneStoreOpenCodeSessions() []OpenCodeMigrationItem {
	var items []OpenCodeMigrationItem
	metas, _ := s.store.SnapshotMetas()
	for _, meta := range metas {
		if s.ocMigrateStop.Load() {
			break
		}
		if meta.Runtime != "opencode" {
			continue
		}
		full, ok := s.store.Get(meta.ID)
		if !ok || full.Runtime != "opencode" {
			continue
		}
		s.recordMigrationJournalUpgraded(full, full.ExternalID, "")
		_, _, err := s.store.UpdateWithoutActivity(full.ID, func(r *SessionRecord) {
			r.Runtime = string(RuntimeNative)
			r.ExternalID = ""
			r.ContextPending = len(r.Turns) > 0
			r.ContextTurnCount = len(r.Turns)
		})
		st := OCMigrateUpgraded
		reason := "promoted_store_opencode_to_native"
		if err != nil {
			st = OCMigrateFailed
			reason = err.Error()
		}
		items = append(items, OpenCodeMigrationItem{
			SessionID:      full.ID,
			SourceID:       full.ExternalID,
			ParentID:       full.ParentID,
			Title:          full.Title,
			Directory:      full.WorktreePath,
			Status:         st,
			Reason:         reason,
			RecoveredTurns: len(full.Turns),
			SandboxMode:    full.SandboxMode,
			Archived:       full.ArchivedAt > 0,
			HasRevert:      full.Revert != nil,
		})
	}
	return items
}

func (s *Service) tryMigrateSingleOpenCodeSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if !validStoreID(sessionID) {
		return false
	}
	tombstones := s.loadDeletedOpenCodeIDs()
	if tombstones[sessionID] {
		return false
	}
	// A full pass already in flight will import this session and announce it;
	// waiting here would stall the request behind the whole pass.
	if !s.ocMigrateRunMu.TryLock() {
		return false
	}
	defer s.ocMigrateRunMu.Unlock()
	ocToHoxi := s.buildExistingOpenCodeToHoxiMap()
	daemonless := s.openCodeDaemonless()
	for _, dbPath := range legacyOpenCodeDBPaths() {
		rows, err := readAllOpenCodeSessionRows(dbPath)
		if err != nil {
			continue
		}
		for _, r := range rows {
			if strings.TrimSpace(r.ID) == sessionID || extractHoxiConversationMetaID(r.Metadata) == sessionID {
				item := s.migrateSingleOpenCodeRow(dbPath, r, tombstones, ocToHoxi, daemonless)
				if item.Status == OCMigrateCreated || item.Status == OCMigrateUpgraded {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) deletedOpenCodeTombstonePath() string {
	if s == nil || s.store == nil || strings.TrimSpace(s.store.Dir()) == "" {
		return ""
	}
	return filepath.Join(s.store.Dir(), "_deleted_opencode_ids.json")
}

func (s *Service) loadDeletedOpenCodeIDs() map[string]bool {
	out := make(map[string]bool)
	p := s.deletedOpenCodeTombstonePath()
	if p == "" {
		return out
	}
	raw, err := os.ReadFile(p)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return out
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		backupCorruptStateFile(p, raw)
		return out
	}
	for _, id := range ids {
		if clean := strings.TrimSpace(id); clean != "" {
			out[clean] = true
		}
	}
	return out
}

func (s *Service) purgeDeletedOpenCodeState(ctx context.Context, id string, rec SessionRecord, childIDs []string) {
	seen := make(map[string]bool)
	var ids []string
	add := func(raw string) {
		if v := strings.TrimSpace(raw); v != "" && !seen[v] {
			seen[v] = true
			ids = append(ids, v)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(id), "ses_") {
		add(id)
	}
	if rec.Runtime == "opencode" || len(rec.OpenCodeIDs) > 0 {
		add(rec.ExternalID)
	}
	for _, ocID := range rec.OpenCodeIDs {
		add(ocID)
	}
	for _, childID := range childIDs {
		add(childID)
	}
	if len(ids) == 0 {
		return
	}
	if p := s.deletedOpenCodeTombstonePath(); p != "" {
		s.mu.Lock()
		merged := s.loadDeletedOpenCodeIDs()
		changed := false
		for _, item := range ids {
			if !merged[item] {
				merged[item] = true
				changed = true
			}
		}
		if changed {
			all := make([]string, 0, len(merged))
			for k := range merged {
				all = append(all, k)
			}
			sort.Strings(all)
			if raw, err := json.Marshal(all); err == nil {
				_ = hoxipaths.WriteFileAtomic(p, raw, 0o600)
			}
		}
		s.mu.Unlock()
	}
	for _, dbPath := range []string{hoxipaths.DBPath(), hoxipaths.EngineLegacyDBPath()} {
		_ = ocstate.DeleteEngineSessions(ctx, dbPath, ids)
	}
}
