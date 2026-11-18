package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

var snapshotRefPattern = regexp.MustCompile(`^@e([1-9][0-9]{0,2})$`)

type actionConflictError struct {
	msg string
}

func (e actionConflictError) Error() string { return e.msg }

// Light-DOM snapshot: at most 200 visible controls (each assigned a stable @eN
// ref for the current snapshot plus its CSS selector) and 32 KiB of rendered text.
// CSS.escape and structural paths avoid assuming IDs are unique or CSS-safe.
// Input values are intentionally omitted so password/secret fields never leak.
const snapshotJS = `(()=>{
 const controls=[];
 const selector=(el)=>{
  const parts=[];
  while(el&&el.nodeType===1){
   let part=el.localName;
   if(el.id){const id='#'+CSS.escape(el.id);if(document.querySelectorAll(id).length===1){parts.unshift(id);break}}
   const parent=el.parentElement;
   if(parent){const siblings=Array.from(parent.children).filter(x=>x.localName===el.localName);if(siblings.length>1)part+=':nth-of-type('+(siblings.indexOf(el)+1)+')'}
   parts.unshift(part);el=parent;
  }
  return parts.join(' > ');
 };
 for(const el of document.querySelectorAll('a,button,input,textarea,select,[role="button"],[contenteditable="true"],[tabindex]')){
  if(controls.length>=200)break;
  if(!el.getClientRects().length||getComputedStyle(el).visibility==='hidden')continue;
  const ref='@e'+(controls.length+1);
  const entry={ref,selector:selector(el).slice(0,2048),tag:el.localName,text:(el.getAttribute('aria-label')||el.innerText||el.getAttribute('placeholder')||'').slice(0,256)};
  const tp=typeof el.type==='string'?el.type.trim().toLowerCase():'';
  if(tp&&(el.localName==='input'||el.localName==='button'))entry.type=tp.slice(0,32);
  controls.push(entry);
 }
 return {text:(document.body?.innerText||'').slice(0,32768),controls};
})()`

func (sc *scope) clearSnapshotRefs(tabID string) {
	if sc == nil || tabID == "" || sc.snapshotRefs == nil {
		return
	}
	delete(sc.snapshotRefs, tabID)
}

func (sc *scope) recordSnapshotRefs(tabID string, raw any) {
	if sc == nil || tabID == "" {
		return
	}
	if sc.snapshotRefs == nil {
		sc.snapshotRefs = make(map[string]map[string]string)
	}
	refs := make(map[string]string)
	if m, ok := raw.(map[string]any); ok {
		if ctrls, ok := m["controls"].([]any); ok {
			for _, item := range ctrls {
				cm, ok := item.(map[string]any)
				if !ok {
					continue
				}
				r, _ := cm["ref"].(string)
				s, _ := cm["selector"].(string)
				if r != "" && s != "" {
					refs[r] = s
				}
			}
		}
	}
	sc.snapshotRefs[tabID] = refs
}

func (sc *scope) resolveSelector(tabID, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "@") {
		return trimmed, nil
	}
	if !snapshotRefPattern.MatchString(trimmed) {
		return "", actionConflictError{msg: fmt.Sprintf("invalid snapshot ref %q (expected @e1..@e200 or a CSS selector)", trimmed)}
	}
	if sc != nil && sc.snapshotRefs != nil {
		if tabMap := sc.snapshotRefs[tabID]; tabMap != nil {
			if sel := tabMap[trimmed]; sel != "" {
				return sel, nil
			}
		}
	}
	return "", actionConflictError{msg: fmt.Sprintf("snapshot ref %s not found or expired; call snapshot first", trimmed)}
}

func (sc *scope) performSnapshot(ctx context.Context) (map[string]any, error) {
	var snap any
	if err := sc.withTab(ctx, chromedp.Evaluate(snapshotJS, &snap)); err != nil {
		return nil, err
	}
	sc.recordSnapshotRefs(sc.tabID, snap)
	return map[string]any{"snapshot": snap}, nil
}

func (sc *scope) performDiagnostics() map[string]any {
	items := []Diagnostic{}
	if sc != nil && sc.diagnostics != nil {
		if d := sc.diagnostics[sc.tabID]; d != nil {
			items = d.snapshot(false)
		}
	}
	return map[string]any{"diagnostics": items}
}

func (sc *scope) performHover(ctx context.Context, a Action) error {
	if strings.TrimSpace(a.Selector) != "" {
		sel, err := sc.resolveSelector(sc.tabID, a.Selector)
		if err != nil {
			return err
		}
		var pt struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		expr := fmt.Sprintf(`(()=>{const e=document.querySelector(%q);if(!e)throw new Error("element not found");e.scrollIntoView({block:"center",inline:"center"});const r=e.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`, sel)
		return sc.withTab(ctx,
			chromedp.Evaluate(expr, &pt),
			chromedp.ActionFunc(func(tctx context.Context) error {
				return input.DispatchMouseEvent(input.MouseMoved, pt.X, pt.Y).Do(tctx)
			}),
		)
	}
	return sc.withTab(ctx, input.DispatchMouseEvent(input.MouseMoved, a.X, a.Y))
}

