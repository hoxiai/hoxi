//go:build darwin

package browser

import (
	"fmt"
	"golang.org/x/sys/unix"
	"time"
)

func quarantineDownload(path string) error {
	return unix.Setxattr(path, "com.apple.quarantine", []byte(fmt.Sprintf("0083;%x;Hoxi;", time.Now().Unix())), 0)
}
