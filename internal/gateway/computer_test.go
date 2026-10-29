package gateway

import (
	"github.com/hoxiai/hoxi/internal/computer"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComputerGatewayRejectsUntrustedOriginsAndAgentGrants(t *testing.T) {
	g := &Gateway{computer: computer.New(), uiOrigins: map[string]bool{}}
	g.SetComputerAgentToken("secret")
	for _, path := range []string{"permissions", "apps", "state", "grant", "revoke", "pause", "stop", "action"} {
		request := httptest.NewRequest("POST", "http://127.0.0.1/api/hoxi/computer/"+path, strings.NewReader(`{}`))
		request.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		g.handleComputer(w, request)
		if w.Code != 403 {
			t.Fatal(path, "accepted hostile origin", w.Code)
		}
	}
	for _, path := range []string{"permissions", "apps", "grant", "pause", "stop"} {
		request := httptest.NewRequest("POST", "http://127.0.0.1/api/hoxi/computer/"+path, strings.NewReader(`{}`))
		request.Header.Set("X-Hoxi-Agent", "true")
		request.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		g.handleComputer(w, request)
		if w.Code != 403 {
			t.Fatal("agent changed UI permissions", path, w.Code)
		}
	}
	for _, headers := range []map[string]string{{"X-Hoxi-Agent": "true", "Authorization": "Bearer wrong"}, {"X-Hoxi-Agent": "true", "Authorization": "Bearer secret", "Origin": "wails://wails"}, {"X-Hoxi-Agent": "true", "Authorization": "Bearer secret", "Sec-Fetch-Site": "same-origin"}} {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/hoxi/computer/action", strings.NewReader(`{}`))
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		g.handleComputer(w, request)
		if w.Code != 403 {
			t.Fatal("computer privilege escalation accepted", w.Code)
		}
	}
}
