package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hoxiai/hoxi/internal/agent"
	"github.com/hoxiai/hoxi/internal/dataprofiles"
	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/skills"
	"github.com/hoxiai/hoxi/internal/termhost"
)

// TerminalHandler is the narrow interface implemented by internal/termhost.Service.
type TerminalHandler interface {
	http.Handler
	CloseAll()
}

type nativeHostState struct {
	termSvc        TerminalHandler
	dataProfileSvc DataProfileHandler
	dataProfileMgr *dataprofiles.Service
	remoteHostMgr  interface {
		RegisterRoutes(mux *http.ServeMux, isRemote func(*http.Request) bool)
	}
}

// DataProfileHandler is the narrow interface implemented by internal/dataprofiles.Service.
type DataProfileHandler interface {
	InvokeJSON(ctx context.Context, raw json.RawMessage) (map[string]any, error)
}

// SetDataProfileService attaches the credential-isolated endpoint profile handler.
func (g *Gateway) SetDataProfileService(dp DataProfileHandler) {
	g.originMu.Lock()
	g.dataProfileSvc = dp
	g.originMu.Unlock()
}

func (g *Gateway) initNativeHost() {
	g.termSvc = termhost.NewDefaultService()
	dp := dataprofiles.NewService(hoxipaths.DataProfilesPath(), nil)
	g.dataProfileSvc = dp
	g.dataProfileMgr = dp
	g.remoteHostMgr = newDefaultRemoteHostService()
	if g.runner != nil {
		g.runner.SetDataProfileSafetyChecker(func(profileID string) (string, bool, bool) {
			pub, ok := dp.GetPublic(profileID)
			if !ok {
				return "", false, false
			}
			return pub.Kind, pub.ReadOnly, true
		})
	}
}

// SetTerminalService attaches the native terminal handler (/pty HTTP + WebSocket).
func (g *Gateway) SetTerminalService(ts TerminalHandler) {
	g.originMu.Lock()
	g.termSvc = ts
	g.originMu.Unlock()
}

func (g *Gateway) closeNativeHost() {
	g.originMu.RLock()
	ts := g.termSvc
	g.originMu.RUnlock()
	if ts != nil {
		ts.CloseAll()
	}
}

func (g *Gateway) doInternalMuxJSON(ctx context.Context, method, targetPath string, body any, bearerToken string) (int, []byte, error) {
	var rawBody []byte
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rawBody = b
		case json.RawMessage:
			rawBody = b
		default:
			var err error
			rawBody, err = json.Marshal(body)
			if err != nil {
				return 0, nil, err
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, targetPath, bytes.NewReader(rawBody))
	if err != nil {
		return 0, nil, err
	}
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hoxi-Agent", "true")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	rec := httptest.NewRecorder()
	g.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), nil
}

func extractHostActionResult(toolName string, status int, raw []byte) (agent.HostToolResult, error) {
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		txt := strings.TrimSpace(string(raw))
		if status >= 400 {
			return agent.HostToolResult{Title: toolName, Output: txt}, fmt.Errorf("%s HTTP %d: %s", toolName, status, txt)
		}
		return agent.HostToolResult{Title: toolName, Output: txt}, nil
	}
	if status >= 400 {
		errMsg, _ := parsed["error"].(string)
		if errMsg == "" {
			errMsg = strings.TrimSpace(string(raw))
		}
		return agent.HostToolResult{Title: toolName, Output: errMsg, Metadata: parsed}, fmt.Errorf("%s: %s", toolName, errMsg)
	}

	var media []agent.ContentBlock
	if imgB64, ok := parsed["image"].(string); ok && strings.TrimSpace(imgB64) != "" {
		trimmed := strings.TrimSpace(imgB64)
		mime, ext := detectBase64ImageMime(trimmed)
		media = append(media, agent.ContentBlock{
			Type:     agent.ContentMedia,
			MimeType: mime,
			Data:     trimmed,
			Filename: toolName + ext,
		})
		delete(parsed, "image")
	}
	cleanRaw, _ := json.Marshal(parsed)
	return agent.HostToolResult{
		Title:    toolName,
		Output:   string(cleanRaw),
		Metadata: parsed,
		Media:    media,
	}, nil
}

