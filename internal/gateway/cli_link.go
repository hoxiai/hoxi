package gateway

import (
	"net/http"
	"os"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

var (
	resolveCLILinkExecutable = os.Executable
	resolveCLILinkBinDir     = func() string { return "" }
)

func (g *Gateway) handleSystemCLILink(w http.ResponseWriter, r *http.Request) {
	if isRemoteRequest(r) {
		writeJSONError(w, http.StatusForbidden, "CLI installation is only available on the local desktop")
		return
	}

	exePath, err := resolveCLILinkExecutable()
	if err != nil || strings.TrimSpace(exePath) == "" {
		writeJSON(w, http.StatusOK, hoxipaths.CLILinkStatus{
			Error: "unable to determine running Hoxi executable path",
		})
		return
	}

	rawPath := os.Getenv("PATH")
	preferredBinDir := resolveCLILinkBinDir()
	searchPath := rawPath
	if preferredBinDir != "" {
		if searchPath == "" {
			searchPath = preferredBinDir
		} else {
			searchPath = preferredBinDir + string(os.PathListSeparator) + searchPath
		}
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, hoxipaths.InspectCLILink(exePath, searchPath))

	case http.MethodPost:
		if _, err := hoxipaths.InstallCLISymlink(exePath, preferredBinDir); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, hoxipaths.InspectCLILink(exePath, searchPath))

	case http.MethodDelete:
		if _, err := hoxipaths.UninstallCLISymlink(exePath, preferredBinDir); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, hoxipaths.InspectCLILink(exePath, searchPath))

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
