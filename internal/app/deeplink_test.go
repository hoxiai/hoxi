package app

import (
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/gateway"
	"github.com/hoxiai/hoxi/internal/statusbar"
)

func TestParseSessionDeepLink(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
		sess string
		msg  string
	}{
		{name: "session host", raw: "hoxi://session/ses_abc123", ok: true, sess: "ses_abc123"},
		{name: "sessions host", raw: "hoxi://sessions/ses_abc123/", ok: true, sess: "ses_abc123"},
		{name: "empty host", raw: "hoxi:///session/ses_abc123", ok: true, sess: "ses_abc123"},
		{name: "query session", raw: "hoxi://open?session=ses_abc123", ok: true, sess: "ses_abc123"},
		{name: "message anchor", raw: "hoxi://session/ses_abc123?message=msg_a_1", ok: true, sess: "ses_abc123", msg: "msg_a_1"},
		{name: "case insensitive scheme", raw: "HOXI://SESSION/ses_abc123", ok: true, sess: "ses_abc123"},
		{name: "encoded id", raw: "hoxi://session/ses_abc%5F123", ok: true, sess: "ses_abc_123"},
		{name: "auth callback", raw: "hoxi://auth-callback?state=x", ok: false},
		{name: "foreign scheme", raw: "wails://wails/#session=ses_abc123", ok: false},
		{name: "missing session", raw: "hoxi://session", ok: false},
		{name: "bare prefix", raw: "hoxi://session/ses_", ok: false},
		{name: "bad prefix", raw: "hoxi://session/abc123", ok: false},
		{name: "dot in session id", raw: "hoxi://session/ses_abc.123", ok: false},
		{name: "too long session id", raw: "hoxi://session/ses_" + strings.Repeat("a", 96), ok: false},
		{name: "invalid characters", raw: "hoxi://session/ses_bad%3Cid%3E", ok: false},
		{name: "empty", raw: "   ", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent, ok := ParseDeepLink(tc.raw)
			if ok != tc.ok {
				t.Fatalf("ParseDeepLink(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
			}
			if !ok {
				return
			}
			if intent.SessionID != tc.sess {
				t.Fatalf("sessionID = %q, want %q", intent.SessionID, tc.sess)
			}
			if intent.MessageID != tc.msg {
				t.Fatalf("messageID = %q, want %q", intent.MessageID, tc.msg)
			}
		})
	}
}

func TestParseSessionDeepLinkDropsInvalidMessageAnchor(t *testing.T) {
	for _, raw := range []string{
		"hoxi://session/ses_abc123?message=msg%20with%20space",
		"hoxi://session/ses_abc123?message=not_msg_prefix",
		"hoxi://session/ses_abc123?message=msg_",
		"hoxi://session/ses_abc123?message=msg_bad.dot",
		"hoxi://session/ses_abc123?message=msg_" + strings.Repeat("a", 96),
	} {
		intent, ok := ParseDeepLink(raw)
		if !ok {
			t.Fatalf("expected session deep link %q to parse", raw)
		}
		if intent.MessageID != "" {
			t.Fatalf("expected invalid message anchor in %q to be dropped, got %q", raw, intent.MessageID)
		}
	}
}

func TestIsAuthCallbackDeepLink(t *testing.T) {
	if !isAuthCallbackDeepLink("hoxi://auth-callback?state=x") {
		t.Fatal("expected auth callback URL to be recognized")
	}
	if isAuthCallbackDeepLink("hoxi://session/ses_abc123") {
		t.Fatal("expected session deep link not to be treated as auth callback")
	}
	if isAuthCallbackDeepLink("https://hoxi.ai/auth-callback") {
		t.Fatal("expected non-hoxi scheme not to be treated as auth callback")
	}
	if isAuthCallbackDeepLink("://bad-url") {
		t.Fatal("expected malformed URL not to be treated as auth callback")
	}
}

