package handoff

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

func (g *Service) httpBridgeBaseURL() string {
	ws := strings.TrimSpace(g.wsBaseURL)
	if strings.HasPrefix(ws, "ws://") {
		return "http://" + strings.TrimPrefix(ws, "ws://")
	}
	if strings.HasPrefix(ws, "wss://") {
		return "https://" + strings.TrimPrefix(ws, "wss://")
	}
	return "http://" + hoxipaths.DefaultReleaseBridgeAddr
}

func (g *Service) syncLocalCLIBridgeURLs() {
	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		return
	}
	changed := false
	deprecatedBridges := map[string]string{
		"claude-code": "/api/hoxi/bridge/claude-code/v1",
		"google":      "/api/hoxi/bridge/gemini-antigravity/v1",
	}
	for pid, suffix := range deprecatedBridges {
		pObj, ok := provMap[pid].(map[string]interface{})
		if !ok {
			continue
		}
		opts, _ := pObj["options"].(map[string]interface{})
		curURL, _ := opts["baseURL"].(string)
		curKey, _ := opts["apiKey"].(string)
		if strings.Contains(curURL, suffix) ||
			curKey == "hoxi-local-antigravity-bridge" ||
			curKey == "hoxi-local-claude-code-bridge" ||
			curKey == "antigravity-local-oauth" ||
			curKey == "claude-code-local-oauth" ||
			strings.HasPrefix(curKey, "ya29.") ||
			strings.HasPrefix(curKey, "sk-ant-oat") {
			delete(provMap, pid)
			changed = true
			continue
		}
		if models, ok := pObj["models"].(map[string]interface{}); ok {
			for mid := range models {
				if strings.ContainsAny(mid, " ()") {
					delete(models, mid)
					changed = true
				}
			}
		}
	}

	if pObj, ok := provMap["grok-cli"].(map[string]interface{}); ok {
		if opts, ok := pObj["options"].(map[string]interface{}); ok {
			suffix := "/api/hoxi/bridge/grok-cli/v1"
			expected := g.httpBridgeBaseURL() + suffix
			if cur, _ := opts["baseURL"].(string); strings.Contains(cur, suffix) && cur != expected {
				opts["baseURL"] = expected
				pObj["options"] = opts
				provMap["grok-cli"] = pObj
				changed = true
			}
		}
	}
	if changed {
		doc["provider"] = provMap
		_ = g.persistHoxiConfigDoc(doc)
	}
}

func (g *Service) handleLocalCLIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	hoxiMap := g.loadHoxiProvidersMap()
	detections := []func() LocalCLIStatusItem{
		func() LocalCLIStatusItem { return detectGrokCLIStatus(hoxiMap) },
		func() LocalCLIStatusItem { return detectClaudeCLIStatus(g, hoxiMap) },
		func() LocalCLIStatusItem { return detectCodexCLIStatus(g, hoxiMap) },
		func() LocalCLIStatusItem { return detectGeminiCLIStatus(g, hoxiMap) },
		func() LocalCLIStatusItem { return detectCodeBuddyCLIStatus(hoxiMap) },
		func() LocalCLIStatusItem { return detectTraeCLIStatus() },
		func() LocalCLIStatusItem { return detectQoderCLIStatus(hoxiMap) },
		func() LocalCLIStatusItem { return detectOpenCodeCLIStatus(g) },
	}
	tools := make([]LocalCLIStatusItem, len(detections))
	var scans sync.WaitGroup
	for i, detect := range detections {
		scans.Add(1)
		go func(i int, detect func() LocalCLIStatusItem) {
			defer scans.Done()
			tools[i] = detect()
		}(i, detect)
	}
	scans.Wait()
	writeJSON(w, http.StatusOK, LocalCLIStatusResponse{Tools: tools})
}

func (g *Service) handleLocalCLIImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req LocalCLIImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, LocalCLIImportResponse{Error: "Invalid JSON payload"})
		return
	}
	var resp LocalCLIImportResponse
	switch req.ToolID {
	case ExternalToolGrok:
		resp = g.importGrokCLIProvider()
	case ExternalToolClaude:
		resp = g.importClaudeCLIProvider()
	case ExternalToolCodex:
		resp = g.importCodexCLIProvider()
	case ExternalToolGemini:
		resp = g.importGeminiCLIProvider()
	case ExternalToolOpenCode:
		resp = g.importOpenCodeCLIProvider()
	case ExternalToolCodeBuddy, ExternalToolTrae, ExternalToolQoder:
		resp = LocalCLIImportResponse{
			OK:     false,
			ToolID: string(req.ToolID),
			Error:  "请通过「会话接管」导入历史会话与记忆 (Please use Session Handoff to import local conversations and memory)",
		}
	default:
		resp = LocalCLIImportResponse{Error: "Unsupported toolID: " + string(req.ToolID)}
	}
	writeJSON(w, http.StatusOK, resp)
}
