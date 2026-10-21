package app

import "testing"

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
		{name: "bad prefix", raw: "hoxi://session/abc123", ok: false},
		{name: "invalid characters", raw: "hoxi://session/ses_bad%3Cid%3E", ok: false},
		{name: "empty", raw: "   ", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent, ok := parseSessionDeepLink(tc.raw)
			if ok != tc.ok {
				t.Fatalf("parseSessionDeepLink(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
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
	intent, ok := parseSessionDeepLink("hoxi://session/ses_abc123?message=msg%20with%20space")
	if !ok {
		t.Fatal("expected session deep link to parse")
	}
	if intent.MessageID != "" {
		t.Fatalf("expected invalid message anchor to be dropped, got %q", intent.MessageID)
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
}
