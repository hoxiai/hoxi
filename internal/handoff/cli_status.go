package handoff

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

func (g *Service) httpBridgeBaseURL() string {
	ws := strings.TrimSpace(g.wsBaseURL)
	if strings.HasPrefix(ws, "ws://") {
		return "http://" + strings.TrimPrefix(ws, "ws://")
	}
	if strings.HasPrefix(ws, "wss://") {
		return "https://" + strings.TrimPrefix(ws, "wss://")
	}
	return "http://127.0.0.1:5088"
}

func (g *Service) syncLocalCLIBridgeURLs() {
	doc := g.loadHoxiConfigDoc()
	provMap, _ := doc["provider"].(map[string]interface{})
	if provMap == nil {
		return
	}
	bridges := map[string]string{
		"grok-cli":    "/api/hoxi/bridge/grok-cli/v1",
		"claude-code": "/api/hoxi/bridge/claude-code/v1",
		"google":      "/api/hoxi/bridge/gemini-antigravity/v1",
	}
	changed := false
	for pid, suffix := range bridges {
		pObj, ok := provMap[pid].(map[string]interface{})
		if !ok {
			continue
		}
		opts, _ := pObj["options"].(map[string]interface{})
		if opts == nil {
			continue
		}
		expected := g.httpBridgeBaseURL() + suffix
		if cur, _ := opts["baseURL"].(string); pid != "grok-cli" && strings.Contains(cur, suffix) {
			models, _ := pObj["models"].(map[string]interface{})
			for _, model := range models {
				if m, ok := model.(map[string]interface{}); ok && m["tool_call"] != false {
					m["tool_call"] = false
					changed = true
				}
			}
		}
		if cur, _ := opts["baseURL"].(string); strings.Contains(cur, suffix) && cur != expected {
			opts["baseURL"] = expected
			pObj["options"] = opts
			provMap[pid] = pObj
			changed = true
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
		func() LocalCLIStatusItem { return detectClaudeCLIStatus(hoxiMap) },
		func() LocalCLIStatusItem { return detectCodexCLIStatus(g, hoxiMap) },
		func() LocalCLIStatusItem { return detectGeminiCLIStatus(g, hoxiMap) },
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
	default:
		resp = LocalCLIImportResponse{Error: "Unsupported toolID: " + string(req.ToolID)}
	}
	writeJSON(w, http.StatusOK, resp)
}
