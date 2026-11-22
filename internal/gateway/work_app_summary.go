package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
)

const (
	workAppRegistryPath        = "/api/hoxi/work/apps-summary"
	workAppOverviewPath        = "/api/hoxi/work/apps-overview"
	workPackageSessionsPrefix  = "/api/hoxi/work/app-sessions/"
	workPackageSessionsPattern = "/api/hoxi/work/app-sessions/*"

	maxWorkPackageSessionBodyBytes = 8 << 10
	maxWorkPackageSessionTitleLen  = 200
	maxWorkPackageCreateCacheSize  = 256
)

// WorkAppRegistryEntry is the strict v1 mobile directory wire contract. Never
// marshal an extension's manifest, connection state, or installation metadata here.
type WorkAppRegistryEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// WorkAppLaunchpadEntry is a sanitized static quick-start prompt item from an
// installed extension package's launchpad slot. Never carries file paths,
// local service commands, permissions, or store credentials.
type WorkAppLaunchpadEntry struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
	Skill       string `json:"skill,omitempty"`
}

// WorkAppCatalogDetail holds sanitized read-only display metadata for an
// installed extension package, plus optional pre-resolved internal session
// matching scopes (never serialized over the wire).
type WorkAppCatalogDetail struct {
	ID                 string
	Name               string
	Status             string
	Description        string
	Launchpad          []WorkAppLaunchpadEntry
	HasAccountBinding  bool
	ActiveAccountLabel string
	ScopeLoaded        bool
	SessionIDs         []string
	Directories        []string
}

// WorkAppOverviewEntry is the enriched mobile directory wire contract combining
// sanitized installed metadata with live session/attention counts.
type WorkAppOverviewEntry struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Status               string `json:"status"`
	Description          string `json:"description,omitempty"`
	SessionCount         int    `json:"sessionCount"`
	RunningCount         int    `json:"runningCount"`
	PendingApprovalCount int    `json:"pendingApprovalCount"`
}

// WorkPackageSessionScope identifies the installed state and session matching
// scope (explicit session bindings + app-scoped workspace directories) for a
// single Work App. Never exposes store credentials, proxies, or package paths.
type WorkPackageSessionScope struct {
	Installed   bool
	SessionIDs  []string
	Directories []string
}

type workAppRemoteMetadata struct {
	ID                 string                  `json:"id"`
	Name               string                  `json:"name"`
	Description        string                  `json:"description,omitempty"`
	Launchpad          []WorkAppLaunchpadEntry `json:"launchpad,omitempty"`
	CanCreateSession   bool                    `json:"canCreateSession"`
	HasAccountBinding  bool                    `json:"hasAccountBinding,omitempty"`
	ActiveAccountLabel string                  `json:"activeAccountLabel,omitempty"`
}

type createWorkPackageSessionRequest struct {
	Title           string `json:"title,omitempty"`
	Runtime         string `json:"runtime,omitempty"`
	ProviderID      string `json:"providerID,omitempty"`
	ModelID         string `json:"modelID,omitempty"`
	ClientRequestID string `json:"clientRequestId,omitempty"`
}

type workPackageSessionCreateRecord struct {
	appID     string
	digest    string
	sessionID string
}

type workPackageSessionCreateCache struct {
	mu       sync.Mutex
	order    []string
	records  map[string]workPackageSessionCreateRecord
	inflight map[string]chan struct{}
}

// acquire returns an existing record for key if completed, or waits for any
// concurrent creator with the same key to finish before taking ownership.
// When ok is false, the caller owns the in-flight slot and must call release.
func (c *workPackageSessionCreateCache) acquire(ctx context.Context, key string) (rec workPackageSessionCreateRecord, found bool, ctxErr error) {
	for {
		c.mu.Lock()
		if c.records != nil {
			if existing, ok := c.records[key]; ok {
				c.mu.Unlock()
				return existing, true, nil
			}
		}
		if c.inflight != nil {
			if waitCh, busy := c.inflight[key]; busy {
				c.mu.Unlock()
				select {
				case <-waitCh:
					continue
				case <-ctx.Done():
					return workPackageSessionCreateRecord{}, false, ctx.Err()
				}
			}
		}
		if c.inflight == nil {
			c.inflight = make(map[string]chan struct{})
		}
		c.inflight[key] = make(chan struct{})
		c.mu.Unlock()
		return workPackageSessionCreateRecord{}, false, nil
	}
}

