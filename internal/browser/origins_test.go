package browser

import (
	"net/http/httptest"
	"testing"
)

func TestUIOrigins(t *testing.T) {
	s := New()
	defer s.Close()
	check := func(host, origin, site string, want bool) {
		t.Helper()
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Hoxi-Browser", "true")
		r.Header.Set("Sec-Fetch-Site", site)
		agent, err := s.authenticate(r)
		if (err == nil) != want || agent {
			t.Errorf("host=%s origin=%s site=%s: agent=%v err=%v", host, origin, site, agent, err)
		}
	}
	for _, host := range []string{"wails", "wails.localhost", "app.wails.localhost"} {
		check(host, "wails://"+host, "", true)
		check(host, "", "", true)
		check(host, "", "same-origin", true)
		check(host, "", "same-site", false)
		check(host, "", "cross-site", false)
	}
	check("localhost:5173", "http://localhost:5173", "same-origin", false)
	check("localhost:5173", "", "same-origin", false)
	s.SetUIOrigins("http://localhost:5173", "http://127.0.0.1:42817", "wails://wails.localhost:34115")
	check("localhost:5173", "http://localhost:5173", "same-origin", true)
	check("127.0.0.1:42817", "http://localhost:5173", "", true)
	check("wails.localhost:34115", "wails://wails.localhost:34115", "", true)
	check("wails.localhost:34115", "", "", true)
	// The reverse proxy preserves the configured bridge Host on Vite GETs.
	check("127.0.0.1:42817", "", "same-origin", true)
	check("localhost:5173", "", "same-origin", true)
	check("127.0.0.1:42818", "", "same-origin", false)
	check("127.0.0.1:42817", "", "", false)
	check("127.0.0.1:42817", "", "same-site", false)
	for _, origin := range []string{"http://localhost:5174", "https://localhost:5173", "http://127.0.0.1:5173", "http://localhost:5173/", "wails://evil", "wails://wails:123"} {
		check("127.0.0.1:42817", origin, "same-origin", false)
	}
	s.SetUIOrigins("http://evil.example", "http://localhost:5173/path", "http://user@localhost:5173", "http://localhost:5173?", "wails://evil")
	check("localhost:5173", "http://localhost:5173", "same-origin", false)
	check("localhost:5173", "", "same-origin", false)
	check("evil.example", "http://evil.example", "same-origin", false)
	s.SetUIOrigins()
	check("wails", "wails://wails", "", true)
}
