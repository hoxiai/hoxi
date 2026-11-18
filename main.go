package main

import (
	_ "embed"
	"log/slog"
	"os"

	"github.com/hoxiai/hoxi/internal/app"
)

//go:embed build/appicon.png
var appIcon []byte

func main() {
	app.SetAppIcon(appIcon)
	distFS, err := loadFrontendAssets()
	if err != nil {
		slog.Error("failed to mount frontend assets", "error", err)
		os.Exit(1)
	}
	app.Run(distFS)
}
