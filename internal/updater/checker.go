package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Checker struct {
	mu             sync.RWMutex
	logger         *slog.Logger
	serverBaseURL  string
	currentVersion string
	httpClient     *http.Client

	lastResult     *UpdateCheckResult
}

func NewChecker(currentVersion string, logger *slog.Logger) *Checker {
	serverURL := strings.TrimRight(os.Getenv("HOXI_SERVER_URL"), "/")
	if serverURL == "" {
		serverURL = "https://hoxi.ai"
	}

	if currentVersion == "" {
		currentVersion = "0.1.0"
	}

	return &Checker{
		logger:         logger,
		serverBaseURL:  serverURL,
		currentVersion: currentVersion,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Checker) Check(ctx context.Context) (*UpdateCheckResult, error) {
	c.mu.RLock()
	serverURL := c.serverBaseURL
	currVer := c.currentVersion
	c.mu.RUnlock()

	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "mac"
	}

	archName := runtime.GOARCH
	if archName == "amd64" {
		archName = "x64"
	}

	apiURL := fmt.Sprintf("%s/api/hoxi/upgrade?package=desktop&os=%s&arch=%s&version=%s",
		serverURL,
		url.QueryEscape(osName),
		url.QueryEscape(archName),
		url.QueryEscape(currVer),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update server returned HTTP %d", resp.StatusCode)
	}

	var raw struct {
		Code         int           `json:"code"`
		Version      string        `json:"version"`
		VersionCode  int           `json:"versionCode"`
		NeedUpdate   bool          `json:"needUpdate"`
		Title        string        `json:"title"`
		ReleaseNotes string        `json:"releaseNotes"`
		DownloadURL  string        `json:"downloadUrl"`
		Mirrors      UpdateMirrors `json:"mirrors"`
		Mandatory    bool          `json:"mandatory"`
		SHA256       string        `json:"sha256"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse update response: %w", err)
	}

	res := &UpdateCheckResult{
		CurrentVersion: currVer,
		LatestVersion:  raw.Version,
		VersionCode:    raw.VersionCode,
		NeedUpdate:     raw.NeedUpdate,
		Title:          raw.Title,
		ReleaseNotes:   raw.ReleaseNotes,
		DownloadURL:    raw.DownloadURL,
		Mirrors:        raw.Mirrors,
		Mandatory:      raw.Mandatory,
		SHA256:         raw.SHA256,
		CheckedAt:      time.Now().Format(time.RFC3339),
	}

	c.mu.Lock()
	c.lastResult = res
	c.mu.Unlock()

	return res, nil
}

func (c *Checker) LastResult() *UpdateCheckResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastResult
}
