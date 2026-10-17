package handoff

import (
	"net/http"
	"sync"
)

func (g *desktopGateway) handleLocalCLIStatus(w http.ResponseWriter, r *http.Request) {
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
