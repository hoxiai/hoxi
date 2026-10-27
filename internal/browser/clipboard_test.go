package browser

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClipboardValidationAndNeverStartsBrowser(t *testing.T) {
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	for _, action := range []Action{{Action: "copy"}, {Action: "context", X: 1, Y: 2}, {Action: "paste", X: 2, Y: 3, Text: "中文🙂"}} {
		if err := validateAction(action, false); err != nil {
			t.Fatal(action, err)
		}
		body := string(mustJSON(t, action))
		if w := request(t, s, dir, "clipboard", "action", body, true); w.Code != 400 {
			t.Fatal("agent clipboard accepted", w.Code, w.Body.String())
		}
		if w := request(t, s, dir, "clipboard", "action", body, false); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(s.scopes) != 0 {
		t.Fatal("clipboard input reserved a browser scope")
	}
	for _, a := range []Action{
		{Action: "copy", Text: "read something else"}, {Action: "copy", Selector: "input"}, {Action: "copy", X: 3},
		{Action: "context", X: math.NaN()}, {Action: "context", Y: math.Inf(1)}, {Action: "context", X: -1},
		{Action: "context", Y: 16385}, {Action: "paste", Text: strings.Repeat("x", 65537)},
		{Action: "paste", Text: strings.Repeat("🙂", 32769)}, {Action: "paste", URL: "https://example.com"},
	} {
		if validateAction(a, false) == nil {
			t.Fatal("invalid clipboard input accepted", a.Action)
		}
	}
}

func TestClipboardChromeLinkCapabilityAndAgentDenial(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "clipboard"}[enabled], func(t *testing.T) {
			s := New()
			defer s.Close()
			s.SetAgentToken("secret")
			dir := t.TempDir()
			features := []string{"pointer"}
			if enabled {
				features = append(features, "clipboard")
			}
			commands := fakeExtension(t, s, dir, "clip", features)
			for _, action := range []string{`{"action":"copy"}`, `{"action":"context","x":1,"y":2}`, `{"action":"paste","x":1,"y":2,"text":"hello"}`} {
				w := request(t, s, dir, "clip", "action", action, false)
				if !enabled {
					if w.Code != 409 || !strings.Contains(w.Body.String(), "reload") {
						t.Fatal(w.Code, w.Body.String())
					}
				} else {
					if w.Code != 200 {
						t.Fatal(w.Code, w.Body.String())
					}
					command := nextCommand(t, commands)
					if !strings.Contains(action, `"`+command["action"].(string)+`"`) {
						t.Fatal("wrong command", command)
					}
				}
				if w = request(t, s, dir, "clip", "action", action, true); w.Code != 400 {
					t.Fatal("agent clipboard forwarded", w.Code)
				}
			}
			noCommand(t, commands, 20*time.Millisecond)
		})
	}
}

