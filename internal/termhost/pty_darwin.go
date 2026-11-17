//go:build darwin

package termhost

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinTIOCPTYGRANT = 0x20007454
	darwinTIOCPTYUNLK  = 0x20007452
	darwinTIOCPTYGNAME = 0x40807453
)

func startPlatformPTY(cmd *exec.Cmd, cols, rows uint16) (io.ReadWriteCloser, func(uint16, uint16) error, func() error, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), darwinTIOCPTYGRANT, 0); errno != 0 {
		_ = master.Close()
		return nil, nil, nil, fmt.Errorf("TIOCPTYGRANT: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), darwinTIOCPTYUNLK, 0); errno != 0 {
		_ = master.Close()
		return nil, nil, nil, fmt.Errorf("TIOCPTYUNLK: %w", errno)
	}
	var nameBuf [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), darwinTIOCPTYGNAME, uintptr(unsafe.Pointer(&nameBuf[0]))); errno != 0 {
		_ = master.Close()
		return nil, nil, nil, fmt.Errorf("TIOCPTYGNAME: %w", errno)
	}
	n := bytes.IndexByte(nameBuf[:], 0)
	if n <= 0 {
		_ = master.Close()
		return nil, nil, nil, fmt.Errorf("empty slave pty name")
	}
	slaveName := string(nameBuf[:n])
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, nil, fmt.Errorf("open slave pty %s: %w", slaveName, err)
	}

	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	_ = unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})

	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		return nil, nil, nil, err
	}
	_ = slave.Close()

	resizeFn := func(c, r uint16) error {
		if c == 0 || r == 0 {
			return nil
		}
		return unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: r, Col: c})
	}
	killFn := func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
			_ = cmd.Process.Kill()
		}
		return master.Close()
	}
	return master, resizeFn, killFn, nil
}
