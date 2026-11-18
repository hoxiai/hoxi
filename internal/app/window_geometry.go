package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/wailsapp/wails/v2/pkg/options"
)

const (
	defaultWindowWidth      = 1360
	defaultWindowHeight     = 860
	defaultWindowMinWidth   = 880
	defaultWindowMinHeight  = 560
	adaptiveMaxWindowWidth  = 1680
	adaptiveMaxWindowHeight = 1040
	sanityMaxWindowWidth    = 7680
	sanityMaxWindowHeight   = 4320
	screenEdgeMarginX       = 32
	screenEdgeMarginY       = 32
	maxNativeChromeOffset   = 80
	initialResizeTolerance  = 2
	windowStateVersion      = 1
	windowSaveDebounce      = 400 * time.Millisecond
)

// screenWorkArea is the primary display's usable work area (excluding system
// menu bars, docks, and taskbars) in Wails logical coordinates.
type screenWorkArea struct {
	Width  int
	Height int
}

// windowState is persisted to ~/.hoxi/window-state.json once the user manually
// resizes or maximizes the desktop window.
type windowState struct {
	Version   int  `json:"version"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised,omitempty"`
}

// windowLaunchSpec holds the resolved startup bounds passed to options.App.
type windowLaunchSpec struct {
	Width      int
	Height     int
	MinWidth   int
	MinHeight  int
	StartState options.WindowStartState
}

// computeAdaptiveWindowSize derives a comfortable initial window size from the
// primary screen's usable work area:
//   - Compact laptops (1280-1440px wide) use ~87-90% of usable space without
//     overlapping the Dock or taskbar.
//   - Standard 14"-16" laptops and 1080p monitors use ~79-85%.
//   - 2K/4K and ultrawide displays cap the default initial window at 1680x1040
//     so first launch stays focused rather than sprawling across the screen.
//   - Unknown/headless screen metrics fall back to 1360x860.
func computeAdaptiveWindowSize(screen screenWorkArea) (width, height, minWidth, minHeight int) {
	minWidth = defaultWindowMinWidth
	minHeight = defaultWindowMinHeight
	if screen.Width <= 0 || screen.Height <= 0 {
		return defaultWindowWidth, defaultWindowHeight, minWidth, minHeight
	}
	if screen.Width < minWidth {
		minWidth = screen.Width
	}
	if screen.Height < minHeight {
		minHeight = screen.Height
	}

	availW := screen.Width
	if screen.Width > minWidth+screenEdgeMarginX {
		availW = screen.Width - screenEdgeMarginX
	}
	availH := screen.Height
	if screen.Height > minHeight+screenEdgeMarginY {
		availH = screen.Height - screenEdgeMarginY
	}

	targetW := int(math.Round(440 + 0.56*float64(screen.Width)))
	targetH := int(math.Round(195 + 0.65*float64(screen.Height)))

	maxW := min(adaptiveMaxWindowWidth, availW)
	maxH := min(adaptiveMaxWindowHeight, availH)

	width = clampWindowDim(targetW, minWidth, max(minWidth, maxW))
	height = clampWindowDim(targetH, minHeight, max(minHeight, maxH))
	return width, height, minWidth, minHeight
}

// resolveWindowLaunchSpec combines the current display's usable work area with
// any previously saved user window geometry, clamping saved dimensions so
// unplugging an external monitor never leaves the window overflowing a smaller
// laptop screen.
func resolveWindowLaunchSpec(screen screenWorkArea, saved *windowState, goos string) windowLaunchSpec {
	defW, defH, minW, minH := computeAdaptiveWindowSize(screen)
	if saved == nil || saved.Width <= 0 || saved.Height <= 0 {
		return windowLaunchSpec{
			Width:      defW,
			Height:     defH,
			MinWidth:   minW,
			MinHeight:  minH,
			StartState: options.Normal,
		}
	}

	ceilW := sanityMaxWindowWidth
	if screen.Width > 0 {
		ceilW = screen.Width
	}
	ceilH := sanityMaxWindowHeight
	if screen.Height > 0 {
		ceilH = screen.Height
	}

	startState := options.Normal
	// On macOS, AppKit's zoom state is already represented by a frame matching
	// visibleFrame, and Wails's startup zoom: call toggles an already-zoomed
	// frame back down. Only Windows uses a separate WS_MAXIMIZE start state.
	if saved.Maximised && goos == "windows" {
		startState = options.Maximised
	}

	return windowLaunchSpec{
		Width:      clampWindowDim(saved.Width, minW, max(minW, ceilW)),
		Height:     clampWindowDim(saved.Height, minH, max(minH, ceilH)),
		MinWidth:   minW,
		MinHeight:  minH,
		StartState: startState,
	}
}

