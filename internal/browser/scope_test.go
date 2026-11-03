package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

func TestResolvedScopeSharesManualOwnershipAcrossUIAndAgent(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	s.SetSessionResolver(func(id, directory string) (string, error) {
		if id == "ses_engine" {
			return "ses_hoxi", nil
		}
		if id == "ses_wrong_directory" {
			return "", errors.New("directory mismatch")
		}
		if id == "ses_invalid_resolved" {
			return "../invalid", nil
		}
		return id, nil
	})
	if w := request(t, s, dir, "ses_hoxi", "action", `{"action":"control","manual":true}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, endpoint := range []string{"state", "frame", "action"} {
		w := request(t, s, dir, "ses_engine", endpoint, `{"action":"screenshot"}`, true)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "manual control is active") {
			t.Fatalf("agent bypassed UI control on %s: %d %s", endpoint, w.Code, w.Body.String())
		}
	}
	if w := request(t, s, dir, "ses_other", "state", "", true); w.Code != 200 || strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatal("unrelated session shared ownership", w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatal("engine handle allocated another browser")
	}
	for _, id := range []string{"ses_wrong_directory", "ses_invalid_resolved"} {
		for _, endpoint := range []string{"state", "frame", "stream", "action"} {
			if w := request(t, s, dir, id, endpoint, `{"action":"snapshot"}`, false); w.Code != 400 {
				t.Fatalf("invalid scope accepted on %s: %d %s", endpoint, w.Code, w.Body.String())
			}
		}
	}
	if w := request(t, s, dir, "ses_engine", "action", `{"action":"control","manual":false}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, s, dir, "ses_engine", "state", "", true); w.Code != 200 {
		t.Fatal("returning control did not unblock engine", w.Code, w.Body.String())
	}
}

