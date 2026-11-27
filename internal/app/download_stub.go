//go:build !darwin || !cgo

package app

// Windows uses the WebView2 download UI. Wails v2 exposes no public
// DownloadStarting hook; do not attach one to the private frontend object.
func installNativeWebViewDownloads() {}
