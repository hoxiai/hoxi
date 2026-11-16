//go:build windows

package computer

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/hoxiai/hoxi/internal/subproc"
)

type nativeBackend struct{}

var desktopUser = windows.NewLazySystemDLL("user32.dll")
var enumDesktopWindows = desktopUser.NewProc("EnumWindows")
var getWindowPID = desktopUser.NewProc("GetWindowThreadProcessId")
var getWindowText = desktopUser.NewProc("GetWindowTextW")
var visibleWindow = desktopUser.NewProc("IsWindowVisible")
var getWindowRect = desktopUser.NewProc("GetWindowRect")
var foregroundWindow = desktopUser.NewProc("GetForegroundWindow")
var setForegroundWindow = desktopUser.NewProc("SetForegroundWindow")
var sendDesktopInput = desktopUser.NewProc("SendInput")
var setDesktopCursor = desktopUser.NewProc("SetCursorPos")
var windowAtPoint = desktopUser.NewProc("WindowFromPoint")
var setThreadDPI = desktopUser.NewProc("SetThreadDpiAwarenessContext")

type winRect struct{ Left, Top, Right, Bottom int32 }
type desktopWindow struct {
	Handle uintptr
	PID    uint32
	Title  string
	Bounds Rect
}

func desktopDPI() func() {
	runtime.LockOSThread()
	previous, _, _ := setThreadDPI.Call(^uintptr(3))
	return func() {
		if previous != 0 {
			setThreadDPI.Call(previous)
		}
		runtime.UnlockOSThread()
	}
}

// NewCallback slots are never freed by Go on Windows. Reuse one callback for
// every enumeration rather than allocating one for each observation.
var windowEnumeration struct {
	sync.Mutex
	rows []desktopWindow
}
var windowEnumerationCallback = syscall.NewCallback(collectDesktopWindow)

func collectDesktopWindow(hwnd, unused uintptr) uintptr {
	if len(windowEnumeration.rows) >= 512 {
		return 0
	}
	visible, _, _ := visibleWindow.Call(hwnd)
	if visible == 0 {
		return 1
	}
	var pid uint32
	getWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 || pid == uint32(os.Getpid()) {
		return 1
	}
	var r winRect
	ok, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 || r.Right <= r.Left || r.Bottom <= r.Top {
		return 1
	}
	var name [257]uint16
	getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	title := windows.UTF16ToString(name[:])
	if title == "" {
		return 1
	}
	windowEnumeration.rows = append(windowEnumeration.rows, desktopWindow{hwnd, pid, title, Rect{float64(r.Left), float64(r.Top), float64(r.Right - r.Left), float64(r.Bottom - r.Top)}})
	return 1
}
func desktopWindows() []desktopWindow {
	windowEnumeration.Lock()
	defer windowEnumeration.Unlock()
	done := desktopDPI()
	defer done()
	windowEnumeration.rows = []desktopWindow{}
	enumDesktopWindows.Call(windowEnumerationCallback, 0)
	rows := windowEnumeration.rows
	windowEnumeration.rows = nil
	return rows
}
func processApp(pid uint32) (App, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return App{}, err
	}
	defer windows.CloseHandle(handle)
	var size uint32 = 32768
	path := make([]uint16, size)
	if err = windows.QueryFullProcessImageName(handle, 0, &path[0], &size); err != nil {
		return App{}, err
	}
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return App{}, err
	}
	file := windows.UTF16ToString(path[:size])
	started := strconv.FormatUint(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime), 10)
	name := filepath.Base(file)
	return App{ID: strconv.FormatUint(uint64(pid), 10) + ":" + started, PID: int(pid), Name: name, BundleID: strings.ToLower(name), Path: file, Started: started}, nil
}
func (nativeBackend) Permissions(ctx context.Context, prompt string) (Permissions, error) {
	open := desktopUser.NewProc("OpenInputDesktop")
	handle, _, _ := open.Call(0, 0, 0x0100)
	interactive := handle != 0
	if handle != 0 {
		desktopUser.NewProc("CloseDesktop").Call(handle)
	}
	return Permissions{Supported: true, Platform: "windows", Accessibility: interactive, ScreenRecording: interactive, ForegroundOnly: true}, ctx.Err()
}
func (nativeBackend) Apps(ctx context.Context) ([]App, error) {
	apps := []App{}
	seen := map[uint32]bool{}
	for _, window := range desktopWindows() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[window.PID] {
			continue
		}
		seen[window.PID] = true
		app, err := processApp(window.PID)
		if err == nil {
			apps = append(apps, app)
		}
	}
	return apps, nil
}

//go:embed windows_accessibility.ps1
var windowsAccessibility string

