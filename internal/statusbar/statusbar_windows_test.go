//go:build windows

package statusbar

import (
	"testing"
	"time"
)

func TestWindowsTrayTooltipAndIconRendering(t *testing.T) {
	if got := formatTrayTooltip(Summary{Available: false}); got != "Hoxi: OpenCode 引擎正在启动" {
		t.Fatalf("unexpected offline tooltip: %q", got)
	}
	if got := formatTrayTooltip(Summary{Available: true, Attention: 2, Running: 3}); got != "Hoxi：待审批 2 · 运行中 3" {
		t.Fatalf("unexpected online tooltip: %q", got)
	}

	pixels := make([]uint32, 32*32)
	renderTrayIconPixels(pixels, 32, 1, 2, true)
	nonZero := 0
	hasBadge := false
	for _, px := range pixels {
		if px != 0 {
			nonZero++
		}
		if px == 0xFFF59E0B {
			hasBadge = true
		}
	}
	if nonZero == 0 || !hasBadge {
		t.Fatalf("expected rendered tray icon pixels and attention badge: nonZero=%d hasBadge=%v", nonZero, hasBadge)
	}

	hIcon := createTrayIcon(1, 0, true)
	if hIcon != 0 {
		_, _, _ = procDestroyIcon.Call(hIcon)
	}
}

func TestWindowsTrayMenuCommandDispatch(t *testing.T) {
	ch := make(chan MenuAction, 4)
	SetActionHandler(func(action MenuAction) {
		ch <- action
	})
	defer SetActionHandler(nil)

	pending := []SessionTarget{{SessionID: "ses_att_1", Title: "Approve deploy"}}
	running := []SessionTarget{{SessionID: "ses_run_1", Title: "Run tests"}}

	dispatchTrayMenuCommand(cmdPendingBase, pending, running)
	select {
	case got := <-ch:
		if got.Kind != MenuActionAttention || got.SessionID != "ses_att_1" {
			t.Fatalf("unexpected pending dispatch: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pending menu action")
	}

	dispatchTrayMenuCommand(cmdRunningBase, pending, running)
	select {
	case got := <-ch:
		if got.Kind != MenuActionRunning || got.SessionID != "ses_run_1" {
			t.Fatalf("unexpected running dispatch: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for running menu action")
	}

	dispatchTrayMenuCommand(cmdKeepAwake, pending, running)
	select {
	case got := <-ch:
		if got.Kind != MenuActionToggleKeepAwake {
			t.Fatalf("unexpected keep-awake dispatch: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for keep-awake menu action")
	}
}
