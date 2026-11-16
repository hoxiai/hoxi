package browser

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func TestChromeTabContextCleanup(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	call := func(a Action) map[string]any {
		t.Helper()
		w := request(t, s, dir, "tabs", "action", string(mustJSON(t, a)), true)
		if w.Code != 200 {
			t.Fatalf("%+v: %d %s", a, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	initial := call(Action{Action: "navigate", URL: "about:blank"})["tabID"].(string)
	key, err := requestScope(httptest.NewRequest("GET", "/?directory="+url.QueryEscape(dir)+"&sessionID=tabs", nil))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.acquire(context.Background(), key, false)
	if err != nil {
		t.Fatal(err)
	}
	<-sc.gate
	root := sc.browser
	assertCounts := func(tabs, cancels int) {
		t.Helper()
		locked, err := s.acquire(context.Background(), key, false)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { <-locked.gate }()
		if len(sc.tabs) != tabs || len(sc.tabCancels) != cancels {
			t.Fatalf("retained tabs=%d cancels=%d; want %d/%d", len(sc.tabs), len(sc.tabCancels), tabs, cancels)
		}
		if root.Err() != nil {
			t.Fatal("root context canceled", root.Err())
		}
	}
	for i := 0; i < 3; i++ {
		id := call(Action{Action: "tab_new"})["tabID"].(string)
		call(Action{Action: "snapshot"})
		assertCounts(2, 1)
		call(Action{Action: "tab_close", TabID: id})
		assertCounts(1, 0)
	}
	// Closing the initial page must not call the root browser cancel function.
	call(Action{Action: "tab_close", TabID: initial})
	call(Action{Action: "snapshot"})
	assertCounts(1, 1)
	for i := 0; i < 3; i++ {
		id := call(Action{Action: "tab_new"})["tabID"].(string)
		call(Action{Action: "snapshot"})
		locked, err := s.acquire(context.Background(), key, false)
		if err != nil {
			t.Fatal(err)
		}
		child := sc.tabs[id]
		ctx, cancel := context.WithTimeout(root, 5*time.Second)
		err = target.CloseTarget(target.ID(id)).Do(cdp.WithExecutor(ctx, chromedp.FromContext(root).Browser))
		cancel()
		<-locked.gate
		if err != nil {
			t.Fatal(err)
		}
		// State reconciliation releases contexts for targets closed outside the API.
		w := request(t, s, dir, "tabs", "state", "", true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		assertCounts(1, 1)
		if child.Err() == nil {
			t.Fatal("disappeared target context not canceled")
		}
	}
	call(Action{Action: "snapshot"})
}
