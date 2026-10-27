package browser

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Navigation is observed separately from frame delivery. A connected stream
// can display an error page, and a successfully loaded page can lose its stream.
type Navigation struct {
	Status     string `json:"status"`
	URL        string `json:"url,omitempty"`
	Error      string `json:"error,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	StartedAt  int64  `json:"startedAt,omitempty"`
}

type navigationTracker struct {
	mu               sync.Mutex
	state            Navigation
	frame            cdp.FrameID
	request          network.RequestID
	documentFinished bool
	generation       uint64
	cancel           context.CancelFunc
}

func navigationAction(action string) bool {
	return action == "navigate" || action == "back" || action == "forward" || action == "reload"
}

var navigationErrorCode = regexp.MustCompile(`\bnet::ERR_[A-Z0-9_]{1,80}\b`)

func (n *navigationTracker) snapshot() *Navigation {
	n.mu.Lock()
	defer n.mu.Unlock()
	copy := n.state
	return &copy
}

func (n *navigationTracker) begin(url string, cancel context.CancelFunc) uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cancel != nil {
		n.cancel()
	}
	n.cancel = cancel
	n.generation++
	n.state = Navigation{Status: "loading", URL: url, StartedAt: time.Now().UnixMilli()}
	n.request, n.documentFinished = "", false
	return n.generation
}

func navigationError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "navigation_timeout"
	}
	if err == nil {
		return ""
	}
	// CDP errors can contain URLs or page-controlled text. Expose only the
	// Chromium network error code or a fixed error label.
	if code := navigationErrorCode.FindString(err.Error()); code != "" {
		return code
	}
	return "navigation_failed"
}

func (n *navigationTracker) finish(generation uint64, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.generation != generation || n.state.Status == "stopped" {
		return
	}
	if err != nil {
		n.state.Status, n.state.Error = "failed", navigationError(err)
	} else if n.state.Status != "failed" {
		n.state.Status = "ready"
		if n.state.HTTPStatus >= 400 {
			n.state.Status = "failed"
		}
	}
}

func (n *navigationTracker) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cancel != nil {
		n.cancel()
		n.cancel = nil
	}
	n.generation++
	n.state.Status, n.state.Error = "stopped", ""
}

func (n *navigationTracker) receive(raw any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch event := raw.(type) {
	case *page.EventFrameStartedNavigating:
		if event.FrameID != n.frame {
			return
		}
		if n.state.Status != "loading" {
			n.generation++
			n.state = Navigation{Status: "loading", StartedAt: time.Now().UnixMilli()}
		}
		n.state.URL = event.URL
		n.request, n.documentFinished = "", false
	case *page.EventFrameNavigated:
		if event.Frame.ParentID == "" {
			n.frame = event.Frame.ID
			if !strings.HasPrefix(event.Frame.URL, "chrome-error:") {
				n.state.URL = event.Frame.URL
			}
		}
	case *network.EventRequestWillBeSent:
		if event.FrameID == n.frame && event.Type == network.ResourceTypeDocument {
			n.request = event.RequestID
			n.state.URL = event.Request.URL
		}
	case *network.EventResponseReceived:
		if event.RequestID == n.request && event.Type == network.ResourceTypeDocument {
			n.state.HTTPStatus = int(event.Response.Status)
		}
	case *network.EventLoadingFailed:
		if event.RequestID != n.request || n.state.Status == "stopped" {
			return
		}
		n.state.Status = "failed"
		n.state.Error = navigationError(errors.New(event.ErrorText))
	case *network.EventLoadingFinished:
		if event.RequestID == n.request {
			n.documentFinished = true
		}
	case *page.EventLoadEventFired:
		if n.documentFinished && n.state.Status == "loading" {
			n.state.Status = "ready"
			if n.state.HTTPStatus >= 400 {
				n.state.Status = "failed"
			}
		}
	}
}

// Called only under the scope gate; callbacks touch their own tracker mutex.
func (sc *scope) trackNavigation(tab context.Context, id string) {
	if sc.navigations == nil {
		sc.navigations = make(map[string]*navigationTracker)
	}
	if sc.navigations[id] != nil {
		return
	}
	n := &navigationTracker{frame: cdp.FrameID(id), state: Navigation{Status: "ready"}}
	sc.navigations[id] = n
	chromedp.ListenTarget(tab, n.receive)
}

// Only explicit UI async navigation uses a background job. Agent tools and
// legacy callers keep their existing wait-until-loaded response contract.
func (sc *scope) startNavigation(ctx context.Context, a Action) error {
	if err := sc.withTab(ctx); err != nil {
		return err
	}
	sc.dropPointer(ctx)
	tab := sc.tabs[sc.tabID]
	n := sc.navigations[sc.tabID]
	op, cancel := context.WithTimeout(tab, 30*time.Second)
	url := a.URL
	if url == "" {
		url = n.snapshot().URL
	}
	generation := n.begin(url, cancel)
	var task chromedp.Action
	switch a.Action {
	case "navigate":
		task = chromedp.Navigate(a.URL)
	case "back":
		task = chromedp.NavigateBack()
	case "forward":
		task = chromedp.NavigateForward()
	case "reload":
		task = chromedp.Reload()
	}
	go func() {
		defer cancel()
		n.finish(generation, chromedp.Run(op, task))
	}()
	return nil
}

func (sc *scope) stopNavigation(ctx context.Context) error {
	if n := sc.navigations[sc.tabID]; n != nil {
		n.stop()
	}
	return sc.withTab(ctx, page.StopLoading())
}
