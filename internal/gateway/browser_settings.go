package gateway

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/hoxiai/hoxi/internal/browser"
)

func (g *Gateway) loadBrowserSettings() {
	if g == nil || g.browser == nil {
		return
	}
	cfg := browser.LoadSettings(browser.SettingsPath())
	_ = g.browser.SetIdleMinutes(cfg.IdleMinutes)
}

func (g *Gateway) browserIdleMinutes() int {
	if g == nil || g.browser == nil {
		return browser.DefaultIdleMinutes
	}
	return g.browser.IdleMinutes()
}

// handleBrowserSettings reads or updates the built-in Chrome lifecycle settings
// (such as idle auto-close minutes). Desktop-only: remote controllers cannot
// mutate host Chrome process policies.
func (g *Gateway) handleBrowserSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "browser settings are only available on the desktop")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, browser.Settings{IdleMinutes: g.browserIdleMinutes()})
	case http.MethodPut, http.MethodPost:
		var input struct {
			IdleMinutes *int `json:"idleMinutes"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || input.IdleMinutes == nil {
			writeJSONError(w, http.StatusBadRequest, "idleMinutes integer is required")
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "expected exactly one JSON object")
			return
		}
		if err := browser.ValidateIdleMinutes(*input.IdleMinutes); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := browser.SaveSettings(browser.SettingsPath(), *input.IdleMinutes); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not persist browser settings")
			return
		}
		if g.browser != nil {
			_ = g.browser.SetIdleMinutes(*input.IdleMinutes)
		}
		writeJSON(w, http.StatusOK, browser.Settings{IdleMinutes: g.browserIdleMinutes()})
	default:
		w.Header().Set("Allow", "GET, PUT, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
