package browser

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGestureValidationAndUnstartedScope(t *testing.T) {
	factor := 1.5
	for _, a := range []Action{{Action: "wheel", X: 1, Y: 2, DeltaX: -120}, {Action: "zoom", X: 1, Y: 2, ScaleFactor: &factor}} {
		if err := validateAction(a, false); err != nil {
			t.Fatal(err)
		}
		if err := validateAction(a, true); err == nil {
			t.Fatal("agent gesture accepted")
		}
	}
	for _, a := range []Action{
		{Action: "zoom"}, {Action: "wheel", X: -1}, {Action: "wheel", Y: math.Inf(1)},
		{Action: "wheel", DeltaX: math.NaN()}, {Action: "wheel", DeltaY: 1e7},
		{Action: "wheel", Text: "document.cookie"}, {Action: "wheel", ScaleFactor: &factor},
		{Action: "zoom", ScaleFactor: &factor, DeltaX: 1}, {Action: "zoom", ScaleFactor: &factor, Phase: "down"},
	} {
		if validateAction(a, false) == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
	for _, scale := range []float64{0, .1, 5, math.NaN(), math.Inf(1)} {
		if validateAction(Action{Action: "zoom", ScaleFactor: &scale}, false) == nil {
			t.Fatal("accepted scale", scale)
		}
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	for _, body := range []string{`{"action":"wheel","deltaX":-200}`, `{"action":"zoom","scaleFactor":1.5}`} {
		if w := request(t, s, dir, "gesture", "action", body, false); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := request(t, s, dir, "gesture", "action", body, true); w.Code != 400 {
			t.Fatal("agent", w.Code)
		}
	}
	if len(s.scopes) != 0 {
		t.Fatal("gesture reserved an unstarted scope")
	}
}

func TestChromeTrackpadHistoryScrollingAndZoom(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<style>body{margin:0}#horizontal{width:300px;height:100px;overflow:auto}#wide{width:2000px;height:50px}#space{height:3000px}</style><div id="horizontal"><div id="wide">horizontal</div></div><div id="space">` + r.URL.Path + `</div>`))
	}))
	defer server.Close()
	call := func(a Action) map[string]any {
		t.Helper()
		w := request(t, s, dir, "gesture", "action", string(mustJSON(t, a)), a.Action == "evaluate")
		if w.Code != 200 {
			t.Fatalf("%+v: %d %s", a, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	eval := func(js string) string {
		t.Helper()
		return call(Action{Action: "evaluate", Text: js})["result"].(string)
	}
	wait := func(js string) {
		t.Helper()
		for i := 0; i < 50; i++ {
			if eval(js) == "true" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("condition did not become true", js)
	}
	call(Action{Action: "navigate", URL: server.URL + "/one"})
	call(Action{Action: "navigate", URL: server.URL + "/two"})
	for i := 0; i < 3; i++ {
		call(Action{Action: "wheel", X: 500, Y: 200, DeltaX: -50})
	}
	wait(`location.pathname==='/one'`)
	call(Action{Action: "wheel", X: 500, Y: 200, DeltaX: -200})
	if got := eval(`location.pathname`); got != `"/one"` {
		t.Fatal("inertia repeated history navigation", got)
	}
	// Start another physical gesture without a slow test sleep.
	for _, sc := range s.scopes {
		sc.gate <- struct{}{}
		sc.wheelGesture.last = time.Time{}
		<-sc.gate
	}
	call(Action{Action: "wheel", X: 500, Y: 200, DeltaX: 150})
	wait(`location.pathname==='/two'`)
	for _, sc := range s.scopes {
		sc.gate <- struct{}{}
		sc.wheelGesture.last = time.Time{}
		<-sc.gate
	}
	call(Action{Action: "wheel", X: 100, Y: 25, DeltaX: 300})
	wait(`document.querySelector('#horizontal').scrollLeft>0`)
	if got := eval(`location.pathname`); got != `"/two"` {
		t.Fatal("horizontal scroller navigated", got)
	}
	call(Action{Action: "wheel", X: 500, Y: 200, DeltaY: 300})
	wait(`scrollY>0`)
	factor := 1.5
	call(Action{Action: "zoom", X: 300, Y: 200, ScaleFactor: &factor})
	wait(`visualViewport.scale>1.1`)
	zoomed, err := strconv.ParseFloat(eval(`visualViewport.scale`), 64)
	if err != nil {
		t.Fatal(err)
	}
	factor = .7
	call(Action{Action: "zoom", X: 300, Y: 200, ScaleFactor: &factor})
	wait(`visualViewport.scale<` + strconv.FormatFloat(zoomed, 'f', 4, 64))
}

func TestChromeLinkZoomCapability(t *testing.T) {
	for _, features := range [][]string{nil, {"pointer", "zoom"}} {
		t.Run(strings.Join(features, "-"), func(t *testing.T) {
			s := New()
			defer s.Close()
			dir := t.TempDir()
			commands := fakeExtension(t, s, dir, "zoom", features)
			w := request(t, s, dir, "zoom", "action", `{"action":"zoom","x":100,"y":100,"scaleFactor":1.5}`, false)
			if len(features) == 0 {
				if w.Code != 502 || !strings.Contains(w.Body.String(), "reload Hoxi Browser Link") {
					t.Fatal(w.Code, w.Body.String())
				}
				noCommand(t, commands, 10*time.Millisecond)
			} else {
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				if got := nextCommand(t, commands); got["action"] != "zoom" || got["scaleFactor"] != 1.5 {
					t.Fatal(got)
				}
			}
		})
	}
}
