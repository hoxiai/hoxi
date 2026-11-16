package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// HostEvalResult holds the JSON string returned by evaluating a trusted,
// host-owned script inside a browser tab or child frame.
type HostEvalResult struct {
	TabID   string `json:"tabId"`
	TabURL  string `json:"tabUrl"`
	FrameID string `json:"frameId,omitempty"`
	Value   string `json:"value"`
}

func (s *Service) resolveHostScope(sessionID, directory, storeID string) *scope {
	sessionID = strings.TrimSpace(sessionID)
	directory = strings.TrimSpace(directory)
	storeID = strings.TrimSpace(storeID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}

	resolvedSession := sessionID
	if resolvedSession == "" && storeID != "" && sessionPattern.MatchString("workstore_"+storeID) {
		resolvedSession = "workstore_" + storeID
	}
	if resolvedSession != "" && s.resolveSession != nil && directory != "" {
		if cleanDir, err := filepath.EvalSymlinks(filepath.Clean(directory)); err == nil {
			if st, statErr := os.Stat(cleanDir); statErr == nil && st.IsDir() {
				directory = cleanDir
				if next, rErr := s.resolveSession(resolvedSession, directory); rErr == nil && sessionPattern.MatchString(next) {
					resolvedSession = next
				}
			}
		}
	}

	compatible := func(sc *scope) bool {
		if sc == nil || sc.closed || (sc.browser == nil && sc.extension == nil) {
			return false
		}
		if storeID != "" && sc.env.StoreID != "" && sc.env.StoreID != storeID {
			return false
		}
		return true
	}

	if resolvedSession != "" && directory != "" {
		if exact := s.scopes[scopeKey{directory: directory, session: resolvedSession}]; compatible(exact) {
			return exact
		}
	}

	unified := resolvedSession
	profKey := ""
	if s.resolveProfile != nil && resolvedSession != "" {
		if profKey = strings.TrimSpace(s.resolveProfile(resolvedSession, directory)); profKey != "" {
			if !(profKey == "default" && !s.defaultWindow) && sessionPattern.MatchString("workstore_"+profKey) {
				unified = "workstore_" + profKey
			}
		}
	}

	for k, sc := range s.scopes {
		if !compatible(sc) {
			continue
		}
		if unified != "" && k.session == unified {
			return sc
		}
		if sessionID != "" && k.session == sessionID {
			return sc
		}
	}

	if storeID != "" {
		for _, sc := range s.scopes {
			if compatible(sc) && sc.env.StoreID == storeID {
				return sc
			}
		}
	}

	// Only match workstore_default when the resolved profile explicitly maps to "default".
	if profKey == "default" || sessionID == "workstore_default" {
		for k, sc := range s.scopes {
			if compatible(sc) && k.session == "workstore_default" {
				return sc
			}
		}
	}
	return nil
}

func (sc *scope) ensurePageTabContext(ctx context.Context, tabID string) (context.Context, error) {
	if tabID == "" {
		return nil, errors.New("empty tab id")
	}
	if existing := sc.tabs[tabID]; existing != nil {
		return existing, nil
	}
	tab, cancel := chromedp.NewContext(sc.browser, chromedp.WithTargetID(target.ID(tabID)))
	done := make(chan error, 1)
	go func() {
		initActions := sc.emulationActions()
		if !sc.window {
			initActions = append([]chromedp.Action{sc.viewport().action(), page.SetInterceptFileChooserDialog(true)}, initActions...)
		}
		done <- chromedp.Run(tab, initActions...)
	}()
	select {
	case err := <-done:
		if err != nil {
			cancel()
			return nil, err
		}
	case <-ctx.Done():
		cancel()
		<-done
		return nil, ctx.Err()
	}
	sc.tabs[tabID] = tab
	sc.trackNavigation(tab, tabID)
	sc.tabCancels[tabID] = cancel
	sc.viewports[tabID] = sc.viewport()
	return tab, nil
}

