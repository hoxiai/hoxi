package gateway

// compactBrowserSnapshotForModel returns a copy of a hoxi_browser action result
// whose snapshot controls omit CSS selectors. Both the desktop browser and the
// Chrome Link extension keep the @eN -> selector map on their side, so the model
// only needs the refs; long nth-of-type selector chains for up to 200 controls
// would otherwise be re-billed on every later step. The original result stays
// untouched for tool metadata.
func compactBrowserSnapshotForModel(parsed map[string]any) map[string]any {
	snap, ok := parsed["snapshot"].(map[string]any)
	if !ok {
		return parsed
	}
	controls, ok := snap["controls"].([]any)
	if !ok || len(controls) == 0 {
		return parsed
	}
	slim := make([]any, 0, len(controls))
	stripped := false
	for _, item := range controls {
		ctrl, isMap := item.(map[string]any)
		if !isMap {
			slim = append(slim, item)
			continue
		}
		ref, _ := ctrl["ref"].(string)
		if _, hasSelector := ctrl["selector"]; !hasSelector || ref == "" {
			// Without a ref the selector is the only usable handle.
			slim = append(slim, ctrl)
			continue
		}
		copyCtrl := make(map[string]any, len(ctrl))
		for k, v := range ctrl {
			if k != "selector" {
				copyCtrl[k] = v
			}
		}
		slim = append(slim, copyCtrl)
		stripped = true
	}
	if !stripped {
		return parsed
	}
	snapCopy := make(map[string]any, len(snap))
	for k, v := range snap {
		snapCopy[k] = v
	}
	snapCopy["controls"] = slim
	out := make(map[string]any, len(parsed))
	for k, v := range parsed {
		out[k] = v
	}
	out["snapshot"] = snapCopy
	return out
}
