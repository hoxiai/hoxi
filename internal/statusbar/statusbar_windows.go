//go:build windows

package statusbar

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

const (
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmNull        = 0x0000
	wmCommand     = 0x0111
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B
	wmApp         = 0x8000

	wmTrayCallback = wmApp + 1
	wmTrayUpdate   = wmApp + 2
	wmTrayStop     = wmApp + 3

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfChecked   = 0x00000008
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100

	idiApplication = 32512
)

const (
	cmdOpenWorkspace = 1001
	cmdAttention     = 1002
	cmdRunning       = 1003
	cmdNewThread     = 1004
	cmdHandoff       = 1005
	cmdKeepAwake     = 1006
	cmdPolicy        = 1007
	cmdSettings      = 1008
	cmdQuit          = 1009

	cmdPendingBase = 2000
	cmdRunningBase = 3000
)

var (
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procShellNotifyIconW   = modShell32.NewProc("Shell_NotifyIconW")
	procRegisterClassExW   = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW    = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW     = modUser32.NewProc("DefWindowProcW")
	procGetMessageW        = modUser32.NewProc("GetMessageW")
	procTranslateMessage   = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW   = modUser32.NewProc("DispatchMessageW")
	procPostMessageW       = modUser32.NewProc("PostMessageW")
	procPostQuitMessage    = modUser32.NewProc("PostQuitMessage")
	procDestroyWindow      = modUser32.NewProc("DestroyWindow")
	procCreatePopupMenu    = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW        = modUser32.NewProc("AppendMenuW")
	procDestroyMenu        = modUser32.NewProc("DestroyMenu")
	procTrackPopupMenu     = modUser32.NewProc("TrackPopupMenu")
	procSetForegroundWnd   = modUser32.NewProc("SetForegroundWindow")
	procGetCursorPos       = modUser32.NewProc("GetCursorPos")
	procCreateIconIndirect = modUser32.NewProc("CreateIconIndirect")
	procDestroyIcon        = modUser32.NewProc("DestroyIcon")
	procLoadIconW          = modUser32.NewProc("LoadIconW")
	procCreateDIBSection   = modGdi32.NewProc("CreateDIBSection")
	procCreateBitmap       = modGdi32.NewProc("CreateBitmap")
	procDeleteObject       = modGdi32.NewProc("DeleteObject")
	procGetModuleHandleW   = modKernel32.NewProc("GetModuleHandleW")
)

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     uintptr
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type winPoint struct {
	x int32
	y int32
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

var (
	statusBarCtxMu sync.RWMutex
	statusBarCtx   context.Context

	trayMu      sync.Mutex
	traySummary = Summary{Available: true, KeepAwakeEnabled: true}
	trayHWND    uintptr
	trayRunning bool
	trayDone    chan struct{}
	trayIcon    uintptr

	trayWndProcOnce sync.Once
	trayWndProcPtr  uintptr
)

func Start(ctx context.Context) {
	statusBarCtxMu.Lock()
	statusBarCtx = ctx
	statusBarCtxMu.Unlock()

	trayMu.Lock()
	if trayRunning {
		hwnd := trayHWND
		trayMu.Unlock()
		if hwnd != 0 {
			_, _, _ = procPostMessageW.Call(hwnd, wmTrayUpdate, 0, 0)
		}
		return
	}
	trayRunning = true
	done := make(chan struct{})
	trayDone = done
	ready := make(chan struct{})
	trayMu.Unlock()

	go runTrayLoop(ready, done)
	<-ready
}

func Update(summary Summary) {
	trayMu.Lock()
	traySummary = summary
	hwnd := trayHWND
	trayMu.Unlock()
	if hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmTrayUpdate, 0, 0)
	}
}

func Stop() {
	statusBarCtxMu.Lock()
	statusBarCtx = nil
	statusBarCtxMu.Unlock()

	trayMu.Lock()
	if !trayRunning {
		trayMu.Unlock()
		return
	}
	hwnd := trayHWND
	done := trayDone
	trayMu.Unlock()

	if hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmTrayStop, 0, 0)
	}
	if done != nil {
		<-done
	}
}

func activeStatusBarCtx() context.Context {
	statusBarCtxMu.RLock()
	defer statusBarCtxMu.RUnlock()
	return statusBarCtx
}

func hasWailsFrontend(ctx context.Context) bool {
	return ctx != nil && ctx.Value("frontend") != nil
}