func detectBase64ImageMime(b64 string) (string, string) {
	switch {
	case strings.HasPrefix(b64, "iVBORw0KGgo"):
		return "image/png", ".png"
	case strings.HasPrefix(b64, "UklGR"):
		return "image/webp", ".webp"
	case strings.HasPrefix(b64, "R0lGOD"):
		return "image/gif", ".gif"
	default:
		return "image/jpeg", ".jpg"
	}
}

// normalizeComputerAgentInput losslessly coerces common model JSON type slips
// (e.g. integer windowID -> string, digit-string elementID -> integer, whole-number
// float deltaX/deltaY/clickCount -> integer) while preserving any genuinely
// invalid or unknown fields so decodeComputerJSON still rejects them.
func normalizeComputerAgentInput(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return raw
	}
	changed := false
	if v, ok := obj["windowID"]; ok {
		if num, isNum := v.(json.Number); isNum {
			if _, err := num.Int64(); err == nil {
				obj["windowID"] = num.String()
				changed = true
			}
		}
	}
	for _, intField := range []string{"elementID", "deltaX", "deltaY", "clickCount"} {
		v, ok := obj[intField]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
				obj[intField] = n
				changed = true
			}
		case json.Number:
			if _, err := val.Int64(); err != nil {
				if f, fErr := val.Float64(); fErr == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) {
					obj[intField] = int64(f)
					changed = true
				}
			}
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

func (g *Gateway) invokeNativeHostTool(
	ctx context.Context,
	tc agent.ToolContext,
	toolName string,
	input json.RawMessage,
) (agent.HostToolResult, error) {
	q := url.Values{}
	if tc.SessionID != "" {
		q.Set("sessionID", tc.SessionID)
	}
	if tc.WorktreeDir != "" {
		q.Set("directory", tc.WorktreeDir)
	}

	switch toolName {
	case "hoxi_browser":
		g.originMu.RLock()
		tok := g.browserAgentToken
		g.originMu.RUnlock()
		status, raw, err := g.doInternalMuxJSON(ctx, http.MethodPost, "/api/hoxi/browser/action?"+q.Encode(), input, tok)
		if err != nil {
			return agent.HostToolResult{Title: toolName}, err
		}
		return extractHostActionResult(toolName, status, raw)

	case "hoxi_computer":
		g.originMu.RLock()
		tok := g.computerAgentToken
		g.originMu.RUnlock()
		status, raw, err := g.doInternalMuxJSON(ctx, http.MethodPost, "/api/hoxi/computer/action?"+q.Encode(), normalizeComputerAgentInput(input), tok)
		if err != nil {
			return agent.HostToolResult{Title: toolName}, err
		}
		return extractHostActionResult(toolName, status, raw)

	case "hoxi_code_index":
		return g.invokeNativeCodeIndexTool(ctx, tc, input)

	case "hoxi_image":
		return g.invokeNativeImageTool(ctx, tc, input)

	case "hoxi_video":
		return g.invokeNativeVideoTool(ctx, tc, input)

	case "hoxi_speech":
		return g.invokeNativeSpeechTool(ctx, tc, input)

	case "hoxi_tools":
		var payload map[string]any
		_ = json.Unmarshal(input, &payload)
		act, _ := payload["action"].(string)
		act = strings.ToLower(strings.TrimSpace(act))
		if act != "discover" && act != "inspect" && act != "run" {
			return agent.HostToolResult{Title: toolName}, fmt.Errorf("invalid hoxi_tools action %q", act)
		}
		status, raw, err := g.doInternalMuxJSON(ctx, http.MethodPost, "/api/hoxi/tools/"+act, payload, "")
		if err != nil {
			return agent.HostToolResult{Title: toolName}, err
		}
		return extractHostActionResult(toolName, status, raw)

	case "hoxi_data":
		g.originMu.RLock()
		dp := g.dataProfileSvc
		g.originMu.RUnlock()
		if dp == nil {
			return agent.HostToolResult{Title: toolName}, fmt.Errorf("data profile service is not configured")
		}
		res, err := dp.InvokeJSON(ctx, input)
		if err != nil {
			raw, _ := json.Marshal(res)
			return agent.HostToolResult{Title: toolName, Output: strings.TrimSpace(string(raw)), Metadata: res}, err
		}
		cleanRaw, _ := json.Marshal(res)
		return agent.HostToolResult{Title: toolName, Output: string(cleanRaw), Metadata: res}, nil

	case "skill":
		return g.invokeNativeSkillTool(ctx, tc, input)

	default:
		return agent.HostToolResult{Title: toolName}, fmt.Errorf("unsupported host tool %s", toolName)
	}
}

