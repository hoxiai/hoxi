package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hoxiai/hoxi/internal/gateway"
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
		if runtime.GOOS == "darwin" {
			args := []string{path}
			if reveal {
				args = []string{"-R", path}
			}
			return exec.CommandContext(ctx, "open", args...).Run()
		}
		if reveal {
			path = filepath.Dir(path)
		}
		return exec.CommandContext(ctx, "xdg-open", path).Run()
	})
}
