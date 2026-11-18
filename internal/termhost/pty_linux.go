//go:build linux

package termhost

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func startPlatformPTY(cmd *exec.Cmd, cols, rows uint16) (io.ReadWriteCloser, func(uint16, uint16) error, func() error, func() error, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	var ptn uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptn))); errno != 0 {
		_ = master.Close()
		return nil, nil, nil, nil, fmt.Errorf("TIOCGPTN: %w", errno)
	}
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = master.Close()
		return nil, nil, nil, nil, fmt.Errorf("TIOCSPTLCK: %w", errno)
	}
	slaveName := "/dev/pts/" + strconv.Itoa(int(ptn))
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, nil, nil, fmt.Errorf("open %s: %w", slaveName, err)
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
		return nil, nil, nil, nil, err
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
	return master, resizeFn, killFn, cmd.Wait, nil
}
