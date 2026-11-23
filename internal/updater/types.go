package updater

import "strings"

// DefaultCDNBaseURL is the official transitional CDN prefix mirroring Hoxi
// Desktop release assets alongside GitHub Releases for faster, more reliable
// downloads without requiring a proxy.
const DefaultCDNBaseURL = "https://cdn.hoxi.dev/hoxi-desktop"

// DownloadSource selects which release mirror family the updater tries first.
type DownloadSource string

const (
	DownloadSourceAuto   DownloadSource = "auto"
	DownloadSourceCDN    DownloadSource = "cdn"
	DownloadSourceGlobal DownloadSource = "global"
)

// NormalizeDownloadSource sanitizes user-provided mirror preferences.
func NormalizeDownloadSource(raw string) DownloadSource {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(DownloadSourceCDN), "cn", "mirror":
		return DownloadSourceCDN
	case string(DownloadSourceGlobal), "github":
		return DownloadSourceGlobal
	default:
		return DownloadSourceAuto
	}
}

// InstallOptions controls optional source routing when initiating an in-app update.
type InstallOptions struct {
	Source         DownloadSource `json:"source,omitempty"`
	UseSelfUpdater bool           `json:"useSelfUpdater,omitempty"`
}

// UpdateMirrors 多平台下载镜像（优先级：CN > CDN > Global）
type UpdateMirrors struct {
	CN     string `json:"cn,omitempty"`
	CDN    string `json:"cdn,omitempty"`
	Global string `json:"global,omitempty"`
}

// UpdateCheckResult 检查更新响应结构
type UpdateCheckResult struct {
	CurrentVersion string        `json:"currentVersion"`
	LatestVersion  string        `json:"latestVersion"`
	VersionCode    int           `json:"versionCode"`
	NeedUpdate     bool          `json:"needUpdate"`
	Title          string        `json:"title"`
	ReleaseNotes   string        `json:"releaseNotes"`
	DownloadURL    string        `json:"downloadUrl"`
	Mirrors        UpdateMirrors `json:"mirrors"`
	Mandatory      bool          `json:"mandatory"`
	SHA256         string        `json:"sha256,omitempty"`
	CheckedAt      string        `json:"checkedAt"`
}
