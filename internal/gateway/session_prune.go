package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/diagnostics"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode/ocstate"
	"github.com/hoxiai/hoxi/internal/runner"
)

// Old-session cleanup is a two-step, desktop-only flow: the preview lists
// root conversations idle for at least N days, and the delete call removes
// only the IDs the user confirmed, re-checking each one first. Nothing here
// runs in the background.
const (
	sessionPruneMinDays    = 7
	sessionPruneMaxDays    = 3650
	sessionPruneMaxIDs     = 5000
	sessionPruneRunTimeout = 10 * time.Minute
)

// Skip reasons returned to the client, one per unconfirmed or failed ID.
const (
	pruneSkipNotEligible   = "notEligible"
	pruneSkipBusy          = "busy"
	pruneSkipWorktreeDirty = "worktreeDirty"
	pruneSkipWorktreeError = "worktreeFailed"
	pruneSkipDeleteError   = "deleteFailed"
)

type sessionPruneCandidate struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory,omitempty"`
	Runtime   string `json:"runtime,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
	Bytes     int64  `json:"bytes"`
	Children  int    `json:"children"`
	Archived  bool   `json:"archived,omitempty"`
	Worktree  bool   `json:"worktree,omitempty"`

	// worktreeDir is the isolated sandbox to remove before deletion, if any.
	worktreeDir string
	busy        bool
}

type sessionPrunePreview struct {
	OlderThanDays int                     `json:"olderThanDays"`
	Cutoff        int64                   `json:"cutoff"`
	Sessions      []sessionPruneCandidate `json:"sessions"`
	TotalBytes    int64                   `json:"totalBytes"`
	BusySkipped   int                     `json:"busySkipped"`
}

type sessionPruneRequest struct {
	OlderThanDays int      `json:"olderThanDays"`
	SessionIDs    []string `json:"sessionIDs"`
}

type sessionPruneSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

type sessionPruneResult struct {
	Deleted        []string           `json:"deleted"`
	Skipped        []sessionPruneSkip `json:"skipped"`
	FreedBytes     int64              `json:"freedBytes"`
	VacuumDeferred bool               `json:"vacuumDeferred,omitempty"`
	Storage        SystemStorageStats `json:"storage"`
}

func parsePruneDays(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < sessionPruneMinDays || n > sessionPruneMaxDays {
		return 0, false
	}
	return n, true
}

func pruneCutoff(days int, now time.Time) int64 {
	return now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
}

func (g *Gateway) handleOldSessionsPreview(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "old session cleanup is only available on the desktop")
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	days, ok := parsePruneDays(r.URL.Query().Get("olderThanDays"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "olderThanDays must be an integer between 7 and 3650")
		return
	}
	if g.runner == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "session store unavailable")
		return
	}
	cutoff := pruneCutoff(days, time.Now())
	all := g.oldSessionCandidates(r.Context(), cutoff, true)
	res := sessionPrunePreview{OlderThanDays: days, Cutoff: cutoff, Sessions: []sessionPruneCandidate{}}
	for _, c := range all {
		if c.busy {
			res.BusySkipped++
			continue
		}
		res.Sessions = append(res.Sessions, c)
		res.TotalBytes += c.Bytes
	}
	writeJSON(w, http.StatusOK, res)
}

func (g *Gateway) handleOldSessionsDelete(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "old session cleanup is only available on the desktop")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body sessionPruneRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	days, ok := parsePruneDays(strconv.Itoa(body.OlderThanDays))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "olderThanDays must be an integer between 7 and 3650")
		return
	}
	if len(body.SessionIDs) == 0 || len(body.SessionIDs) > sessionPruneMaxIDs {
		writeJSONError(w, http.StatusBadRequest, "sessionIDs must list between 1 and 5000 confirmed sessions")
		return
	}
	if g.runner == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "session store unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sessionPruneRunTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, g.deleteOldSessions(ctx, days, body.SessionIDs))
}

// oldSessionCandidates lists root conversations whose whole subagent tree has
// been idle since cutoff, oldest first. withSizes adds a read-only size
// estimate from the session store and the engine database.
func (g *Gateway) oldSessionCandidates(ctx context.Context, cutoff int64, withSizes bool) []sessionPruneCandidate {
	store := g.runner.Store()
	var engineBytes map[string]int64
	if withSizes {
		engineBytes, _ = ocstate.SessionContentBytes(ctx, hoxipaths.DBPath())
	}
	var out []sessionPruneCandidate
	for _, info := range store.List("") {
		rec, ok := store.Meta(info.ID)
		if !ok || rec.ParentID != "" || rec.Subagent {
			continue
		}
		tree := sessionTree(store, rec)
		lastActive := int64(0)
		busy := false
		for _, node := range tree {
			lastActive = max(lastActive, node.UpdatedAt, node.CreatedAt)
			busy = busy || g.runner.IsSessionBusy(node.ID)
		}
		if lastActive <= 0 || lastActive >= cutoff {
			continue
		}
		removed := deletedWithRoot(tree)
		c := sessionPruneCandidate{
			ID:        rec.ID,
			Title:     rec.Title,
			Directory: rec.ProjectDir,
			Runtime:   rec.Runtime,
			UpdatedAt: lastActive,
			Children:  len(removed) - 1,
			Archived:  rec.ArchivedAt > 0,
			busy:      busy,
		}
		if dir := isolatedWorktreeDir(rec); dir != "" {
			c.Worktree, c.worktreeDir = true, dir
		}
		if withSizes {
			c.Bytes = sessionTreeBytes(store.Dir(), removed, engineBytes)
			if g.browser != nil {
				for _, node := range removed {
					c.Bytes += g.browser.SessionProfileBytes(node.ID)
				}
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt < out[j].UpdatedAt })
	return out
}

// deletedWithRoot narrows a tree to what DeleteConversation removes: the root
// and its direct subagent children.
func deletedWithRoot(tree []runner.SessionRecord) []runner.SessionRecord {
	out := []runner.SessionRecord{tree[0]}
	for _, node := range tree[1:] {
		if node.Subagent && node.ParentID == tree[0].ID {
			out = append(out, node)
		}
	}
	return out
}

// sessionTree returns rec followed by every descendant. Activity and busy
// checks span the whole tree, so a recently used child keeps its root.
func sessionTree(store *runner.Store, rec runner.SessionRecord) []runner.SessionRecord {
	tree := []runner.SessionRecord{rec}
	seen := map[string]bool{rec.ID: true}
	for i := 0; i < len(tree); i++ {
		children, _ := store.ChildSessionIDs(tree[i].ID)
		for _, id := range children {
			if seen[id] {
				continue
			}
			seen[id] = true
			if child, ok := store.Meta(id); ok {
				tree = append(tree, child)
			}
		}
	}
	return tree
}

func sessionTreeBytes(storeDir string, tree []runner.SessionRecord, engineBytes map[string]int64) int64 {
	var total int64
	counted := map[string]bool{}
	addEngine := func(id string) {
		if id != "" && !counted[id] {
			counted[id] = true
			total += engineBytes[id]
		}
	}
	for _, node := range tree {
		total += runner.MeasureDirBytes(filepath.Join(storeDir, node.ID))
		addEngine(node.ID)
		for _, id := range node.OpenCodeIDs {
			addEngine(id)
		}
		for _, id := range node.OpenCodeChildIDs {
			addEngine(id)
		}
	}
	return total
}

// isolatedWorktreeDir returns the session's own sandbox under
// <project>/.hoxi/worktrees, or "" when the session works in the project
// directory itself, which cleanup must never touch.
func isolatedWorktreeDir(rec runner.SessionRecord) string {
	wt := strings.TrimSpace(rec.WorktreePath)
	project := strings.TrimSpace(rec.ProjectDir)
	if wt == "" || project == "" {
		return ""
	}
	wt, project = filepath.Clean(wt), filepath.Clean(project)
	if wt == project || filepath.Dir(wt) != filepath.Join(project, ".hoxi", "worktrees") {
		return ""
	}
	if info, err := os.Stat(wt); err != nil || !info.IsDir() {
		return ""
	}
	return wt
}

func (g *Gateway) deleteOldSessions(ctx context.Context, days int, ids []string) sessionPruneResult {
	g.storageCleanMu.Lock()
	defer g.storageCleanMu.Unlock()

	res := sessionPruneResult{Deleted: []string{}, Skipped: []sessionPruneSkip{}}
	before := g.InspectStorage()
	// Re-evaluate eligibility now: a session may have been reopened, become
	// busy or been deleted since the preview.
	eligible := map[string]sessionPruneCandidate{}
	for _, c := range g.oldSessionCandidates(ctx, pruneCutoff(days, time.Now()), false) {
		eligible[c.ID] = c
	}
	seen := map[string]bool{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if seen[id] {
			continue
		}
		seen[id] = true
		c, ok := eligible[id]
		switch {
		case !ok:
			res.Skipped = append(res.Skipped, sessionPruneSkip{ID: id, Reason: pruneSkipNotEligible})
			continue
		case c.busy || g.runner.IsSessionBusy(id):
			res.Skipped = append(res.Skipped, sessionPruneSkip{ID: id, Reason: pruneSkipBusy})
			continue
		}
		if skip, ok := g.removeOldSessionWorktree(c); !ok {
			res.Skipped = append(res.Skipped, skip)
			continue
		}
		if err := g.runner.DeleteConversation(ctx, id); err != nil {
			res.Skipped = append(res.Skipped, sessionPruneSkip{ID: id, Reason: pruneSkipDeleteError, Error: err.Error()})
			continue
		}
		res.Deleted = append(res.Deleted, id)
	}

	if len(res.Deleted) > 0 {
		dbPath := hoxipaths.DBPath()
		if dbPath != "" && !g.isAnySessionBusy() {
			if err := ocstate.CleanDatabaseEventLog(ctx, dbPath); err != nil {
				diagnostics.Record(diagnostics.Entry{Kind: "system", Level: "warn", Source: "storage", Message: err.Error()})
			}
		} else if dbPath != "" {
			res.VacuumDeferred = true
		}
	}
	res.Storage = g.InspectStorage()
	res.FreedBytes = max(0, before.TotalBytes-res.Storage.TotalBytes)
	return res
}

// removeOldSessionWorktree drops the session's clean isolated worktree but
// keeps its branch, so committed work stays recoverable. Uncommitted changes
// make the session ineligible instead of being discarded.
func (g *Gateway) removeOldSessionWorktree(c sessionPruneCandidate) (sessionPruneSkip, bool) {
	if c.worktreeDir == "" {
		return sessionPruneSkip{}, true
	}
	if g.worktrees == nil {
		return sessionPruneSkip{ID: c.ID, Reason: pruneSkipWorktreeError, Error: "worktree manager unavailable"}, false
	}
	if err := g.worktrees.ForDirectory(c.Directory).RemoveWithOptions(c.ID, false, true); err != nil {
		reason := pruneSkipWorktreeError
		if strings.Contains(err.Error(), "uncommitted changes") {
			reason = pruneSkipWorktreeDirty
		}
		return sessionPruneSkip{ID: c.ID, Reason: reason, Error: err.Error()}, false
	}
	if info, err := os.Stat(c.worktreeDir); err == nil && info.IsDir() {
		return sessionPruneSkip{ID: c.ID, Reason: pruneSkipWorktreeError, Error: "worktree directory still exists"}, false
	}
	return sessionPruneSkip{}, true
}
