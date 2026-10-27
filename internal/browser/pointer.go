package browser

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/chromedp/cdproto/input"
)

// defaultPointerHold releases buttons the inspector still holds when no pointer
// input arrives for this long, so a lost release (closed window, dropped
// request, crashed WebView) never leaves the page in a pressed state.
const defaultPointerHold = 10 * time.Second

// Button bits are shared by DOM MouseEvent.buttons and CDP Input.
var pointerButtons = []struct {
	name input.MouseButton
	bit  int64
}{{input.Left, 1}, {input.Right, 2}, {input.Middle, 4}}

func pointerBit(button string) int64 {
	for _, b := range pointerButtons {
		if string(b.name) == button {
			return b.bit
		}
	}
	return 0
}

// pointerState is the UI pointer as the page last saw it. Hoxi, not the UI,
// tracks held buttons, so moves report them and releases are never duplicated.
type pointerState struct {
	held      int64
	tabID     string
	x, y      float64
	modifiers input.Modifier
	clicks    int64
	seq       uint64
	timer     *time.Timer
}

// mouseEvent is one CDP Input.dispatchMouseEvent. Chrome Link receives it as
// the "pointer" action; every field is sent so the extension can validate it.
type mouseEvent struct {
	Action     string            `json:"action"`
	Type       input.MouseType   `json:"type"`
	X          float64           `json:"x"`
	Y          float64           `json:"y"`
	Button     input.MouseButton `json:"button"`
	Buttons    int64             `json:"buttons"`
	ClickCount int64             `json:"clickCount"`
	Modifiers  input.Modifier    `json:"modifiers"`
	// A release Hoxi makes on its own, not one the user performed.
	cancel bool
}

// releasesPointer lists actions that must not run while the UI holds a
// button: they replace the page or press buttons themselves.
func releasesPointer(action string) bool {
	switch action {
	case "navigate", "back", "forward", "reload", "click", "tab_new", "tab_select", "tab_close":
		return true
	}
	return false
}

func validatePointer(a Action, agent bool) error {
	if agent {
		return errors.New("only UI may send pointer input")
	}
	if a.Selector != "" || a.URL != "" || a.Text != "" || a.Key != "" || a.TabID != "" || a.DeltaX != 0 || a.DeltaY != 0 {
		return errors.New("pointer input takes only phase, x, y, button, clickCount and modifiers")
	}
	if a.Modifiers < 0 || a.Modifiers > 15 {
		return errors.New("modifiers must be a 0..15 bit field")
	}
	switch a.Phase {
	case "down", "up":
		if pointerBit(a.Button) == 0 || a.ClickCount < 1 || a.ClickCount > 3 {
			return errors.New("pointer down and up need a left, middle or right button and clickCount 1..3")
		}
	case "move":
		if a.Button != "" || a.ClickCount != 0 {
			return errors.New("pointer move takes no button or clickCount")
		}
	case "cancel":
		// Releases happen where the pointer last was.
		if a.Button != "" || a.ClickCount != 0 || a.X != 0 || a.Y != 0 || a.Modifiers != 0 {
			return errors.New("pointer cancel takes no other fields")
		}
		return nil
	default:
		return errors.New("pointer phase must be down, move, up or cancel")
	}
	for _, v := range []float64{a.X, a.Y} {
		if math.IsNaN(v) || v < 0 || v > 16384 {
			return errors.New("pointer coordinates must be 0..16384")
		}
	}
	return nil
}