func TestProfileResolverIsolatesPersistentStoreProfiles(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	root := filepath.Join(t.TempDir(), "browser-profile")
	s.SetProfileRoot(root)
	s.SetProfileResolver(func(sessionID, _ string) string {
		switch sessionID {
		case "ses_store_a", "workstore_store-1688-a":
			return "store-1688-a"
		case "ses_store_b", "workstore_store-1688-b":
			return "store-1688-b"
		case "ses_unsafe":
			return "../escape"
		default:
			return ""
		}
	})

	dir := t.TempDir()
	// Opening/controlling via workstore_store-1688-a and then accessing via ses_store_a
	// must hit the exact same live store browser scope without allocating a duplicate scope.
	if w := request(t, s, dir, "workstore_store-1688-a", "action", `{"action":"control","manual":true}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatalf("expected 1 scope after workstore_ action, got %d", len(s.scopes))
	}
	if w := request(t, s, dir, "ses_store_a", "state", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatalf("expected ses_store_a to share workstore_store-1688-a scope state, got %d %s", w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatalf("expected ses_store_a to reuse the store scope, got %d scopes", len(s.scopes))
	}

	scA := &scope{service: s, sessionID: "workstore_store-1688-a", directory: dir, window: true}
	profA, err := s.acquirePersistentProfile(scA)
	if err != nil {
		t.Fatalf("acquirePersistentProfile for store A failed: %v", err)
	}
	wantA := filepath.Join(root+"-stores", "store-1688-a")
	if profA != wantA {
		t.Fatalf("expected store A profile %q, got %q", wantA, profA)
	}

	scB := &scope{service: s, sessionID: "ses_store_b", directory: dir, window: true}
	profB, err := s.acquirePersistentProfile(scB)
	if err != nil {
		t.Fatalf("acquirePersistentProfile for store B failed: %v", err)
	}
	wantB := filepath.Join(root+"-stores", "store-1688-b")
	if profB != wantB {
		t.Fatalf("expected store B profile %q, got %q", wantB, profB)
	}

	scUnsafe := &scope{service: s, sessionID: "ses_unsafe", directory: dir, window: true}
	profUnsafe, err := s.acquirePersistentProfile(scUnsafe)
	if err != nil {
		t.Fatalf("acquirePersistentProfile for unsafe failed: %v", err)
	}
	if profUnsafe != root {
		t.Fatalf("expected unsafe profile key to fall back to root %q, got %q", root, profUnsafe)
	}
}

func TestWindowAllocatorOptionsAvoidAutomationFlags(t *testing.T) {
	collectArgs := func(t *testing.T, window bool) []string {
		t.Helper()
		sc := &scope{window: window, width: 1280, height: 800}
		var captured []string
		opts := append(sc.allocatorOptions("/tmp/hoxi-test-profile"),
			chromedp.ExecPath("/nonexistent-chrome-binary"),
			chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
				captured = append([]string(nil), cmd.Args...)
			}),
		)
		allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
		defer cancel()
		browserCtx, browserCancel := chromedp.NewContext(allocCtx)
		defer browserCancel()
		_ = chromedp.Run(browserCtx)
		if len(captured) == 0 {
			t.Fatal("expected ModifyCmdFunc to capture command args before Start failed")
		}
		return captured
	}

	winArgs := collectArgs(t, true)
	for _, forbidden := range []string{
		"--headless",
		"--enable-automation",
		"--disable-extensions",
		"--use-mock-keychain",
		"--password-store=basic",
	} {
		if slices.Contains(winArgs, forbidden) {
			t.Fatalf("window mode should not pass %q, got args: %v", forbidden, winArgs)
		}
	}
	for _, required := range []string{
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-blink-features=AutomationControlled",
		"--disable-back-forward-cache",
		"--user-data-dir=/tmp/hoxi-test-profile",
	} {
		if !slices.Contains(winArgs, required) {
			t.Fatalf("window mode missing %q in args: %v", required, winArgs)
		}
	}

	headlessArgs := collectArgs(t, false)
	if !slices.Contains(headlessArgs, "--headless") {
		t.Fatalf("headless mode should still include --headless, got: %v", headlessArgs)
	}
}

func TestHeadlessModeIsolatesFromSharedDefaultWindowScope(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetDefaultWindowMode(true)
	s.SetProfileResolver(func(_, _ string) string { return "default" })

	dirA := t.TempDir()
	dirB := t.TempDir()

	// Two window-mode sessions share workstore_default.
	if w := request(t, s, dirA, "ses_win_a", "action", `{"action":"control","manual":true}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(t, s, dirB, "ses_win_b", "state", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatalf("expected window-mode sessions to share workstore_default, got %d %s", w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatalf("expected 1 unified window scope, got %d", len(s.scopes))
	}

	// Switching ses_headless to headless mode allocates an isolated scope without resetting workstore_default.
	if w := request(t, s, dirB, "ses_headless", "action", `{"action":"mode","window":false}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.scopes) != 2 {
		t.Fatalf("expected isolated headless scope alongside unified window scope, got %d", len(s.scopes))
	}
	if w := request(t, s, dirB, "ses_headless", "state", "", false); w.Code != 200 || strings.Contains(w.Body.String(), `"manual":true`) || strings.Contains(w.Body.String(), `"mode":"window"`) {
		t.Fatalf("expected ses_headless to have isolated headless state, got %d %s", w.Code, w.Body.String())
	}
	if w := request(t, s, dirA, "ses_win_a", "state", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatalf("expected workstore_default manual state untouched, got %d %s", w.Code, w.Body.String())
	}

	// Switching ses_headless back to window mode removes its isolated scope and rejoins workstore_default.
	if w := request(t, s, dirB, "ses_headless", "action", `{"action":"mode","window":true}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.scopes) != 1 {
		t.Fatalf("expected isolated headless scope removed after returning to window mode, got %d", len(s.scopes))
	}
	if w := request(t, s, dirB, "ses_headless", "state", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"manual":true`) {
		t.Fatalf("expected ses_headless to rejoin workstore_default, got %d %s", w.Code, w.Body.String())
	}

	sc := &scope{sessionID: "workstore_default", sessionTabs: map[string]string{"ses_a": "tab_1"}}
	if !sc.tabOwnedByOtherSession("tab_1", "ses_b") {
		t.Fatal("expected tab_1 owned by ses_a to be reported as owned for ses_b")
	}
	if sc.tabOwnedByOtherSession("tab_1", "ses_a") {
		t.Fatal("expected tab_1 not reported as owned by other session for its owner ses_a")
	}
}

func TestConversationsUseDifferentTabsForSameURL(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 to run Chrome integration tests")
	}
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>" + r.URL.Path + "</title><p>" + r.URL.String() + "</p>"))
	}))
	defer pageServer.Close()

	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.SetProfileRoot(filepath.Join(t.TempDir(), "browser-profile"))
	s.SetProfileResolver(func(_, _ string) string { return "store-shared" })

	dir := t.TempDir()
	sharedURL := pageServer.URL + "/shared?from=first"
	secondURL := pageServer.URL + "/second"

	doPost := func(session, body string) map[string]any {
		t.Helper()
		w := request(t, s, dir, session, "action", body, true)
		if w.Code != 200 {
			t.Fatalf("session=%s body=%s status=%d resp=%s", session, body, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := doPost("ses_conv_a", `{"action":"navigate","url":`+strconvQuote(sharedURL)+`}`)
	second := doPost("ses_conv_b", `{"action":"navigate","url":`+strconvQuote(sharedURL)+`}`)
	if first["tabID"] == second["tabID"] {
		t.Fatalf("independent conversations attached to the same tab %q for shared URL", first["tabID"])
	}

	doPost("ses_conv_b", `{"action":"navigate","url":`+strconvQuote(secondURL)+`}`)
	evalA := doPost("ses_conv_a", `{"action":"evaluate","text":"window.location.href"}`)
	if evalA["result"] != strconvQuote(sharedURL) || evalA["url"] != sharedURL {
		t.Fatalf("conversation A tab was overwritten by conversation B: got result=%v url=%v, want %q", evalA["result"], evalA["url"], sharedURL)
	}

	// Direct workstore_<storeKey> caller (where callerSession == sc.sessionID)
	// must also get its own tab and restore it without overwriting coding sessions.
	storeInitialURL := pageServer.URL + "/store"
	storeNextURL := pageServer.URL + "/store-next"
	storeFirst := doPost("workstore_store-shared", `{"action":"navigate","url":`+strconvQuote(storeInitialURL)+`}`)
	if storeFirst["tabID"] == first["tabID"] || storeFirst["tabID"] == second["tabID"] {
		t.Fatalf("direct workstore caller overwrote conversation tab: storeTab=%v convA=%v convB=%v", storeFirst["tabID"], first["tabID"], second["tabID"])
	}
	// Switch active tab to ses_conv_a, then navigate workstore_store-shared again.
	doPost("ses_conv_a", `{"action":"snapshot"}`)
	storeSecond := doPost("workstore_store-shared", `{"action":"navigate","url":`+strconvQuote(storeNextURL)+`}`)
	if storeSecond["tabID"] != storeFirst["tabID"] {
		t.Fatalf("direct workstore caller did not restore its own tab: got %v, want %v", storeSecond["tabID"], storeFirst["tabID"])
	}
	stateA := request(t, s, dir, "ses_conv_a", "state", "", true)
	if stateA.Code != 200 {
		t.Fatalf("ses_conv_a state: %d %s", stateA.Code, stateA.Body.String())
	}
	var parsedStateA map[string]any
	if err := json.Unmarshal(stateA.Body.Bytes(), &parsedStateA); err != nil {
		t.Fatal(err)
	}
	if parsedStateA["tabID"] != first["tabID"] || parsedStateA["url"] != sharedURL {
		t.Fatalf("ses_conv_a state corrupted after direct workstore navigation: got tab=%v url=%v, want tab=%v url=%q", parsedStateA["tabID"], parsedStateA["url"], first["tabID"], sharedURL)
	}
	stateStore := request(t, s, dir, "workstore_store-shared", "state", "", true)
	if stateStore.Code != 200 {
		t.Fatalf("workstore_store-shared state: %d %s", stateStore.Code, stateStore.Body.String())
	}
	var parsedStateStore map[string]any
	if err := json.Unmarshal(stateStore.Body.Bytes(), &parsedStateStore); err != nil {
		t.Fatal(err)
	}
	if parsedStateStore["tabID"] != storeFirst["tabID"] || parsedStateStore["url"] != storeNextURL {
		t.Fatalf("workstore_store-shared state did not restore its own tab: got tab=%v url=%v, want tab=%v url=%q", parsedStateStore["tabID"], parsedStateStore["url"], storeFirst["tabID"], storeNextURL)
	}

	// Also verify when direct workstore_* is the FIRST caller on a fresh shared scope,
	// followed by a coding session, followed by direct workstore_* navigating again.
	s2 := New()
	defer s2.Close()
	s2.SetAgentToken("secret")
	s2.SetProfileRoot(filepath.Join(t.TempDir(), "browser-profile-2"))
	s2.SetProfileResolver(func(_, _ string) string { return "audit-shared" })
	dir2 := t.TempDir()
	doPost2 := func(session, body string) map[string]any {
		t.Helper()
		w := request(t, s2, dir2, session, "action", body, true)
		if w.Code != 200 {
			t.Fatalf("session=%s body=%s status=%d resp=%s", session, body, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	storeTab := doPost2("workstore_audit-shared", `{"action":"navigate","url":`+strconvQuote(storeInitialURL)+`}`)
	codingTab := doPost2("ses_coding", `{"action":"navigate","url":`+strconvQuote(secondURL)+`}`)
	storeChanged := doPost2("workstore_audit-shared", `{"action":"navigate","url":`+strconvQuote(storeNextURL)+`}`)
	if storeChanged["tabID"] != storeTab["tabID"] || storeChanged["tabID"] == codingTab["tabID"] {
		t.Fatalf("direct store scope changed coding session tab instead of restoring its own: storeTab=%v codingTab=%v storeNextTab=%v", storeTab["tabID"], codingTab["tabID"], storeChanged["tabID"])
	}
	codingStateW := request(t, s2, dir2, "ses_coding", "state", "", true)
	var codingState map[string]any
	if err := json.Unmarshal(codingStateW.Body.Bytes(), &codingState); err != nil {
		t.Fatal(err)
	}
	if codingState["tabID"] != codingTab["tabID"] || codingState["url"] != secondURL {
		t.Fatalf("coding session URL overwritten by direct store navigation: got tab=%v url=%v, want tab=%v url=%q", codingState["tabID"], codingState["url"], codingTab["tabID"], secondURL)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
