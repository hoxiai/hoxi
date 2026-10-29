package browser

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func navigationURL(raw string) bool {
	if raw == "about:blank" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func validateAction(a Action, agent bool) error {
	if a.RequestID != "" && a.Action != "upload_cancel" {
		return errors.New("requestID is only valid for a file request")
	}
	if a.Accept != nil {
		return errors.New("accept is only valid for a page dialog")
	}
	if a.Async && (agent || !navigationAction(a.Action)) {
		return errors.New("async navigation is UI only")
	}
	if a.ScaleFactor != nil && a.Action != "zoom" {
		return errors.New("scaleFactor is only valid for zoom")
	}
	if a.Action != "resize" && (a.Width != nil || a.Height != nil || a.DeviceScaleFactor != nil) {
		return errors.New("width and height are only valid for resize")
	}
	if a.Manual != nil && a.Action != "control" {
		return errors.New("manual is only valid for control")
	}
	if a.Window != nil && (agent || (a.Action != "mode" && a.Action != "navigate" && a.Action != "tab_new")) {
		return errors.New("window is only valid for UI mode, navigate or tab_new")
	}
	if a.Action != "pointer" && (a.Phase != "" || a.Button != "" || a.ClickCount != 0 || a.Modifiers != 0) {
		return errors.New("phase, button, clickCount and modifiers are only valid for pointer")
	}
	switch a.Action {
	case "mode":
		if agent || a.Window == nil {
			return errors.New("only UI may set browser mode")
		}
	case "focus":
		if agent {
			return errors.New("only UI may focus the browser window")
		}
	case "zoom_reset":
		if agent {
			return errors.New("only UI may reset page zoom")
		}
	case "upload_cancel":
		if agent || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(a.RequestID) {
			return errors.New("only UI may cancel an identified file request")
		}
	case "pair":
		if agent {
			return errors.New("only UI may pair Chrome")
		}
	case "resize":
		if agent || a.Width == nil || a.Height == nil {
			return errors.New("only UI may resize; integer width and height are required")
		}
		width, height := *a.Width, *a.Height
		scale := 1.0
		if a.DeviceScaleFactor != nil {
			scale = *a.DeviceScaleFactor
		}
		if math.IsNaN(scale) || math.IsInf(scale, 0) || scale < 1 || scale > 2 || width < 1 || width > 4096 || height < 1 || height > 4096 || math.Ceil(float64(width)*scale)*math.Ceil(float64(height)*scale) > 8388608 {
			return errors.New("viewport dimensions must be 1..4096 with at most 8388608 pixels")
		}
	case "navigate":
		if !navigationURL(a.URL) {
			return errors.New("only http, https and about:blank navigation is allowed")
		}
	case "tab_new":
		if a.URL != "" && !navigationURL(a.URL) {
			return errors.New("invalid tab URL")
		}
	case "control":
		if agent || a.Manual == nil {
			return errors.New("only UI may set manual control")
		}
	case "stop_loading":
		if agent {
			return errors.New("only UI may stop loading")
		}
	case "context", "copy", "paste":
		if agent {
			return errors.New("only UI may use clipboard actions")
		}
		if math.IsNaN(a.X) || math.IsInf(a.X, 0) || math.IsNaN(a.Y) || math.IsInf(a.Y, 0) ||
			a.X < 0 || a.X > 16384 || a.Y < 0 || a.Y > 16384 || a.Selector != "" || a.URL != "" || a.Key != "" ||
			a.DeltaX != 0 || a.DeltaY != 0 || a.TabID != "" || (a.Action != "paste" && a.Text != "") ||
			(a.Action == "copy" && (a.X != 0 || a.Y != 0)) || len(a.Text) > 262144 {
			return errors.New("invalid clipboard action")
		}
		units := 0
		for _, r := range a.Text {
			units++
			if r > 0xffff {
				units++
			}
		}
		if units > 65536 {
			return errors.New("clipboard text exceeds 65536 characters")
		}
	case "evaluate":
		if !agent || a.Text == "" {
			return errors.New("evaluate requires an agent and text expression")
		}
	case "tab_select", "tab_close":
		if a.TabID == "" {
			return errors.New("tabID required")
		}
	case "pointer":
		return validatePointer(a, agent)
	case "wheel", "zoom":
		return validateGesture(a, agent)
	case "type":
	case "key":
		_, err := keyAction(a.Key)
		return err
	case "back", "forward", "reload", "click", "scroll", "snapshot", "screenshot", "close":
	default:
		return errors.New("unknown browser action")
	}
	return nil
}

func (sc *scope) state(ctx context.Context) (State, error) {
	infos, err := chromedp.Targets(ctx)
	if err != nil {
		return State{}, err
	}
	state := emptyState()
	state.Manual = sc.manual
	if sc.window {
		state.Mode = "window"
	}
	live := make(map[string]bool)
	for _, info := range infos {
		if info.Type == "page" {
			live[string(info.TargetID)] = true
			state.Tabs = append(state.Tabs, Tab{string(info.TargetID), info.Title, info.URL})
		}
	}
	for id := range sc.tabs {
		if !live[id] {
			sc.releaseTab(id)
		}
	}
	sort.Slice(state.Tabs, func(i, j int) bool { return state.Tabs[i].ID < state.Tabs[j].ID })
	found := false
	for _, tab := range state.Tabs {
		if tab.ID == sc.tabID {
			found = true
		}
	}
	if !found {
		sc.stopStream()
		sc.tabID = ""
		if len(state.Tabs) > 0 {
			sc.tabID = state.Tabs[0].ID
		}
	}
	state.TabID = sc.tabID
	if n := sc.navigations[sc.tabID]; n != nil {
		state.Navigation = n.snapshot()
	}
	for _, tab := range state.Tabs {
		if tab.ID == sc.tabID {
			state.URL, state.Title = tab.URL, tab.Title
		}
	}
	return state, nil
}

// Tab contexts belong to the scope, never to the request that attached them.
func (sc *scope) withTab(ctx context.Context, tasks ...chromedp.Action) error {
	if sc.tabID == "" {
		return errors.New("no open tab; use tab_new")
	}
	tab := sc.tabs[sc.tabID]
	if tab == nil {
		var cancel context.CancelFunc
		tab, cancel = chromedp.NewContext(sc.browser, chromedp.WithTargetID(target.ID(sc.tabID)))
		// Initial attachment starts a target event loop using this context.
		// Keep it alive across requests, just like the initial browser target.
		done := make(chan error, 1)
		go func() {
			if sc.window {
				done <- chromedp.Run(tab)
			} else {
				done <- chromedp.Run(tab, sc.viewport().action(), page.SetInterceptFileChooserDialog(true))
			}
		}()
		select {
		case err := <-done:
			if err != nil {
				cancel()
				return err
			}
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		}
		sc.tabs[sc.tabID] = tab
		sc.trackNavigation(tab, sc.tabID)
		sc.tabCancels[sc.tabID] = cancel
		sc.viewports[sc.tabID] = sc.viewport()
	}
	op, cancel := context.WithCancel(tab)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	if !sc.window && sc.viewports[sc.tabID] != sc.viewport() {
		if err := chromedp.Run(op, sc.viewport().action()); err != nil {
			return err
		}
		sc.viewports[sc.tabID] = sc.viewport()
	}
	return chromedp.Run(op, tasks...)
}

func (sc *scope) capture(ctx context.Context) ([]byte, error) {
	var image []byte
	err := sc.withTab(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		// Without a document-coordinate clip, Chrome captures the visible viewport,
		// including its current scroll offset, at the scope's emulated size.
		image, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(80).
			WithOptimizeForSpeed(true).WithCaptureBeyondViewport(false).Do(ctx)
		return err
	}))
	return image, err
}

