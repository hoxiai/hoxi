//go:build windows

package app

import "golang.org/x/sys/windows"

func platformDownloadDirectory() string {
	dir, _ := windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DONT_VERIFY)
	return dir
}
