package browser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPointerValidation(t *testing.T) {
	ok := []Action{
		{Action: "pointer", Phase: "move", X: 10, Y: 20},
		{Action: "pointer", Phase: "move", X: 0, Y: 0, Modifiers: 15},
		{Action: "pointer", Phase: "down", X: 1, Y: 2, Button: "left", ClickCount: 1},
		{Action: "pointer", Phase: "down", X: 1, Y: 2, Button: "right", ClickCount: 1, Modifiers: 2},
		{Action: "pointer", Phase: "up", X: 16384, Y: 2, Button: "middle", ClickCount: 3},
		{Action: "pointer", Phase: "cancel"},
	}
	for _, a := range ok {
		if err := validateAction(a, false); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
		// Pointer input belongs to the person at the inspector, never an agent.
		if err := validateAction(a, true); err == nil || !strings.Contains(err.Error(), "only UI") {
			t.Errorf("agent pointer accepted: %+v (%v)", a, err)
		}
	}
	bad := []Action{
		{Action: "pointer"},
		{Action: "pointer", Phase: "drag", X: 1, Y: 1},
		{Action: "pointer", Phase: "down", X: 1, Y: 1, ClickCount: 1},
		{Action: "pointer", Phase: "down", X: 1, Y: 1, Button: "back", ClickCount: 1},
		{Action: "pointer", Phase: "down", X: 1, Y: 1, Button: "left"},
		{Action: "pointer", Phase: "up", X: 1, Y: 1, Button: "left", ClickCount: 4},
		{Action: "pointer", Phase: "move", X: 1, Y: 1, Button: "left"},
		{Action: "pointer", Phase: "move", X: 1, Y: 1, ClickCount: 1},
		{Action: "pointer", Phase: "move", X: -1, Y: 1},
		{Action: "pointer", Phase: "move", X: 1, Y: 16385},
		{Action: "pointer", Phase: "move", X: math.NaN(), Y: 1},
		{Action: "pointer", Phase: "move", X: math.Inf(1), Y: 1},
		{Action: "pointer", Phase: "move", X: 1, Y: 1, Modifiers: 16},
		{Action: "pointer", Phase: "move", X: 1, Y: 1, Modifiers: -1},
		{Action: "pointer", Phase: "cancel", X: 1},
		{Action: "pointer", Phase: "cancel", Button: "left"},
		{Action: "pointer", Phase: "move", X: 1, Y: 1, Selector: "#a"},
		{Action: "pointer", Phase: "move", X: 1, Y: 1, DeltaY: 3},
		{Action: "pointer", Phase: "move", X: 1, Y: 1, Text: "x"},
		{Action: "click", X: 1, Y: 1, Button: "right"},
		{Action: "click", X: 1, Y: 1, ClickCount: 2},
		{Action: "scroll", X: 1, Y: 1, Phase: "move"},
		{Action: "key", Key: "a", Modifiers: 2},
	}
	for _, a := range bad {
		if err := validateAction(a, false); err == nil {
			t.Errorf("accepted %+v", a)
		}
	}
}

