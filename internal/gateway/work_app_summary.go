package gateway

import (
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/runner"
)

const (
	workAppRegistryPath        = "/api/hoxi/work/apps-summary"
	workPackageSessionsPrefix  = "/api/hoxi/work/app-sessions/"
	workPackageSessionsPattern = "/api/hoxi/work/app-sessions/*"
)

// WorkAppRegistryEntry is the entire mobile wire contract. Never marshal an
// extension's manifest, connection state, or installation metadata here.
type WorkAppRegistryEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// WorkPackageSessionScope identifies the installed state and session matching
// scope (explicit session bindings + app-scoped workspace directories) for a
// single Work App. Never exposes store credentials, proxies, or package paths.
type WorkPackageSessionScope struct {
	Installed   bool
	SessionIDs  []string
	Directories []string
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

func (g *Gateway) handleWorkPackageSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "paired controller required")
		return
	}
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		writeJSONError(w, http.StatusForbidden, "only a read-only session listing is supported")
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
	for _, ext := range g.extensions {
		if source, ok := ext.(interface {
			WorkPackageSessionScope(appID string) (WorkPackageSessionScope, error)
		}); ok {
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
			if g.runner != nil {
				items, statuses = g.runner.ListSessionsByScope(scope.SessionIDs, scope.Directories)
				if items == nil {
					items = []runner.SessionInfo{}
				}
				if statuses == nil {
					statuses = map[string]string{}
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"items":    items,
				"statuses": statuses,
			})
			return
		}
	}
	writeJSONError(w, http.StatusNotImplemented, "work app sessions not supported")
}
