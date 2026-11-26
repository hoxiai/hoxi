package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func isDisallowedCodeIndexPath(worktreeDir, rawPath string) bool {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return false
	}
	for _, seg := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	base := filepath.Clean(strings.TrimSpace(worktreeDir))
	target := trimmed
	if !filepath.IsAbs(target) && base != "" {
		target = filepath.Join(base, target)
	}
	target = filepath.Clean(target)
	if base != "" {
		rel, err := filepath.Rel(base, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
		slashRel := strings.ToLower(filepath.ToSlash(rel))
		if slashRel == ".vscode/settings.json" || strings.HasSuffix(slashRel, "/.vscode/settings.json") {
			return true
		}
		for _, part := range strings.Split(slashRel, "/") {
			switch part {
			case ".git", ".opencode", ".claude", ".hoxi", ".agents", ".gemini", ".envrc":
				return true
			}
		}
	}
	if hoxipaths.IsDeniedDocOrSpreadsheetPath(target) {
		return true
	}
	lowerBase := strings.ToLower(filepath.Base(target))
	if lowerBase == ".env" || strings.HasPrefix(lowerBase, ".env.") || lowerBase == ".envrc" {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(lowerBase, ext) {
			return true
		}
	}
	return false
}

func (g *Gateway) invokeNativeCodeIndexTool(ctx context.Context, tc agent.ToolContext, input json.RawMessage) (agent.HostToolResult, error) {
	if strings.TrimSpace(tc.WorktreeDir) == "" {
		return agent.HostToolResult{Title: "hoxi_code_index"}, fmt.Errorf("workspace directory scope is required for hoxi_code_index")
	}
	var req struct {
		Action       string `json:"action"`
		Query        string `json:"query"`
		Symbol       string `json:"symbol"`
		Path         string `json:"path"`
		FilePath     string `json:"filePath"`
		PathPrefix   string `json:"pathPrefix"`
		StartLine    int    `json:"startLine"`
		EndLine      int    `json:"endLine"`
		ContextLines *int   `json:"contextLines"`
		Depth        int    `json:"depth"`
		Limit        int    `json:"limit"`
		Refresh      bool   `json:"refresh"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return agent.HostToolResult{Title: "hoxi_code_index"}, fmt.Errorf("invalid hoxi_code_index arguments: %w", err)
	}
	act := strings.ToLower(strings.TrimSpace(req.Action))
	switch act {
	case "map", "context", "search", "inspect", "callers", "callees", "impact", "refresh":
	default:
		return agent.HostToolResult{Title: "hoxi_code_index"}, fmt.Errorf("invalid hoxi_code_index action %q (expected map|context|search|inspect|callers|callees|impact|refresh)", req.Action)
	}

	relPath := strings.TrimSpace(req.Path)
	if relPath == "" {
		relPath = strings.TrimSpace(req.FilePath)
	}
	if relPath == "" {
		relPath = strings.TrimSpace(req.PathPrefix)
	}
	if relPath != "" && isDisallowedCodeIndexPath(tc.WorktreeDir, relPath) {
		return agent.HostToolResult{Title: "CodeIndex: " + act}, fmt.Errorf("refusing to inspect protected or secret path: %s", relPath)
	}

	q := url.Values{"directory": {tc.WorktreeDir}}
	method := http.MethodGet
	var body any
	targetEndpoint := "/api/hoxi/code-index/" + act

	switch act {
	case "refresh":
		method = http.MethodPost
		body = map[string]string{"directory": tc.WorktreeDir}
	case "inspect":
		if relPath != "" {
			q.Set("path", relPath)
		}
		if s := strings.TrimSpace(req.Symbol); s != "" {
			q.Set("symbol", s)
		}
		if req.StartLine > 0 {
			q.Set("startLine", strconv.Itoa(req.StartLine))
		}
		if req.EndLine > 0 {
			q.Set("endLine", strconv.Itoa(req.EndLine))
		}
		if req.ContextLines != nil {
			q.Set("contextLines", strconv.Itoa(*req.ContextLines))
		}
	case "callers", "callees":
		if s := strings.TrimSpace(req.Symbol); s != "" {
			q.Set("symbol", s)
		}
		if relPath != "" {
			q.Set("path", relPath)
		}
		if req.Limit > 0 {
			q.Set("limit", strconv.Itoa(req.Limit))
		}
	case "impact":
		if s := strings.TrimSpace(req.Symbol); s != "" {
			q.Set("symbol", s)
		}
		if relPath != "" {
			q.Set("path", relPath)
		}
		if req.Depth > 0 {
			q.Set("depth", strconv.Itoa(req.Depth))
		}
		if req.Limit > 0 {
			q.Set("limit", strconv.Itoa(req.Limit))
		}
	case "map":
		if relPath != "" {
			q.Set("path", relPath)
		}
		if qry := strings.TrimSpace(req.Query); qry != "" {
			q.Set("q", qry)
		} else if s := strings.TrimSpace(req.Symbol); s != "" {
			q.Set("q", s)
		}
		if req.Limit > 0 {
			q.Set("limit", strconv.Itoa(req.Limit))
		}
		if req.Refresh {
			q.Set("refresh", "1")
		}
	case "context":
		if s := strings.TrimSpace(req.Symbol); s != "" {
			q.Set("symbol", s)
		}
		if qry := strings.TrimSpace(req.Query); qry != "" {
			q.Set("q", qry)
		}
		if relPath != "" {
			q.Set("path", relPath)
		}
		if req.ContextLines != nil {
			q.Set("contextLines", strconv.Itoa(*req.ContextLines))
		}
		if req.Limit > 0 {
			q.Set("limit", strconv.Itoa(req.Limit))
		}
	default: // search
		qry := strings.TrimSpace(req.Query)
		if qry == "" {
			qry = strings.TrimSpace(req.Symbol)
		}
		q.Set("q", qry)
		if relPath != "" {
			q.Set("path", relPath)
		}
		if req.Limit > 0 {
			q.Set("limit", strconv.Itoa(req.Limit))
		}
		if req.Refresh {
			q.Set("refresh", "1")
		}
	}

	status, raw, err := g.doInternalMuxJSON(ctx, method, targetEndpoint+"?"+q.Encode(), body, "")
	if err != nil {
		return agent.HostToolResult{Title: "CodeIndex: " + act}, err
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		txt := strings.TrimSpace(string(raw))
		if status >= 400 {
			return agent.HostToolResult{Title: "CodeIndex: " + act, Output: txt}, fmt.Errorf("hoxi_code_index HTTP %d: %s", status, txt)
		}
		return agent.HostToolResult{Title: "CodeIndex: " + act, Output: txt}, nil
	}
	if status >= 400 {
		errMsg, _ := parsed["error"].(string)
		if errMsg == "" {
			errMsg = strings.TrimSpace(string(raw))
		}
		return agent.HostToolResult{Title: "CodeIndex: " + act, Output: errMsg, Metadata: parsed}, fmt.Errorf("hoxi_code_index: %s", errMsg)
	}
	return agent.HostToolResult{
		Title:    "CodeIndex: " + act,
		Output:   formatCodeIndexHostOutput(act, parsed),
		Metadata: parsed,
	}, nil
}

func codeIndexCountHeading(label string, shown, total int) string {
	if shown < total {
		return fmt.Sprintf("%s (showing %d of %d):", label, shown, total)
	}
	return fmt.Sprintf("%s (%d):", label, shown)
}

func formatCodeIndexHostOutput(action string, payload map[string]any) string {
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	num := func(m map[string]any, k string) int {
		if f, ok := m[k].(float64); ok {
			return int(f)
		}
		return 0
	}
	sliceMaps := func(v any) []map[string]any {
		arr, _ := v.([]any)
		out := make([]map[string]any, 0, len(arr))
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	ambiguity := func(hit map[string]any) string {
		if v, _ := hit["possible"].(bool); v {
			return " [possible target]"
		}
		return ""
	}
	fmtHit := func(h map[string]any) string {
		sym := ""
		if s := str(h, "symbol"); s != "" {
			sym = " [" + s + "]"
		}
		return fmt.Sprintf("  %s:%d%s — %s", str(h, "path"), num(h, "line"), sym+ambiguity(h), str(h, "snippet"))
	}
	fmtCallee := func(c map[string]any) string {
		return fmt.Sprintf("  %s:%d-%d [%s] (called at L%d)", str(c, "path"), num(c, "startLine"), num(c, "endLine"), str(c, "symbol"), num(c, "callLine")) + ambiguity(c)
	}

	switch action {
	case "inspect":
		if content, ok := payload["content"].(string); ok && str(payload, "path") != "" {
			sym := ""
			if s := str(payload, "symbol"); s != "" {
				sym = " [" + s + "]"
			}
			trunc := ""
			if b, _ := payload["truncated"].(bool); b {
				trunc = " [truncated]"
			}
			return fmt.Sprintf("# %s:%d-%d (of %d)%s%s\n%s", str(payload, "path"), num(payload, "startLine"), num(payload, "endLine"), num(payload, "totalLines"), sym, trunc, content)
		}
	case "search":
		if _, has := payload["results"]; has {
			hits := sliceMaps(payload["results"])
			if len(hits) == 0 {
				return "No matches found."
			}
			lines := make([]string, 0, len(hits))
			for _, r := range hits {
				sym := ""
				if s := str(r, "symbol"); s != "" {
					sym = " [" + s + "]"
				}
				score, _ := r["score"].(float64)
				snip := strings.TrimSpace(str(r, "snippet"))
				if snip != "" {
					snip = "\n  " + strings.ReplaceAll(snip, "\n", "\n  ")
				}
				lines = append(lines, fmt.Sprintf("%s:%d-%d%s (score %.2f)%s", str(r, "path"), num(r, "startLine"), num(r, "endLine"), sym, score, snip))
			}
			return strings.Join(lines, "\n")
		}
	case "callers":
		callers := sliceMaps(payload["callers"])
		tests := sliceMaps(payload["tests"])
		var lines []string
		lines = append(lines, codeIndexCountHeading("Callers", len(callers), num(payload, "totalCallers")))
		if len(callers) == 0 {
			lines = append(lines, "  (none)")
		} else {
			for _, h := range callers {
				lines = append(lines, fmtHit(h))
			}
		}
		lines = append(lines, codeIndexCountHeading("Tests", len(tests), num(payload, "totalTests")))
		if len(tests) == 0 {
			lines = append(lines, "  (none)")
		} else {
			for _, h := range tests {
				lines = append(lines, fmtHit(h))
			}
		}
		return strings.Join(lines, "\n")
	case "callees":
		callees := sliceMaps(payload["callees"])
		var b strings.Builder
		if impRaw, ok := payload["imports"].([]any); ok && len(impRaw) > 0 {
			imps := make([]string, 0, len(impRaw))
			for _, v := range impRaw {
				if s, ok := v.(string); ok && s != "" {
					imps = append(imps, s)
				}
			}
			if len(imps) > 0 {
				b.WriteString("Imports: " + strings.Join(imps, ", ") + "\n")
			}
		}
		b.WriteString(codeIndexCountHeading("Callees", len(callees), num(payload, "totalCallees")) + "\n")
		if len(callees) == 0 {
			b.WriteString("  (none)")
		} else {
			rows := make([]string, 0, len(callees))
			for _, c := range callees {
				rows = append(rows, fmtCallee(c))
			}
			b.WriteString(strings.Join(rows, "\n"))
		}
		return b.String()
	case "impact":
		direct := sliceMaps(payload["directCallers"])
		trans := sliceMaps(payload["transitiveCallers"])
		tests := sliceMaps(payload["tests"])
		var lines []string
		lines = append(lines, codeIndexCountHeading("Direct Callers", len(direct), num(payload, "totalCallers")))
		if len(direct) == 0 {
			lines = append(lines, "  (none)")
		} else {
			for _, h := range direct {
				lines = append(lines, fmtHit(h))
			}
		}
		if len(trans) > 0 {
			lines = append(lines, codeIndexCountHeading("Transitive Callers", len(trans), num(payload, "totalTransitiveCallers")))
			for _, h := range trans {
				sym := ""
				if s := str(h, "symbol"); s != "" {
					sym = " [" + s + "]"
				}
				lines = append(lines, fmt.Sprintf("  %s:%d%s (depth %d, via %s) — %s", str(h, "path"), num(h, "line"), sym+ambiguity(h), num(h, "depth"), str(h, "via"), str(h, "snippet")))
			}
		}
		lines = append(lines, codeIndexCountHeading("Related Tests", len(tests), num(payload, "totalTests")))
		if len(tests) == 0 {
			lines = append(lines, "  (none)")
		} else {
			for _, h := range tests {
				lines = append(lines, fmtHit(h))
			}
		}
		return strings.Join(lines, "\n")
	case "context":
		if insp, ok := payload["inspect"].(map[string]any); ok && str(insp, "path") != "" {
			sym := ""
			if s := str(insp, "symbol"); s != "" {
				sym = " [" + s + "]"
			}
			trunc := ""
			if b, _ := insp["truncated"].(bool); b {
				trunc = " [truncated]"
			}
			callees := sliceMaps(payload["callees"])
			callers := sliceMaps(payload["callers"])
			trans := sliceMaps(payload["transitiveCallers"])
			tests := sliceMaps(payload["tests"])
			lines := []string{
				fmt.Sprintf("# %s:%d-%d (of %d)%s%s", str(insp, "path"), num(insp, "startLine"), num(insp, "endLine"), num(insp, "totalLines"), sym, trunc),
				str(insp, "content"),
				codeIndexCountHeading("Callees", len(callees), num(payload, "totalCallees")),
			}
			if len(callees) == 0 {
				lines = append(lines, "  (none)")
			} else {
				for _, c := range callees {
					lines = append(lines, fmtCallee(c))
				}
			}
			lines = append(lines, codeIndexCountHeading("Callers", len(callers), num(payload, "totalCallers")))
			if len(callers) == 0 {
				lines = append(lines, "  (none)")
			} else {
				for _, h := range callers {
					lines = append(lines, fmtHit(h))
				}
			}
			if len(trans) > 0 {
				lines = append(lines, codeIndexCountHeading("Transitive Callers", len(trans), num(payload, "totalTransitiveCallers")))
				for _, h := range trans {
					hsym := ""
					if s := str(h, "symbol"); s != "" {
						hsym = " [" + s + "]"
					}
					lines = append(lines, fmt.Sprintf("  %s:%d%s (depth %d, via %s)", str(h, "path"), num(h, "line"), hsym+ambiguity(h), num(h, "depth"), str(h, "via")))
				}
			}
			lines = append(lines, codeIndexCountHeading("Tests", len(tests), num(payload, "totalTests")))
			if len(tests) == 0 {
				lines = append(lines, "  (none)")
			} else {
				for _, h := range tests {
					lines = append(lines, fmtHit(h))
				}
			}
			return strings.Join(lines, "\n")
		}
	case "map":
		entries := sliceMaps(payload["entries"])
		if len(entries) == 0 {
			return "No indexed code files found for map."
		}
		lines := make([]string, 0, len(entries)+1)
		hdr := fmt.Sprintf("Repository Backbone Map (%d top of %d code files):", len(entries), num(payload, "files"))
		if pfx := str(payload, "pathPrefix"); pfx != "" {
			hdr = fmt.Sprintf("Repository Backbone Map [%s] (%d files):", pfx, len(entries))
		}
		lines = append(lines, hdr)
		for _, e := range entries {
			lines = append(lines, fmt.Sprintf("%s (rank %d)", str(e, "path"), num(e, "score")))
			for _, sym := range sliceMaps(e["symbols"]) {
				refNote := ""
				if r := num(sym, "references"); r > 0 {
					refNote = fmt.Sprintf(" [%d refs]", r)
				}
				sig := str(sym, "signature")
				if sig == "" {
					sig = str(sym, "symbol")
				}
				lines = append(lines, fmt.Sprintf("  L%d-%d%s: %s", num(sym, "startLine"), num(sym, "endLine"), refNote, sig))
			}
		}
		return strings.Join(lines, "\n")
	case "refresh":
		if _, hasFiles := payload["files"]; hasFiles {
			return fmt.Sprintf("Index refreshed (%d files, %d chunks).", num(payload, "files"), num(payload, "chunks"))
		}
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func (g *Gateway) invokeNativeImageTool(ctx context.Context, tc agent.ToolContext, input json.RawMessage) (agent.HostToolResult, error) {
	var payload map[string]any
	_ = json.Unmarshal(input, &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["sessionID"] = tc.SessionID
	payload["directory"] = tc.WorktreeDir
	dirQ := url.Values{}
	if tc.WorktreeDir != "" {
		dirQ.Set("directory", tc.WorktreeDir)
	}
	status, raw, err := g.doInternalMuxJSON(ctx, http.MethodPost, "/api/hoxi/media/image?"+dirQ.Encode(), payload, "")
	if err != nil {
		return agent.HostToolResult{Title: "hoxi_image"}, err
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		txt := strings.TrimSpace(string(raw))
		if status >= 400 {
			return agent.HostToolResult{Title: "hoxi_image", Output: txt}, fmt.Errorf("hoxi_image HTTP %d: %s", status, txt)
		}
		return agent.HostToolResult{Title: "hoxi_image", Output: txt}, nil
	}
	if status >= 400 {
		errMsg, _ := parsed["error"].(string)
		if errMsg == "" {
			errMsg = strings.TrimSpace(string(raw))
		}
		return agent.HostToolResult{Title: "hoxi_image", Output: errMsg, Metadata: parsed}, fmt.Errorf("hoxi_image: %s", errMsg)
	}
	if okVal, hasOK := parsed["ok"].(bool); hasOK && !okVal {
		errMsg, _ := parsed["error"].(string)
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "Image generation failed"
		}
		return agent.HostToolResult{Title: "hoxi_image", Output: errMsg, Metadata: parsed}, fmt.Errorf("hoxi_image: %s", errMsg)
	}

	provID, _ := parsed["providerID"].(string)
	modID, _ := parsed["modelID"].(string)
	note, _ := parsed["text"].(string)
	rawImages, _ := parsed["images"].([]any)

	count := len(rawImages)
	if count == 0 {
		count = 1
	}
	lines := []string{fmt.Sprintf("Generated %d image(s) via %s/%s.", count, provID, modID)}
	if strings.TrimSpace(note) != "" {
		lines = append(lines, "Model note: "+strings.TrimSpace(note))
	}
	var mediaAtts []map[string]any
	for _, it := range rawImages {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		pStr, _ := m["path"].(string)
		fStr, _ := m["filename"].(string)
		uStr, _ := m["url"].(string)
		mimeStr, _ := m["mime"].(string)
		if mimeStr == "" {
			mimeStr = "image/png"
		}
		if fStr == "" {
			fStr = "generated.png"
		}
		disp := pStr
		if disp == "" {
			disp = fStr
		}
		lines = append(lines, fmt.Sprintf("- File: %s (attachment URL: %s)", disp, uStr))
		if strings.TrimSpace(uStr) != "" {
			mediaAtts = append(mediaAtts, map[string]any{
				"type":     "file",
				"mime":     mimeStr,
				"filename": fStr,
				"url":      uStr,
			})
		}
	}
	if len(mediaAtts) > 0 {
		parsed["mediaAttachments"] = mediaAtts
	}
	titleModel := strings.TrimSpace(modID)
	if titleModel == "" {
		titleModel = "generated"
	}
	return agent.HostToolResult{
		Title:    "HoxiImage: " + titleModel,
		Output:   strings.Join(lines, "\n"),
		Metadata: parsed,
	}, nil
}

func (g *Gateway) invokeNativeVideoTool(ctx context.Context, tc agent.ToolContext, input json.RawMessage) (agent.HostToolResult, error) {
	var payload map[string]any
	_ = json.Unmarshal(input, &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["sessionID"] = tc.SessionID
	payload["directory"] = tc.WorktreeDir
	dirQ := url.Values{}
	if tc.WorktreeDir != "" {
		dirQ.Set("directory", tc.WorktreeDir)
	}
	status, raw, err := g.doInternalMuxJSON(ctx, http.MethodPost, "/api/hoxi/media/video?"+dirQ.Encode(), payload, "")
	if err != nil {
		return agent.HostToolResult{Title: "hoxi_video"}, err
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		txt := strings.TrimSpace(string(raw))
		if status >= 400 {
			return agent.HostToolResult{Title: "hoxi_video", Output: txt}, fmt.Errorf("hoxi_video HTTP %d: %s", status, txt)
		}
		return agent.HostToolResult{Title: "hoxi_video", Output: txt}, nil
	}
	if status >= 400 {
		errMsg, _ := parsed["error"].(string)
		if errMsg == "" {
			errMsg = strings.TrimSpace(string(raw))
		}
		return agent.HostToolResult{Title: "hoxi_video", Output: errMsg, Metadata: parsed}, fmt.Errorf("hoxi_video: %s", errMsg)
	}
	if okVal, hasOK := parsed["ok"].(bool); hasOK && !okVal {
		errMsg, _ := parsed["error"].(string)
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "Video generation failed"
		}
		return agent.HostToolResult{Title: "hoxi_video", Output: errMsg, Metadata: parsed}, fmt.Errorf("hoxi_video: %s", errMsg)
	}

	provID, _ := parsed["providerID"].(string)
	modID, _ := parsed["modelID"].(string)
	note, _ := parsed["text"].(string)
	rawVideos, _ := parsed["videos"].([]any)

	count := len(rawVideos)
	if count == 0 {
		count = 1
	}
	lines := []string{fmt.Sprintf("Generated %d video(s) via %s/%s.", count, provID, modID)}
	if strings.TrimSpace(note) != "" {
		lines = append(lines, "Model note: "+strings.TrimSpace(note))
	}
	var mediaAtts []map[string]any
	for _, it := range rawVideos {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		pStr, _ := m["path"].(string)
		fStr, _ := m["filename"].(string)
		uStr, _ := m["url"].(string)
		mimeStr, _ := m["mime"].(string)
		if mimeStr == "" {
			mimeStr = "video/mp4"
		}
		if fStr == "" {
			fStr = "generated.mp4"
		}
		disp := pStr
		if disp == "" {
			disp = fStr
		}
		lines = append(lines, fmt.Sprintf("- File: %s (attachment URL: %s)", disp, uStr))
		if strings.TrimSpace(uStr) != "" {
			mediaAtts = append(mediaAtts, map[string]any{
				"type":     "file",
				"mime":     mimeStr,
				"filename": fStr,
				"url":      uStr,
			})
		}
	}
	if len(mediaAtts) > 0 {
		parsed["mediaAttachments"] = mediaAtts
	}
	titleModel := strings.TrimSpace(modID)
	if titleModel == "" {
		titleModel = "generated"
	}
	return agent.HostToolResult{
		Title:    "HoxiVideo: " + titleModel,
		Output:   strings.Join(lines, "\n"),
		Metadata: parsed,
	}, nil
}

func (g *Gateway) invokeNativeSpeechTool(ctx context.Context, tc agent.ToolContext, input json.RawMessage) (agent.HostToolResult, error) {
	var payload map[string]any
	_ = json.Unmarshal(input, &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["sessionID"] = tc.SessionID
	payload["directory"] = tc.WorktreeDir
	dirQ := url.Values{}
	if tc.WorktreeDir != "" {
		dirQ.Set("directory", tc.WorktreeDir)
	}
	status, raw, err := g.doInternalMuxJSON(ctx, http.MethodPost, "/api/hoxi/media/speech?"+dirQ.Encode(), payload, "")
	if err != nil {
		return agent.HostToolResult{Title: "hoxi_speech"}, err
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		txt := strings.TrimSpace(string(raw))
		if status >= 400 {
			return agent.HostToolResult{Title: "hoxi_speech", Output: txt}, fmt.Errorf("hoxi_speech HTTP %d: %s", status, txt)
		}
		return agent.HostToolResult{Title: "hoxi_speech", Output: txt}, nil
	}
	if status >= 400 {
		errMsg, _ := parsed["error"].(string)
		if errMsg == "" {
			errMsg = strings.TrimSpace(string(raw))
		}
		return agent.HostToolResult{Title: "hoxi_speech", Output: errMsg, Metadata: parsed}, fmt.Errorf("hoxi_speech: %s", errMsg)
	}
	if okVal, hasOK := parsed["ok"].(bool); hasOK && !okVal {
		errMsg, _ := parsed["error"].(string)
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "Speech synthesis failed"
		}
		return agent.HostToolResult{Title: "hoxi_speech", Output: errMsg, Metadata: parsed}, fmt.Errorf("hoxi_speech: %s", errMsg)
	}

	provID, _ := parsed["providerID"].(string)
	modID, _ := parsed["modelID"].(string)
	rawAudios, _ := parsed["audios"].([]any)
	lines := []string{fmt.Sprintf("Synthesized speech audio via %s/%s.", provID, modID)}
	var mediaAtts []map[string]any
	for _, it := range rawAudios {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		pStr, _ := m["path"].(string)
		fStr, _ := m["filename"].(string)
		uStr, _ := m["url"].(string)
		mimeStr, _ := m["mime"].(string)
		if mimeStr == "" {
			mimeStr = "audio/mpeg"
		}
		if fStr == "" {
			fStr = "generated.mp3"
		}
		disp := pStr
		if disp == "" {
			disp = fStr
		}
		lines = append(lines, fmt.Sprintf("- File: %s (attachment URL: %s)", disp, uStr))
		if strings.TrimSpace(uStr) != "" {
			mediaAtts = append(mediaAtts, map[string]any{
				"type":     "file",
				"mime":     mimeStr,
				"filename": fStr,
				"url":      uStr,
			})
		}
	}
	if len(mediaAtts) > 0 {
		parsed["mediaAttachments"] = mediaAtts
	}
	titleModel := strings.TrimSpace(modID)
	if titleModel == "" {
		titleModel = "generated"
	}
	return agent.HostToolResult{
		Title:    "HoxiSpeech: " + titleModel,
		Output:   strings.Join(lines, "\n"),
		Metadata: parsed,
	}, nil
}
