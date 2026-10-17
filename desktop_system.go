package main

import (
	"context"
	"net/http"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"
)

type systemRuntimeInfoResponse struct {
	OpenCodeBinPath     string             `json:"openCodeBinPath"`
	OpenCodeVersion     string             `json:"openCodeVersion"`
	OpenCodePort        int                `json:"openCodePort"`
	WSBaseURL           string             `json:"wsBaseURL"`
	RootDir             string             `json:"rootDir"`
	UserConfigPath      string             `json:"userConfigPath"`
	WorkspaceConfigPath string             `json:"workspaceConfigPath"`
	OS                  string             `json:"os"`
	Arch                string             `json:"arch"`
	GoVersion           string             `json:"goVersion"`
	SystemUsername      string             `json:"systemUsername"`
	GitUserName         string             `json:"gitUserName"`
	GitUserEmail        string             `json:"gitUserEmail"`
	Status              desktopCodeSummary `json:"status"`
}

var (
	cachedOpenCodeVerMu   sync.Mutex
	cachedOpenCodeVerPath string
	cachedOpenCodeVerStr  string

	cachedUserIdentityOnce sync.Once
	cachedSystemUsername   string
	cachedGitUserName      string
	cachedGitUserEmail     string
)

func resolveLocalUserIdentity() (sysUser, gitName, gitEmail string) {
	cachedUserIdentityOnce.Do(func() {
		if u, err := user.Current(); err == nil && u != nil {
			uname := strings.TrimSpace(u.Username)
			if idx := strings.LastIndex(uname, `\`); idx >= 0 && idx < len(uname)-1 {
				uname = uname[idx+1:]
			}
			cachedSystemUsername = uname
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "git", "config", "--global", "user.name").Output(); err == nil {
			cachedGitUserName = strings.TrimSpace(string(out))
		}
		if out, err := exec.CommandContext(ctx, "git", "config", "--global", "user.email").Output(); err == nil {
			cachedGitUserEmail = strings.TrimSpace(string(out))
		}
	})
	return cachedSystemUsername, cachedGitUserName, cachedGitUserEmail
}

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
	sysUser, gitName, gitEmail := resolveLocalUserIdentity()

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
		SystemUsername:      sysUser,
		GitUserName:         gitName,
		GitUserEmail:        gitEmail,
		Status:              st,
	})
}
