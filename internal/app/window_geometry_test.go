package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestComputeAdaptiveWindowSize(t *testing.T) {
	w, h, minW, minH := computeAdaptiveWindowSize(screenWorkArea{})
	if w != defaultWindowWidth || h != defaultWindowHeight || minW != defaultWindowMinWidth || minH != defaultWindowMinHeight {
		t.Fatalf("fallback size = %dx%d (min %dx%d), want %dx%d (min %dx%d)",
			w, h, minW, minH, defaultWindowWidth, defaultWindowHeight, defaultWindowMinWidth, defaultWindowMinHeight)
	}

	// Sub-minimum screen must shrink min constraints rather than overflowing.
	tinyW, tinyH, tinyMinW, tinyMinH := computeAdaptiveWindowSize(screenWorkArea{Width: 720, Height: 480})
	if tinyW != 720 || tinyH != 480 || tinyMinW != 720 || tinyMinH != 480 {
		t.Fatalf("tiny screen = %dx%d (min %dx%d), want 720x480", tinyW, tinyH, tinyMinW, tinyMinH)
	}

	screens := []struct {
		name   string
		area   screenWorkArea
		capped bool
	}{
		{name: "win-1080p-150pct", area: screenWorkArea{Width: 1280, Height: 672}},
		{name: "macbook-air-13", area: screenWorkArea{Width: 1440, Height: 805}},
		{name: "macbook-pro-14", area: screenWorkArea{Width: 1512, Height: 875}},
		{name: "macbook-pro-16", area: screenWorkArea{Width: 1728, Height: 1010}},
		{name: "desktop-1080p", area: screenWorkArea{Width: 1920, Height: 1032}},
		{name: "studio-display-2k", area: screenWorkArea{Width: 2560, Height: 1340}, capped: true},
		{name: "ultrawide-34", area: screenWorkArea{Width: 3440, Height: 1360}, capped: true},
	}

	prevW, prevH := 0, 0
	for _, tc := range screens {
		gotW, gotH, gotMinW, gotMinH := computeAdaptiveWindowSize(tc.area)
		if gotMinW != defaultWindowMinWidth || gotMinH != defaultWindowMinHeight {
			t.Fatalf("%s: unexpected min %dx%d", tc.name, gotMinW, gotMinH)
		}
		if gotW > tc.area.Width-screenEdgeMarginX || gotH > tc.area.Height-screenEdgeMarginY {
			t.Fatalf("%s: window %dx%d overflows usable work area %dx%d with margin",
				tc.name, gotW, gotH, tc.area.Width, tc.area.Height)
		}
		if gotW < gotMinW || gotH < gotMinH {
			t.Fatalf("%s: window %dx%d below min %dx%d", tc.name, gotW, gotH, gotMinW, gotMinH)
		}
		if gotW < prevW || gotH < prevH {
			t.Fatalf("%s: adaptive size %dx%d decreased from previous smaller screen %dx%d",
				tc.name, gotW, gotH, prevW, prevH)
		}
		if tc.capped && (gotW != adaptiveMaxWindowWidth || gotH != adaptiveMaxWindowHeight) {
			t.Fatalf("%s: expected cap %dx%d, got %dx%d",
				tc.name, adaptiveMaxWindowWidth, adaptiveMaxWindowHeight, gotW, gotH)
		}
		prevW, prevH = gotW, gotH
	}
}

func TestResolveWindowLaunchSpec(t *testing.T) {
	// Custom size above default 1680x1040 cap is respected when current display fits it.
	savedLarge := &windowState{Version: 1, Width: 2040, Height: 1180, Maximised: true}
	on2K := resolveWindowLaunchSpec(screenWorkArea{Width: 2560, Height: 1340}, savedLarge, "darwin")
	if on2K.Width != 2040 || on2K.Height != 1180 || on2K.StartState != options.Normal {
		t.Fatalf("darwin 2K spec = %+v, want 2040x1180 Normal", on2K)
	}

	// Windows honors Maximised while preserving normal restore dimensions.
	onWin := resolveWindowLaunchSpec(screenWorkArea{Width: 1920, Height: 1032}, &windowState{
		Version:   1,
		Width:     1480,
		Height:    880,
		Maximised: true,
	}, "windows")
	if onWin.Width != 1480 || onWin.Height != 880 || onWin.StartState != options.Maximised {
		t.Fatalf("windows maximised spec = %+v, want 1480x880 Maximised", onWin)
	}

	// Unplugging external monitor clamps saved 2040x1180 down to laptop usable work area.
	onLaptop := resolveWindowLaunchSpec(screenWorkArea{Width: 1512, Height: 875}, savedLarge, "darwin")
	if onLaptop.Width != 1512 || onLaptop.Height != 875 {
		t.Fatalf("clamped laptop spec = %+v, want 1512x875", onLaptop)
	}

	// Invalid saved dimensions fall back to adaptive default.
	invalid := resolveWindowLaunchSpec(screenWorkArea{Width: 1512, Height: 875}, &windowState{Width: 0, Height: -5}, "darwin")
	wantW, wantH, _, _ := computeAdaptiveWindowSize(screenWorkArea{Width: 1512, Height: 875})
	if invalid.Width != wantW || invalid.Height != wantH {
		t.Fatalf("invalid saved fallback = %dx%d, want %dx%d", invalid.Width, invalid.Height, wantW, wantH)
	}

	// Unknown screen still caps absurd saved values to sanity bounds.
	absurd := resolveWindowLaunchSpec(screenWorkArea{}, &windowState{Width: 999999, Height: 999999}, "linux")
	if absurd.Width != sanityMaxWindowWidth || absurd.Height != sanityMaxWindowHeight {
		t.Fatalf("sanity clamp = %dx%d, want %dx%d", absurd.Width, absurd.Height, sanityMaxWindowWidth, sanityMaxWindowHeight)
	}
}

