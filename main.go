package main

import (
	"embed"
	"io/fs"
	"log/slog"
	"os"

	"github.com/hoxiai/hoxi/internal/app"
)

//go:embed all:web/dist
var embeddedAssets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	app.SetAppIcon(appIcon)
	distFS, err := fs.Sub(embeddedAssets, "web/dist")
	if err != nil {
		slog.Error("failed to mount embedded web/dist", "error", err)
		os.Exit(1)
	}
	app.Run(distFS)
}