func (g *Gateway) invokeNativeSkillTool(ctx context.Context, tc agent.ToolContext, input json.RawMessage) (agent.HostToolResult, error) {
	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(input, &args)
	want := strings.TrimSpace(args.Name)

	q := url.Values{
		"directory": {tc.WorktreeDir},
		"sessionID": {tc.SessionID},
		"runtime":   {"native"},
	}
	status, raw, err := g.doInternalMuxJSON(ctx, http.MethodGet, "/api/hoxi/skills/list?"+q.Encode(), nil, "")
	if err == nil && status == http.StatusNotFound && tc.SessionID != "" {
		q.Del("sessionID")
		status, raw, err = g.doInternalMuxJSON(ctx, http.MethodGet, "/api/hoxi/skills/list?"+q.Encode(), nil, "")
	}
	if err != nil {
		return agent.HostToolResult{Title: "skill"}, err
	}
	var listResp skills.ListResponse
	if status >= 400 || json.Unmarshal(raw, &listResp) != nil {
		return agent.HostToolResult{Title: "skill", Output: string(raw)}, fmt.Errorf("failed to list skills")
	}

	if want == "" {
		if len(listResp.Skills) == 0 {
			return agent.HostToolResult{Title: "skills", Output: "No skills found in workspace or user directories."}, nil
		}
		var b strings.Builder
		b.WriteString("Available skills (call `skill` with `name` to load full instructions):\n")
		for _, sk := range listResp.Skills {
			if sk.Exposure == skills.ExposureUnavailable {
				continue
			}
			b.WriteString("- **" + sk.Name + "**")
			if sk.Description != "" {
				b.WriteString(": " + sk.Description)
			}
			b.WriteString("\n")
		}
		return agent.HostToolResult{Title: "skills", Output: strings.TrimSpace(b.String())}, nil
	}

	var matched *skills.SkillDescriptor
	for i := range listResp.Skills {
		if strings.EqualFold(listResp.Skills[i].Name, want) {
			matched = &listResp.Skills[i]
			break
		}
	}
	if matched == nil {
		return agent.HostToolResult{Title: "skill: " + want}, fmt.Errorf("skill %q not found in workspace", want)
	}
	if matched.Exposure == skills.ExposureUnavailable {
		return agent.HostToolResult{Title: "skill: " + matched.Name}, fmt.Errorf("skill %q is unavailable: %s", matched.Name, matched.ExposureReason)
	}

	dq := url.Values{
		"key":       {matched.Key},
		"directory": {tc.WorktreeDir},
		"sessionID": {tc.SessionID},
		"runtime":   {"native"},
	}
	dStatus, dRaw, err := g.doInternalMuxJSON(ctx, http.MethodGet, "/api/hoxi/skills/detail?"+dq.Encode(), nil, "")
	if err == nil && dStatus == http.StatusNotFound && tc.SessionID != "" {
		dq.Del("sessionID")
		dStatus, dRaw, err = g.doInternalMuxJSON(ctx, http.MethodGet, "/api/hoxi/skills/detail?"+dq.Encode(), nil, "")
	}
	if err != nil || dStatus >= 400 {
		return agent.HostToolResult{Title: "skill: " + matched.Name}, fmt.Errorf("failed to load skill %q", matched.Name)
	}
	var detail skills.SkillDetail
	if err := json.Unmarshal(dRaw, &detail); err != nil {
		return agent.HostToolResult{Title: "skill: " + matched.Name}, err
	}
	out := "<skill name=\"" + detail.Name + "\">\n" + strings.TrimSpace(detail.Content) + "\n</skill>"
	return agent.HostToolResult{
		Title:  "skill: " + detail.Name,
		Output: out,
	}, nil
}

