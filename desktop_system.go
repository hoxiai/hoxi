package main

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type systemRuntimeInfoResponse struct {
	OpenCodeBinPath     string        `json:"openCodeBinPath"`
	OpenCodeVersion     string        `json:"openCodeVersion"`
	OpenCodePort        int           `json:"openCodePort"`
	WSBaseURL           string        `json:"wsBaseURL"`
	RootDir             string        `json:"rootDir"`
	UserConfigPath      string        `json:"userConfigPath"`
	WorkspaceConfigPath string        `json:"workspaceConfigPath"`
	OS                  string        `json:"os"`
	Arch                string        `json:"arch"`
	GoVersion           string             `json:"goVersion"`
	Status              desktopCodeSummary `json:"status"`
}

var (
	cachedOpenCodeVerMu   sync.Mutex
	cachedOpenCodeVerPath string
	cachedOpenCodeVerStr  string
)

func resolveOpenCodeVersion(binPath string) string {
	if binPath == "" {
		return ""
	}
	cachedOpenCodeVerMu.Lock()
	if cachedOpenCodeVerPath == binPath && cachedOpenCodeVerStr != "" {
		ver := cachedOpenCodeVerStr
		cachedOpenCodeVerMu.Unlock()
		return ver
	}
	cachedOpenCodeVerMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	ver := strings.TrimSpace(lines[0])
	if ver != "" {
		cachedOpenCodeVerMu.Lock()
		cachedOpenCodeVerPath = binPath
		cachedOpenCodeVerStr = ver
		cachedOpenCodeVerMu.Unlock()
	}
	return ver
}

func (g *desktopGateway) handleSystemRuntimeInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	binPath := findOpenCodeBinary("opencode")
	ver := resolveOpenCodeVersion(binPath)

	var st desktopCodeSummary
	if g.status != nil {
		st = g.status.snapshot()
	}

	writeJSON(w, http.StatusOK, systemRuntimeInfoResponse{
		OpenCodeBinPath:     binPath,
		OpenCodeVersion:     ver,
		OpenCodePort:        g.openCodePort,
		WSBaseURL:           g.wsBaseURL,
		RootDir:             g.rootDir,
		UserConfigPath:      userHoxiModelsPath(),
		WorkspaceConfigPath: g.workspaceHoxiModelsPath(),
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		GoVersion:           runtime.Version(),
		Status:              st,
	})
}
