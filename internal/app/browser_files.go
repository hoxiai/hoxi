package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/hoxiai/hoxi/internal/osopen"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *DesktopApp) configureBrowserFiles(gw *gateway.Gateway) {
	gw.SetBrowserFileUI(func(name, title string) (string, error) {
		ctx := a.desktopContext()
		if ctx == nil {
			return "", errors.New("desktop window context not ready")
		}
		home, _ := os.UserHomeDir()
		return wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
			Title: title, DefaultFilename: name, DefaultDirectory: filepath.Join(home, "Downloads"),
		})
	}, func(path string, reveal bool) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return osopen.Open(ctx, path, reveal)
	})
}
