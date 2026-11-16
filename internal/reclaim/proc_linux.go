package reclaim

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// inspectProcess reads /proc. The start token combines the boot ID with the
// start time in clock ticks since boot, so it stays unique across reboots.
func inspectProcess(pid int) (procInfo, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return procInfo{}, errNoProcess
	}
	if err != nil {
		return procInfo{}, err
	}
	// The command name is parenthesised and may contain spaces or ')'.
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return procInfo{}, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(stat[end+1:]))
	// fields[0] is the state (field 3); starttime is field 22.
	if len(fields) < 20 {
		return procInfo{}, fmt.Errorf("short /proc/%d/stat", pid)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return procInfo{}, err
	}
	info := procInfo{
		start:  strings.TrimSpace(string(boot)) + ":" + fields[19],
		zombie: fields[0] == "Z" || fields[0] == "X",
	}
	if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil && len(raw) > 0 {
		info.args = strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	}
	return info, nil
}