func HoxiShowMainWindow() {
	ctx := activeStatusBarCtx()
	fn := activeActionHandler()
	if ctx == nil && fn == nil {
		return
	}
	go func() {
		if hasWailsFrontend(ctx) {
			wailsruntime.WindowShow(ctx)
			wailsruntime.WindowUnminimise(ctx)
		}
		invokeActionHandler(fn, MenuActionWorkspace, "")
	}()
}

func triggerMenuAction(kind, sessionID string) {
	ctx := activeStatusBarCtx()
	fn := activeActionHandler()
	if ctx == nil && fn == nil {
		return
	}
	go func() {
		if kind != MenuActionToggleKeepAwake && hasWailsFrontend(ctx) {
			wailsruntime.WindowShow(ctx)
			wailsruntime.WindowUnminimise(ctx)
		}
		invokeActionHandler(fn, kind, sessionID)
	}()
}

func HoxiQuitApplication() {
	ctx := activeStatusBarCtx()
	if !hasWailsFrontend(ctx) {
		return
	}
	go wailsruntime.Quit(ctx)
}

func formatTrayTooltip(summary Summary) string {
	if !summary.Available {
		return "Hoxi: OpenCode 引擎正在启动"
	}
	return fmt.Sprintf("Hoxi：待审批 %d · 运行中 %d", summary.Attention, summary.Running)
}

func runTrayLoop(ready chan<- struct{}, done chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		trayMu.Lock()
		if trayIcon != 0 {
			_, _, _ = procDestroyIcon.Call(trayIcon)
			trayIcon = 0
		}
		trayHWND = 0
		trayRunning = false
		trayMu.Unlock()
		close(done)
	}()

	trayWndProcOnce.Do(func() {
		trayWndProcPtr = syscall.NewCallback(trayWndProc)
	})

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString("HoxiSystemTrayWindowClass")
	wcx := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   trayWndProcPtr,
		hInstance:     hInst,
		lpszClassName: className,
	}
	_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcx)))

	winName, _ := windows.UTF16PtrFromString("Hoxi System Tray")
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(winName)),
		0,
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		close(ready)
		return
	}

	trayMu.Lock()
	trayHWND = hwnd
	snap := traySummary
	trayMu.Unlock()

	addOrModifyTrayIcon(hwnd, snap, true)
	close(ready)

	var msg winMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmTrayCallback:
		switch uint32(lParam) & 0xFFFF {
		case wmLButtonUp, wmLButtonDbl:
			HoxiShowMainWindow()
			return 0
		case wmRButtonUp, wmContextMenu:
			showTrayPopupMenu(hwnd)
			return 0
		}
	case wmTrayUpdate:
		trayMu.Lock()
		snap := traySummary
		trayMu.Unlock()
		addOrModifyTrayIcon(hwnd, snap, false)
		return 0
	case wmTrayStop:
		deleteTrayIcon(hwnd)
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		deleteTrayIcon(hwnd)
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func addOrModifyTrayIcon(hwnd uintptr, summary Summary, add bool) {
	icon := createTrayIcon(summary.Attention, summary.Running, summary.Available)

	trayMu.Lock()
	oldIcon := trayIcon
	if icon != 0 {
		trayIcon = icon
	} else {
		icon = trayIcon
	}
	trayMu.Unlock()

	if icon == 0 {
		icon, _, _ = procLoadIconW.Call(0, idiApplication)
	}

	nid := notifyIconDataW{
		cbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayCallback,
		hIcon:            icon,
	}
	tip := windows.StringToUTF16(formatTrayTooltip(summary))
	if len(tip) > len(nid.szTip) {
		tip = tip[:len(nid.szTip)]
		tip[len(tip)-1] = 0
	}
	copy(nid.szTip[:], tip)

	action := uintptr(nimModify)
	if add {
		action = uintptr(nimAdd)
	}
	ok, _, _ := procShellNotifyIconW.Call(action, uintptr(unsafe.Pointer(&nid)))
	if ok == 0 && !add {
		_, _, _ = procShellNotifyIconW.Call(uintptr(nimAdd), uintptr(unsafe.Pointer(&nid)))
	}
	if oldIcon != 0 && oldIcon != icon {
		_, _, _ = procDestroyIcon.Call(oldIcon)
	}
}

func deleteTrayIcon(hwnd uintptr) {
	nid := notifyIconDataW{
		cbSize: uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:   hwnd,
		uID:    1,
	}
	_, _, _ = procShellNotifyIconW.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&nid)))
}

