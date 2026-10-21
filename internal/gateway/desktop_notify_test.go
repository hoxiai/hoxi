package gateway

import (
	"errors"
	"net/http"
	"testing"
)

func TestDesktopNotifyRouteDelivers(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)
	var got DesktopNotification
	gw.SetDesktopNotifier(func(notification DesktopNotification) error {
		got = notification
		return nil
	})

	res := serve(gw, http.MethodPost, "/api/hoxi/desktop/notify", `{"title":" Hoxi ","body":" Turn completed ","sessionID":" ses_1 "}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if got.Title != "Hoxi" || got.Body != "Turn completed" || got.SessionID != "ses_1" {
		t.Fatalf("unexpected notification: %+v", got)
	}
}

func TestDesktopNotifyRouteUnavailable(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)
	res := serve(gw, http.MethodPost, "/api/hoxi/desktop/notify", `{"title":"Hoxi"}`)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.Code)
	}
}

func TestDesktopNotifyRouteRejectsBadInput(t *testing.T) {
	gw := New(0, "test", t.TempDir(), nil, nil)
	gw.SetDesktopNotifier(func(DesktopNotification) error { return errors.New("unavailable") })

	res := serve(gw, http.MethodPost, "/api/hoxi/desktop/notify", `{`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	res = serve(gw, http.MethodGet, "/api/hoxi/desktop/notify", "")
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", res.Code)
	}
	res = serve(gw, http.MethodPost, "/api/hoxi/desktop/notify", `{"title":"Hoxi","sessionID":"bad_session_id"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid sessionID", res.Code)
	}
	res = serve(gw, http.MethodPost, "/api/hoxi/desktop/notify", `{"title":"Hoxi","sessionID":"ses_bad.dot"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for sessionID with disallowed chars", res.Code)
	}
	res = serve(gw, http.MethodPost, "/api/hoxi/desktop/notify", `{"title":"Hoxi"}`)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when delivery fails", res.Code)
	}
}

func TestDesktopNotifyStaysOffTunnel(t *testing.T) {
	if tunnelRouteAllowed(http.MethodPost, "/api/hoxi/desktop/notify") {
		t.Fatal("desktop notifications must not be reachable over the remote tunnel")
	}
}
