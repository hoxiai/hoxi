package gateway

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/opencode"
	"github.com/hoxiai/hoxi/internal/provider"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

type systemRuntimeInfoResponse struct {
	AppVersion          string            `json:"appVersion"`
	OpenCodeBinPath     string            `json:"openCodeBinPath"`
	OpenCodeVersion     string            `json:"openCodeVersion"`
	OpenCodePort        int               `json:"openCodePort"`
	WSBaseURL           string            `json:"wsBaseURL"`
	RootDir             string            `json:"rootDir"`
	UserConfigPath      string            `json:"userConfigPath"`
	WorkspaceConfigPath string            `json:"workspaceConfigPath"`
	OS                  string            `json:"os"`
	Arch                string            `json:"arch"`
	GoVersion           string            `json:"goVersion"`
	SystemUsername      string            `json:"systemUsername"`
	GitUserName         string            `json:"gitUserName"`
	GitUserEmail        string            `json:"gitUserEmail"`
	Status              statusbar.Summary `json:"status"`
}

var (
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
		readGitGlobal := func(key string) string {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "git", "config", "--global", key)
			cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
			out, err := cmd.Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}
		cachedGitUserName = readGitGlobal("user.name")
		cachedGitUserEmail = readGitGlobal("user.email")
	})
	return cachedSystemUsername, cachedGitUserName, cachedGitUserEmail
}

func (g *Gateway) handleSystemRuntimeInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	binPath := opencode.FindBinary("opencode")
	ver := opencode.ResolveVersion(binPath)
	sysUser, gitName, gitEmail := resolveLocalUserIdentity()

	var st statusbar.Summary
	if g.status != nil {
		st = g.status.Snapshot()
	}

	var wsConfigPath string
	if g.providers != nil {
		wsConfigPath = g.providers.WorkspaceHoxiModelsPath()
	}

	writeJSON(w, http.StatusOK, systemRuntimeInfoResponse{
		AppVersion:          buildinfo.Version,
		OpenCodeBinPath:     binPath,
		OpenCodeVersion:     ver,
		OpenCodePort:        g.openCodePort,
		WSBaseURL:           g.wsBaseURL,
		RootDir:             g.rootDir,
		UserConfigPath:      provider.UserHoxiModelsPath(),
		WorkspaceConfigPath: wsConfigPath,
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		GoVersion:           runtime.Version(),
		SystemUsername:      sysUser,
		GitUserName:         gitName,
		GitUserEmail:        gitEmail,
		Status:              st,
	})
}