func clampWindowDim(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absWindowDiff(a, b int) int {
	if a >= b {
		return a - b
	}
	return b - a
}

type windowStateStore struct {
	mu             sync.Mutex
	path           string
	logger         *slog.Logger
	lastSeq        uint64
	persistBlocked error
}

func newWindowStateStore(path string, logger *slog.Logger) *windowStateStore {
	return &windowStateStore{path: path, logger: logger}
}

func (s *windowStateStore) Load() (*windowState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var st windowState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, s.quarantineCorruptLocked(err)
	}
	if st.Width <= 0 || st.Height <= 0 {
		return nil, s.quarantineCorruptLocked(fmt.Errorf("invalid window dimensions %dx%d", st.Width, st.Height))
	}
	return &st, nil
}

func (s *windowStateStore) quarantineCorruptLocked(parseErr error) error {
	backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().UTC().UnixNano())
	if renameErr := os.Rename(s.path, backup); renameErr != nil {
		s.persistBlocked = fmt.Errorf("window-state file is corrupt (%v) and could not be backed up: %w", parseErr, renameErr)
		return s.persistBlocked
	}
	if s.logger != nil {
		s.logger.Warn("corrupt window-state file backed up", "backup", backup, "error", parseErr)
	}
	return fmt.Errorf("corrupt window-state file backed up: %w", parseErr)
}

func (s *windowStateStore) SaveSeq(seq uint64, st windowState) error {
	if st.Width <= 0 || st.Height <= 0 {
		return fmt.Errorf("refusing to persist invalid window dimensions %dx%d", st.Width, st.Height)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return errors.New("window-state path is unknown")
	}
	if s.persistBlocked != nil {
		return s.persistBlocked
	}
	if seq > 0 && seq <= s.lastSeq {
		return nil
	}
	if existing, err := os.ReadFile(s.path); err == nil && len(bytes.TrimSpace(existing)) > 0 {
		var prev windowState
		if err := json.Unmarshal(existing, &prev); err != nil || prev.Width <= 0 || prev.Height <= 0 {
			if err == nil {
				err = fmt.Errorf("invalid window dimensions %dx%d", prev.Width, prev.Height)
			}
			_ = s.quarantineCorruptLocked(err)
			if s.persistBlocked != nil {
				return s.persistBlocked
			}
		}
	}
	if st.Version <= 0 {
		st.Version = windowStateVersion
	}
	if err := hoxipaths.WriteJSONAtomic(s.path, st, 0o600); err != nil {
		return err
	}
	if seq > s.lastSeq {
		s.lastSeq = seq
	}
	return nil
}

// windowGeometryTracker tracks user-initiated window size changes in memory and
// flushes them atomically off the UI thread. If the user never resizes or
// maximizes the window during a session, nothing is written so Hoxi stays
// screen-adaptive across laptop and external monitors.
type windowGeometryTracker struct {
	store    *windowStateStore
	logger   *slog.Logger
	debounce time.Duration

	mu               sync.Mutex
	initialWidth     int
	initialHeight    int
	initialMaximised bool
	chromeDeltaW     int
	chromeDeltaH     int
	chromeCalibrated bool
	currentWidth     int
	currentHeight    int
	currentMaximised bool
	dirty            bool
	closed           bool
	seq              uint64
	writtenSeq       uint64
	timer            *time.Timer
	writeWg          sync.WaitGroup
}

func newWindowGeometryTracker(store *windowStateStore, spec windowLaunchSpec, logger *slog.Logger) *windowGeometryTracker {
	maximised := spec.StartState == options.Maximised
	return &windowGeometryTracker{
		store:            store,
		logger:           logger,
		debounce:         windowSaveDebounce,
		initialWidth:     spec.Width,
		initialHeight:    spec.Height,
		initialMaximised: maximised,
		currentWidth:     spec.Width,
		currentHeight:    spec.Height,
		currentMaximised: maximised,
	}
}

// CalibrateInitialFrame records any fixed platform window-chrome offset between
// options.App.{Width,Height} and the native outer window frame before user
// interaction so saved dimensions round-trip with zero drift across restarts.
func (t *windowGeometryTracker) CalibrateInitialFrame(frameWidth, frameHeight int) {
	if t == nil || frameWidth <= 0 || frameHeight <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.dirty || t.chromeCalibrated {
		return
	}
	t.chromeCalibrated = true
	dw := frameWidth - t.initialWidth
	dh := frameHeight - t.initialHeight
	if absWindowDiff(dw, 0) <= maxNativeChromeOffset && absWindowDiff(dh, 0) <= maxNativeChromeOffset {
		t.chromeDeltaW = dw
		t.chromeDeltaH = dh
	}
}

