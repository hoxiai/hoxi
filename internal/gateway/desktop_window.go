package gateway

import "net/http"

// SetDesktopWindowCloseHandler wires the desktop window close callback used by
// custom frameless window chrome (for example on Windows). It stays nil in
// headless mode and is never exposed through remote tunnels.
func (g *Gateway) SetDesktopWindowCloseHandler(fn func()) {
	g.windowMu.Lock()
	g.onDesktopWindowClose = fn
	g.windowMu.Unlock()
}

func (g *Gateway) registerDesktopWindowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/hoxi/desktop/window/close", func(w http.ResponseWriter, r *http.Request) {
		if isRemoteRequest(r) {
			writeJSONError(w, http.StatusForbidden, "desktop window controls are local only")
			return
		}
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		g.windowMu.RLock()
		closeFn := g.onDesktopWindowClose
		g.windowMu.RUnlock()
		if closeFn == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "desktop window not attached")
			return
		}
		closeFn()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}
