package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hoxiai/hoxi/internal/sandbox"
)

func newSandboxTestGateway(t *testing.T) *Gateway {
	t.Helper()
	return &Gateway{sandbox: sandbox.NewManager(t.TempDir())}
}

func sandboxRequest(t *testing.T, g *Gateway, method, body string) (int, sandboxSessionResponse) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/hoxi/sandbox/session", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleSandboxSession(rec, req)
	var out sandboxSessionResponse
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func TestSandboxSessionToggleForOpenCode(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("seatbelt backend unavailable")
	}
	g := newSandboxTestGateway(t)
	dir := t.TempDir()
	body := `{"sessionID":"ses_open_1","directory":"` + dir + `","enabled":true,"network":"deny"}`

	code, state := sandboxRequest(t, g, http.MethodPost, body)
	if code != http.StatusOK {
		t.Fatalf("enable status = %d body = %#v", code, state)
	}
	if !state.Enabled || state.Network != "deny" {
		t.Fatalf("unexpected state: %#v", state)
	}
	snapshot, enabled, err := g.sandbox.Get("ses_open_1")
	if err != nil || !enabled {
		t.Fatalf("snapshot missing: %v %v", err, enabled)
	}
	if _, err := os.Stat(snapshot.ProfilePath); err != nil {
		t.Fatalf("profile missing: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/hoxi/sandbox/session?sessionID=ses_open_1", nil)
	getRec := httptest.NewRecorder()
	g.handleSandboxSession(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d", getRec.Code)
	}
	var got sandboxSessionResponse
	_ = json.Unmarshal(getRec.Body.Bytes(), &got)
	if !got.Enabled {
		t.Fatalf("expected enabled session: %#v", got)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/hoxi/sandbox/session?sessionID=ses_open_1", nil)
	deleteRec := httptest.NewRecorder()
	g.handleSandboxSession(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body = %s", deleteRec.Code, deleteRec.Body.String())
	}
	if _, enabled, _ := g.sandbox.Get("ses_open_1"); enabled {
		t.Fatal("session still enabled after delete")
	}
}

func TestSandboxSessionRequiresDirectory(t *testing.T) {
	g := newSandboxTestGateway(t)
	code, _ := sandboxRequest(t, g, http.MethodPost, `{"sessionID":"ses_open_2","enabled":true}`)
	if code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", code)
	}
}
