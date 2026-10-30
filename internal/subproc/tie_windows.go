//go:build windows

package subproc

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

	tieJobOnce sync.Once
	tieJob     windows.Handle
	tieJobErr  error
)

// tiedJob returns the job that tied children join. Hoxi creates it once and
// never closes it: the handle is not inheritable, so it closes only when Hoxi
// exits, however it exits, and KILL_ON_JOB_CLOSE then ends every process left
// in the job. BREAKAWAY_OK lets a child that asks for CREATE_BREAKAWAY_FROM_JOB
// leave instead of failing to start.
func tiedJob() (windows.Handle, error) {
	tieJobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			tieJobErr = fmt.Errorf("create job object: %w", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK,
			},
		}
		if _, err := windows.SetInformationJobObject(
			job,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(job)
			tieJobErr = fmt.Errorf("set job object limits: %w", err)
			return
		}
		tieJob = job
	})
	return tieJob, tieJobErr
}

// startTied starts the child suspended so it cannot start processes of its
// own before it is in the job; those would not be tied.
func startTied(cmd *exec.Cmd, onUntied func(error)) error {
	job, err := tiedJob()
	if err != nil {
		reportUntied(onUntied, err)
		return cmd.Start()
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := tieAndResume(job, uint32(cmd.Process.Pid), onUntied); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("resume %s: %w", cmd.Path, err)
	}
	return nil
}

// tieAndResume puts the suspended process in the job and resumes it. Failing
// to join the job is reported and the child runs untied; failing to resume
// is returned, since the child could never run. cmd.Process holds a handle to
// the child, so its PID cannot be reused while this runs.
func tieAndResume(job windows.Handle, pid uint32, onUntied func(error)) error {
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		pid,
	)
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		reportUntied(onUntied, fmt.Errorf("assign process %d to job: %w", pid, err))
	}
	if err := procNtResumeProcess.Find(); err != nil {
		return err
	}
	if status, _, _ := procNtResumeProcess.Call(uintptr(proc)); status != 0 {
		return fmt.Errorf("NtResumeProcess: NTSTATUS 0x%08x", uint32(status))
	}
	return nil
}