func showTrayPopupMenu(hwnd uintptr) {
	trayMu.Lock()
	summary := traySummary
	trayMu.Unlock()

	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendMenuString(hMenu, mfString, cmdOpenWorkspace, "唤醒 Hoxi 工作台")
	appendMenuSeparator(hMenu)

	var pendingTargets, runningTargets []SessionTarget
	if !summary.Available {
		appendMenuString(hMenu, mfString, cmdAttention, "待审批任务（引擎正在连接…）")
		appendMenuString(hMenu, mfString, cmdRunning, "运行中任务 (0)")
	} else {
		appendMenuString(hMenu, mfString, cmdAttention, fmt.Sprintf("待审批任务 (%d)", summary.Attention))
		pendingTargets = summary.PendingTargets
		for i, target := range pendingTargets {
			if i >= maxMenuTargets {
				break
			}
			appendMenuString(hMenu, mfString, uintptr(cmdPendingBase+i), FormatMenuTargetLabel(target))
		}
		appendMenuString(hMenu, mfString, cmdRunning, fmt.Sprintf("运行中任务 (%d)", summary.Running))
		runningTargets = summary.RunningTargets
		for i, target := range runningTargets {
			if i >= maxMenuTargets {
				break
			}
			appendMenuString(hMenu, mfString, uintptr(cmdRunningBase+i), FormatMenuTargetLabel(target))
		}
	}

	appendMenuSeparator(hMenu)
	appendMenuString(hMenu, mfString, cmdNewThread, "新建对话")
	appendMenuString(hMenu, mfString, cmdHandoff, "导入外部 CLI 会话…")
	appendMenuSeparator(hMenu)

	keepAwakeFlags := uintptr(mfString)
	if summary.KeepAwakeEnabled {
		keepAwakeFlags |= mfChecked
	}
	keepAwakeTitle := "任务运行时保持唤醒"
	if summary.KeepAwakeEnabled && summary.KeepAwakeActive {
		keepAwakeTitle = "任务运行时保持唤醒（生效中）"
	}
	appendMenuString(hMenu, keepAwakeFlags, cmdKeepAwake, keepAwakeTitle)
	appendMenuString(hMenu, mfString, cmdPolicy, "审批与安全策略…")
	appendMenuString(hMenu, mfString, cmdSettings, "偏好设置…")
	appendMenuSeparator(hMenu)
	appendMenuString(hMenu, mfString, cmdQuit, "退出 Hoxi")

	var pt winPoint
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWnd.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(
		hMenu,
		tpmRightButton|tpmReturnCmd|tpmNonotify,
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		hwnd,
		0,
	)
	_, _, _ = procPostMessageW.Call(hwnd, wmNull, 0, 0)
	dispatchTrayMenuCommand(int(cmd), pendingTargets, runningTargets)
}

func dispatchTrayMenuCommand(cmd int, pendingTargets, runningTargets []SessionTarget) {
	switch {
	case cmd == cmdOpenWorkspace:
		HoxiShowMainWindow()
	case cmd == cmdAttention:
		triggerMenuAction(MenuActionAttention, "")
	case cmd == cmdRunning:
		triggerMenuAction(MenuActionRunning, "")
	case cmd == cmdNewThread:
		triggerMenuAction(MenuActionNewThread, "")
	case cmd == cmdHandoff:
		triggerMenuAction(MenuActionHandoff, "")
	case cmd == cmdKeepAwake:
		triggerMenuAction(MenuActionToggleKeepAwake, "")
	case cmd == cmdPolicy:
		triggerMenuAction(MenuActionPolicy, "")
	case cmd == cmdSettings:
		triggerMenuAction(MenuActionSettings, "")
	case cmd == cmdQuit:
		HoxiQuitApplication()
	case cmd >= cmdPendingBase && cmd < cmdPendingBase+len(pendingTargets):
		triggerMenuAction(MenuActionAttention, pendingTargets[cmd-cmdPendingBase].SessionID)
	case cmd >= cmdRunningBase && cmd < cmdRunningBase+len(runningTargets):
		triggerMenuAction(MenuActionRunning, runningTargets[cmd-cmdRunningBase].SessionID)
	}
}

func appendMenuString(hMenu uintptr, flags uintptr, id uintptr, label string) {
	ptr, err := windows.UTF16PtrFromString(label)
	if err != nil {
		return
	}
	_, _, _ = procAppendMenuW.Call(hMenu, flags, id, uintptr(unsafe.Pointer(ptr)))
}

func appendMenuSeparator(hMenu uintptr) {
	_, _, _ = procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
}

