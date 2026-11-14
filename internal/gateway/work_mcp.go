package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/hoxiai/hoxi/internal/workcatalog"
)

func (g *Gateway) resolveWorkMCP(r *http.Request) (workcatalog.Connection, error) {
	if isRemoteRequest(r) {
		return workcatalog.Connection{}, errors.New("Work MCP is desktop-only")
	}
	// Authorization survives the normal own-route X-Hoxi-Token stripping. Never
	// accept tokens in URLs: MCP clients should send an Authorization header.
	token := g.GatewayToken()
	if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		return workcatalog.Connection{}, errors.New("Work MCP requires the local gateway bearer token")
	}
	if g.runner == nil || g.workApps == nil {
		return workcatalog.Connection{}, errors.New("Work session service is unavailable")
	}
	sessionID, directory, storeID := r.URL.Query().Get("sessionID"), r.URL.Query().Get("directory"), r.URL.Query().Get("storeId")
	if sessionID == "" || directory == "" || storeID == "" {
		return workcatalog.Connection{}, errors.New("sessionID, directory and storeId are required")
	}
	resolved, err := g.runner.ResolveComputerSession(sessionID, directory)
	if err != nil {
		return workcatalog.Connection{}, errors.New("Work MCP requires a managed session in its exact directory")
	}
	var boundID string
	g.walkWorkSessionAncestors(resolved, func(cur string) bool {
		profile := g.workApps.ResolveSessionStoreProfile(cur)
		if profile != nil {
			boundID = profile.ID
			return true
		}
		return false
	})
	if boundID != storeID {
		return workcatalog.Connection{}, errors.New("requested store does not match the session's bound store")
	}
	target, err := g.workCatalogTarget(r)
	if err != nil {
		return workcatalog.Connection{}, err
	}
	if !target.Capabilities.Supported || target.Scope.SellerLoginID == "unconfigured" {
		return workcatalog.Connection{}, errors.New("configure the bound store's seller identity first")
	}
	return workcatalog.Connection{
		Scope:               target.Scope,
		AuthorizationSource: target.Authorization.Source,
		Platform:            target.Platform,
		Authorize: func(ctx context.Context, draft workcatalog.Draft) error {
			// Recheck after waiting for approval as well: store bindings and session
			// lifetime can change while the user reviews the diff.
			if _, err := g.resolveWorkMCP(r.WithContext(ctx)); err != nil {
				return err
			}
			changes := map[string]any{"draftId": draft.ID, "productId": draft.ProductID, "revision": draft.Revision, "changes": draft.Changes}
			if err := g.runner.RequestWorkPublishApproval(ctx, resolved, directory, storeID, changes); err != nil {
				return err
			}
			_, err := g.resolveWorkMCP(r.WithContext(ctx))
			return err
		},
	}, nil
}

func (g *Gateway) registerWorkMCPRoutes() {
	if g.workCatalog == nil {
		return
	}
	g.mux.Handle("/api/hoxi/work/mcp", desktopOnlyWorkHandler(workcatalog.NewMCPHandler(g.workCatalog, g.resolveWorkMCP)))
	handler := workcatalog.NewHTTPHandler(g.workCatalog, g.resolveWorkCatalogUI, func(r *http.Request) (workcatalog.Authorize, error) {
		connection, err := g.resolveWorkMCP(r)
		return connection.Authorize, err
	})
	g.mux.Handle("/api/hoxi/work/catalog/", desktopOnlyWorkHandler(handler))
}
