//go:build !darwin

package browser

func quarantineDownload(string) error { return nil }
