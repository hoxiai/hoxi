package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/hoxiai/hoxi/internal/opencode"
)

const engineInstallTimeout = 6 * time.Minute

type engineInstallResponse struct {
	OK              bool   `json:"ok"`
	OpenCodeBinPath string `json:"openCodeBinPath"`
	OpenCodeVersion string `json:"openCodeVersion"`
	Ready           bool   `json:"ready"`
}

// engineReady probes the supervised daemon's health endpoint. It never
// surfaces transport details, so callers only learn ready/not-ready.
func (g *Gateway) engineReady() bool {
	if g.openCode == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	status, _, err := g.openCode.Raw(ctx, http.MethodGet, "/global/health", nil, nil)
	return err == nil && status == http.StatusOK
}

// handleEngineInstall installs (when missing) and starts the OpenCode daemon.
// It is idempotent: a healthy engine returns immediately, while a failed
// background bootstrap can be retried without restarting Hoxi.
func (g *Gateway) handleEngineInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	g.engineMu.Lock()
	installer := g.engineInstaller
	if installer == nil {
		g.engineMu.Unlock()
		writeJSONError(w, http.StatusServiceUnavailable, "engine installer is unavailable in this host")
		return
	}
	if g.engineInstalling {
		g.engineMu.Unlock()
		writeJSONError(w, http.StatusConflict, "engine install already in progress")
		return
	}
	g.engineInstalling = true
	g.engineMu.Unlock()
	defer func() {
		g.engineMu.Lock()
		g.engineInstalling = false
		g.engineMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(r.Context(), engineInstallTimeout)
	defer cancel()
	if err := installer(ctx); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	binPath := opencode.FindBinary("opencode")
	writeJSON(w, http.StatusOK, engineInstallResponse{
		OK:              true,
		OpenCodeBinPath: binPath,
		OpenCodeVersion: opencode.ResolveVersion(binPath),
		Ready:           g.engineReady(),
	})
}
