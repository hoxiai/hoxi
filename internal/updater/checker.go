package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/buildinfo"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

type Checker struct {
	mu             sync.RWMutex
	logger         *slog.Logger
	serverBaseURL  string
	currentVersion string
	httpClient     *http.Client

	lastResult *UpdateCheckResult
}

func NewChecker(currentVersion string, logger *slog.Logger) *Checker {
	if currentVersion == "" {
		currentVersion = buildinfo.Version
	}

	return &Checker{
		logger:         logger,
		serverBaseURL:  hoxipaths.ServerBaseURL(),
		currentVersion: currentVersion,
		httpClient: &http.Client{
			Timeout:   15 * time.Second,
			Transport: newUpdateTransport(),
		},
	}
}

func (c *Checker) Check(ctx context.Context) (*UpdateCheckResult, error) {
	c.mu.RLock()
	serverURL := c.serverBaseURL
	currVer := c.currentVersion
	c.mu.RUnlock()

	osName := hoxipaths.NormalizedPlatform(runtime.GOOS)
	archName := hoxipaths.NormalizedArch(runtime.GOARCH)

	if hoxipaths.AuxiliaryNetworkDisabled() {
		res := &UpdateCheckResult{
			CurrentVersion: currVer,
			LatestVersion:  currVer,
			NeedUpdate:     false,
			CheckedAt:      time.Now().Format(time.RFC3339),
		}
		c.mu.Lock()
		c.lastResult = res
		c.mu.Unlock()
		return res, nil
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

	mirrors := normalizeUpdateMirrors(raw.Version, raw.DownloadURL, raw.Mirrors)
	res := &UpdateCheckResult{
		CurrentVersion: currVer,
		LatestVersion:  raw.Version,
		VersionCode:    raw.VersionCode,
		NeedUpdate:     raw.NeedUpdate,
		Title:          raw.Title,
		ReleaseNotes:   raw.ReleaseNotes,
		DownloadURL:    raw.DownloadURL,
		Mirrors:        mirrors,
		Mandatory:      raw.Mandatory,
		SHA256:         raw.SHA256,
		CheckedAt:      time.Now().Format(time.RFC3339),
	}

	c.mu.Lock()
	c.lastResult = res
	c.mu.Unlock()

	return res, nil
}

// normalizeUpdateMirrors ensures CDN and GitHub download links are cleanly
// separated even when an older release API puts a CDN URL inside mirrors.global
// or omits the complementary GitHub fallback link.
func normalizeUpdateMirrors(version, downloadURL string, mirrors UpdateMirrors) UpdateMirrors {
	dl := strings.TrimSpace(downloadURL)
	mirrors.CN = strings.TrimSpace(mirrors.CN)
	mirrors.CDN = strings.TrimSpace(mirrors.CDN)
	mirrors.Global = strings.TrimSpace(mirrors.Global)

	if mirrors.CDN == "" && classifyUpdateSource(dl) == string(DownloadSourceCDN) {
		mirrors.CDN = dl
	}
	if classifyUpdateSource(mirrors.Global) == string(DownloadSourceCDN) {
		if mirrors.CDN == "" {
			mirrors.CDN = mirrors.Global
		}
		mirrors.Global = ""
	}
	if mirrors.Global == "" && classifyUpdateSource(dl) == string(DownloadSourceGlobal) {
		mirrors.Global = dl
	}
	if mirrors.Global == "" && mirrors.CDN != "" && isOfficialHoxiReleaseOrigin(mirrors.CDN) {
		if u, err := url.Parse(mirrors.CDN); err == nil {
			asset := path.Base(u.Path)
			ver := strings.TrimPrefix(strings.TrimSpace(version), "v")
			if ver != "" && strings.HasPrefix(asset, "Hoxi-") {
				mirrors.Global = fmt.Sprintf("%s/v%s/%s", githubReleaseDownloadBase, ver, asset)
			}
		}
	}
	return mirrors
}

func (c *Checker) LastResult() *UpdateCheckResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastResult
}
