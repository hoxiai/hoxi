//go:build dev

package main

import (
	"io/fs"
	"os"
	"path/filepath"
)

// loadFrontendAssets skips embedding web/dist on every `wails dev` recompile:
// Vite serves live assets in dev mode, while any existing on-disk web/dist
// remains available as a local fallback if Vite exits.
func loadFrontendAssets() (fs.FS, error) {
	distDir := filepath.Join("web", "dist")
	if info, err := os.Stat(filepath.Join(distDir, "index.html")); err == nil && !info.IsDir() {
		return os.DirFS(distDir), nil
	}
	return nil, nil
}
