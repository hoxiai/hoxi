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

	g.uiOrigins = map[string]bool{
		"http://127.0.0.1:5420":          true,
		"http://localhost:5421":          true,
		"wails://wails.localhost:34115":  true,
	}
	checkCaller := func(host, origin, site string, want bool) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/hoxi/computer/permissions", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("X-Hoxi-Computer", "true")
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		agent, err := g.computerCaller(r)
		if (err == nil) != want || agent {
			t.Fatalf("host=%s origin=%s site=%s: agent=%v err=%v, want=%v", host, origin, site, agent, err, want)
		}
	}
	checkCaller("wails.localhost", "", "", true)
	checkCaller("wails.localhost:34115", "", "", true)
	checkCaller("wails.localhost:34115", "wails://wails.localhost:34115", "", true)
	checkCaller("127.0.0.1:5420", "", "same-origin", true)
	checkCaller("localhost:5421", "", "same-origin", true)
	checkCaller("127.0.0.1:5420", "http://localhost:5421", "", true)
	checkCaller("127.0.0.1:5420", "", "", false)
	checkCaller("127.0.0.1:9999", "", "same-origin", false)
}