func TestChromeClipboardSelectionContextAndPaste(t *testing.T) {
	if os.Getenv("HOXI_BROWSER_TEST") != "1" {
		t.Skip("set HOXI_BROWSER_TEST=1 for Chrome integration")
	}
	s := New()
	defer s.Close()
	s.SetAgentToken("secret")
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><body>
<p id="paragraph">网页文字 中文🙂</p><a id="link" href="https://example.com/path?q=1"><span>Link</span></a>
<a id="unsafe" href="javascript:alert(1)">Unsafe link</a>
<input id="text" value="hello world"><textarea id="area">first second</textarea>
<input id="password" type="password" value="private-password"><input id="masked" style="-webkit-text-security:disc" value="private-masked">
<input id="readonly" readonly value="unchanged"><input id="disabled" disabled value="unchanged">
<div id="editor" contenteditable><span>Editable</span></div><div id="shadow"></div><iframe id="frame" srcdoc="<input>"></iframe>
<script>window.inputs=0;document.addEventListener('input',()=>inputs++);shadow.attachShadow({mode:'open'}).innerHTML='<textarea>shadow words</textarea>';</script>`))
	}))
	defer server.Close()
	call := func(action Action, agent bool) map[string]any {
		t.Helper()
		w := request(t, s, dir, "clip", "action", string(mustJSON(t, action)), agent)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", action.Action, w.Code, w.Body.String())
		}
		var result map[string]any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Body.String())
		}
		return result
	}
	eval := func(js string) string { return call(Action{Action: "evaluate", Text: js}, true)["result"].(string) }
	point := func(selector string) (float64, float64) {
		t.Helper()
		var p struct{ X, Y float64 }
		if err := json.Unmarshal([]byte(eval(`(()=>{const r=document.querySelector(`+string(mustJSON(t, selector))+`).getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`)), &p); err != nil {
			t.Fatal(err)
		}
		return p.X, p.Y
	}
	call(Action{Action: "navigate", URL: server.URL}, false)
	eval(`text.focus();text.setSelectionRange(0,5)`)
	if got := call(Action{Action: "copy"}, false)["clipboardText"]; got != "hello" {
		t.Fatal("input selection", got)
	}
	x, y := point("#link span")
	result := call(Action{Action: "context", X: x, Y: y}, false)
	context := result["context"].(map[string]any)
	if context["linkURL"] != "https://example.com/path?q=1" || context["canCopy"] != true || result["clipboardText"] != nil {
		t.Fatal("context leaked text or missed link", result)
	}
	x, y = point("#unsafe")
	if c := call(Action{Action: "context", X: x, Y: y}, false)["context"].(map[string]any); c["linkURL"] != "" {
		t.Fatal("unsafe link exposed", c)
	}
	for _, id := range []string{"password", "masked"} {
		eval(id + `.focus();` + id + `.select()`)
		if r := call(Action{Action: "copy"}, false); r["clipboardError"] != "protected" || r["clipboardText"] != nil {
			t.Fatal("protected selection exposed", r)
		}
	}
	eval(`document.activeElement.blur();const range=document.createRange();range.selectNodeContents(paragraph);getSelection().removeAllRanges();getSelection().addRange(range)`)
	if got := call(Action{Action: "copy"}, false)["clipboardText"]; got != "网页文字 中文🙂" {
		t.Fatal("page selection", got)
	}
	eval(`area.focus();area.setSelectionRange(6,12)`)
	x, y = point("#area")
	call(Action{Action: "paste", X: x, Y: y, Text: "中文🙂\nline"}, false)
	if eval(`({value:area.value,hasInput:inputs>0})`) != `{"value":"first 中文🙂\nline","hasInput":true}` {
		t.Fatal("paste did not replace selection or fire input", eval(`({value:area.value,inputs})`))
	}
	for _, selector := range []string{"#readonly", "#disabled", "#paragraph", "#frame"} {
		x, y = point(selector)
		if r := call(Action{Action: "paste", X: x, Y: y, Text: "must not insert"}, false); r["clipboardError"] != "notEditable" {
			t.Fatal(selector, r)
		}
	}
	x, y = point("#editor span")
	if r := call(Action{Action: "paste", X: x, Y: y, Text: "new"}, false); r["clipboardError"] != nil || !strings.Contains(eval(`editor.innerText`), "new") {
		t.Fatal("contenteditable paste", r, eval(`editor.innerText`))
	}
	eval(`shadow.shadowRoot.querySelector('textarea').focus();shadow.shadowRoot.querySelector('textarea').setSelectionRange(0,6)`)
	if got := call(Action{Action: "copy"}, false)["clipboardText"]; got != "shadow" {
		t.Fatal("shadow selection", got)
	}
	eval(`text.value='x'.repeat(65537);text.focus();text.select()`)
	if r := call(Action{Action: "copy"}, false); r["clipboardError"] != "tooLarge" || r["clipboardText"] != nil {
		t.Fatal("oversized selection transported", r)
	}
}
