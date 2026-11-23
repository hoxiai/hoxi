package browser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreBrowserValidationAndIdleNoColdStart(t *testing.T) {
	svc := New()
	defer svc.Close()

	dir := t.TempDir()

	// Reject non-workstore session IDs.
	if _, _, err := svc.StoreState(context.Background(), "ses_123", dir); err == nil {
		t.Fatal("expected StoreState to reject non-workstore sessionID")
	}
	if _, err := svc.StoreOpen(context.Background(), "ses_123", dir, "https://offer.1688.com/offer/manage.htm", StoreOpenOptions{}); err == nil {
		t.Fatal("expected StoreOpen to reject non-workstore sessionID")
	}

	// Reject non-https or credentialed URLs.
	for _, bad := range []string{
		"http://offer.1688.com/offer/manage.htm",
		"javascript:alert(1)",
		"file:///etc/passwd",
		"https://user:pass@offer.1688.com/offer/manage.htm",
		"about:blank",
	} {
		if _, err := svc.StoreOpen(context.Background(), "workstore_default", dir, bad, StoreOpenOptions{}); err == nil {
			t.Fatalf("expected StoreOpen to reject %q", bad)
		}
	}

	// Empty URL with navigate=true or without tabId is rejected; focus-only on idle scope never launches Chrome.
	if _, err := svc.StoreOpen(context.Background(), "workstore_default", dir, "", StoreOpenOptions{Navigate: true}); err == nil {
		t.Fatal("expected StoreOpen to reject empty URL with Navigate=true")
	}
	if _, err := svc.StoreOpen(context.Background(), "workstore_default", dir, "", StoreOpenOptions{TabID: "t1", Focus: true, Navigate: false}); err == nil {
		t.Fatal("expected focus-only StoreOpen on idle scope to fail without cold-starting Chrome")
	}
	svc.mu.Lock()
	scopeCount := len(svc.scopes)
	svc.mu.Unlock()
	if scopeCount != 0 {
		t.Fatalf("focus-only StoreOpen must not create scope, got %d", scopeCount)
	}

	// StoreState must NOT cold-start Chrome or allocate a scope.
	st, running, err := svc.StoreState(context.Background(), "workstore_default", dir)
	if err != nil {
		t.Fatalf("StoreState failed: %v", err)
	}
	if running || len(st.Tabs) != 0 {
		t.Fatalf("expected idle not-running state, got running=%v tabs=%d", running, len(st.Tabs))
	}
	svc.mu.Lock()
	scopeCount = len(svc.scopes)
	svc.mu.Unlock()
	if scopeCount != 0 {
		t.Fatalf("StoreState must not create scope, got %d", scopeCount)
	}

	// StoreEvaluate on idle scope returns Blocked="not_running" without launching Chrome.
	res, err := svc.StoreEvaluate(context.Background(), "workstore_default", dir, "1+1", StoreEvaluateOptions{}, nil)
	if err != nil {
		t.Fatalf("StoreEvaluate idle returned error: %v", err)
	}
	if res.Blocked != "not_running" {
		t.Fatalf("expected Blocked=not_running, got %q", res.Blocked)
	}

	// Empty or oversized expressions are rejected.
	if _, err := svc.StoreEvaluate(context.Background(), "workstore_default", dir, "   ", StoreEvaluateOptions{}, nil); err == nil {
		t.Fatal("expected empty expression error")
	}
	huge := strings.Repeat("a", maxStoreEvaluateExprBytes+1)
	if _, err := svc.StoreEvaluate(context.Background(), "workstore_default", dir, huge, StoreEvaluateOptions{}, nil); err == nil {
		t.Fatal("expected oversized expression error")
	}
}