func TestOnURLOpenOnlyRevealsWindowForValidDeepLink(t *testing.T) {
	gw := gateway.New(0, "test", t.TempDir(), nil, nil)
	app := &DesktopApp{gateway: gw}

	app.onURLOpen("hoxi://unsupported/path")
	if app.revealOnStartup {
		t.Fatal("expected unsupported deep link not to reveal window")
	}
	if _, ok := gw.ConsumeDesktopIntent(); ok {
		t.Fatal("expected no desktop intent for unsupported deep link")
	}

	app.onURLOpen("hoxi://session/ses_abc123?message=msg_456")
	if !app.revealOnStartup {
		t.Fatal("expected valid session deep link to reveal window")
	}
	intent, ok := gw.ConsumeDesktopIntent()
	if !ok || intent.SessionID != "ses_abc123" || intent.MessageID != "msg_456" {
		t.Fatalf("unexpected queued intent: %+v (ok=%v)", intent, ok)
	}
}

func TestHandleStatusBarMenuActionQueuesDesktopIntents(t *testing.T) {
	t.Setenv("HOXI_KEEP_AWAKE_CONFIG", t.TempDir()+"/keep-awake.json")
	tracker := statusbar.NewTracker()
	tracker.SetSessionResolver(func(sessionID string) (string, string, string) {
		switch sessionID {
		case "ses_pending":
			return "ses_pending", "Approve deploy", "/repo/deploy"
		case "ses_running":
			return "ses_running", "Build release", "/repo/release"
		default:
			return sessionID, "", ""
		}
	})
	tracker.SetPendingApproval("perm_1", "ses_pending", true)
	tracker.SetSessionBusy("ses_running", true)

	gw := gateway.New(0, "test", t.TempDir(), nil, nil)
	app := &DesktopApp{gateway: gw, status: tracker}

	// 1. Attention menu click without explicit session resolves first pending target
	app.handleStatusBarMenuAction(statusbar.MenuAction{Kind: statusbar.MenuActionAttention})
	if !app.revealOnStartup {
		t.Fatal("expected attention menu action to reveal window")
	}
	intent, ok := gw.ConsumeDesktopIntent()
	if !ok || intent.Action != gateway.DesktopIntentActionAttention || intent.SessionID != "ses_pending" || intent.Directory != "/repo/deploy" {
		t.Fatalf("unexpected attention intent: %+v (ok=%v)", intent, ok)
	}

	// 2. Running menu click with explicit session resolves target directory
	app.handleStatusBarMenuAction(statusbar.MenuAction{Kind: statusbar.MenuActionRunning, SessionID: "ses_running"})
	intent, ok = gw.ConsumeDesktopIntent()
	if !ok || intent.Action != gateway.DesktopIntentActionRunning || intent.SessionID != "ses_running" || intent.Directory != "/repo/release" {
		t.Fatalf("unexpected running intent: %+v (ok=%v)", intent, ok)
	}

	// 3. Policy, Handoff, NewThread, Settings
	for _, tc := range []struct {
		kind   string
		action string
	}{
		{statusbar.MenuActionPolicy, gateway.DesktopIntentActionPolicy},
		{statusbar.MenuActionHandoff, gateway.DesktopIntentActionHandoff},
		{statusbar.MenuActionNewThread, gateway.DesktopIntentActionNewThread},
		{statusbar.MenuActionSettings, gateway.DesktopIntentActionSettings},
	} {
		app.handleStatusBarMenuAction(statusbar.MenuAction{Kind: tc.kind})
		got, ok := gw.ConsumeDesktopIntent()
		if !ok || got.Action != tc.action {
			t.Fatalf("menu kind %q -> intent %+v (ok=%v), want action %q", tc.kind, got, ok, tc.action)
		}
	}

	// 4. Toggle keep-awake updates tracker and queues sync-keep-awake without revealing hidden window
	app.revealOnStartup = false
	initialKeepAwake := tracker.KeepAwakeEnabled()
	app.handleStatusBarMenuAction(statusbar.MenuAction{Kind: statusbar.MenuActionToggleKeepAwake})
	if tracker.KeepAwakeEnabled() == initialKeepAwake {
		t.Fatal("expected toggle-keep-awake to flip KeepAwakeEnabled")
	}
	if app.revealOnStartup {
		t.Fatal("expected toggle-keep-awake not to force-open window")
	}
	got, ok := gw.ConsumeDesktopIntent()
	if !ok || got.Action != gateway.DesktopIntentActionSyncKeepAwake {
		t.Fatalf("unexpected sync-keep-awake intent: %+v (ok=%v)", got, ok)
	}
}
