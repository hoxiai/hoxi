//go:build !dev

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var embeddedAssets embed.FS

func loadFrontendAssets() (fs.FS, error) {
	return fs.Sub(embeddedAssets, "web/dist")
}