func (g *Gateway) buildNativeSkillCatalogPrompt(ctx context.Context, sessionID, worktreeDir string) string {
	dir := strings.TrimSpace(worktreeDir)
	if dir == "" {
		dir = g.rootDir
	}
	q := url.Values{
		"directory": {dir},
		"runtime":   {"native"},
	}
	if strings.TrimSpace(sessionID) != "" {
		q.Set("sessionID", strings.TrimSpace(sessionID))
	}
	status, raw, err := g.doInternalMuxJSON(ctx, http.MethodGet, "/api/hoxi/skills/list?"+q.Encode(), nil, "")
	if err == nil && status == http.StatusNotFound && q.Get("sessionID") != "" {
		q.Del("sessionID")
		status, raw, err = g.doInternalMuxJSON(ctx, http.MethodGet, "/api/hoxi/skills/list?"+q.Encode(), nil, "")
	}
	if err != nil || status >= 400 {
		return ""
	}
	var listResp skills.ListResponse
	if json.Unmarshal(raw, &listResp) != nil || len(listResp.Skills) == 0 {
		return ""
	}
	sortedSkills := append([]skills.SkillDescriptor(nil), listResp.Skills...)
	sort.SliceStable(sortedSkills, func(i, j int) bool {
		li, lj := strings.ToLower(sortedSkills[i].Name), strings.ToLower(sortedSkills[j].Name)
		if li != lj {
			return li < lj
		}
		return sortedSkills[i].Name < sortedSkills[j].Name
	})
	var autoEntries, onDemand []string
	for _, sk := range sortedSkills {
		switch sk.Exposure {
		case skills.ExposureUnavailable:
			continue
		case skills.ExposureOnDemand:
			onDemand = append(onDemand, sk.Name)
		default:
			entry := "<skill>\n  <name>" + sk.Name + "</name>"
			if strings.TrimSpace(sk.Description) != "" {
				entry += "\n  <description>" + strings.TrimSpace(sk.Description) + "</description>"
			}
			entry += "\n</skill>"
			autoEntries = append(autoEntries, entry)
		}
	}
	if len(autoEntries) == 0 && len(onDemand) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Skills provide specialized instructions and workflows for specific tasks.\n<available_skills>\n")
	if len(autoEntries) > 0 {
		b.WriteString(strings.Join(autoEntries, "\n"))
		b.WriteByte('\n')
	}
	b.WriteString("</available_skills>")
	if len(onDemand) > 0 {
		b.WriteString("\nOn-demand skills, listed by name only: ")
		b.WriteString(strings.Join(onDemand, ", "))
		b.WriteString(". Load one with the skill tool only when the user names it or asks for its specific workflow.")
	}
	return b.String()
}

// rewriteHoxiHostProxyPath maps canonical /api/hoxi/* host routes onto the
// legacy upstream paths when an explicit OpenCode daemon is active.
func rewriteHoxiHostProxyPath(p string) (string, bool) {
	switch {
	case p == "/api/hoxi/pty":
		return "/pty", true
	case strings.HasPrefix(p, "/api/hoxi/pty/"):
		return "/pty/" + strings.TrimPrefix(p, "/api/hoxi/pty/"), true
	case p == "/api/hoxi/agents":
		return "/agent", true
	case p == "/api/hoxi/commands":
		return "/command", true
	case p == "/api/hoxi/lsp":
		return "/lsp", true
	case p == "/api/hoxi/projects":
		return "/project", true
	case p == "/api/hoxi/projects/current":
		return "/project/current", true
	default:
		return "", false
	}
}

