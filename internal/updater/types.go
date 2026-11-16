package updater

// UpdateMirrors 多平台下载镜像
type UpdateMirrors struct {
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
