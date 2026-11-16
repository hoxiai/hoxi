//go:build !darwin && !windows

package browser

func quarantineDownload(string) error { return nil }