func (sc *scope) performType(ctx context.Context, a Action) error {
	tasks := chromedp.Tasks{}
	if strings.TrimSpace(a.Selector) != "" {
		sel, err := sc.resolveSelector(sc.tabID, a.Selector)
		if err != nil {
			return err
		}
		tasks = append(tasks, chromedp.Focus(sel, chromedp.ByQuery))
	}
	if a.Clear {
		const clearFocusedJS = `(()=>{
			const el=document.activeElement;
			if(!el)return;
			if(el instanceof HTMLInputElement){
				const d=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value');
				if(d&&d.set)d.set.call(el,'');else el.value='';
				el.dispatchEvent(new Event('input',{bubbles:true}));
			}else if(el instanceof HTMLTextAreaElement){
				const d=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value');
				if(d&&d.set)d.set.call(el,'');else el.value='';
				el.dispatchEvent(new Event('input',{bubbles:true}));
			}else if(el.isContentEditable){
				el.textContent='';
				el.dispatchEvent(new Event('input',{bubbles:true}));
			}
		})()`
		tasks = append(tasks, chromedp.Evaluate(clearFocusedJS, nil))
	}
	if a.Text != "" {
		tasks = append(tasks, input.InsertText(a.Text))
	}
	if strings.TrimSpace(a.Key) != "" {
		ka, err := keyAction(a.Key)
		if err != nil {
			return err
		}
		tasks = append(tasks, ka)
	}
	if len(tasks) == 0 {
		return sc.withTab(ctx)
	}
	return sc.withTab(ctx, tasks)
}

func (sc *scope) performSelect(ctx context.Context, a Action) (map[string]any, error) {
	sel, err := sc.resolveSelector(sc.tabID, a.Selector)
	if err != nil {
		return nil, err
	}
	selJSON, _ := json.Marshal(sel)
	wantJSON, _ := json.Marshal(a.Text)
	expr := fmt.Sprintf(`(()=>{
		const el=document.querySelector(%s);
		if(!el)throw new Error("element not found");
		if(!(el instanceof HTMLSelectElement))throw new Error("target element is not a <select>");
		const want=String(%s);
		const wantLower=want.trim().toLowerCase();
		const opts=Array.from(el.options);
		let hit=opts.find(o=>o.value===want)||opts.find(o=>(o.label||'').trim()===want.trim()||(o.text||'').trim()===want.trim())||opts.find(o=>o.value.toLowerCase()===wantLower||(o.label||'').trim().toLowerCase()===wantLower||(o.text||'').trim().toLowerCase()===wantLower);
		if(!hit)throw new Error("option not found in <select>: "+want);
		el.value=hit.value;
		el.dispatchEvent(new Event('input',{bubbles:true}));
		el.dispatchEvent(new Event('change',{bubbles:true}));
		return {selectedValue:hit.value,selectedText:(hit.label||hit.text||'').trim()};
	})()`, string(selJSON), string(wantJSON))
	var out map[string]any
	if err := sc.withTab(ctx, chromedp.Evaluate(expr, &out)); err != nil {
		return nil, err
	}
	return out, nil
}

func (sc *scope) performWait(ctx context.Context, a Action) (map[string]any, error) {
	start := time.Now()
	if strings.TrimSpace(a.Selector) == "" {
		durMs := a.TimeoutMs
		if durMs <= 0 {
			durMs = 1000
		}
		if durMs > 5000 {
			durMs = 5000
		}
		timer := time.NewTimer(time.Duration(durMs) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return map[string]any{"waitedMs": int(time.Since(start).Milliseconds())}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	sel, err := sc.resolveSelector(sc.tabID, a.Selector)
	if err != nil {
		return nil, err
	}
	durMs := a.TimeoutMs
	if durMs <= 0 {
		durMs = 2000
	}
	if durMs > 10000 {
		durMs = 10000
	}
	selJSON, _ := json.Marshal(sel)
	checkExpr := fmt.Sprintf(`(()=>{const e=document.querySelector(%s);return !!(e&&e.getClientRects().length&&getComputedStyle(e).visibility!=='hidden')})()`, string(selJSON))
	deadline := start.Add(time.Duration(durMs) * time.Millisecond)
	for {
		var matched bool
		if err := sc.withTab(ctx, chromedp.Evaluate(checkExpr, &matched)); err != nil {
			return nil, err
		}
		if matched {
			return map[string]any{"matched": true, "waitedMs": int(time.Since(start).Milliseconds())}, nil
		}
		if time.Now().After(deadline) {
			return map[string]any{"matched": false, "waitedMs": int(time.Since(start).Milliseconds())}, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
}