func TestWindowStateStoreAtomicAndCorruptBackup(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	path := filepath.Join(dir, "window-state.json")
	store := newWindowStateStore(path, logger)

	// Missing file is a clean nil state.
	got, err := store.Load()
	if err != nil || got != nil {
		t.Fatalf("Load missing = (%+v, %v), want (nil, nil)", got, err)
	}

	// Atomic write + 0600 permissions + sequence ordering.
	if err := store.SaveSeq(2, windowState{Version: 1, Width: 1520, Height: 910, Maximised: true}); err != nil {
		t.Fatalf("SaveSeq(2) failed: %v", err)
	}
	// Stale sequence must be ignored.
	if err := store.SaveSeq(1, windowState{Version: 1, Width: 900, Height: 600}); err != nil {
		t.Fatalf("SaveSeq(1) failed: %v", err)
	}
	loaded, err := store.Load()
	if err != nil || loaded == nil || loaded.Width != 1520 || loaded.Height != 910 || !loaded.Maximised {
		t.Fatalf("Load after stale SaveSeq = (%+v, %v), want 1520x910 maximised", loaded, err)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %v (err=%v), want 0600", info.Mode().Perm(), statErr)
		}
	}

	// Newer schema version with valid dimensions must not be treated as corrupt.
	if err := os.WriteFile(path, []byte(`{"version":9,"width":1600,"height":960}`), 0o600); err != nil {
		t.Fatal(err)
	}
	newer, err := store.Load()
	if err != nil || newer == nil || newer.Width != 1600 || newer.Height != 960 {
		t.Fatalf("future version Load = (%+v, %v), want 1600x960", newer, err)
	}

	// Corrupt JSON must be moved aside to .corrupt-* before returning an error.
	corruptBytes := []byte(`{"version":1,"width":broken`)
	if err := os.WriteFile(path, corruptBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if bad, loadErr := store.Load(); loadErr == nil || bad != nil {
		t.Fatalf("expected error on corrupt JSON, got (%+v, %v)", bad, loadErr)
	}
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("expected 1 corrupt backup, got %v", backups)
	}
	if raw, readErr := os.ReadFile(backups[0]); readErr != nil || string(raw) != string(corruptBytes) {
		t.Fatalf("backup content = %q (err=%v), want %q", string(raw), readErr, string(corruptBytes))
	}

	// When backup rename fails, SaveSeq must refuse to clobber the corrupt file.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		roDir := filepath.Join(t.TempDir(), "ro")
		if err := os.MkdirAll(roDir, 0o700); err != nil {
			t.Fatal(err)
		}
		roPath := filepath.Join(roDir, "window-state.json")
		if err := os.WriteFile(roPath, corruptBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(roDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })

		roStore := newWindowStateStore(roPath, logger)
		if _, err := roStore.Load(); err == nil {
			t.Fatal("expected corrupt Load error when backup fails")
		}
		_ = os.Chmod(roDir, 0o700)
		if err := roStore.SaveSeq(1, windowState{Version: 1, Width: 1400, Height: 860}); err == nil {
			t.Fatal("expected SaveSeq to stay blocked after failed corruption backup")
		}
		if raw, _ := os.ReadFile(roPath); string(raw) != string(corruptBytes) {
			t.Fatalf("corrupt file was overwritten: got %q", string(raw))
		}
	}
}

