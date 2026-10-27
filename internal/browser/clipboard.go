package browser

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

//go:embed clipboard_page.js
var clipboardPageJS string

// The fixed script only reads the current selection/point. OS clipboard access
// belongs to explicit desktop UI gestures, never the service or agent tools.
func (sc *scope) clipboard(ctx context.Context, a Action) (map[string]any, error) {
	var result map[string]any
	expression := fmt.Sprintf("(%s)(%q,%g,%g)", clipboardPageJS, a.Action, a.X, a.Y)
	err := sc.withTab(ctx, chromedp.Evaluate(expression, &result))
	if err == nil && a.Action == "paste" && result["clipboardError"] == nil {
		err = sc.withTab(ctx, input.InsertText(a.Text))
	}
	return result, err
}
