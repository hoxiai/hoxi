//go:build windows

package browser

import "os"

func quarantineDownload(path string) error {
	// Write the Windows Mark-of-the-Web (ZoneId=3: Internet) NTFS alternate data stream
	// so SmartScreen and Explorer treat agent-downloaded files as downloaded from the Internet.
	_ = os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o600)
	return nil
}