// registerNativeHostRoutes wires Hoxi's pure-Go host endpoints (/api/hoxi/pty,
// /api/hoxi/agents, /api/hoxi/commands, /api/hoxi/lsp) plus legacy OpenCode-compatible
// aliases (/pty, /agent, /command, /lsp, /project, /find/file, /skill, /global, /path).
// When an explicit OpenCode upstream is configured (g.openCode.Available()),
// requests proxy upstream so reverse-proxy compatibility and security tests pass.
func (g *Gateway) registerNativeHostRoutes() {
	wrap := func(nativeFn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if g.openCode.Available() {
				if rewritten, ok := rewriteHoxiHostProxyPath(r.URL.Path); ok {
					r2 := r.Clone(r.Context())
					r2.URL.Path = rewritten
					r2.URL.RawPath = ""
					g.proxy.ServeHTTP(w, r2)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/api/") {
					http.StripPrefix("/api", g.proxy).ServeHTTP(w, r)
					return
				}
				g.proxy.ServeHTTP(w, r)
				return
			}
			nativeFn(w, r)
		}
	}

	ptyHandler := wrap(func(w http.ResponseWriter, r *http.Request) {
		g.originMu.RLock()
		ts := g.termSvc
		g.originMu.RUnlock()
		if ts == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "terminal service is not configured")
			return
		}
		ts.ServeHTTP(w, r)
	})
	g.mux.HandleFunc("/api/hoxi/pty", ptyHandler)
	g.mux.HandleFunc("/api/hoxi/pty/", ptyHandler)
	g.mux.HandleFunc("/pty", ptyHandler)
	g.mux.HandleFunc("/pty/", ptyHandler)
	g.mux.HandleFunc("/api/pty", ptyHandler)
	g.mux.HandleFunc("/api/pty/", ptyHandler)

	agentHandler := wrap(g.handleNativeAgentList)
	g.mux.HandleFunc("/api/hoxi/agents", agentHandler)
	g.mux.HandleFunc("/agent", agentHandler)
	g.mux.HandleFunc("/api/agent", agentHandler)

	cmdHandler := wrap(g.handleNativeCommandList)
	g.mux.HandleFunc("/api/hoxi/commands", cmdHandler)
	g.mux.HandleFunc("/command", cmdHandler)
	g.mux.HandleFunc("/api/command", cmdHandler)

	lspHandler := wrap(g.handleNativeLSPStatus)
	g.mux.HandleFunc("/api/hoxi/lsp", lspHandler)
	g.mux.HandleFunc("/lsp", lspHandler)
	g.mux.HandleFunc("/api/lsp", lspHandler)

	projHandler := wrap(g.handleNativeProjectRoute)
	g.mux.HandleFunc("/api/hoxi/projects", projHandler)
	g.mux.HandleFunc("/api/hoxi/projects/current", projHandler)
	g.mux.HandleFunc("/project", projHandler)
	g.mux.HandleFunc("/project/", projHandler)
	g.mux.HandleFunc("/api/project", projHandler)
	g.mux.HandleFunc("/api/project/", projHandler)

	findFileHandler := wrap(g.handleNativeFindFile)
	g.mux.HandleFunc("/find/file", findFileHandler)
	g.mux.HandleFunc("/api/find/file", findFileHandler)

	skillHandler := wrap(g.handleNativeSkillRaw)
	g.mux.HandleFunc("/skill", skillHandler)
	g.mux.HandleFunc("/api/skill", skillHandler)

	globalDisposeHandler := wrap(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	g.mux.HandleFunc("/api/global/dispose", globalDisposeHandler)

	globalHealthHandler := wrap(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"healthy": true, "engine": "native"})
	})
	g.mux.HandleFunc("/global/health", globalHealthHandler)
	g.mux.HandleFunc("/api/global/health", globalHealthHandler)

	pathHandler := wrap(g.handleNativePathInfo)
	g.mux.HandleFunc("/path", pathHandler)
	g.mux.HandleFunc("/api/path", pathHandler)

	if g.dataProfileMgr != nil {
		g.dataProfileMgr.RegisterRoutes(g.mux, isRemoteRequest)
	}
	registerRemoteHostRoutes(g)
}

func (g *Gateway) handleNativeLSPStatus(w http.ResponseWriter, r *http.Request) {
	dir := ""
	if r != nil {
		dir = strings.TrimSpace(r.URL.Query().Get("directory"))
	}
	if dir == "" {
		dir = g.rootDir
	}
	writeJSON(w, http.StatusOK, detectNativeLSPStatus(dir))
}

