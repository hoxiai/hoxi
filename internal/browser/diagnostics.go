package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Diagnostic struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Level   string `json:"level"`
	Message string `json:"message"`
	URL     string `json:"url,omitempty"`
	Time    int64  `json:"time"`
}
type diagnosticTracker struct {
	mu       sync.Mutex
	next     int64
	items    []Diagnostic
	requests map[network.RequestID]string
}

func diagnosticURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	return clipped(u.String(), 1024)
}
func (d *diagnosticTracker) append(kind, level, message, url string) {
	d.next++
	d.items = append(d.items, Diagnostic{d.next, kind, level, clipped(message, 2048), diagnosticURL(url), time.Now().UnixMilli()})
	if len(d.items) > 100 {
		d.items = d.items[len(d.items)-100:]
	}
}
func (d *diagnosticTracker) receive(raw any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch event := raw.(type) {
	case *cdpruntime.EventConsoleAPICalled:
		if event.Type != cdpruntime.APITypeError && event.Type != cdpruntime.APITypeWarning {
			return
		}
		parts := []string{}
		for _, arg := range event.Args {
			if len(parts) >= 8 {
				break
			}
			var value string
			if arg.Type == cdpruntime.TypeString {
				_ = json.Unmarshal(arg.Value, &value)
			} else {
				value = arg.Description
			}
			parts = append(parts, clipped(value, 512))
		}
		d.append("console", string(event.Type), strings.Join(parts, " "), "")
	case *cdpruntime.EventExceptionThrown:
		message := event.ExceptionDetails.Text
		if event.ExceptionDetails.Exception != nil {
			message += " · " + clipped(event.ExceptionDetails.Exception.Description, 1500)
		}
		d.append("console", "error", message, event.ExceptionDetails.URL)
	case *network.EventRequestWillBeSent:
		if len(d.requests) >= 256 {
			clear(d.requests)
		}
		d.requests[event.RequestID] = diagnosticURL(event.Request.URL)
	case *network.EventResponseReceived:
		if event.Response.Status >= 400 {
			d.append("request", "error", fmt.Sprintf("HTTP %d", event.Response.Status), event.Response.URL)
		}
	case *network.EventLoadingFailed:
		if !event.Canceled {
			code := navigationErrorCode.FindString(event.ErrorText)
			if code == "" {
				code = "request_failed"
			}
			d.append("request", "error", code, d.requests[event.RequestID])
		}
		delete(d.requests, event.RequestID)
	case *network.EventLoadingFinished:
		delete(d.requests, event.RequestID)
	}
}
func (d *diagnosticTracker) snapshot(clearItems bool) []Diagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	if clearItems {
		d.items = nil
	}
	return append([]Diagnostic{}, d.items...)
}
func (sc *scope) trackDiagnostics(tab context.Context, id string) {
	if sc.diagnostics == nil {
		sc.diagnostics = make(map[string]*diagnosticTracker)
	}
	d := &diagnosticTracker{requests: make(map[network.RequestID]string)}
	sc.diagnostics[id] = d
	chromedp.ListenTarget(tab, d.receive)
}
func (s *Service) serveDiagnostics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	agent, err := s.authenticate(r)
	if err != nil || agent {
		fail(w, 403, errors.New("only UI may inspect browser diagnostics"))
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		fail(w, 405, errors.New("GET or POST required"))
		return
	}
	key, err := s.requestScope(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	sc, err := s.acquire(ctx, key, false)
	if err != nil {
		fail(w, 409, err)
		return
	}
	if sc == nil {
		writeJSON(w, 200, map[string]any{"diagnostics": []Diagnostic{}})
		return
	}
	defer func() { <-sc.gate }()
	if sc.extension != nil {
		if !sc.extension.developer {
			fail(w, 409, errors.New("reload Hoxi Browser Link to use browser diagnostics"))
			return
		}
		result, e := sc.extension.call(ctx, map[string]any{"action": "diagnostics", "clear": r.Method == "POST"})
		if e != nil {
			fail(w, 409, e)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	items := []Diagnostic{}
	if d := sc.diagnostics[sc.tabID]; d != nil {
		items = d.snapshot(r.Method == "POST")
	}
	writeJSON(w, 200, map[string]any{"diagnostics": items})
}