// RecordNativeFrameSize records a resize reported in native outer-frame units.
func (t *windowGeometryTracker) RecordNativeFrameSize(frameWidth, frameHeight int) {
	if t == nil || frameWidth <= 0 || frameHeight <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordNormalSizeLocked(frameWidth-t.chromeDeltaW, frameHeight-t.chromeDeltaH)
}

// RecordNormalSize records a resize already expressed in Wails logical units.
func (t *windowGeometryTracker) RecordNormalSize(width, height int) {
	if t == nil || width <= 0 || height <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordNormalSizeLocked(width, height)
}

func (t *windowGeometryTracker) recordNormalSizeLocked(width, height int) {
	if t.closed || width <= 0 || height <= 0 {
		return
	}
	if !t.dirty &&
		absWindowDiff(width, t.initialWidth) <= initialResizeTolerance &&
		absWindowDiff(height, t.initialHeight) <= initialResizeTolerance &&
		!t.initialMaximised {
		return
	}
	if t.dirty && width == t.currentWidth && height == t.currentHeight && !t.currentMaximised {
		return
	}
	t.currentWidth = width
	t.currentHeight = height
	t.currentMaximised = false
	t.dirty = true
	t.seq++
	t.scheduleFlushLocked()
}

// RecordWindowSnapshot captures normal/maximised window state on platforms that
// query window state before close or restart (such as Windows).
func (t *windowGeometryTracker) RecordWindowSnapshot(width, height int, isNormal, isMaximised bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	nextW := t.currentWidth
	nextH := t.currentHeight
	nextMax := t.currentMaximised
	if isMaximised {
		nextMax = true
	} else if isNormal && width > 0 && height > 0 {
		nextW = width
		nextH = height
		nextMax = false
	} else {
		return
	}
	if !t.dirty &&
		absWindowDiff(nextW, t.initialWidth) <= initialResizeTolerance &&
		absWindowDiff(nextH, t.initialHeight) <= initialResizeTolerance &&
		nextMax == t.initialMaximised {
		return
	}
	if t.dirty && nextW == t.currentWidth && nextH == t.currentHeight && nextMax == t.currentMaximised {
		return
	}
	t.currentWidth = nextW
	t.currentHeight = nextH
	t.currentMaximised = nextMax
	t.dirty = true
	t.seq++
}

func (t *windowGeometryTracker) scheduleFlushLocked() {
	if t.store == nil || t.closed {
		return
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	d := t.debounce
	if d <= 0 {
		d = windowSaveDebounce
	}
	t.timer = time.AfterFunc(d, t.flushFromTimer)
}

func (t *windowGeometryTracker) flushFromTimer() {
	t.mu.Lock()
	if t.closed || !t.dirty || t.seq <= t.writtenSeq || t.store == nil {
		t.mu.Unlock()
		return
	}
	seq := t.seq
	snap := windowState{
		Version:   windowStateVersion,
		Width:     t.currentWidth,
		Height:    t.currentHeight,
		Maximised: t.currentMaximised,
	}
	t.writeWg.Add(1)
	t.mu.Unlock()

	defer t.writeWg.Done()
	t.persistSnapshot(seq, snap)
}

// Flush synchronously writes any pending user window geometry change.
func (t *windowGeometryTracker) Flush() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
	}
	if !t.dirty || t.seq <= t.writtenSeq || t.store == nil {
		t.mu.Unlock()
		return
	}
	seq := t.seq
	snap := windowState{
		Version:   windowStateVersion,
		Width:     t.currentWidth,
		Height:    t.currentHeight,
		Maximised: t.currentMaximised,
	}
	t.mu.Unlock()

	t.persistSnapshot(seq, snap)
}

// Close stops background debounce timers, waits for any in-flight write, and
// flushes the final dirty window state if needed.
func (t *windowGeometryTracker) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.closed = true
	if t.timer != nil {
		t.timer.Stop()
	}
	t.mu.Unlock()

	t.writeWg.Wait()

	t.mu.Lock()
	needFinal := t.dirty && t.seq > t.writtenSeq && t.store != nil
	seq := t.seq
	snap := windowState{
		Version:   windowStateVersion,
		Width:     t.currentWidth,
		Height:    t.currentHeight,
		Maximised: t.currentMaximised,
	}
	t.mu.Unlock()

	if needFinal {
		t.persistSnapshot(seq, snap)
	}
}

func (t *windowGeometryTracker) persistSnapshot(seq uint64, snap windowState) {
	if err := t.store.SaveSeq(seq, snap); err != nil {
		if t.logger != nil {
			t.logger.Warn("failed to persist window geometry", "error", err)
		}
		return
	}
	t.mu.Lock()
	if seq > t.writtenSeq {
		t.writtenSeq = seq
	}
	t.mu.Unlock()
}
