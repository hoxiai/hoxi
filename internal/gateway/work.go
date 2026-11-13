package gateway

import (
	"net/http"
	"strings"

	"github.com/hoxiai/hoxi/internal/attachment"
	"github.com/hoxiai/hoxi/internal/browser"
	"github.com/hoxiai/hoxi/internal/ecom"
	"github.com/hoxiai/hoxi/internal/workapps"
)

type workStoreLookup struct {
	svc *workapps.Service
}

func (l workStoreLookup) LookupStoreDefaults(storeID string) (ecom.StoreDefaults, bool) {
	if l.svc == nil {
		return ecom.StoreDefaults{}, false
	}
	prof, ok := l.svc.GetStore(storeID)
	if !ok || prof == nil {
		return ecom.StoreDefaults{}, false
	}
	return ecom.StoreDefaults{
		StoreID:            prof.ID,
		Platform:           prof.Platform,
		Marketplace:        prof.Marketplace,
		Currency:           prof.Currency,
		BrandName:          prof.BrandName,
		ReferralFeePct:     prof.ReferralFeePct,
		TargetNetMarginPct: prof.TargetNetMarginPct,
		ShippingRatePerKg:  prof.ShippingRatePerKg,
		ShippingCurrency:   prof.ShippingCurrency,
		FXRate:             prof.FXRate,
		ReturnRatePct:      prof.ReturnRatePct,
		TargetACOSPct:      prof.TargetACOSPct,
		TargetROAS:         prof.TargetROAS,
		ForbiddenTerms:     prof.ForbiddenTerms,
	}, true
}

type gatewaySpreadsheetIO struct{}

func (gatewaySpreadsheetIO) ParseSpreadsheet(path string, maxRows int) ([]ecom.SpreadsheetSheet, error) {
	raw, err := attachment.ParseSpreadsheetFile(path, maxRows)
	if err != nil {
		return nil, err
	}
	out := make([]ecom.SpreadsheetSheet, len(raw))
	for i, s := range raw {
		out[i] = ecom.SpreadsheetSheet{Name: s.Name, Rows: s.Rows}
	}
	return out, nil
}

func (gatewaySpreadsheetIO) WriteXLSX(path string, sheets []ecom.SpreadsheetSheet) error {
	raw := make([]attachment.SpreadsheetSheet, len(sheets))
	for i, s := range sheets {
		raw[i] = attachment.SpreadsheetSheet{Name: s.Name, Rows: s.Rows}
	}
	return attachment.WriteXLSXFile(path, raw)
}

func (gatewaySpreadsheetIO) WriteDelimited(path string, rows [][]string, sep rune) error {
	return attachment.WriteDelimitedFile(path, rows, sep)
}

func (g *Gateway) walkWorkSessionAncestors(sessionID string, visit func(cur string) bool) {
	cur := strings.TrimSpace(sessionID)
	for depth := 0; cur != "" && depth < 8; depth++ {
		if visit(cur) {
			return
		}
		if g.runner == nil || g.runner.Store() == nil {
			return
		}
		rec, ok := g.runner.Store().Meta(cur)
		if !ok || strings.TrimSpace(rec.ParentID) == "" || rec.ParentID == cur {
			return
		}
		cur = strings.TrimSpace(rec.ParentID)
	}
}

func (g *Gateway) wireWorkResolvers() {
	if g == nil || g.workApps == nil {
		return
	}
	if g.browser != nil {
		g.browser.SetProfileResolver(func(sessionID, _ string) string {
			resolved := "default"
			g.walkWorkSessionAncestors(sessionID, func(cur string) bool {
				if key := g.workApps.ResolveBrowserProfileKey(cur); key != "" {
					resolved = key
					return true
				}
				return false
			})
			return resolved
		})
		g.browser.SetEnvironmentResolver(func(sessionID, _ string) (browser.StoreEnvConfig, error) {
			var (
				cfg browser.StoreEnvConfig
				err error
			)
			g.walkWorkSessionAncestors(sessionID, func(cur string) bool {
				if g.workApps.ResolveBrowserProfileKey(cur) == "" {
					return false
				}
				env, resolveErr := g.workApps.ResolveBrowserEnvironment(cur)
				if resolveErr != nil {
					err = resolveErr
					return true
				}
				cfg = browser.StoreEnvConfig{
					StoreID:       env.StoreID,
					ProxyURL:      env.ProxyURL,
					RedactedProxy: env.RedactedProxy,
					ProxyRequired: env.ProxyRequired,
					Timezone:      env.Timezone,
					Locale:        env.Locale,
				}
				return true
			})
			return cfg, err
		})
	}
	if g.runner != nil {
		g.runner.SetSessionContextResolver(func(sessionID, _ string) string {
			var block string
			g.walkWorkSessionAncestors(sessionID, func(cur string) bool {
				if ctxBlock := g.workApps.FormatSessionStoreContext(cur); ctxBlock != "" {
					block = ctxBlock
					return true
				}
				return false
			})
			return block
		})
	}
}

func desktopOnlyWorkHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, "work store endpoints are only available on the desktop")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gateway) registerWorkRoutes() {
	if g == nil || g.mux == nil {
		return
	}
	if g.workApps != nil {
		wh := desktopOnlyWorkHandler(workapps.NewHTTPHandler(g.workApps))
		g.mux.Handle("/api/hoxi/work/apps", wh)
		g.mux.Handle("/api/hoxi/work/stores", wh)
		g.mux.Handle("/api/hoxi/work/proxy-check", wh)
	}
	g.registerWorkMCPRoutes()
	eh := desktopOnlyWorkHandler(ecom.NewHTTPHandler(workStoreLookup{svc: g.workApps}, gatewaySpreadsheetIO{}))
	g.mux.Handle("/api/hoxi/work/ecom/", eh)
}
