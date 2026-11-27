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
		return wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
			Title: title, DefaultFilename: name, DefaultDirectory: defaultDownloadDirectory(),
		})
	}, func(path string, reveal bool) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return osopen.Open(ctx, path, reveal)
	})

	gw.SetSaveFileDialog(func(opts gateway.SaveFileOptions) (string, error) {
		ctx := a.desktopContext()
		if ctx == nil {
			return "", errors.New("desktop window context not ready")
		}
		defaultDir := opts.DefaultDirectory
		if defaultDir == "" {
			defaultDir = defaultDownloadDirectory()
		}
		saveOpts := wailsruntime.SaveDialogOptions{
			Title:                opts.Title,
			DefaultFilename:      opts.DefaultFilename,
			DefaultDirectory:     defaultDir,
			CanCreateDirectories: true,
		}
		if opts.FilterPattern != "" {
			displayName := opts.FilterName
			if displayName == "" {
				displayName = opts.FilterPattern
			}
			saveOpts.Filters = []wailsruntime.FileFilter{{
				DisplayName: displayName,
				Pattern:     opts.FilterPattern,
			}}
		}
		return wailsruntime.SaveFileDialog(ctx, saveOpts)
	})
}

func defaultDownloadDirectory() string {
	if dir := platformDownloadDirectory(); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Downloads")
	}
	return ""
}
