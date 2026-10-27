package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
)

func TestNavigationObservationFencesStoppedAndReplacedJobs(t *testing.T) {
	n := &navigationTracker{frame: "main"}
	canceled := false
	old := n.begin("https://one.example", func() { canceled = true })
	current := n.begin("https://two.example", nil)
	if !canceled {
		t.Fatal("previous job was not canceled")
	}
	n.finish(old, errors.New("old failure"))
	if n.snapshot().Status != "loading" {
		t.Fatal("stale job replaced new navigation")
	}
	n.receive(&network.EventRequestWillBeSent{FrameID: "child", Type: network.ResourceTypeDocument, RequestID: "child", Request: &network.Request{URL: "https://child.example"}})
	n.receive(&network.EventLoadingFailed{RequestID: "child", ErrorText: "net::ERR_FAILED"})
	if n.snapshot().Status != "loading" {
		t.Fatal("iframe changed main navigation")
	}
	n.receive(&network.EventRequestWillBeSent{FrameID: "main", Type: network.ResourceTypeDocument, RequestID: "doc", Request: &network.Request{URL: "https://two.example"}})
	n.receive(&network.EventResponseReceived{Type: network.ResourceTypeDocument, RequestID: "doc", Response: &network.Response{Status: 404}})
	n.receive(&network.EventLoadingFinished{RequestID: "doc"})
	n.receive(&page.EventLoadEventFired{})
	if got := n.snapshot(); got.Status != "failed" || got.HTTPStatus != 404 {
		t.Fatal(got)
	}
	n.stop()
	n.finish(current, context.Canceled)
	n.receive(&network.EventLoadingFailed{RequestID: "doc", ErrorText: "net::ERR_ABORTED"})
	if n.snapshot().Status != "stopped" {
		t.Fatal("stopped navigation was overwritten")
	}
	if navigationError(errors.New("https://secret.example/credential arbitrary content")) != "navigation_failed" {
		t.Fatal("untrusted error echoed")
	}
	if navigationError(errors.New("page load error net::ERR_NAME_NOT_RESOLVED")) != "net::ERR_NAME_NOT_RESOLVED" {
		t.Fatal("network code lost")
	}
}

func TestAsyncNavigationAndStopValidation(t *testing.T) {
	for _, action := range []string{"navigate", "back", "forward", "reload"} {
		a := Action{Action: action, Async: true, URL: "https://example.com"}
		if err := validateAction(a, false); err != nil {
			t.Fatal(err)
		}
		if validateAction(a, true) == nil {
			t.Fatal("agent accepted async navigation")
		}
	}
	if validateAction(Action{Action: "stop_loading"}, true) == nil {
		t.Fatal("agent stop accepted")
	}
	if validateAction(Action{Action: "control", Async: true}, false) == nil {
		t.Fatal("async control accepted")
	}
	s := New()
	defer s.Close()
	dir := t.TempDir()
	if w := request(t, s, dir, "empty", "action", `{"action":"stop_loading"}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.scopes) != 0 {
		t.Fatal("stop launched a browser")
	}
}

func TestChromeAsyncNavigationIsObservableAndCanStop(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case <-release:
			}
		}
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
		}
		_, _ = w.Write([]byte(`<title>Verification page</title><p>Please complete the site verification</p>`))
	}))
	defer server.Close()
	defer close(release)
	call := func(a Action) State {
		t.Helper()
		w := request(t, s, dir, "load", "action", string(mustJSON(t, a)), false)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var st State
		if json.Unmarshal(w.Body.Bytes(), &st) != nil {
			t.Fatal(w.Body.String())
		}
		return st
	}
	call(Action{Action: "navigate", URL: "about:blank"})
	begin := time.Now()
	st := call(Action{Action: "navigate", URL: server.URL + "/slow", Async: true})
	if time.Since(begin) > 2*time.Second || st.Navigation == nil || st.Navigation.Status != "loading" {
		t.Fatal("navigation did not return promptly", st.Navigation)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("navigation never started")
	}
	w := request(t, s, dir, "load", "state", "", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	begin = time.Now()
	st = call(Action{Action: "stop_loading"})
	if time.Since(begin) > 2*time.Second || st.Navigation.Status != "stopped" {
		t.Fatal("stop waited on navigation", st.Navigation)
	}
	call(Action{Action: "navigate", URL: server.URL + "/missing", Async: true})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w = request(t, s, dir, "load", "state", "", false)
		_ = json.Unmarshal(w.Body.Bytes(), &st)
		if st.Navigation != nil && st.Navigation.Status == "failed" && st.Navigation.HTTPStatus == 404 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Navigation == nil || st.Navigation.Status != "failed" || st.Navigation.HTTPStatus != 404 {
		t.Fatal("HTTP failure not observed", st.Navigation)
	}
	call(Action{Action: "navigate", URL: server.URL + "/verification", Async: true})
	for deadline = time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		w = request(t, s, dir, "load", "state", "", false)
		_ = json.Unmarshal(w.Body.Bytes(), &st)
		if st.Navigation.Status == "ready" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Navigation.Status != "ready" || st.Title != "Verification page" {
		t.Fatal("verification document not shown as loaded", st)
	}
	if frame := request(t, s, dir, "load", "frame", "", false); frame.Code != 200 || !strings.HasPrefix(frame.Header().Get("Content-Type"), "image/") {
		t.Fatal("verification frame unavailable")
	}
}

func TestNavigationMainFrameEventUpdatesURL(t *testing.T) {
	n := &navigationTracker{frame: "main"}
	n.receive(&page.EventFrameNavigated{Frame: &cdp.Frame{ID: "new-main", URL: "https://redirect.example"}})
	if n.snapshot().URL != "https://redirect.example" {
		t.Fatal(n.snapshot())
	}
}

func TestChromeLinkNavigationCapability(t *testing.T) {
	for _, features := range [][]string{nil, {"navigation"}} {
		t.Run(strings.Join(features, "-"), func(t *testing.T) {
			s := New()
			defer s.Close()
			dir := t.TempDir()
			commands := fakeExtension(t, s, dir, "navigation", features)
			w := request(t, s, dir, "navigation", "action", `{"action":"navigate","url":"https://example.com","async":true}`, false)
			if len(features) == 0 {
				if w.Code != 409 || !strings.Contains(w.Body.String(), "reload Hoxi Browser Link") {
					t.Fatal(w.Code, w.Body.String())
				}
				noCommand(t, commands, 10*time.Millisecond)
			} else {
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				if got := nextCommand(t, commands); got["action"] != "navigate" || got["async"] != true {
					t.Fatal(got)
				}
			}
		})
	}
}
