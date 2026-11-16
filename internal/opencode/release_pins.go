package opencode

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// opencode.lock is the single source of truth for the OpenCode release that
// Hoxi downloads, bundles and verifies. The Go resolver, release workflows and
// scripts/opencode-lock.mjs all read this file, so a version bump cannot drift
// between the runtime fallback and the sidecar bundled into desktop builds.
//
//go:embed opencode.lock
var releaseLockJSON []byte

type releaseAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type releaseLock struct {
	Version string                  `json:"version"`
	Assets  map[string]releaseAsset `json:"assets"`
}

var pinnedRelease = mustLoadReleaseLock()

// releasePlatforms is the build matrix Hoxi ships sidecars for. The lock must
// cover exactly these platforms so no build silently falls back to an
// unverified download.
var releasePlatforms = []string{
	"darwin/arm64",
	"darwin/amd64",
	"linux/arm64",
	"linux/amd64",
	"windows/arm64",
	"windows/amd64",
}

var pinnedReleaseVersion = pinnedRelease.Version

var pinnedReleaseSHA256 = func() map[string]string {
	digests := make(map[string]string, len(pinnedRelease.Assets))
	for platform, asset := range pinnedRelease.Assets {
		digests[platform] = asset.SHA256
	}
	return digests
}()

func mustLoadReleaseLock() *releaseLock {
	lock, err := parseReleaseLock(releaseLockJSON)
	if err != nil {
		panic(fmt.Sprintf("embedded opencode.lock is invalid: %v", err))
	}
	return lock
}

func parseReleaseLock(raw []byte) (*releaseLock, error) {
	var lock releaseLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil, err
	}
	if err := validateReleaseLock(&lock); err != nil {
		return nil, err
	}
	return &lock, nil
}

func validateReleaseLock(lock *releaseLock) error {
	if lock.Version == "" || strings.TrimPrefix(lock.Version, "v") != lock.Version {
		return fmt.Errorf("version %q must be a release version without a leading v", lock.Version)
	}
	known := make(map[string]bool, len(releasePlatforms))
	for _, platform := range releasePlatforms {
		known[platform] = true
		if _, ok := lock.Assets[platform]; !ok {
			return fmt.Errorf("missing pinned asset for %s", platform)
		}
	}
	for platform := range lock.Assets {
		if !known[platform] {
			return fmt.Errorf("unknown pinned platform %q", platform)
		}
	}
	platforms := make([]string, 0, len(lock.Assets))
	for platform := range lock.Assets {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		asset := lock.Assets[platform]
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok {
			return fmt.Errorf("asset key %q must be <goos>/<goarch>", platform)
		}
		name, _, err := releaseAssetFile(goos, goarch)
		if err != nil {
			return fmt.Errorf("asset %q: %w", platform, err)
		}
		if !isSHA256Hex(asset.SHA256) {
			return fmt.Errorf("asset %q has an invalid SHA-256 digest %q", platform, asset.SHA256)
		}
		parsed, err := url.Parse(asset.URL)
		if err != nil {
			return fmt.Errorf("asset %q URL: %w", platform, err)
		}
		if parsed.Scheme != "https" || parsed.Host != "github.com" {
			return fmt.Errorf("asset %q URL %q must be an HTTPS github.com release URL", platform, asset.URL)
		}
		suffix := "/download/v" + lock.Version + "/" + name
		if !strings.HasSuffix(parsed.Path, suffix) {
			return fmt.Errorf("asset %q URL %q must end with %q", platform, asset.URL, suffix)
		}
	}
	return nil
}

// releaseAssetFile maps a Go platform to the official archive name. The name
// layout is fixed by the upstream release workflow.
func releaseAssetFile(goos, goarch string) (name string, isZip bool, err error) {
	var osName string
	switch goos {
	case "darwin":
		osName, isZip = "darwin", true
	case "linux":
		osName, isZip = "linux", false
	case "windows":
		osName, isZip = "windows", true
	default:
		return "", false, fmt.Errorf("unsupported OS: %s", goos)
	}
	var archName string
	switch goarch {
	case "arm64":
		archName = "arm64"
	case "amd64":
		archName = "x64"
	default:
		return "", false, fmt.Errorf("unsupported architecture: %s", goarch)
	}
	ext := ".tar.gz"
	if isZip {
		ext = ".zip"
	}
	return fmt.Sprintf("opencode-%s-%s%s", osName, archName, ext), isZip, nil
}

func lockedReleaseAsset(goos, goarch string) (releaseAsset, error) {
	asset, ok := pinnedRelease.Assets[goos+"/"+goarch]
	if !ok {
		return releaseAsset{}, fmt.Errorf("no pinned OpenCode release for %s/%s", goos, goarch)
	}
	return asset, nil
}

// releaseURLForVersion points a pinned official URL at another version while
// keeping its repository and asset name. Custom versions still require an
// explicit HOXI_OPENCODE_SHA256 because the lock only vouches for its own tag.
func releaseURLForVersion(assetURL, version string) (string, error) {
	marker := "/download/v" + pinnedRelease.Version + "/"
	idx := strings.Index(assetURL, marker)
	if idx < 0 {
		return "", fmt.Errorf("official OpenCode URL %q does not contain %q", assetURL, marker)
	}
	return assetURL[:idx] + "/download/v" + version + "/" + assetURL[idx+len(marker):], nil
}
