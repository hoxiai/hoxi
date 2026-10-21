package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
)

func consumeIntentInTest(t *testing.T, gw *Gateway) (DesktopIntent, bool) {
	t.Helper()
	res := serve(gw, http.MethodPost, "/api/hoxi/desktop/intent", "")
	if res.Code != http.StatusOK {
		t.Fatalf("consume intent status = %d, want 200", res.Code)
	}
	var body struct {
		Intent *DesktopIntent `json:"intent"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode intent response: %v", err)
	}
	if body.Intent == nil {
		return DesktopIntent{}, false
	}
	return *body.Intent, true
}

func TestDesktopIntentRoundTrip(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)
	notified := 0
	gw.SetDesktopIntentNotifier(func() { notified++ })

	gw.QueueDesktopIntent(DesktopIntent{SessionID: " ses_abc ", MessageID: " msg_a_1 "})
	if notified != 1 {
		t.Fatalf("notifier calls = %d, want 1", notified)
	}

	intent, ok := consumeIntentInTest(t, gw)
	if !ok {
		t.Fatal("expected a pending intent")
	}
	if intent.SessionID != "ses_abc" || intent.MessageID != "msg_a_1" {
		t.Fatalf("unexpected intent: %+v", intent)
	}

	if _, ok := consumeIntentInTest(t, gw); ok {
		t.Fatal("expected intent to be cleared after consume")
	}
}

func TestDesktopIntentIgnoresEmptySession(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)
	notified := 0
	gw.SetDesktopIntentNotifier(func() { notified++ })

	gw.QueueDesktopIntent(DesktopIntent{SessionID: "   "})
	if notified != 0 {
		t.Fatalf("notifier calls = %d, want 0", notified)
	}
	if _, ok := consumeIntentInTest(t, gw); ok {
		t.Fatal("expected no pending intent")
	}
}

func TestDesktopFocusQueuesIntent(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)
	focused := 0
	gw.SetDesktopFocusHandler(func(directory, sessionID string) { focused++ })

	res := serve(gw, http.MethodPost, "/api/hoxi/desktop/focus", `{"directory":"/repo","sessionID":"ses_1"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("focus status = %d, want 200", res.Code)
	}
	if focused != 1 {
		t.Fatalf("focus handler calls = %d, want 1", focused)
	}
	intent, ok := gw.ConsumeDesktopIntent()
	if !ok || intent.SessionID != "ses_1" || intent.Directory != "/repo" {
		t.Fatalf("unexpected intent: %+v (ok=%v)", intent, ok)
	}
}

func TestDesktopIntentRequiresPost(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)
	res := serve(gw, http.MethodGet, "/api/hoxi/desktop/intent", "")
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", res.Code)
	}
}
