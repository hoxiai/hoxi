package gateway

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"

	"github.com/hoxiai/hoxi/internal/workcatalog"
)

func (g *Gateway) workCatalogTarget(r *http.Request) (workcatalog.Target, error) {
	if g.workApps == nil || g.workCatalog == nil {
		return workcatalog.Target{}, errors.New("Work catalog is unavailable")
	}
	if len(r.URL.Query()["storeId"]) != 1 {
		return workcatalog.Target{}, errors.New("one registered storeId is required")
	}
	profile, ok := g.workApps.GetStore(r.URL.Query().Get("storeId"))
	if !ok {
		return workcatalog.Target{}, errors.New("Work store not found")
	}
	return g.workCatalog.ResolveTarget(r.Context(), workcatalog.StoreSettings{StoreID: profile.ID, Platform: profile.Platform,
		SellerLoginID: profile.Alibaba1688SellerLoginID, CredentialEnv: profile.Alibaba1688CredentialEnv,
		ProxyURL: profile.ProxyURL, ProxyRequired: profile.ProxyRequired})
}

// The catalog's UI facade is store-scoped, so browsing does not create a chat.
// MCP callers retain their managed-session binding. Credential changes require
// both the per-boot token and a recognized desktop UI origin/context.
func (g *Gateway) resolveWorkCatalogUI(r *http.Request) (workcatalog.Target, error) {
	if isRemoteRequest(r) || !trustedHost(r.Host) || r.Header.Get("X-Hoxi-Work-UI") != "true" {
		return workcatalog.Target{}, errors.New("Work catalog requires the desktop UI")
	}
	token := g.GatewayToken()
	if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		return workcatalog.Target{}, errors.New("Work catalog requires the local gateway token")
	}
	origin := r.Header.Get("Origin")
	allowed, configuredHost, native := g.trustedComputerUI(origin, r.Host)
	trustedReferer := false
	wailsReferer := false
	if ref, err := url.Parse(r.Header.Get("Referer")); err == nil && ref.User == nil && ref.Host != "" {
		allowedRef, _, refNative := g.trustedComputerUI(ref.Scheme+"://"+ref.Host, ref.Host)
		trustedReferer = allowedRef
		wailsReferer = ref.Scheme == "wails" && allowedRef && refNative
	}
	if origin != "" {
		if len(r.Header.Values("Origin")) != 1 || !allowed {
			return workcatalog.Target{}, errors.New("untrusted Work catalog origin")
		}
	} else {
		site := r.Header.Get("Sec-Fetch-Site")
		if !native && site == "" && wailsReferer {
			native = true
		}
		nativeAllowed := native && (site == "" || site == "same-origin" || site == "none")
		httpAllowed := site == "same-origin" && (configuredHost || trustedReferer)
		if !nativeAllowed && !httpAllowed {
			return workcatalog.Target{}, errors.New("Work catalog requires a desktop UI context")
		}
	}
	return g.workCatalogTarget(r)
}
