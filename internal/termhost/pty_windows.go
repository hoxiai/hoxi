//go:build windows

package termhost

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/hoxiai/hoxi/internal/subproc"
	"golang.org/x/sys/windows"
)

var (
	kernel32DLL                   = windows.NewLazySystemDLL("kernel32.dll")
	procUpdateProcThreadAttribute = kernel32DLL.NewProc("UpdateProcThreadAttribute")

	ptyJobOnce sync.Once
	ptyJob     windows.Handle
)

func termJobObject() windows.Handle {
	ptyJobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
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
			return
		}
		ptyJob = job
	})
	return ptyJob
}

func clampConPTYDim(v, fallback uint16) int16 {
	if v == 0 {
		v = fallback
	}
	if v > 32767 {
		return 32767
	}
	return int16(v)
}

func startPlatformPTY(cmd *exec.Cmd, cols, rows uint16) (io.ReadWriteCloser, func(uint16, uint16) error, func() error, func() error, error) {
	if rw, resizeFn, killFn, waitFn, err := startConPTY(cmd, cols, rows); err == nil {
		return rw, resizeFn, killFn, waitFn, nil
	}
	return startPipeFallback(cmd)
}

func startConPTY(cmd *exec.Cmd, cols, rows uint16) (io.ReadWriteCloser, func(uint16, uint16) error, func() error, func() error, error) {
	if err := procUpdateProcThreadAttribute.Find(); err != nil {
		return nil, nil, nil, nil, err
	}

	ptyInR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	outR, ptyOutW, err := os.Pipe()
	if err != nil {
		_ = ptyInR.Close()
		_ = inW.Close()
		return nil, nil, nil, nil, err
	}

	size := windows.Coord{
		X: clampConPTYDim(cols, 120),
		Y: clampConPTYDim(rows, 32),
	}
	var hpc windows.Handle
	err = windows.CreatePseudoConsole(size, windows.Handle(ptyInR.Fd()), windows.Handle(ptyOutW.Fd()), 0, &hpc)
	runtime.KeepAlive(ptyInR)
	runtime.KeepAlive(ptyOutW)
	_ = ptyInR.Close()
	_ = ptyOutW.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, nil, nil, nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}

	cleanupFail := func() {
		windows.ClosePseudoConsole(hpc)
		_ = inW.Close()
		_ = outR.Close()
	}

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		cleanupFail()
		return nil, nil, nil, nil, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	defer attrList.Delete()

	// Win32 PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE uniquely expects the HPCON value
	// itself (not &hpc) in lpValue; pass uintptr(hpc) directly to the syscall so
	// Go never stores a non-pointer HANDLE inside an unsafe.Pointer slot.
	r1, _, callErr := procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(attrList.List())),
		0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(hpc),
		unsafe.Sizeof(hpc),
		0,
		0,
	)
	if r1 == 0 {
		cleanupFail()
		return nil, nil, nil, nil, fmt.Errorf("UpdateProcThreadAttribute(PSEUDOCONSOLE): %w", callErr)
	}

	var appPtr *uint16
	if strings.TrimSpace(cmd.Path) != "" {
		appPtr, err = windows.UTF16PtrFromString(cmd.Path)
		if err != nil {
			cleanupFail()
			return nil, nil, nil, nil, err
		}
	}
	args := cmd.Args
	if len(args) == 0 && strings.TrimSpace(cmd.Path) != "" {
		args = []string{cmd.Path}
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		cleanupFail()
		return nil, nil, nil, nil, err
	}
	var dirPtr *uint16
	if strings.TrimSpace(cmd.Dir) != "" {
		dirPtr, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			cleanupFail()
			return nil, nil, nil, nil, err
		}
	}
	envList := cmd.Env
	if len(envList) == 0 {
		envList = os.Environ()
	}
	envBlock := buildUTF16EnvBlock(subproc.WithUTF8Env(envList))

	var siEx windows.StartupInfoEx
	siEx.StartupInfo.Cb = uint32(unsafe.Sizeof(siEx))
	siEx.ProcThreadAttributeList = attrList.List()

	// Do NOT combine CREATE_NO_WINDOW with PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE:
	// ConPTY is already headless, and CREATE_NO_WINDOW detaches the child from HPCON.
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP)
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(
		appPtr,
		cmdLinePtr,
		nil,
		nil,
		false,
		flags,
		&envBlock[0],
		dirPtr,
		&siEx.StartupInfo,
		&pi,
	); err != nil {
		cleanupFail()
		return nil, nil, nil, nil, fmt.Errorf("CreateProcess(ConPTY): %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)

	if job := termJobObject(); job != 0 {
		_ = windows.AssignProcessToJobObject(job, pi.Process)
	}

	var waitHandle windows.Handle
	curProc := windows.CurrentProcess()
	if dupErr := windows.DuplicateHandle(curProc, pi.Process, curProc, &waitHandle, 0, false, windows.DUPLICATE_SAME_ACCESS); dupErr != nil {
		waitHandle = 0
	}

	stream := &conPTYStream{
		r:    outR,
		w:    inW,
		hpc:  hpc,
		proc: pi.Process,
	}
	waitFn := func() error {
		if waitHandle == 0 {
			return nil
		}
		defer windows.CloseHandle(waitHandle)
		_, wErr := windows.WaitForSingleObject(waitHandle, windows.INFINITE)
		return wErr
	}
	return stream, stream.Resize, stream.Kill, waitFn, nil
}

type conPTYStream struct {
	r    *os.File
	w    *os.File
	hpc  windows.Handle
	proc windows.Handle

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func (s *conPTYStream) Read(b []byte) (int, error)  { return s.r.Read(b) }
func (s *conPTYStream) Write(b []byte) (int, error) { return s.w.Write(b) }

func (s *conPTYStream) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.hpc == 0 {
		return nil
	}
	return windows.ResizePseudoConsole(s.hpc, windows.Coord{
		X: clampConPTYDim(cols, 120),
		Y: clampConPTYDim(rows, 32),
	})
}

func (s *conPTYStream) Kill() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		hpc := s.hpc
		proc := s.proc
		s.hpc = 0
		s.proc = 0
		s.mu.Unlock()

		_ = s.w.Close()
		if proc != 0 {
			_ = windows.TerminateProcess(proc, 1)
			_ = windows.CloseHandle(proc)
		}
		if hpc != 0 {
			// Drain unread ConPTY output while ClosePseudoConsole flushes conhost so
			// it can never block on a full pipe buffer.
			go func() { _, _ = io.Copy(io.Discard, s.r) }()
			windows.ClosePseudoConsole(hpc)
		}
		_ = s.r.Close()
	})
	return nil
}

func (s *conPTYStream) Close() error {
	return s.Kill()
}

func buildUTF16EnvBlock(env []string) []uint16 {
	var block []uint16
	for _, entry := range env {
		if entry == "" || strings.IndexByte(entry, 0) >= 0 {
			continue
		}
		block = append(block, utf16.Encode([]rune(entry))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		return []uint16{0, 0}
	}
	return append(block, 0)
}

func startPipeFallback(cmd *exec.Cmd) (io.ReadWriteCloser, func(uint16, uint16) error, func() error, func() error, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, nil, nil, nil, err
	}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, nil, nil, nil, err
	}
	_ = inR.Close()
	_ = outW.Close()

	rw := &pipeReadWriteCloser{r: outR, w: inW}
	resizeFn := func(uint16, uint16) error { return nil }
	killFn := func() error {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return rw.Close()
	}
	return rw, resizeFn, killFn, cmd.Wait, nil
}

type pipeReadWriteCloser struct {
	r *os.File
	w *os.File
}

func (p *pipeReadWriteCloser) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeReadWriteCloser) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeReadWriteCloser) Close() error {
	_ = p.w.Close()
	return p.r.Close()
}
