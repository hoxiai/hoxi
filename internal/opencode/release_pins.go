package opencode

// pinnedReleaseVersion is the official OpenCode release that Hoxi installs when
// no local binary is available. Auto-install must be reproducible and
// verifiable, so the version and every shipped archive's SHA-256 are pinned
// here instead of following GitHub's mutable "latest" tag.
//
// Maintainers bump this pair together with pinnedReleaseSHA256. Builds that
// bundle a newer sidecar through HOXI_OPENCODE are unaffected; this pin only
// covers the fallback download into ~/.hoxi/bin.
const pinnedReleaseVersion = "1.19.3"

// pinnedReleaseSHA256 holds the official archive checksum per GOOS/GOARCH.
var pinnedReleaseSHA256 = map[string]string{
	"darwin/arm64":  "c2d0fecd27acf6e08be02c1f5d384614d07495587766bcc4a6bff3971749bb64",
	"darwin/amd64":  "4e29dbe2f61ace728370523c88027d0742b3a79edcdd2c70e404f616d4ef8f36",
	"linux/arm64":   "7900818a6a98881edbbadab4b7525940eff3b9750040c200481f9f56cacee405",
	"linux/amd64":   "13a8fb2978302071b86f9d7e00da9c8895f9cd846978379c3bdc6a5333536c9b",
	"windows/arm64": "c1b9daf18c97564b2d171703279621f7edc3613e70750d796ef78db87257ad89",
	"windows/amd64": "e4ce9117f55fc068414513789f8c7d1049f855d7518bb4e350b9170a3a20d8c3",
}