// EvaluateHostScript executes a server-owned synchronous JS expression on the
// store's isolated browser (both Hoxi-managed Chrome and Chrome Link extension),
// without exposing arbitrary UI evaluate privileges or blocking on manual takeover.
func (s *Service) EvaluateHostScript(
	ctx context.Context,
	sessionID, directory, storeID, expression string,
	scanAllTabs bool,
) ([]HostEvalResult, State, error) {
	if strings.TrimSpace(expression) == "" {
		return nil, emptyState(), errors.New("empty host script")
	}
	sc := s.resolveHostScope(sessionID, directory, storeID)
	if sc == nil {
		return nil, emptyState(), nil
	}

	select {
	case sc.gate <- struct{}{}:
		if sc.closed {
			<-sc.gate
			return nil, emptyState(), nil
		}
	case <-ctx.Done():
		return nil, emptyState(), ctx.Err()
	}
	defer func() {
		sc.lastUsed = s.now()
		<-sc.gate
	}()

	if sc.extension != nil {
		res, err := sc.extension.call(ctx, Action{Action: "evaluate", Text: expression})
		if err != nil {
			return nil, State{Mode: "chrome", Manual: sc.manual}, err
		}
		raw, _ := res["result"].(string)
		urlStr, _ := res["url"].(string)
		titleStr, _ := res["title"].(string)
		tabID, _ := res["tabID"].(string)
		st := State{
			Mode:   "chrome",
			Manual: sc.manual,
			URL:    urlStr,
			Title:  titleStr,
			TabID:  tabID,
			Tabs:   []Tab{{ID: tabID, Title: titleStr, URL: urlStr}},
		}
		var out []HostEvalResult
		if strings.TrimSpace(raw) != "" {
			out = append(out, HostEvalResult{TabID: tabID, TabURL: urlStr, Value: raw})
		}
		return out, st, nil
	}

	if sc.browser == nil || sc.browser.Err() != nil {
		return nil, emptyState(), nil
	}

	opctx, opcancel := context.WithTimeout(sc.browser, 18*time.Second)
	stop := context.AfterFunc(ctx, opcancel)
	defer stop()
	defer opcancel()

	st, err := sc.state(opctx)
	if err != nil {
		return nil, emptyState(), err
	}
	if len(st.Tabs) == 0 {
		return nil, st, nil
	}

	var orderedTabs []Tab
	for _, t := range st.Tabs {
		if t.ID == st.TabID {
			orderedTabs = append(orderedTabs, t)
			break
		}
	}
	if scanAllTabs {
		for _, t := range st.Tabs {
			if t.ID == st.TabID {
				continue
			}
			u := strings.TrimSpace(t.URL)
			if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				orderedTabs = append(orderedTabs, t)
			}
		}
	} else if len(orderedTabs) == 0 {
		orderedTabs = append(orderedTabs, st.Tabs[0])
	}

	var results []HostEvalResult
	for _, t := range orderedTabs {
		tabCtx, attachErr := sc.ensurePageTabContext(opctx, t.ID)
		if attachErr != nil {
			continue
		}
		tabOp, tabCancel := context.WithTimeout(tabCtx, 7*time.Second)
		tabStop := context.AfterFunc(opctx, tabCancel)

		var mainVal string
		if evalErr := chromedp.Run(tabOp, chromedp.Evaluate(expression, &mainVal)); evalErr == nil && strings.TrimSpace(mainVal) != "" {
			results = append(results, HostEvalResult{TabID: t.ID, TabURL: t.URL, Value: mainVal})
		}

		_ = chromedp.Run(tabOp, chromedp.ActionFunc(func(c context.Context) error {
			tree, treeErr := page.GetFrameTree().Do(c)
			if treeErr != nil || tree == nil {
				return nil
			}
			var frames []*cdp.Frame
			var walk func(nodes []*page.FrameTree)
			walk = func(nodes []*page.FrameTree) {
				for _, node := range nodes {
					if node == nil || len(frames) >= 8 {
						return
					}
					if node.Frame != nil {
						u := strings.TrimSpace(node.Frame.URL)
						if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
							frames = append(frames, node.Frame)
						}
					}
					walk(node.ChildFrames)
				}
			}
			walk(tree.ChildFrames)
			for _, fr := range frames {
				worldID, wErr := page.CreateIsolatedWorld(fr.ID).WithWorldName("hoxi_catalog").Do(c)
				if wErr != nil || worldID == 0 {
					continue
				}
				rem, exp, eErr := cdpruntime.Evaluate(expression).WithContextID(worldID).WithReturnByValue(true).Do(c)
				if eErr != nil || exp != nil || rem == nil || rem.Type != cdpruntime.TypeString || len(rem.Value) == 0 {
					continue
				}
				var frameVal string
				if json.Unmarshal(rem.Value, &frameVal) == nil && strings.TrimSpace(frameVal) != "" {
					results = append(results, HostEvalResult{
						TabID:   t.ID,
						TabURL:  fr.URL,
						FrameID: string(fr.ID),
						Value:   frameVal,
					})
				}
			}
			return nil
		}))

		tabStop()
		tabCancel()
	}

	return results, st, nil
}