func detectNativeLSPStatus(dir string) []map[string]any {
	if strings.TrimSpace(dir) == "" {
		return []map[string]any{}
	}
	type lspProbe struct {
		id       string
		name     string
		markers  []string
		binaries []string
	}
	probes := []lspProbe{
		{id: "gopls", name: "Go", markers: []string{"go.mod", "go.work"}, binaries: []string{"gopls", "go"}},
		{id: "typescript", name: "TypeScript", markers: []string{"tsconfig.json", "package.json", "web/tsconfig.json"}, binaries: []string{"typescript-language-server", "tsc"}},
		{id: "pyright", name: "Python", markers: []string{"pyproject.toml", "requirements.txt"}, binaries: []string{"pyright-langserver", "ruff", "python3"}},
		{id: "rust-analyzer", name: "Rust", markers: []string{"Cargo.toml"}, binaries: []string{"rust-analyzer", "cargo"}},
		{id: "dart", name: "Dart / Flutter", markers: []string{"pubspec.yaml", "client/pubspec.yaml"}, binaries: []string{"dart"}},
	}
	out := make([]map[string]any, 0, len(probes))
	for _, p := range probes {
		hasMarker := false
		markerRoot := dir
		for _, m := range p.markers {
			cand := filepath.Join(dir, filepath.FromSlash(m))
			if _, err := os.Stat(cand); err == nil {
				hasMarker = true
				markerRoot = filepath.Dir(cand)
				break
			}
		}
		if !hasMarker {
			continue
		}
		status := "disconnected"
		resolvedBin := ""
		for _, b := range p.binaries {
			if found, err := exec.LookPath(b); err == nil && found != "" {
				status = "connected"
				resolvedBin = filepath.Base(found)
				break
			}
		}
		lbl := p.name
		if resolvedBin != "" {
			lbl = p.name + " (" + resolvedBin + ")"
		}
		out = append(out, map[string]any{
			"id":     p.id,
			"name":   lbl,
			"root":   markerRoot,
			"status": status,
		})
	}
	return out
}

func (g *Gateway) handleNativeProjectRoute(w http.ResponseWriter, r *http.Request) {
	clean := strings.TrimPrefix(r.URL.Path, "/api")
	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	if dir == "" {
		dir = g.rootDir
	}
	if strings.HasSuffix(clean, "/current") {
		projID := filepath.Base(dir)
		if g.workspaces != nil {
			if id, ok := g.workspaces.ProjectIDForDirectory(dir); ok && id != "" {
				projID = id
				if roots := g.workspaces.ProjectRoots(id); len(roots) > 0 && roots[0] != "" {
					dir = roots[0]
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":       projID,
			"worktree": dir,
			"vcs":      "git",
		})
		return
	}
	var out []map[string]any
	if g.workspaces != nil {
		for _, ws := range g.workspaces.List() {
			wt := ""
			for _, f := range ws.Folders {
				if f.IsPrimary && f.Path != "" {
					wt = f.Path
					break
				}
				if wt == "" && f.Path != "" {
					wt = f.Path
				}
			}
			if wt == "" {
				continue
			}
			out = append(out, map[string]any{
				"id":       ws.ID,
				"worktree": wt,
				"name":     ws.Name,
				"vcs":      "git",
			})
		}
	}
	if len(out) == 0 && g.rootDir != "" {
		out = append(out, map[string]any{
			"id":       filepath.Base(g.rootDir),
			"worktree": g.rootDir,
			"vcs":      "git",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handleNativeFindFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchQ := strings.TrimSpace(q.Get("query"))
	if searchQ == "" {
		searchQ = strings.TrimSpace(q.Get("q"))
	}
	u := url.Values{}
	u.Set("q", searchQ)
	if dir := strings.TrimSpace(q.Get("directory")); dir != "" {
		u.Set("directory", dir)
	}
	if lim := strings.TrimSpace(q.Get("limit")); lim != "" {
		u.Set("limit", lim)
	}
	subReq := r.Clone(r.Context())
	subReq.URL.RawQuery = u.Encode()
	rec := httptest.NewRecorder()
	g.handleFileSearch(rec, subReq)
	if rec.Code >= 400 {
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	var entries []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Path != "" {
			paths = append(paths, e.Path)
		}
	}
	writeJSON(w, http.StatusOK, paths)
}

func (g *Gateway) handleNativeSkillRaw(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	if dir == "" {
		dir = g.rootDir
	}
	q := url.Values{"directory": {dir}, "runtime": {"native"}}
	status, raw, err := g.doInternalMuxJSON(r.Context(), http.MethodGet, "/api/hoxi/skills/list?"+q.Encode(), nil, "")
	if err != nil || status >= 400 {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	var listResp skills.ListResponse
	if json.Unmarshal(raw, &listResp) != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	out := make([]map[string]any, 0, len(listResp.Skills))
	for _, s := range listResp.Skills {
		out = append(out, map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"location":    s.SourceLabel,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handleNativePathInfo(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("directory"))
	if dir == "" {
		dir = g.rootDir
	}
	home, _ := os.UserHomeDir()
	writeJSON(w, http.StatusOK, map[string]any{
		"home":      home,
		"state":     hoxipaths.Home(),
		"config":    hoxipaths.Home(),
		"worktree":  dir,
		"directory": dir,
	})
}