func (c *workPackageSessionCreateCache) invalidateIf(key, sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rec, ok := c.records[key]; ok && rec.sessionID == sessionID {
		delete(c.records, key)
		for i, k := range c.order {
			if k == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
}

func (c *workPackageSessionCreateCache) complete(key string, rec *workPackageSessionCreateRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rec != nil {
		if c.records == nil {
			c.records = make(map[string]workPackageSessionCreateRecord)
		}
		if _, exists := c.records[key]; !exists {
			if len(c.order) >= maxWorkPackageCreateCacheSize {
				evict := c.order[0]
				c.order = c.order[1:]
				delete(c.records, evict)
			}
			c.order = append(c.order, key)
		}
		c.records[key] = *rec
	}
	if ch, ok := c.inflight[key]; ok {
		delete(c.inflight, key)
		close(ch)
	}
}

func (g *Gateway) handleWorkAppRegistryEntry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "paired controller required")
		return
	}
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		writeJSONError(w, http.StatusForbidden, "only a directory read is supported")
		return
	}
	for _, ext := range g.extensions {
		if source, ok := ext.(interface {
			ListWorkAppRegistryEntries() ([]WorkAppRegistryEntry, error)
		}); ok {
			items, err := source.ListWorkAppRegistryEntries()
			if err != nil {
				writeJSONError(w, http.StatusServiceUnavailable, "work directory unavailable")
				return
			}
			if items == nil {
				items = []WorkAppRegistryEntry{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
	}
	writeJSONError(w, http.StatusNotImplemented, "work directory not supported")
}

func (g *Gateway) handleWorkAppOverview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "paired controller required")
		return
	}
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		writeJSONError(w, http.StatusForbidden, "only a directory read is supported")
		return
	}

	var (
		details []WorkAppCatalogDetail
		found   bool
		err     error
		scopeFn func(string) (WorkPackageSessionScope, error)
	)
	for _, ext := range g.extensions {
		if s, ok := ext.(interface {
			WorkPackageSessionScope(appID string) (WorkPackageSessionScope, error)
		}); ok && scopeFn == nil {
			scopeFn = s.WorkPackageSessionScope
		}
		if rich, ok := ext.(interface {
			ListWorkAppCatalogDetails() ([]WorkAppCatalogDetail, error)
		}); ok {
			details, err = rich.ListWorkAppCatalogDetails()
			found = true
			break
		}
		if basic, ok := ext.(interface {
			ListWorkAppRegistryEntries() ([]WorkAppRegistryEntry, error)
		}); ok {
			var entries []WorkAppRegistryEntry
			entries, err = basic.ListWorkAppRegistryEntries()
			found = true
			if err == nil {
				details = make([]WorkAppCatalogDetail, len(entries))
				for i, e := range entries {
					details[i] = WorkAppCatalogDetail{ID: e.ID, Name: e.Name, Status: e.Status}
				}
			}
			break
		}
	}
	if !found {
		writeJSONError(w, http.StatusNotImplemented, "work directory not supported")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "work directory unavailable")
		return
	}

	var pendingBySession map[string]int
	if g.runner != nil {
		pendingBySession = g.runner.PendingAttentionCountsBySession()
	}

	items := make([]WorkAppOverviewEntry, 0, len(details))
	for _, d := range details {
		entry := WorkAppOverviewEntry{
			ID:          d.ID,
			Name:        d.Name,
			Status:      d.Status,
			Description: strings.TrimSpace(d.Description),
		}
		if g.runner != nil {
			if d.ScopeLoaded {
				sessions, statuses := g.runner.ListSessionsByScope(d.SessionIDs, d.Directories)
				entry.SessionCount, entry.RunningCount, entry.PendingApprovalCount =
					summarizeWorkPackageSessions(sessions, statuses, pendingBySession)
			} else if scopeFn != nil {
				if scope, scopeErr := scopeFn(d.ID); scopeErr == nil && scope.Installed {
					sessions, statuses := g.runner.ListSessionsByScope(scope.SessionIDs, scope.Directories)
					entry.SessionCount, entry.RunningCount, entry.PendingApprovalCount =
						summarizeWorkPackageSessions(sessions, statuses, pendingBySession)
				}
			}
		}
		items = append(items, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func summarizeWorkPackageSessions(
	sessions []runner.SessionInfo,
	statuses map[string]string,
	pendingBySession map[string]int,
) (sessionCount, runningCount, pendingCount int) {
	if len(sessions) == 0 {
		return 0, 0, 0
	}
	byID := make(map[string]runner.SessionInfo, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}
	rootOf := func(id string) string {
		cur := id
		seen := map[string]bool{cur: true}
		for {
			s, ok := byID[cur]
			if !ok || s.ParentID == "" {
				return cur
			}
			if _, parentInScope := byID[s.ParentID]; !parentInScope {
				return cur
			}
			if seen[s.ParentID] {
				return cur
			}
			seen[s.ParentID] = true
			cur = s.ParentID
		}
	}
	roots := make(map[string]bool)
	busyRoots := make(map[string]bool)
	for _, s := range sessions {
		rootID := rootOf(s.ID)
		roots[rootID] = true
		if statuses[s.ID] == "busy" {
			busyRoots[rootID] = true
		}
		if n := pendingBySession[s.ID]; n > 0 {
			pendingCount += n
		}
	}
	return len(roots), len(busyRoots), pendingCount
}

func (g *Gateway) handleWorkPackageSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "paired controller required")
		return
	}
	if r.URL.RawQuery != "" {
		writeJSONError(w, http.StatusForbidden, "query parameters are not supported on app sessions route")
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, workPackageSessionsPrefix)
	if rawID == "" || strings.Contains(rawID, "/") {
		writeJSONError(w, http.StatusForbidden, "invalid work app sessions route")
		return
	}
	appID, err := hoxipaths.ValidateWorkAppID(rawID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid work app id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.handleWorkPackageSessionsGet(w, appID)
	case http.MethodPost:
		g.handleWorkPackageSessionsCreate(w, r, appID)
	default:
		writeJSONError(w, http.StatusForbidden, "method not allowed on app sessions route")
	}
}

