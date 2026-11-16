package cloudflared

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// ResolveBinary finds the cloudflared executable. Priority: the HOXI_CLOUDFLARED
// override, PATH, then ~/.hoxi/bin. It returns "" when none is found; Hoxi never
// downloads or executes a binary the user did not provide.
func ResolveBinary() string {
	if override := strings.TrimSpace(os.Getenv("HOXI_CLOUDFLARED")); override != "" {
		return override
	}
	if path, err := exec.LookPath("cloudflared"); err == nil {
		return path
	}
	if dir := hoxipaths.BinDir(); dir != "" {
		names := []string{"cloudflared"}
		if runtime.GOOS == "windows" {
			names = []string{"cloudflared.exe", "cloudflared"}
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}