func windowsAX(ctx context.Context, pid int, hwnd uintptr, operation string, out any) error {
	// PowerShell must initialize UI Automation and compile the PrintWindow
	// helper on every invocation. This can exceed four seconds on Windows ARM
	// running the x64 build even when the target window is responsive.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	script := utf16.Encode([]rune(windowsAccessibility))
	encoded := make([]byte, len(script)*2)
	for i, v := range script {
		binary.LittleEndian.PutUint16(encoded[2*i:], v)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	executable := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	command := subproc.CommandContext(ctx, executable, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded))
	payload, _ := json.Marshal(map[string]any{"window": strconv.FormatUint(uint64(hwnd), 10), "process": pid, "operation": operation})
	command.Stdin = bytes.NewReader(payload)
	result, err := command.Output()
	if err != nil {
		return errors.New("accessibility_inspection_unavailable")
	}
	if len(result) > 2<<20 {
		return errors.New("accessibility_budget_exceeded")
	}
	return json.Unmarshal(bytes.TrimPrefix(result, []byte{0xef, 0xbb, 0xbf}), out)
}
func (nativeBackend) Inspect(ctx context.Context, app App, wanted string, capture bool) (Snapshot, error) {
	snapshot := Snapshot{App: app, Windows: []Window{}, Elements: []Element{}}
	var target *desktopWindow
	for _, window := range desktopWindows() {
		if int(window.PID) != app.PID {
			continue
		}
		id := strconv.FormatUint(uint64(window.Handle), 10)
		snapshot.Windows = append(snapshot.Windows, Window{id, window.Title, window.Bounds})
		if target == nil && (wanted == "" || wanted == id) {
			copy := window
			target = &copy
		}
	}
	if target == nil {
		return Snapshot{}, errors.New("no_visible_target_window")
	}
	snapshot.WindowID = strconv.FormatUint(uint64(target.Handle), 10)
	var ax struct {
		Elements []Element `json:"elements"`
	}
	if windowsAX(ctx, app.PID, target.Handle, "inspect", &ax) == nil {
		snapshot.Elements = ax.Elements
	}
	if capture {
		data, width, height, err := captureDesktopWindow(ctx, *target)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Image = base64.StdEncoding.EncodeToString(data)
		snapshot.Width = width
		snapshot.Height = height
	}
	return snapshot, nil
}
func captureDesktopWindow(ctx context.Context, window desktopWindow) ([]byte, int, int, error) {
	var captured struct {
		Image  string `json:"image"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if err := windowsAX(ctx, int(window.PID), window.Handle, "capture", &captured); err != nil {
		return nil, 0, 0, err
	}
	if captured.Width < 1 || captured.Height < 1 || captured.Width > 1600 || captured.Height > 1600 || len(captured.Image) > 20<<20 {
		return nil, 0, 0, errors.New("capture_exceeds_budget")
	}
	data, err := base64.StdEncoding.DecodeString(captured.Image)
	return data, captured.Width, captured.Height, err
}

type desktopInput struct {
	Kind uint32
	_    uint32
	Data [32]byte
}

func sendWindowsKey(code uint16, flags uint32) error {
	var event desktopInput
	event.Kind = 1
	binary.LittleEndian.PutUint16(event.Data[0:], code)
	binary.LittleEndian.PutUint32(event.Data[4:], flags)
	sent, _, _ := sendDesktopInput.Call(1, uintptr(unsafe.Pointer(&event)), unsafe.Sizeof(event))
	if sent != 1 {
		return errors.New("input_blocked_by_windows_permissions")
	}
	return nil
}
func sendWindowsUnicode(char uint16, up bool) error {
	var event desktopInput
	event.Kind = 1
	binary.LittleEndian.PutUint16(event.Data[2:], char)
	flags := uint32(4)
	if up {
		flags |= 2
	}
	binary.LittleEndian.PutUint32(event.Data[4:], flags)
	sent, _, _ := sendDesktopInput.Call(1, uintptr(unsafe.Pointer(&event)), unsafe.Sizeof(event))
	if sent != 1 {
		return errors.New("input_blocked_by_windows_permissions")
	}
	return nil
}
func sendWindowsMouse(flags uint32, data int32) error {
	var event desktopInput
	binary.LittleEndian.PutUint32(event.Data[8:], uint32(data))
	binary.LittleEndian.PutUint32(event.Data[12:], flags)
	sent, _, _ := sendDesktopInput.Call(1, uintptr(unsafe.Pointer(&event)), unsafe.Sizeof(event))
	if sent != 1 {
		return errors.New("input_blocked_by_windows_permissions")
	}
	return nil
}
func windowForeground(hwnd uintptr, pid int) bool {
	front, _, _ := foregroundWindow.Call()
	var owner uint32
	getWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
	return front == hwnd && int(owner) == pid
}
func ownedWindowsPoint(x, y int32, pid int) bool {
	packed := uintptr(uint64(uint32(x)) | uint64(uint32(y))<<32)
	window, _, _ := windowAtPoint.Call(packed)
	var owner uint32
	getWindowPID.Call(window, uintptr(unsafe.Pointer(&owner)))
	return int(owner) == pid
}
func (nativeBackend) Input(ctx context.Context, app App, window Window, action Action) error {
	done := desktopDPI()
	defer done()
	number, err := strconv.ParseUint(window.ID, 10, 64)
	if err != nil {
		return err
	}
	hwnd := uintptr(number)
	if err := ctx.Err(); err != nil {
		return err
	}
	var owner uint32
	getWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
	if int(owner) != app.PID {
		return errors.New("target_app_changed")
	}
	setForegroundWindow.Call(hwnd)
	for i := 0; i < 50 && !windowForeground(hwnd, app.PID); i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !windowForeground(hwnd, app.PID) {
		return errors.New("target_app_not_frontmost")
	}
	var current winRect
	ok, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&current)))
	if ok == 0 || float64(current.Right-current.Left) != window.Bounds.Width || float64(current.Bottom-current.Top) != window.Bounds.Height {
		return errors.New("window_resized_or_closed")
	}
	window.Bounds.X, window.Bounds.Y = float64(current.Left), float64(current.Top)
	if action.Action == "focus" {
		return nil
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !windowForeground(hwnd, app.PID) {
			return errors.New("target_app_changed")
		}
		return nil
	}
	if action.Action == "type" || action.Action == "key" {
		var focus struct {
			Secure bool `json:"secure"`
		}
		if err := windowsAX(ctx, app.PID, hwnd, "focused", &focus); err != nil {
			return err
		}
		if focus.Secure {
			return errors.New("secure_field_requires_manual_input")
		}
	}
	if action.Action == "type" {
		for _, char := range utf16.Encode([]rune(action.Text)) {
			if err := check(); err != nil {
				return err
			}
			if err := sendWindowsUnicode(char, false); err != nil {
				return err
			}
			if err := sendWindowsUnicode(char, true); err != nil {
				return err
			}
		}
		return nil
	}
	if action.Action == "key" {
		chord, err := ParseKey(action.Key)
		if chord.Meta {
			return errors.New("system_shortcut_not_allowed")
		}
		if err != nil {
			return err
		}
		codes := map[string]uint16{"Enter": 13, "Tab": 9, "Escape": 27, "Backspace": 8, "Delete": 46, "ArrowLeft": 37, "ArrowRight": 39, "ArrowUp": 38, "ArrowDown": 40, "Home": 36, "End": 35, "PageUp": 33, "PageDown": 34, "Space": 32, "F1": 112, "F2": 113, "F3": 114, "F4": 115, "F5": 116, "F6": 117, "F7": 118, "F8": 119, "F9": 120, "F10": 121, "F11": 122, "F12": 123}
		code := codes[chord.Key]
		if code == 0 && len(chord.Key) == 1 {
			value, _, _ := desktopUser.NewProc("VkKeyScanW").Call(uintptr(chord.Key[0]))
			if uint16(value) == 0xffff {
				return errors.New("unsupported_key")
			}
			code = uint16(value) & 255
			if value&0x100 != 0 {
				chord.Shift = true
			}
		}
		modifiers := []uint16{}
		if chord.Control {
			modifiers = append(modifiers, 17)
		}
		if chord.Alt {
			modifiers = append(modifiers, 18)
		}
		if chord.Shift {
			modifiers = append(modifiers, 16)
		}
		held := []uint16{}
		defer func() {
			for i := len(held) - 1; i >= 0; i-- {
				_ = sendWindowsKey(held[i], 2)
			}
		}()
		for _, modifier := range modifiers {
			if err := sendWindowsKey(modifier, 0); err != nil {
				return err
			}
			held = append(held, modifier)
		}
		if err := check(); err != nil {
			return err
		}
		if err := sendWindowsKey(code, 0); err != nil {
			return err
		}
		held = append(held, code)
		return nil
	}
	x, y := int32(window.Bounds.X+action.X), int32(window.Bounds.Y+action.Y)
	if !ownedWindowsPoint(x, y, app.PID) {
		return errors.New("target_window_occluded")
	}
	setDesktopCursor.Call(uintptr(x), uintptr(y))
	if err := check(); err != nil {
		return err
	}
	if action.Action == "move" {
		return nil
	}
	if action.Action == "scroll" {
		if action.DeltaY != 0 {
			if err := sendWindowsMouse(0x0800, int32(-action.DeltaY)); err != nil {
				return err
			}
		}
		if action.DeltaX != 0 {
			return sendWindowsMouse(0x1000, int32(action.DeltaX))
		}
		return nil
	}
	down, up := uint32(2), uint32(4)
	if action.Button == "right" {
		down, up = 8, 16
	} else if action.Button == "middle" {
		down, up = 32, 64
	}
	if action.Action == "drag" {
		if err := sendWindowsMouse(down, 0); err != nil {
			return err
		}
		defer sendWindowsMouse(up, 0)
		for i := 1; i <= 20; i++ {
			if err := check(); err != nil {
				return err
			}
			px := int32(window.Bounds.X + action.X + (action.EndX-action.X)*float64(i)/20)
			py := int32(window.Bounds.Y + action.Y + (action.EndY-action.Y)*float64(i)/20)
			if !ownedWindowsPoint(px, py, app.PID) {
				return errors.New("target_window_occluded")
			}
			setDesktopCursor.Call(uintptr(px), uintptr(py))
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	}
	for i := 0; i < max(1, action.ClickCount); i++ {
		if err := check(); err != nil {
			return err
		}
		if err := sendWindowsMouse(down, 0); err != nil {
			return err
		}
		if err := sendWindowsMouse(up, 0); err != nil {
			_ = sendWindowsMouse(up, 0)
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}
