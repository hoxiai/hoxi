package reclaim

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

const darwinZombie = 5 // SZOMB in <sys/proc.h>

// inspectProcess reads the start time and argv of pid from the kernel. The
// start time has microsecond resolution, so a recycled PID never matches.
func inspectProcess(pid int) (procInfo, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return procInfo{}, err
	}
	if len(procs) == 0 {
		return procInfo{}, errNoProcess
	}
	p := procs[0].Proc
	if int(p.P_pid) != pid {
		return procInfo{}, fmt.Errorf("sysctl returned pid %d for %d", p.P_pid, pid)
	}
	info := procInfo{
		start:  fmt.Sprintf("%d.%06d", p.P_starttime.Sec, p.P_starttime.Usec),
		zombie: p.P_stat == darwinZombie,
	}
	if !info.zombie {
		if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
			info.args = parseProcArgs(raw)
		}
	}
	return info, nil
}

// parseProcArgs decodes kern.procargs2: a native-endian int32 argc, the
// executable path, NUL padding, then argc NUL-terminated arguments.
func parseProcArgs(raw []byte) []string {
	if len(raw) < 4 {
		return nil
	}
	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	args := make([]string, 0, argc)
	for len(args) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return args
}