func (g *Gateway) handleWorkPackageSessionsGet(w http.ResponseWriter, appID string) {
	for _, ext := range g.extensions {
		source, ok := ext.(interface {
			WorkPackageSessionScope(appID string) (WorkPackageSessionScope, error)
		})
		if !ok {
			continue
		}
		scope, err := source.WorkPackageSessionScope(appID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "work app sessions unavailable")
			return
		}
		if !scope.Installed {
			writeJSONError(w, http.StatusNotFound, "work app not installed")
			return
		}
		items := []runner.SessionInfo{}
		statuses := map[string]string{}
		pendingApprovals := map[string]int{}
		if g.runner != nil {
			items, statuses = g.runner.ListSessionsByScope(scope.SessionIDs, scope.Directories)
			if items == nil {
				items = []runner.SessionInfo{}
			}
			if statuses == nil {
				statuses = map[string]string{}
			}
			if allPending := g.runner.PendingAttentionCountsBySession(); len(allPending) > 0 {
				for _, item := range items {
					if n := allPending[item.ID]; n > 0 {
						pendingApprovals[item.ID] = n
					}
				}
			}
		}
		resp := map[string]any{
			"items":            items,
			"statuses":         statuses,
			"pendingApprovals": pendingApprovals,
		}
		_, canCreate := ext.(interface {
			PrepareWorkPackageSession(appID string) (string, bool, error)
			BindWorkPackageSession(appID, sessionID string) error
		})
		if detailSrc, ok := ext.(interface {
			WorkPackageRemoteDetail(appID string) (WorkAppCatalogDetail, bool, error)
		}); ok {
			if detail, found, dErr := detailSrc.WorkPackageRemoteDetail(appID); dErr == nil && found {
				resp["app"] = workAppRemoteMetadata{
					ID:                 detail.ID,
					Name:               detail.Name,
					Description:        strings.TrimSpace(detail.Description),
					Launchpad:          detail.Launchpad,
					CanCreateSession:   canCreate,
					HasAccountBinding:  detail.HasAccountBinding,
					ActiveAccountLabel: trimRunes(strings.TrimSpace(detail.ActiveAccountLabel), 80),
				}
			}
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	writeJSONError(w, http.StatusNotImplemented, "work app sessions not supported")
}

func (g *Gateway) handleWorkPackageSessionsCreate(w http.ResponseWriter, r *http.Request, appID string) {
	if g.runner == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "runner unavailable")
		return
	}
	type sessionCreator interface {
		PrepareWorkPackageSession(appID string) (string, bool, error)
		BindWorkPackageSession(appID, sessionID string) error
	}
	var creator sessionCreator
	for _, ext := range g.extensions {
		if c, ok := ext.(sessionCreator); ok {
			creator = c
			break
		}
	}
	if creator == nil {
		writeJSONError(w, http.StatusNotImplemented, "work app session creation not supported")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxWorkPackageSessionBodyBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if len(raw) > maxWorkPackageSessionBodyBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var body createWorkPackageSessionRequest
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid work app session request")
			return
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "invalid work app session request")
			return
		}
	}

	title := trimRunes(strings.TrimSpace(body.Title), maxWorkPackageSessionTitleLen)
	runtimeName := strings.TrimSpace(body.Runtime)
	if runtimeName == "" {
		runtimeName = string(runner.RuntimeNative)
	}
	providerID := strings.TrimSpace(body.ProviderID)
	modelID := strings.TrimSpace(body.ModelID)
	reqID := strings.TrimSpace(body.ClientRequestID)
	if reqID == "" {
		reqID = strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
	}
	digest := runtimeName + "\x00" + providerID + "\x00" + modelID + "\x00" + title
	var createdRec *workPackageSessionCreateRecord
	if reqID != "" {
		for {
			prev, found, acqErr := g.workPkgCreateCache.acquire(r.Context(), reqID)
			if acqErr != nil {
				writeJSONError(w, http.StatusRequestTimeout, "request cancelled while waiting for duplicate request")
				return
			}
			if !found {
				defer func() {
					g.workPkgCreateCache.complete(reqID, createdRec)
				}()
				break
			}
			if prev.appID != appID || prev.digest != digest {
				writeJSONError(w, http.StatusConflict, "clientRequestId reused with different parameters")
				return
			}
			if existing, ok := g.runner.GetSessionInfo(prev.sessionID); ok {
				writeJSON(w, http.StatusOK, existing)
				return
			}
			g.workPkgCreateCache.invalidateIf(reqID, prev.sessionID)
		}
	}

	appDir, installed, err := creator.PrepareWorkPackageSession(appID)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "work app workspace unavailable")
		return
	}
	if !installed {
		writeJSONError(w, http.StatusNotFound, "work app not installed")
		return
	}

	info, status, err := g.runner.CreateConversation(r.Context(), runner.CreateSessionRequest{
		Title:       title,
		Directory:   appDir,
		Surface:     "work",
		Runtime:     runtimeName,
		ProviderID:  providerID,
		ModelID:     modelID,
		SandboxMode: "local",
	}, appDir)
	if err != nil {
		writeJSONError(w, status, err.Error())
		return
	}
	if err := creator.BindWorkPackageSession(appID, info.ID); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.runner.DeleteConversation(rollbackCtx, info.ID)
		writeJSONError(w, http.StatusServiceUnavailable, "failed to bind work app session")
		return
	}
	if reqID != "" {
		createdRec = &workPackageSessionCreateRecord{
			appID:     appID,
			digest:    digest,
			sessionID: info.ID,
		}
	}
	writeJSON(w, http.StatusOK, info)
}

func trimRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