func TestPickReusableStoreTabPrefersExactThenSameHostThenBlank(t *testing.T) {
	target, _ := url.Parse("https://offer.1688.com/offer/manage.htm")

	stExact := State{
		TabID: "t1",
		Tabs: []Tab{
			{ID: "t1", URL: "https://example.com/"},
			{ID: "t2", URL: "https://offer.1688.com/offer/manage.htm?status=online"},
		},
	}
	id, nav := pickReusableStoreTab(stExact, target, false)
	if id != "t2" || nav {
		t.Fatalf("expected exact tab t2 without nav when forceNavigate=false, got id=%q nav=%v", id, nav)
	}
	id, nav = pickReusableStoreTab(stExact, target, true)
	if id != "t2" || !nav {
		t.Fatalf("expected exact tab t2 with nav=true when forceNavigate=true, got id=%q nav=%v", id, nav)
	}

	stSameHost := State{
		TabID: "t1",
		Tabs: []Tab{
			{ID: "t1", URL: "https://example.com/"},
			{ID: "t2", URL: "https://offer.1688.com/offer/post"},
		},
	}
	id, nav = pickReusableStoreTab(stSameHost, target, true)
	if id != "t2" || !nav {
		t.Fatalf("expected same-host tab t2 with nav=true, got id=%q nav=%v", id, nav)
	}

	stBlank := State{
		TabID: "t1",
		Tabs: []Tab{
			{ID: "t1", URL: "about:blank"},
			{ID: "t2", URL: "https://work.1688.com/"},
		},
	}
	id, nav = pickReusableStoreTab(stBlank, target, true)
	if id != "t1" || !nav {
		t.Fatalf("expected blank tab t1 with nav=true, got id=%q nav=%v", id, nav)
	}

	stOtherOnly := State{
		TabID: "t1",
		Tabs: []Tab{
			{ID: "t1", URL: "https://work.1688.com/"},
		},
	}
	id, nav = pickReusableStoreTab(stOtherOnly, target, true)
	if id != "" || !nav {
		t.Fatalf("expected new tab (id=\"\") to avoid clobbering work.1688.com, got id=%q nav=%v", id, nav)
	}
}

func TestStoreNavRecoverableErrAndTimeout(t *testing.T) {
	if !isStoreNavRecoverableErr(context.DeadlineExceeded) {
		t.Fatal("expected DeadlineExceeded to be recoverable")
	}
	if !isStoreNavRecoverableErr(errors.New("page load error net::ERR_ABORTED")) {
		t.Fatal("expected ERR_ABORTED to be recoverable")
	}
	if isStoreNavRecoverableErr(errors.New("page load error net::ERR_NAME_NOT_RESOLVED")) {
		t.Fatal("expected ERR_NAME_NOT_RESOLVED to remain non-recoverable")
	}
	shortCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if d := computeStoreNavTimeout(shortCtx); d > 4*time.Second || d < storeOpenMinNavWait {
		t.Fatalf("unexpected bounded nav timeout: %v", d)
	}
}

func TestStoreBrowserChromeSmoke(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>StoreSmoke</title></head><body><div id="ok">ready-1688</div></body></html>`))
	}))
	defer srv.Close()

	svc := New()
	defer svc.Close()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Start a headless store scope and navigate to local test server, then verify
	// StoreState, StoreOpen (focus-only), and StoreEvaluate all succeed with plain
	// non-chromedp request contexts (regression for chromedp.ErrInvalidContext).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	key, err := svc.resolveStoreScopeKey("workstore_default", dir)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := svc.acquire(ctx, key, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.start(ctx); err != nil {
		<-sc.gate
		t.Fatal(err)
	}
	opctx, stopOp := sc.browserOpCtx(ctx)
	_, navErr := sc.perform(opctx, Action{Action: "navigate", URL: srv.URL})
	stopOp()
	<-sc.gate
	if navErr != nil {
		t.Fatalf("initial navigate failed: %v", navErr)
	}

	st, running, err := svc.StoreState(ctx, "workstore_default", dir)
	if err != nil || !running || st.TabID == "" {
		t.Fatalf("StoreState failed: running=%v st=%+v err=%v", running, st, err)
	}
	if st.Title != "StoreSmoke" {
		t.Fatalf("expected title StoreSmoke, got %q (url=%q)", st.Title, st.URL)
	}

	focused, err := svc.StoreOpen(ctx, "workstore_default", dir, "", StoreOpenOptions{TabID: st.TabID, Focus: true, Navigate: false})
	if err != nil || focused.TabID != st.TabID {
		t.Fatalf("StoreOpen focus-only failed: st=%+v err=%v", focused, err)
	}

	evalRes, err := svc.StoreEvaluate(ctx, "workstore_default", dir, `({ title: document.title, txt: document.getElementById("ok").textContent })`, StoreEvaluateOptions{TabID: st.TabID}, nil)
	if err != nil {
		t.Fatalf("StoreEvaluate failed: %v", err)
	}
	if !strings.Contains(evalRes.RawJSON, "ready-1688") {
		t.Fatalf("unexpected StoreEvaluate payload: %s", evalRes.RawJSON)
	}
}
