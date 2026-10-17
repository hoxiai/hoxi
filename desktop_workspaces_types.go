package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const defaultWorkspaceIdentityColor = "#5EA0F7"

var (
	shortHexColorRE = regexp.MustCompile(`^#([0-9a-fA-F]{3})$`)
	longHexColorRE  = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)
)

type workspaceFolderInput struct {
	Path      string `json:"path"`
	IsPrimary bool   `json:"isPrimary"`
}

type workspaceFolder struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	IsPrimary bool   `json:"isPrimary"`
	Position  int    `json:"position"`
}

type workspaceProfile struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Color     string            `json:"color,omitempty"`
	Folders   []workspaceFolder `json:"folders"`
	CreatedAt string            `json:"createdAt"`
	UpdatedAt string            `json:"updatedAt"`
}

type saveWorkspaceRequest struct {
	ID      string                 `json:"id,omitempty"`
	Name    string                 `json:"name,omitempty"`
	Color   string                 `json:"color,omitempty"`
	Folders []workspaceFolderInput `json:"folders"`
}

type workspaceStoreFile struct {
	Workspaces []workspaceProfile `json:"workspaces"`
	Dismissed  []string           `json:"dismissed,omitempty"`
}

func userHoxiWorkspacesPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".hoxi", "workspaces.json")
}

func normalizeWorkspaceIdentityColor(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if m := shortHexColorRE.FindStringSubmatch(trimmed); len(m) == 2 {
		d := strings.ToUpper(m[1])
		return fmt.Sprintf("#%c%c%c%c%c%c", d[0], d[0], d[1], d[1], d[2], d[2])
	}
	if m := longHexColorRE.FindStringSubmatch(trimmed); len(m) == 2 {
		return "#" + strings.ToUpper(m[1])
	}
	return ""
}

func comparableFolderPath(p string) string {
	cleaned := filepath.Clean(strings.TrimSpace(p))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

func canonicalHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != "" {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(home)
}

func generateWorkspaceUUID() string {
	if hexStr, err := generateRandomHex(16); err == nil && len(hexStr) == 32 {
		return fmt.Sprintf("%s-%s-%s-%s-%s",
			hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
	}
	return fmt.Sprintf("ws_%d", time.Now().UnixNano())
}
