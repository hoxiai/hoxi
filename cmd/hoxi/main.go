package main

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hoxiai/hoxi/internal/app"
)

func main() {
	var distFS fs.FS
	if info, err := os.Stat(filepath.Join("web", "dist")); err == nil && info.IsDir() {
		distFS = os.DirFS(filepath.Join("web", "dist"))
	}
	app.Run(distFS)
}