func (sc *scope) perform(ctx context.Context, a Action) (map[string]any, error) {
	if a.Async {
		return nil, sc.startNavigation(ctx, a)
	}
	if a.Action == "stop_loading" {
		return nil, sc.stopNavigation(ctx)
	}
	if a.Action == "context" || a.Action == "copy" || a.Action == "paste" {
		return sc.clipboard(ctx, a)
	}
	if a.Action == "upload_cancel" {
		return nil, sc.workflowAction(ctx, a)
	}
	if a.Action == "wheel" {
		return sc.wheel(ctx, a)
	}
	if a.Action == "zoom" {
		return sc.zoom(ctx, a)
	}
	if releasesPointer(a.Action) {
		sc.dropPointer(ctx)
	}
	var task chromedp.Action
	switch a.Action {
	case "focus":
		return nil, sc.focusWindow(ctx)
	case "pointer":
		_, err := sc.pointer(ctx, a)
		return nil, err
	case "resize":
		return nil, sc.withTab(ctx)
	case "zoom_reset":
		return nil, sc.withTab(ctx, emulation.SetPageScaleFactor(1))
	case "navigate":
		task = chromedp.Navigate(a.URL)
	case "back":
		task = chromedp.NavigateBack()
	case "forward":
		task = chromedp.NavigateForward()
	case "reload":
		task = chromedp.Reload()
	case "click":
		if a.Selector != "" {
			task = chromedp.Click(a.Selector, chromedp.ByQuery)
		} else {
			task = chromedp.MouseClickXY(a.X, a.Y)
		}
	case "type":
		tasks := chromedp.Tasks{}
		if a.Selector != "" {
			tasks = append(tasks, chromedp.Focus(a.Selector, chromedp.ByQuery))
		}
		task = append(tasks, input.InsertText(a.Text))
	case "key":
		var err error
		task, err = keyAction(a.Key)
		if err != nil {
			return nil, err
		}
	case "scroll":
		task = input.DispatchMouseEvent(input.MouseWheel, a.X, a.Y).WithDeltaX(a.DeltaX).WithDeltaY(a.DeltaY)
	case "screenshot":
		image, err := sc.capture(ctx)
		return map[string]any{"image": image}, err
	case "snapshot":
		var snapshot any
		err := sc.withTab(ctx, chromedp.Evaluate(snapshotJS, &snapshot))
		return map[string]any{"snapshot": snapshot}, err
	case "evaluate":
		// Bound serialization in the page before CDP transports the result.
		var result string
		expression := fmt.Sprintf(`(()=>{const v=(0,eval)(%q);const s=JSON.stringify(v);return (s===undefined?"undefined":s).slice(0,65536)})()`, a.Text)
		err := sc.withTab(ctx, chromedp.Evaluate(expression, &result))
		return map[string]any{"result": result}, err
	case "tab_new":
		u := a.URL
		if u == "" {
			u = "about:blank"
		}
		id, err := target.CreateTarget(u).Do(cdp.WithExecutor(ctx, chromedp.FromContext(sc.browser).Browser))
		if err == nil {
			sc.stopStream()
			sc.tabID = string(id)
			err = sc.withTab(ctx)
		}
		return nil, err
	case "tab_select", "tab_close":
		state, err := sc.state(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, tab := range state.Tabs {
			if tab.ID == a.TabID {
				found = true
			}
		}
		if !found {
			return nil, errors.New("tab does not belong to this browser")
		}
		if a.Action == "tab_select" {
			if sc.tabID != a.TabID {
				sc.stopStream()
			}
			sc.tabID = a.TabID
			return nil, sc.withTab(ctx, chromedp.ActionFunc(func(ctx context.Context) error { return target.ActivateTarget(target.ID(a.TabID)).Do(ctx) }))
		}
		executor := cdp.WithExecutor(ctx, chromedp.FromContext(sc.browser).Browser)
		if len(state.Tabs) == 1 {
			sc.stopStream()
			// Keep Chrome and the scope usable when its last page is closed.
			id, err := target.CreateTarget("about:blank").Do(executor)
			if err != nil {
				return nil, err
			}
			sc.tabID = string(id)
		}
		err = target.CloseTarget(target.ID(a.TabID)).Do(executor)
		if err == nil {
			sc.releaseTab(a.TabID)
			// CloseTarget acknowledges the request before target destruction finishes.
			// Wait before returning state so the closed tab cannot be selected again.
			for {
				infos, listErr := chromedp.Targets(ctx)
				if listErr != nil {
					return nil, listErr
				}
				live := false
				for _, info := range infos {
					if string(info.TargetID) == a.TabID {
						live = true
					}
				}
				if !live {
					break
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
		return nil, err
	default:
		return nil, errors.New("unsupported action")
	}
	return nil, sc.withTab(ctx, task)
}

func (sc *scope) releaseTab(id string) {
	if n := sc.navigations[id]; n != nil {
		n.stop()
		delete(sc.navigations, id)
	}
	if sc.stream != nil && sc.stream.frame.TabID == id {
		sc.stopStream()
	}
	// The initial page shares the root browser context. Dropping that page must
	// not cancel Chrome; only attached child contexts have entries in this map.
	if cancel := sc.tabCancels[id]; cancel != nil {
		cancel()
		delete(sc.tabCancels, id)
	}
	delete(sc.tabs, id)
	delete(sc.viewports, id)
	delete(sc.diagnostics, id)
	if sc.workflows != nil {
		sc.workflows.mu.Lock()
		delete(sc.workflows.uploads, id)
		delete(sc.workflows.dialogs, id)
		delete(sc.workflows.tabs, id)
		sc.workflows.mu.Unlock()
	}
}

// Light-DOM snapshot: at most 200 visible controls and 32 KiB of rendered text.
// CSS.escape and structural paths avoid assuming IDs are unique or CSS-safe.
const snapshotJS = `(()=>{
 const controls=[];
 const selector=(el)=>{
  const parts=[];
  while(el&&el.nodeType===1){
   let part=el.localName;
   if(el.id){const id='#'+CSS.escape(el.id);if(document.querySelectorAll(id).length===1){parts.unshift(id);break}}
   const parent=el.parentElement;
   if(parent){const siblings=Array.from(parent.children).filter(x=>x.localName===el.localName);if(siblings.length>1)part+=':nth-of-type('+(siblings.indexOf(el)+1)+')'}
   parts.unshift(part);el=parent;
  }
  return parts.join(' > ');
 };
 for(const el of document.querySelectorAll('a,button,input,textarea,select,[role="button"],[contenteditable="true"],[tabindex]')){
  if(controls.length>=200)break;
  if(!el.getClientRects().length||getComputedStyle(el).visibility==='hidden')continue;
  controls.push({selector:selector(el).slice(0,2048),tag:el.localName,text:(el.getAttribute('aria-label')||el.innerText||el.getAttribute('placeholder')||'').slice(0,256)});
 }
 return {text:(document.body?.innerText||'').slice(0,32768),controls};
})()`
