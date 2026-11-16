// Package sqlitecli resolves the SQLite command-line tool used for local databases.
package sqlitecli

import (
	"os"
	"path/filepath"
	"runtime"
)

// Executable prefers the Windows release's bundled sidecar, falling back to
// PATH for development builds and platforms that do not bundle SQLite.
func Executable() string {
	exe, _ := os.Executable()
	return resolve(runtime.GOOS, exe)
}

func resolve(goos, exe string) string {
	if goos == "windows" && exe != "" {
		path := filepath.Join(filepath.Dir(exe), "sqlite3.exe")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return "sqlite3"
}