func TestPointerNeverStartsBrowser(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	if w := request(t, s, dir, "a", "action", `{"action":"pointer","phase":"move","x":1,"y":1}`, true); w.Code != 400 {
		t.Fatal("agent pointer input", w.Code, w.Body.String())
	}
	for _, body := range []string{
		`{"action":"pointer","phase":"move","x":1,"y":1}`,
		`{"action":"pointer","phase":"down","x":1,"y":1,"button":"left","clickCount":1}`,
		`{"action":"pointer","phase":"cancel"}`,
	} {
		if w := request(t, s, dir, "a", "action", body, false); w.Code != 200 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	// Input for a browser that is not running (or was just stopped for
	// inactivity) must not launch Chrome or reserve a scope.
	if len(s.scopes) != 0 {
		t.Fatal("pointer input reserved a browser scope")
	}
	request(t, s, dir, "a", "action", `{"action":"resize","width":800,"height":600}`, false)
	if w := request(t, s, dir, "a", "action", `{"action":"pointer","phase":"move","x":1,"y":1}`, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, sc := range s.scopes {
		if sc.browser != nil {
			t.Fatal("pointer input started Chrome")
		}
	}
}

// fakeExtension pairs a Chrome Link extension that records every command.
func fakeExtension(t *testing.T, s *Service, dir, session string, features []string) <-chan map[string]any {
	t.Helper()
	s.SetBridgeURL("ws://127.0.0.1:5420")
	pair := request(t, s, dir, session, "action", `{"action":"pair"}`, false)
	var payload struct {
		Code string `json:"code"`
	}
	if pair.Code != 200 || json.Unmarshal(pair.Body.Bytes(), &payload) != nil {
		t.Fatal(pair.Code, pair.Body.String())
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload.Code)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(s.ServeExtension))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"),
		http.Header{"Origin": {"chrome-extension://" + strings.Repeat("a", 32)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hello := map[string]any{"token": strings.Split(string(decoded), "#")[1]}
	if features != nil {
		hello["features"] = features
	}
	var connected map[string]any
	if conn.WriteJSON(hello) != nil || conn.ReadJSON(&connected) != nil || connected["connected"] != true {
		t.Fatal("extension handshake failed", connected)
	}
	commands := make(chan map[string]any, 64)
	go func() {
		for {
			var command map[string]any
			if conn.ReadJSON(&command) != nil {
				return
			}
			commands <- command
			reply := extensionReply{Result: map[string]any{"tabID": "42", "url": "https://example.com", "tabs": []Tab{{ID: "42"}}}}
			if conn.WriteJSON(reply) != nil {
				return
			}
		}
	}()
	return commands
}

func nextCommand(t *testing.T, commands <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(5 * time.Second):
		t.Fatal("no extension command")
		return nil
	}
}

func noCommand(t *testing.T, commands <-chan map[string]any, wait time.Duration) {
	t.Helper()
	select {
	case command := <-commands:
		t.Fatalf("unexpected extension command %v", command)
	case <-time.After(wait):
	}
}

func TestPointerThroughChromeLink(t *testing.T) {
	s := New()
	defer s.Close()
	s.pointerHold = 300 * time.Millisecond
	dir := t.TempDir()
	commands := fakeExtension(t, s, dir, "a", []string{"pointer"})
	send := func(body string) {
		t.Helper()
		if w := request(t, s, dir, "a", "action", body, false); w.Code != 200 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	expect := func(want string) {
		t.Helper()
		got := nextCommand(t, commands)
		// Every pointer reply already carries the tab state.
		if got["action"] == "state" {
			t.Fatalf("pointer reply followed by a state request: %v", got)
		}
		parts := []string{}
		for _, key := range []string{"action", "type", "x", "y", "button", "buttons", "clickCount", "modifiers"} {
			parts = append(parts, strings.TrimSuffix(strings.TrimPrefix(string(mustJSON(t, got[key])), `"`), `"`))
		}
		if strings.Join(parts, " ") != want {
			t.Fatalf("got %q, want %q", strings.Join(parts, " "), want)
		}
	}
	send(`{"action":"pointer","phase":"move","x":5,"y":6}`)
	expect("pointer mouseMoved 5 6 none 0 0 0")
	send(`{"action":"pointer","phase":"down","x":5,"y":6,"button":"left","clickCount":1,"modifiers":8}`)
	expect("pointer mousePressed 5 6 left 1 1 8")
	send(`{"action":"pointer","phase":"move","x":50,"y":6}`)
	expect("pointer mouseMoved 50 6 left 1 0 0")
	send(`{"action":"pointer","phase":"down","x":50,"y":6,"button":"right","clickCount":1}`)
	expect("pointer mousePressed 50 6 right 3 1 0")
	send(`{"action":"pointer","phase":"up","x":51,"y":7,"button":"left","clickCount":1}`)
	expect("pointer mouseReleased 51 7 left 2 1 0")
	// A cancel releases what is still held, where the pointer last was.
	send(`{"action":"pointer","phase":"cancel"}`)
	expect("pointer mouseReleased 51 7 right 0 1 0")
	send(`{"action":"pointer","phase":"up","x":51,"y":7,"button":"right","clickCount":1}`)
	send(`{"action":"pointer","phase":"cancel"}`)
	if got := nextCommand(t, commands); got["action"] != "state" {
		t.Fatal("released twice", got)
	}
	nextCommand(t, commands)

	// Without an up, the hold timeout releases the press once.
	send(`{"action":"pointer","phase":"down","x":9,"y":9,"button":"left","clickCount":2}`)
	expect("pointer mousePressed 9 9 left 1 2 0")
	expect("pointer mouseReleased 9 9 left 0 2 0")
	noCommand(t, commands, 2*s.pointerHold)
	send(`{"action":"pointer","phase":"up","x":9,"y":9,"button":"left","clickCount":2}`)
	if got := nextCommand(t, commands); got["action"] != "state" {
		t.Fatal("late up was dispatched after the timeout released it", got)
	}

	// Navigation never starts with a button still pressed on the old page.
	send(`{"action":"pointer","phase":"down","x":3,"y":4,"button":"left","clickCount":1}`)
	expect("pointer mousePressed 3 4 left 1 1 0")
	send(`{"action":"navigate","url":"https://example.com/next"}`)
	expect("pointer mouseReleased 3 4 left 0 1 0")
	if got := nextCommand(t, commands); got["action"] != "navigate" {
		t.Fatal(got)
	}
	noCommand(t, commands, 2*s.pointerHold)
}

func TestPointerLegacyChromeLinkClicks(t *testing.T) {
	s := New()
	defer s.Close()
	dir := t.TempDir()
	commands := fakeExtension(t, s, dir, "a", nil)
	send := func(body string, code int) {
		t.Helper()
		if w := request(t, s, dir, "a", "action", body, false); w.Code != code {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	state := func() {
		t.Helper()
		if got := nextCommand(t, commands); got["action"] != "state" {
			t.Fatal(got)
		}
	}
	// An extension loaded before pointer support keeps plain clicks.
	send(`{"action":"pointer","phase":"move","x":5,"y":6}`, 200)
	state()
	send(`{"action":"pointer","phase":"down","x":5,"y":6,"button":"left","clickCount":1}`, 200)
	state()
	send(`{"action":"pointer","phase":"up","x":7,"y":8,"button":"left","clickCount":1}`, 200)
	if got := nextCommand(t, commands); got["action"] != "click" || got["x"] != 7.0 || got["y"] != 8.0 {
		t.Fatal(got)
	}
	send(`{"action":"pointer","phase":"down","x":5,"y":6,"button":"left","clickCount":1}`, 200)
	state()
	send(`{"action":"pointer","phase":"cancel"}`, 200)
	state()
	if w := request(t, s, dir, "a", "action", `{"action":"pointer","phase":"down","x":5,"y":6,"button":"right","clickCount":1}`, false); w.Code != 502 ||
		!strings.Contains(w.Body.String(), "reload Hoxi Browser Link") {
		t.Fatal(w.Code, w.Body.String())
	}
	send(`{"action":"pointer","phase":"up","x":5,"y":6,"button":"right","clickCount":1}`, 200)
	state()
	noCommand(t, commands, 100*time.Millisecond)
}

// pointerPage records mouse and pointer events, and offers a CSS :hover menu,
// a range slider, selectable text and a pad for drags and clicks.
const pointerPage = `<!doctype html><style>
body{margin:0;font:16px/1.5 sans-serif}
#hover{position:absolute;left:0;top:0;width:200px;height:40px;background:#eee}
#menu{display:none}#hover:hover #menu{display:block}
#range{position:absolute;left:20px;top:100px;width:400px;margin:0}
#text{position:absolute;left:20px;top:160px;margin:0;white-space:nowrap}
#pad{position:absolute;left:0;top:300px;width:800px;height:300px;background:#ddd}
#src{position:absolute;left:900px;top:300px;width:60px;height:60px;background:#c00}
#dst{position:absolute;left:1000px;top:300px;width:120px;height:120px;background:#00c}
</style><div id="hover">Hover<div id="menu">Menu</div></div>
<input id="range" type="range" min="0" max="100" value="0">
<p id="text">The quick brown fox jumps over the lazy dog</p><div id="pad"></div>
<div id="src" draggable="true"></div><div id="dst"></div>
<script>window.events=[];
for(const type of ['mousedown','mousemove','mouseup','click','dblclick','contextmenu','pointerdown','pointerup'])
 addEventListener(type,e=>events.push({type,x:e.clientX,y:e.clientY,button:e.button,buttons:e.buttons,detail:e.detail,shift:e.shiftKey,target:e.target.id}),true);
document.querySelector('#pad').addEventListener('contextmenu',e=>e.preventDefault());
window.drops=[];const dst=document.querySelector('#dst');dst.addEventListener('dragover',e=>e.preventDefault());
dst.addEventListener('drop',e=>{e.preventDefault();drops.push(true)});</script>`

type pageEvent struct {
	Type    string  `json:"type"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Button  int     `json:"button"`
	Buttons int     `json:"buttons"`
	Detail  int     `json:"detail"`
	Shift   bool    `json:"shift"`
	Target  string  `json:"target"`
}

func TestChromePointerInput(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	s.pointerHold = 500 * time.Millisecond
	dir := t.TempDir()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pointerPage))
	}))
	defer page.Close()
	call := func(a Action, agent bool) map[string]any {
		t.Helper()
		w := request(t, s, dir, "pointer", "action", string(mustJSON(t, a)), agent)
		if w.Code != 200 {
			t.Fatalf("%+v: %d %s", a, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	eval := func(text string, out any) {
		t.Helper()
		raw := call(Action{Action: "evaluate", Text: text}, true)["result"].(string)
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			t.Fatal(raw, err)
		}
	}
	events := func() []pageEvent {
		t.Helper()
		var list []pageEvent
		eval(`events.splice(0)`, &list)
		return list
	}
	ptr := func(phase string, x, y float64, button string, clicks int) {
		t.Helper()
		call(Action{Action: "pointer", Phase: phase, X: x, Y: y, Button: button, ClickCount: clicks}, false)
	}
	rect := func(selector string) (r struct{ Left, Top, Right, Bottom float64 }) {
		t.Helper()
		eval(`(()=>{const r=document.querySelector('`+selector+`').getBoundingClientRect();return {left:r.left,top:r.top,right:r.right,bottom:r.bottom}})()`, &r)
		return r
	}
	first := call(Action{Action: "navigate", URL: page.URL}, false)
	if w := request(t, s, dir, "pointer", "action", `{"action":"pointer","phase":"move","x":1,"y":1}`, true); w.Code != 400 {
		t.Fatal("agent pointer input", w.Code)
	}

	// Hover: a move with no button shows a CSS :hover menu, and leaving hides it.
	ptr("move", 100, 20, "", 0)
	var shown string
	eval(`getComputedStyle(document.querySelector('#menu')).display`, &shown)
	if shown != "block" {
		t.Fatal("hover menu not shown:", shown)
	}
	ptr("move", 700, 250, "", 0)
	eval(`getComputedStyle(document.querySelector('#menu')).display`, &shown)
	if shown != "none" {
		t.Fatal("hover menu stayed open:", shown)
	}

	// Drag: every move reports the held button, in order.
	events()
	path := [][2]float64{{100, 400}, {150, 410}, {200, 420}, {250, 430}}
	ptr("down", path[0][0], path[0][1], "left", 1)
	for _, p := range path[1:] {
		ptr("move", p[0], p[1], "", 0)
	}
	ptr("up", 250, 430, "left", 1)
	var trail []string
	for _, e := range events() {
		if e.Type == "mousedown" || e.Type == "mousemove" || e.Type == "mouseup" {
			trail = append(trail, fmt.Sprintf("%s %g,%g b%d/%d", e.Type, e.X, e.Y, e.Button, e.Buttons))
		}
	}
	want := "mousedown 100,400 b0/1 | mousemove 150,410 b0/1 | mousemove 200,420 b0/1 | mousemove 250,430 b0/1 | mouseup 250,430 b0/0"
	if got := strings.Join(trail, " | "); got != want {
		t.Fatalf("drag trail\n got %s\nwant %s", got, want)
	}

	// Slider: dragging the thumb from the left end to the right end.
	slider := rect("#range")
	mid := (slider.Top + slider.Bottom) / 2
	ptr("down", slider.Left+2, mid, "left", 1)
	for x := slider.Left + 50; x <= slider.Right+40; x += 50 {
		ptr("move", x, mid, "", 0)
	}
	ptr("up", slider.Right+40, mid, "left", 1)
	var value string
	eval(`document.querySelector('#range').value`, &value)
	if value != "100" {
		t.Fatal("slider value", value)
	}

	// Text selection: press before "quick" and drag past "brown".
	text := rect("#text")
	var offsets []float64
	eval(`(()=>{const n=document.querySelector('#text').firstChild,r=document.createRange(),at=i=>{r.setStart(n,i);r.setEnd(n,i+1);return r.getBoundingClientRect().left};return [at(4),at(15)]})()`, &offsets)
	line := (text.Top + text.Bottom) / 2
	ptr("down", offsets[0]+1, line, "left", 1)
	ptr("move", (offsets[0]+offsets[1])/2, line, "", 0)
	ptr("move", offsets[1], line, "", 0)
	ptr("up", offsets[1], line, "left", 1)
	var selected string
	eval(`getSelection().toString()`, &selected)
	if !strings.HasPrefix(selected, "quick brown") {
		t.Fatalf("selection %q", selected)
	}

	// Double click and modifiers.
	events()
	ptr("down", 400, 450, "left", 1)
	ptr("up", 400, 450, "left", 1)
	call(Action{Action: "pointer", Phase: "down", X: 400, Y: 450, Button: "left", ClickCount: 2, Modifiers: 8}, false)
	ptr("up", 400, 450, "left", 2)
	dbl, shift := false, false
	for _, e := range events() {
		dbl = dbl || (e.Type == "dblclick" && e.Detail == 2 && e.Target == "pad")
		shift = shift || (e.Type == "mousedown" && e.Detail == 2 && e.Shift)
	}
	if !dbl || !shift {
		t.Fatal("double click", dbl, "shift", shift)
	}

	// Right click reaches the page's contextmenu handler.
	ptr("down", 500, 500, "right", 1)
	ptr("up", 500, 500, "right", 1)
	got := map[string]bool{}
	for _, e := range events() {
		if e.Button == 2 && e.Target == "pad" {
			got[e.Type] = true
		}
	}
	if !got["mousedown"] || !got["mouseup"] || !got["contextmenu"] {
		t.Fatal("right click events", got)
	}

	// A cancelled drag still ends with mouseup where the pointer last was.
	ptr("down", 300, 400, "left", 1)
	ptr("move", 320, 410, "", 0)
	call(Action{Action: "pointer", Phase: "cancel"}, false)
	lastUp := func() *pageEvent {
		for _, e := range events() {
			if e.Type == "mouseup" {
				return &e
			}
		}
		return nil
	}
	if up := lastUp(); up == nil || up.X != 320 || up.Y != 410 || up.Button != 0 || up.Buttons != 0 {
		t.Fatal("cancel did not release", up)
	}

	// A lost release is lifted by the hold timeout.
	ptr("down", 300, 400, "left", 1)
	time.Sleep(3 * s.pointerHold)
	if up := lastUp(); up == nil || up.X != 300 {
		t.Fatal("hold timeout did not release", up)
	}
	ptr("up", 300, 400, "left", 1)
	if up := lastUp(); up != nil {
		t.Fatal("released twice", up)
	}

	// Switching tabs releases the press on the page that had it.
	ptr("down", 300, 400, "left", 1)
	call(Action{Action: "tab_new"}, false)
	call(Action{Action: "tab_select", TabID: first["tabID"].(string)}, false)
	if up := lastUp(); up == nil || up.X != 300 {
		t.Fatal("tab switch left the button pressed", up)
	}

	// HTML5 drag and drop inside the page, with a pause over the target as a
	// person makes before letting go.
	ptr("down", 930, 330, "left", 1)
	for _, x := range []float64{960, 1010, 1050} {
		ptr("move", x, 340, "", 0)
		time.Sleep(100 * time.Millisecond)
	}
	ptr("move", 1060, 350, "", 0)
	time.Sleep(200 * time.Millisecond)
	ptr("up", 1060, 350, "left", 1)
	var drops []bool
	for range 20 {
		if eval(`drops`, &drops); len(drops) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(drops) != 1 {
		t.Fatal("HTML5 drop", drops)
	}

	// Latency of one move through the HTTP action endpoint, for the record.
	start := time.Now()
	const moves = 100
	for i := range moves {
		ptr("move", float64(100+i), 450, "", 0)
	}
	t.Logf("pointer move round trip: %s", time.Since(start)/moves)
}