// pointer applies one UI pointer input under the scope gate. The result is
// Chrome Link's reply, or nil when nothing was sent there.
func (sc *scope) pointer(ctx context.Context, a Action) (map[string]any, error) {
	p := &sc.pointerState
	if p.held != 0 && p.tabID != sc.tabID {
		// The pressed page is no longer selected; the tab switch released it.
		sc.forgetPointer()
	}
	if a.Phase == "cancel" {
		return sc.releasePointer(ctx)
	}
	mods := input.Modifier(a.Modifiers)
	ev := mouseEvent{X: a.X, Y: a.Y, Modifiers: mods}
	switch a.Phase {
	case "move":
		ev.Type, ev.Button, ev.Buttons = input.MouseMoved, input.None, p.held
		for _, b := range pointerButtons {
			if p.held&b.bit != 0 {
				ev.Button = b.name
				break
			}
		}
	case "down":
		// Record the press first: if dispatch fails midway, the release
		// timer still lifts whatever Chrome received.
		p.held |= pointerBit(a.Button)
		p.clicks = int64(a.ClickCount)
		ev.Type, ev.Button, ev.Buttons, ev.ClickCount = input.MousePressed, input.MouseButton(a.Button), p.held, p.clicks
	case "up":
		bit := pointerBit(a.Button)
		if p.held&bit == 0 {
			// Already released by a cancel, a tab switch or the hold timeout.
			return nil, nil
		}
		ev.Type, ev.Button, ev.Buttons, ev.ClickCount = input.MouseReleased, input.MouseButton(a.Button), p.held&^bit, int64(a.ClickCount)
	}
	p.tabID, p.x, p.y, p.modifiers = sc.tabID, a.X, a.Y, mods
	result, err := sc.dispatchMouse(ctx, ev)
	if err == nil && ev.Type == input.MouseReleased {
		// A failed release stays held so the timer retries it.
		p.held = ev.Buttons
	}
	sc.armPointerRelease()
	return result, err
}

// releasePointer lifts every held button where the pointer last was.
func (sc *scope) releasePointer(ctx context.Context) (map[string]any, error) {
	p := &sc.pointerState
	var result map[string]any
	for _, b := range pointerButtons {
		if p.held&b.bit == 0 {
			continue
		}
		r, err := sc.dispatchMouse(ctx, mouseEvent{Type: input.MouseReleased, X: p.x, Y: p.y, Button: b.name,
			Buttons: p.held &^ b.bit, ClickCount: max(p.clicks, 1), Modifiers: p.modifiers, cancel: true})
		if err != nil {
			sc.armPointerRelease()
			return nil, err
		}
		p.held &^= b.bit
		result = r
	}
	sc.armPointerRelease()
	return result, nil
}

// dropPointer releases held buttons before an action that replaces the page
// or presses buttons itself. The press is forgotten even if Chrome fails.
func (sc *scope) dropPointer(ctx context.Context) {
	if sc.pointerState.held != 0 && sc.pointerState.tabID == sc.tabID {
		_, _ = sc.releasePointer(ctx)
	}
	sc.forgetPointer()
}

func (sc *scope) forgetPointer() {
	sc.pointerState.held = 0
	sc.armPointerRelease()
}

// armPointerRelease restarts the hold timeout while a button is held and
// stops it otherwise. Callers hold the scope gate.
func (sc *scope) armPointerRelease() {
	p := &sc.pointerState
	p.seq++
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if p.held == 0 || sc.closed {
		return
	}
	seq, hold := p.seq, sc.pointerHold
	if hold <= 0 {
		hold = defaultPointerHold
	}
	p.timer = time.AfterFunc(hold, func() { sc.expirePointer(seq) })
}

func (sc *scope) expirePointer(seq uint64) {
	select {
	case sc.gate <- struct{}{}:
	case <-sc.ctx.Done():
		return
	}
	defer func() { <-sc.gate }()
	if sc.closed || sc.pointerState.seq != seq || sc.pointerState.held == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(sc.ctx, 5*time.Second)
	defer cancel()
	sc.dropPointer(ctx)
}

func (sc *scope) dispatchMouse(ctx context.Context, ev mouseEvent) (map[string]any, error) {
	if sc.extension != nil {
		if !sc.extension.pointer {
			return sc.legacyExtensionPointer(ctx, ev)
		}
		ev.Action = "pointer"
		return sc.extension.call(ctx, ev)
	}
	return nil, sc.withTab(ctx, input.DispatchMouseEvent(ev.Type, ev.X, ev.Y).WithButton(ev.Button).
		WithButtons(ev.Buttons).WithClickCount(ev.ClickCount).WithModifiers(ev.Modifiers))
}

// A Chrome Link extension loaded before pointer support only clicks. Keep
// plain left clicks working there; hover, drag and other buttons need an
// extension reload.
func (sc *scope) legacyExtensionPointer(ctx context.Context, ev mouseEvent) (map[string]any, error) {
	if ev.Type == input.MousePressed && ev.Button != input.Left {
		return nil, errors.New("reload Hoxi Browser Link in chrome://extensions to use right-click, middle-click, hover and drag")
	}
	if ev.Type != input.MouseReleased || ev.Button != input.Left || ev.cancel {
		return nil, nil
	}
	return sc.extension.call(ctx, Action{Action: "click", X: ev.X, Y: ev.Y})
}
