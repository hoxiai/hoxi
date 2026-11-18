//go:build windows

package procgroup

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"github.com/hoxiai/hoxi/internal/subproc"
	"golang.org/x/sys/windows"
)

const (
	winStillActive                   = 259
	winProcessCommandLineInformation = 60
	winMaxCommandLineBytes           = 1 << 20
)

func SetProcessGroup(cmd *exec.Cmd) {
	subproc.HideWindow(cmd)
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func SelfProcessGroupID() int { return 0 }

// SignalProcessGroup terminates p and any live descendants in its process tree.
func SignalProcessGroup(p *os.Process, sig os.Signal) error {
	if p == nil {
		return nil
	}
	desc := DescendantPIDs(p.Pid)
	var err error
	if sig == os.Kill {
		err = p.Kill()
	} else {
		err = p.Signal(sig)
		if err != nil {
			err = p.Kill()
		}
	}
	if len(desc) > 0 {
		KillPIDs(desc)
	}
	return err
}

func ProcessGroupAlive(p *os.Process) bool {
	if p == nil || p.Pid <= 4 {
		return false
	}
	return ProcessAlive(p.Pid) || len(DescendantPIDs(p.Pid)) > 0
}

func DescendantPIDs(pid int) []int {
	if pid <= 4 {
		return nil
	}
	children := readProcessChildrenMap()
	if len(children) == 0 {
		return nil
	}
	var out []int
	seen := map[int]bool{pid: true}
	queue := []int{pid}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			if c > 4 && !seen[c] {
				seen[c] = true
				out = append(out, c)
				queue = append(queue, c)
			}
		}
	}
	return out
}

func KillPIDs(pids []int) {
	selfPID := os.Getpid()
	for _, pid := range pids {
		if pid <= 4 || pid == selfPID {
			continue
		}
		terminateWindowsPID(pid)
	}
}

func terminateWindowsPID(pid int) bool {
	if pid <= 4 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err == nil && exitCode != winStillActive {
		return false
	}
	return windows.TerminateProcess(h, 1) == nil
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == winStillActive
}

func readProcessChildrenMap() map[int][]int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil
	}
	children := make(map[int][]int)
	for {
		pid := int(pe.ProcessID)
		ppid := int(pe.ParentProcessID)
		if pid > 0 && pid != ppid {
			children[ppid] = append(children[ppid], pid)
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return children
}

func ReadProcessSnapshots() (map[int]Snapshot, map[int][]int) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, nil
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, nil
	}
	byPID := make(map[int]Snapshot)
	children := make(map[int][]int)
	for {
		pid := int(pe.ProcessID)
		ppid := int(pe.ParentProcessID)
		if pid > 0 {
			args, createTime := readWindowsProcessDetails(pid)
			if args == "" {
				args = windows.UTF16ToString(pe.ExeFile[:])
			}
			byPID[pid] = Snapshot{PID: pid, PPID: ppid, PGID: 0, CreateTime: createTime, Args: args}
			if pid != ppid {
				children[ppid] = append(children[ppid], pid)
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return byPID, children
}

func readWindowsProcessDetails(pid int) (string, uint64) {
	if pid <= 4 {
		return "", 0
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", 0
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	var createTime uint64
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err == nil {
		createTime = uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	}

	var retLen uint32
	_ = windows.NtQueryInformationProcess(h, winProcessCommandLineInformation, nil, 0, &retLen)
	if retLen == 0 || retLen > winMaxCommandLineBytes {
		return "", createTime
	}
	buf := make([]byte, retLen)
	if err := windows.NtQueryInformationProcess(h, winProcessCommandLineInformation, unsafe.Pointer(&buf[0]), retLen, &retLen); err != nil {
		return "", createTime
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	return strings.TrimSpace(us.String()), createTime
}

func stopOpenCodeToolShell(shell int, byPID map[int]Snapshot, children map[int][]int, protected map[int]bool) int {
	if shell <= 4 || protected[shell] {
		return 0
	}
	return killSubtrees([]int{shell}, byPID, children, protected)
}

func killSubtrees(roots []int, _ map[int]Snapshot, children map[int][]int, protected map[int]bool) int {
	targetSet := make(map[int]bool)
	var targetPIDs []int
	for _, root := range roots {
		for _, pid := range CollectSubtreePIDs(root, children) {
			if pid <= 4 || protected[pid] || targetSet[pid] {
				continue
			}
			targetSet[pid] = true
			targetPIDs = append(targetPIDs, pid)
		}
	}
	if len(targetPIDs) == 0 {
		return 0
	}
	KillPIDs(targetPIDs)
	return len(targetPIDs)
}