// createTrayIcon renders a 32x32 32-bit BGRA icon with the Hoxi glyph and a
// live status badge dot when tasks are running or awaiting approval.
func createTrayIcon(attention, running int, available bool) uintptr {
	const size = 32
	hdr := bitmapInfoHeader{
		biSize:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:    size,
		biHeight:   -size, // top-down DIB
		biPlanes:   1,
		biBitCount: 32,
	}
	var bits unsafe.Pointer
	hbmColor, _, _ := procCreateDIBSection.Call(
		0,
		uintptr(unsafe.Pointer(&hdr)),
		0,
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hbmColor == 0 || bits == nil {
		return 0
	}
	defer procDeleteObject.Call(hbmColor)

	pixels := unsafe.Slice((*uint32)(bits), size*size)
	renderTrayIconPixels(pixels, size, attention, running, available)

	maskBits := make([]byte, (size*size)/8)
	hbmMask, _, _ := procCreateBitmap.Call(size, size, 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	if hbmMask == 0 {
		return 0
	}
	defer procDeleteObject.Call(hbmMask)

	ii := iconInfo{
		fIcon:    1,
		hbmMask:  hbmMask,
		hbmColor: hbmColor,
	}
	hIcon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	return hIcon
}

func renderTrayIconPixels(pixels []uint32, size int, attention, running int, available bool) {
	const bgColor = uint32(0xFF18181B)
	const fgColor = uint32(0xFFFFFFFF)

	setPixel := func(x, y int, argb uint32) {
		if x >= 0 && x < size && y >= 0 && y < size {
			pixels[y*size+x] = argb
		}
	}

	// 1. Rounded dark tile (x,y in 1..30, corner radius 7)
	for y := 1; y < size-1; y++ {
		for x := 1; x < size-1; x++ {
			if insideRoundedRect(float64(x)+0.5, float64(y)+0.5, 1, 1, float64(size-2), float64(size-2), 7.0) {
				setPixel(x, y, bgColor)
			}
		}
	}

	// 2. Hoxi mark (roof chevron, eyes, smile)
	for y := 5; y <= 26; y++ {
		for x := 5; x <= 26; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// Roof chevron: peak at (16, 6.5), slopes to (7, 13.5) and (25, 13.5)
			if distToSegment(fx, fy, 16.0, 6.5, 7.5, 13.0) <= 1.8 ||
				distToSegment(fx, fy, 16.0, 6.5, 24.5, 13.0) <= 1.8 {
				setPixel(x, y, fgColor)
				continue
			}
			// Left & right eyes
			if insideRoundedRect(fx, fy, 10.0, 14.5, 3.2, 6.0, 1.6) ||
				insideRoundedRect(fx, fy, 18.8, 14.5, 3.2, 6.0, 1.6) {
				setPixel(x, y, fgColor)
				continue
			}
			// Smile curve (parabolic arc between x=10.5..21.5)
			if fx >= 10.5 && fx <= 21.5 {
				dx := (fx - 16.0) / 5.5
				curveY := 24.2 - 2.0*dx*dx
				if math.Abs(fy-curveY) <= 1.45 {
					setPixel(x, y, fgColor)
				}
			}
		}
	}

	// 3. Status badge dot at bottom-right when offline, attention > 0, or running > 0
	var badgeColor uint32
	switch {
	case !available:
		badgeColor = 0xFFA1A1AA // zinc-400
	case attention > 0:
		badgeColor = 0xFFF59E0B // amber-500
	case running > 0:
		badgeColor = 0xFF10B981 // emerald-500
	}
	if badgeColor != 0 {
		cx, cy := 25.0, 25.0
		for y := 18; y < size; y++ {
			for x := 18; x < size; x++ {
				d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
				if d <= 4.6 {
					setPixel(x, y, badgeColor)
				} else if d <= 6.0 {
					setPixel(x, y, bgColor)
				}
			}
		}
	}
}

func insideRoundedRect(px, py, rx, ry, rw, rh, radius float64) bool {
	if px < rx || px > rx+rw || py < ry || py > ry+rh {
		return false
	}
	cx := math.Max(rx+radius, math.Min(px, rx+rw-radius))
	cy := math.Max(ry+radius, math.Min(py, ry+rh-radius))
	return math.Hypot(px-cx, py-cy) <= radius
}

func distToSegment(px, py, ax, ay, bx, by float64) float64 {
	vx, vy := bx-ax, by-ay
	wx, wy := px-ax, py-ay
	c1 := wx*vx + wy*vy
	if c1 <= 0 {
		return math.Hypot(px-ax, py-ay)
	}
	c2 := vx*vx + vy*vy
	if c2 <= c1 {
		return math.Hypot(px-bx, py-by)
	}
	b := c1 / c2
	pbx, pby := ax+b*vx, ay+b*vy
	return math.Hypot(px-pbx, py-pby)
}