func TestWindowGeometryTrackerPreservesAdaptiveAndExternalDisplaySizes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "window-state.json")
	store := newWindowStateStore(path, logger)

	// 1. Fresh launch with no user resize does not write window-state.json.
	spec := resolveWindowLaunchSpec(screenWorkArea{Width: 1512, Height: 875}, nil, "darwin")
	tracker := newWindowGeometryTracker(store, spec, logger)
	tracker.debounce = 15 * time.Millisecond
	tracker.CalibrateInitialFrame(spec.Width, spec.Height+14)
	// Initial creation resize event with native +14pt toolbar chrome must be ignored.
	tracker.RecordNativeFrameSize(spec.Width, spec.Height+14)
	tracker.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unmodified window session must not create window-state.json, stat err=%v", err)
	}

	// 2. User resizes on external 27" monitor -> persisted with chrome offset subtracted.
	extSpec := resolveWindowLaunchSpec(screenWorkArea{Width: 2560, Height: 1340}, nil, "darwin")
	extTracker := newWindowGeometryTracker(store, extSpec, logger)
	extTracker.debounce = 15 * time.Millisecond
	extTracker.CalibrateInitialFrame(extSpec.Width, extSpec.Height+14)
	extTracker.RecordNativeFrameSize(1980, 1160+14)
	extTracker.Close()

	saved, err := store.Load()
	if err != nil || saved == nil || saved.Width != 1980 || saved.Height != 1160 {
		t.Fatalf("saved after external resize = (%+v, %v), want 1980x1160", saved, err)
	}

	// 3. User opens on 14" laptop without resizing -> clamped for session, 1980x1160 preserved on disk!
	laptopSpec := resolveWindowLaunchSpec(screenWorkArea{Width: 1512, Height: 875}, saved, "darwin")
	if laptopSpec.Width != 1512 || laptopSpec.Height != 875 {
		t.Fatalf("laptop clamped spec = %+v, want 1512x875", laptopSpec)
	}
	laptopTracker := newWindowGeometryTracker(store, laptopSpec, logger)
	laptopTracker.RecordNormalSize(laptopSpec.Width, laptopSpec.Height)
	laptopTracker.Close()

	stillSaved, err := store.Load()
	if err != nil || stillSaved == nil || stillSaved.Width != 1980 || stillSaved.Height != 1160 {
		t.Fatalf("expected external monitor preference 1980x1160 to remain intact, got (%+v, %v)", stillSaved, err)
	}
}

func TestWindowGeometryTrackerConcurrentRace(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "window-state.json")
	store := newWindowStateStore(path, logger)
	spec := windowLaunchSpec{Width: 1360, Height: 860, MinWidth: 880, MinHeight: 560, StartState: options.Normal}
	tracker := newWindowGeometryTracker(store, spec, logger)
	tracker.debounce = 5 * time.Millisecond

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				tracker.RecordNormalSize(1400+offset+i, 880+i)
				if i%7 == 0 {
					tracker.Flush()
				}
				time.Sleep(time.Millisecond)
			}
		}(g * 10)
	}
	wg.Wait()
	tracker.RecordWindowSnapshot(1620, 980, true, false)
	tracker.Close()

	got, err := store.Load()
	if err != nil || got == nil || got.Width != 1620 || got.Height != 980 {
		t.Fatalf("final persisted state = (%+v, %v), want 1620x980", got, err)
	}
}

func TestDesktopAppWindowHooksSafeInNonWailsTests(t *testing.T) {
	// Calling primaryScreenWorkArea from a test worker goroutine must never deadlock.
	_ = primaryScreenWorkArea()

	dapp := &DesktopApp{}
	if prevent := dapp.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose must not prevent shutdown")
	}
	dapp.persistWindowGeometry()
	dapp.closeWindow()
}

func TestWindowChromePlatformOptions(t *testing.T) {
	if !usesFramelessWindow("windows") {
		t.Fatal("expected Windows to use frameless integrated window chrome")
	}
	if usesFramelessWindow("darwin") || usesFramelessWindow("linux") {
		t.Fatal("expected non-Windows platforms to keep native window framing")
	}
	if !hideWindowOnClose("windows") || !hideWindowOnClose("darwin") || hideWindowOnClose("linux") {
		t.Fatal("unexpected hideWindowOnClose platform matrix")
	}
	winOpts := buildWindowsAppOptions()
	if winOpts == nil {
		t.Fatal("expected non-nil Windows options")
	}
	if winOpts.DisableFramelessWindowDecorations {
		t.Fatal("expected frameless window decorations (Win11 rounded corners & Aero shadow) to remain enabled")
	}

	// closeWindow must flush dirty window geometry before returning even in tests.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	statePath := filepath.Join(t.TempDir(), "window-state.json")
	store := newWindowStateStore(statePath, logger)
	spec := windowLaunchSpec{Width: 1360, Height: 860, MinWidth: 880, MinHeight: 560, StartState: options.Normal}
	tracker := newWindowGeometryTracker(store, spec, logger)
	tracker.debounce = time.Hour
	tracker.RecordNormalSize(1540, 920)

	dapp := &DesktopApp{windowTracker: tracker}
	dapp.closeWindow()
	tracker.Close()

	saved, err := store.Load()
	if err != nil || saved == nil || saved.Width != 1540 || saved.Height != 920 {
		t.Fatalf("closeWindow persisted geometry = (%+v, %v), want 1540x920", saved, err)
	}
}
