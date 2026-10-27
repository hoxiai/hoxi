package browser

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type wheelGesture struct {
	tabID                          string
	last                           time.Time
	distance, horizontal, vertical float64
	checked, blocked, navigated    bool
}

func validateGesture(a Action, agent bool) error {
	if agent {
		return errors.New("only UI may send wheel or zoom gestures")
	}
	if a.Selector != "" || a.URL != "" || a.Text != "" || a.Key != "" || a.TabID != "" {
		return errors.New("gestures take only viewport coordinates, wheel deltas or a scale factor")
	}
	for _, v := range []float64{a.X, a.Y} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 16384 {
			return errors.New("gesture coordinates must be 0..16384")
		}
	}
	if a.Action == "zoom" {
		if a.ScaleFactor == nil || math.IsNaN(*a.ScaleFactor) || math.IsInf(*a.ScaleFactor, 0) || *a.ScaleFactor < .25 || *a.ScaleFactor > 4 || a.DeltaX != 0 || a.DeltaY != 0 {
			return errors.New("zoom requires a scaleFactor between 0.25 and 4 and no wheel deltas")
		}
	} else {
		for _, v := range []float64{a.DeltaX, a.DeltaY} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e6 {
				return errors.New("wheel deltas must be finite and bounded")
			}
		}
	}
	return nil
}

func (sc *scope) zoom(ctx context.Context, a Action) (map[string]any, error) {
	if sc.extension != nil {
		if !sc.extension.zoom {
			return nil, errors.New("reload Hoxi Browser Link in chrome://extensions to use pinch zoom")
		}
		return sc.extension.call(ctx, a)
	}
	return nil, sc.withTab(ctx, input.SynthesizePinchGesture(a.X, a.Y, *a.ScaleFactor).WithGestureSourceType(input.GestureMouse))
}

// Horizontal wheel bursts navigate once, unless the page owns horizontal
// scrolling at this point. Normal vertical/diagonal scrolling is unchanged.
func (sc *scope) wheel(ctx context.Context, a Action) (map[string]any, error) {
	g := &sc.wheelGesture
	now := time.Now()
	if g.tabID != sc.tabID || now.Sub(g.last) > 300*time.Millisecond {
		*g = wheelGesture{tabID: sc.tabID}
	}
	g.last = now
	if sc.pointerState.held != 0 {
		g.blocked = true
	}
	if g.navigated {
		return nil, nil
	}
	g.distance += a.DeltaX
	g.horizontal += math.Abs(a.DeltaX)
	g.vertical += math.Abs(a.DeltaY)
	if a.DeltaX != 0 && !g.checked {
		g.checked = true
		// An uncertain page keeps its wheel events; it never triggers history.
		g.blocked = sc.ownsHorizontalWheel(ctx, a.X, a.Y)
	}
	if !g.blocked && sc.pointerState.held == 0 && math.Abs(g.distance) >= 120 && g.horizontal > g.vertical*2 {
		g.navigated = true
		action := "back"
		if g.distance > 0 {
			action = "forward"
		}
		sc.dropPointer(ctx)
		var result map[string]any
		var err error
		if sc.extension != nil {
			result, err = sc.extension.call(ctx, Action{Action: action})
		} else {
			err = sc.withTab(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				index, entries, err := page.GetNavigationHistory().Do(ctx)
				if err != nil {
					return err
				}
				next := index - 1
				if action == "forward" {
					next = index + 1
				}
				if next < 0 || next >= int64(len(entries)) {
					return nil
				}
				return page.NavigateToHistoryEntry(entries[next].ID).Do(ctx)
			}))
		}
		g.last = time.Now()
		return result, err
	}
	a.Action = "scroll"
	if sc.extension != nil {
		return sc.extension.call(ctx, a)
	}
	return nil, sc.withTab(ctx, input.DispatchMouseEvent(input.MouseWheel, a.X, a.Y).WithDeltaX(a.DeltaX).WithDeltaY(a.DeltaY))
}

func (sc *scope) ownsHorizontalWheel(ctx context.Context, x, y float64) bool {
	point, _ := json.Marshal([]float64{x, y})
	expression := `(` + horizontalWheelOwnerJS + `)(...` + string(point) + `)`
	var owns bool
	if sc.extension != nil {
		result, err := sc.extension.call(ctx, Action{Action: "evaluate", Text: expression})
		if err != nil {
			return true
		}
		raw, ok := result["result"].(string)
		if !ok || json.Unmarshal([]byte(raw), &owns) != nil {
			return true
		}
	} else if sc.withTab(ctx, chromedp.Evaluate(expression, &owns)) != nil {
		return true
	}
	return owns
}

// Respect nested scrollers, shadow DOM, overscroll containment and interactive
// surfaces whose wheel behavior cannot be inferred without running user code.
const horizontalWheelOwnerJS = `(x,y)=>{
 let e=document.elementFromPoint(x,y);
 while(e?.shadowRoot){const child=e.shadowRoot.elementFromPoint(x,y);if(!child||child===e)break;e=child}
 for(;e;e=e.parentElement||e.getRootNode()?.host){
  const s=getComputedStyle(e);
  if(e.matches('iframe,canvas,video,[role="slider"],input[type="range"]'))return true;
  if(s.overscrollBehaviorX==='contain'||s.overscrollBehaviorX==='none')return true;
  if(e.scrollWidth>e.clientWidth+1&&/^(auto|scroll)$/.test(s.overflowX))return true;
 }
 return document.documentElement.scrollWidth>innerWidth+1;
}`
