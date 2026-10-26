package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/opencode"
)

const (
	engineInstallTimeout = 6 * time.Minute
	// engineHealthCacheTTL keeps the settings page from paying a 1.5s probe on
	// every system-info refresh while a dead engine is being diagnosed.
	engineHealthCacheTTL = 5 * time.Second
)

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
	g.engineReadyMu.Lock()
	if !g.engineReadyAt.IsZero() && time.Since(g.engineReadyAt) < engineHealthCacheTTL {
		ok := g.engineReadyOK
		g.engineReadyMu.Unlock()
		return ok
	}
	g.engineReadyMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	status, _, err := g.openCode.Raw(ctx, http.MethodGet, "/global/health", nil, nil)
	ok := err == nil && status == http.StatusOK

	g.engineReadyMu.Lock()
	g.engineReadyOK = ok
	g.engineReadyAt = time.Now()
	g.engineReadyMu.Unlock()
	return ok
}

// invalidateEngineReadyCache forces the next readiness probe to hit the engine,
// so an install or restart is reflected immediately.
func (g *Gateway) invalidateEngineReadyCache() {
	g.engineReadyMu.Lock()
	g.engineReadyAt = time.Time{}
	g.engineReadyMu.Unlock()
}

// StartEnginePrewarm warms the OpenCode daemon's per-directory caches for every
// known project folder, so the first expand of a workspace never waits on the
// engine's lazy per-directory start. It runs until ctx is cancelled.
func (g *Gateway) StartEnginePrewarm(ctx context.Context) {
	if g.runner != nil {
		g.runner.StartProjectPrewarm(ctx)
	}
	g.StartStorageMaintenance(ctx)
}

// openCodeBinarySource classifies the resolved binary for the settings page:
// bundled with the app, downloaded by Hoxi, or user-provided.
func openCodeBinarySource(binPath string) string {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		return ""
	}
	cleanBin := filepath.Clean(binPath)
	if binDir := hoxipaths.BinDir(); binDir != "" {
		if strings.HasPrefix(cleanBin, filepath.Clean(binDir)+string(os.PathSeparator)) {
			return "downloaded"
		}
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		exeDir := filepath.Dir(filepath.Clean(exe))
		for _, dir := range []string{exeDir, filepath.Clean(filepath.Join(exeDir, "..", "Resources"))} {
			if filepath.Dir(cleanBin) == dir {
				return "bundled"
			}
		}
	}
	return "external"
}

// handleEngineInstall installs (when missing) and starts the OpenCode daemon.
// It is idempotent: a healthy engine returns immediately, while a failed
// background bootstrap can be retried without restarting Hoxi. Passing
// {"restart":true} forces a fresh process.
func (g *Gateway) handleEngineInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	restart := strings.TrimSpace(r.URL.Query().Get("restart")) != ""
	var body struct {
		Restart bool `json:"restart"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil && err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	restart = restart || body.Restart

	// The desktop host serialises installs (and the boot-time bootstrap) on
	// its own engine mutex, so concurrent requests simply wait and then see
	// the healthy engine.
	g.engineMu.Lock()
	installer := g.engineInstaller
	g.engineMu.Unlock()
	if installer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "engine installer is unavailable in this host")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), engineInstallTimeout)
	defer cancel()
	binPath, err := installer(ctx, restart)
	g.invalidateEngineReadyCache()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, engineInstallResponse{
		OK:              true,
		OpenCodeBinPath: binPath,
		OpenCodeVersion: opencode.ResolveVersion(binPath),
		Ready:           g.engineReady(),
	})
}
