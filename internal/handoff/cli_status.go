package handoff

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
)

// grokCLIStoredBridgeURL is the process-neutral value kept in the shared
// ~/.hoxi/models.json. Release, dev and headless Hoxi processes all read that
// file, so storing a process's own port made the last one started win and left
// the others pointing at a gateway that may exit. Each process instead points
// its own OpenCode (opencode.PrepareBrowserPlugin) and internal LLM calls
// (provider.Service) at its own gateway at runtime.
const grokCLIStoredBridgeURL = "http://" + hoxipaths.DefaultReleaseBridgeAddr + hoxipaths.GrokCLIBridgePath

// isLoopbackBridgeURL reports whether raw is a Hoxi bridge route on a loopback
// host, i.e. a value Hoxi wrote itself rather than a user-configured endpoint.
func isLoopbackBridgeURL(raw, bridgePath string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	return strings.TrimRight(u.Path, "/") == bridgePath
}

func (g *Service) syncLocalCLIBridgeURLs() {
	_ = g.updateHoxiConfigDoc(func(doc map[string]interface{}) error {
		provMap, _ := doc["provider"].(map[string]interface{})
		if provMap == nil {
			return nil
		}
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
				continue
			}
			if models, ok := pObj["models"].(map[string]interface{}); ok {
				for mid := range models {
					if strings.ContainsAny(mid, " ()") {
						delete(models, mid)
					}
				}
			}
		}

		// Migrate the per-process port earlier versions stored to the neutral value.
		if pObj, ok := provMap["grok-cli"].(map[string]interface{}); ok {
			if opts, ok := pObj["options"].(map[string]interface{}); ok {
				if cur, _ := opts["baseURL"].(string); cur != grokCLIStoredBridgeURL && isLoopbackBridgeURL(cur, hoxipaths.GrokCLIBridgePath) {
					opts["baseURL"] = grokCLIStoredBridgeURL
				}
			}
		}
		doc["provider"] = provMap
		return nil
	})
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
